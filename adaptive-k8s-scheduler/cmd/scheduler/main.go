package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/action"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/config"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/scheduler"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
)

func main() {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	cfg := config.LoadConfig()
	schedCfg := scheduler.DefaultSchedulerConfig()

	logger.Info("Starting Adaptive Kubernetes Scheduler & Reclamation Controller",
		zap.String("schedulerName", schedCfg.SchedulerName),
		zap.String("prometheusUrl", cfg.Collector.PrometheusURL),
		zap.Int("httpPort", cfg.Collector.HTTPPort),
	)

	// 1. Build Kubernetes REST client
	k8sConfig, err := buildKubeConfig(cfg.KubeConfig)
	if err != nil {
		logger.Fatal("Failed to construct Kubernetes REST config", zap.Error(err))
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		logger.Fatal("Failed to create Kubernetes clientset", zap.Error(err))
	}

	// 2. Telemetry Ingestion Layer
	promClient, err := metrics.NewPrometheusClient(cfg.Collector.PrometheusURL, logger)
	if err != nil {
		logger.Fatal("Failed to initialize Prometheus client", zap.Error(err))
	}

	cache := metrics.NewMetricsCache(cfg.Collector.WindowSize)
	collector := metrics.NewMetricsCollector(cfg.Collector, cache, promClient, clientset, logger)

	// 3. Event Recorder
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: schedCfg.SchedulerName})

	// 4. Storage & Action Manager
	store := storage.NewLocalFSStorage("/var/lib/kubelet/checkpoints")
	validator := action.NewCheckpointValidator(store, logger)
	kubeletClient, _ := action.NewHTTPKubeletClient(clientset, k8sConfig, true, logger)
	evictor := action.NewK8sPodEvictor(clientset, logger)
	softReclaimer := action.NewSoftReclaimer(clientset, logger)
	_ = action.NewActionManager(kubeletClient, validator, evictor, softReclaimer, nil, logger)

	// 5. Adaptive Scheduler
	adaptiveSched := scheduler.NewAdaptiveScheduler(schedCfg, clientset, cache, eventRecorder, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Info("Received termination signal", zap.String("signal", sig.String()))
		cancel()
	}()

	// 6. Start HTTP Server
	server := startHTTPServer(cfg.Collector.HTTPPort, cache, adaptiveSched, logger)

	// 7. Start Metrics Collector in background
	go func() {
		if err := collector.Start(ctx); err != nil {
			logger.Error("Collector terminated with error", zap.Error(err))
		}
	}()

	// 8. Run Scheduler (with optional Leader Election)
	runScheduler := func(runCtx context.Context) {
		logger.Info("Acquired leadership; running Adaptive Scheduler loop...")
		if err := adaptiveSched.Start(runCtx); err != nil {
			logger.Error("Scheduler loop exited with error", zap.Error(err))
		}
	}

	if schedCfg.LeaderElect {
		runWithLeaderElection(ctx, clientset, schedCfg, runScheduler, logger)
	} else {
		runScheduler(ctx)
	}

	// Graceful shutdown HTTP Server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)

	logger.Info("Adaptive Scheduler shutdown cleanly")
}

func runWithLeaderElection(ctx context.Context, clientset kubernetes.Interface, cfg *scheduler.SchedulerConfig, runFn func(context.Context), logger *zap.Logger) {
	hostname, _ := os.Hostname()
	id := fmt.Sprintf("%s_%s", hostname, os.Getenv("POD_NAME"))
	if id == "_" {
		id = fmt.Sprintf("adaptive-scheduler-%d", time.Now().UnixNano())
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaderElectResourceName,
			Namespace: cfg.LeaderElectNamespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(c context.Context) {
				runFn(c)
			},
			OnStoppedLeading: func() {
				logger.Warn("Lost leadership lease; shutting down")
			},
			OnNewLeader: func(identity string) {
				if identity == id {
					return
				}
				logger.Info("Observed current leader", zap.String("leader", identity))
			},
		},
	})
}

func buildKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	inClusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return inClusterConfig, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed locating user home dir: %w", err)
	}
	defaultPath := fmt.Sprintf("%s/.kube/config", home)
	if _, err := os.Stat(defaultPath); err == nil {
		return clientcmd.BuildConfigFromFlags("", defaultPath)
	}
	return nil, fmt.Errorf("could not locate valid kubeconfig: %w", err)
}

func startHTTPServer(port int, cache *metrics.MetricsCache, sched *scheduler.AdaptiveScheduler, logger *zap.Logger) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	})

	mux.HandleFunc("/api/v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snapshot := cache.GetSnapshot()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snapshot)
	})

	mux.HandleFunc("/api/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		nodes := cache.GetAllNodes()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(nodes)
	})

	mux.HandleFunc("/api/v1/pods", func(w http.ResponseWriter, r *http.Request) {
		pods := cache.GetAllPods()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(pods)
	})

	mux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		logger.Info("HTTP server listening", zap.String("addr", addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server failed", zap.Error(err))
		}
	}()

	return server
}
