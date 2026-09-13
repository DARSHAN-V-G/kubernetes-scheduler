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

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/config"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	cfg := config.LoadConfig()
	logger.Info("Starting Adaptive K8s Metrics Collector",
		zap.String("prometheusUrl", cfg.Collector.PrometheusURL),
		zap.Int("httpPort", cfg.Collector.HTTPPort),
	)

	// Build Kubernetes client
	k8sConfig, err := buildKubeConfig(cfg.KubeConfig)
	if err != nil {
		logger.Fatal("Failed to construct Kubernetes REST config", zap.Error(err))
	}

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		logger.Fatal("Failed to create Kubernetes clientset", zap.Error(err))
	}

	// Build Prometheus client
	promClient, err := metrics.NewPrometheusClient(cfg.Collector.PrometheusURL, logger)
	if err != nil {
		logger.Fatal("Failed to initialize Prometheus client", zap.Error(err))
	}

	cache := metrics.NewMetricsCache(cfg.Collector.WindowSize)
	collector := metrics.NewMetricsCollector(cfg.Collector, cache, promClient, clientset, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful termination
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Info("Received termination signal, shutting down...", zap.String("signal", sig.String()))
		cancel()
	}()

	// Start HTTP Server
	server := startHTTPServer(cfg.Collector.HTTPPort, cache, logger)

	// Run Collector (blocks until context is cancelled)
	if err := collector.Start(ctx); err != nil {
		logger.Error("Metrics collector terminated with error", zap.Error(err))
	}

	// Shutdown HTTP Server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("HTTP server forced to shutdown", zap.Error(err))
	}

	logger.Info("Metrics collector stopped cleanly")
}

func buildKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}

	// Try in-cluster config first
	inClusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return inClusterConfig, nil
	}

	// Fallback to default kubeconfig path (~/.kube/config)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to find user home dir: %w", err)
	}
	defaultPath := fmt.Sprintf("%s/.kube/config", home)
	if _, err := os.Stat(defaultPath); err == nil {
		return clientcmd.BuildConfigFromFlags("", defaultPath)
	}

	return nil, fmt.Errorf("could not locate valid kubeconfig: %w", err)
}

func startHTTPServer(port int, cache *metrics.MetricsCache, logger *zap.Logger) *http.Server {
	mux := http.NewServeMux()

	// Health and readiness endpoints
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ready"))
	})

	// Unified cluster snapshot endpoint
	mux.HandleFunc("/api/v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		snapshot := cache.GetSnapshot()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(snapshot); err != nil {
			logger.Error("Failed to encode cluster snapshot", zap.Error(err))
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// Node metrics endpoint
	mux.HandleFunc("/api/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		nodes := cache.GetAllNodes()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(nodes); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// Pod metrics endpoint
	mux.HandleFunc("/api/v1/pods", func(w http.ResponseWriter, r *http.Request) {
		pods := cache.GetAllPods()
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pods); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	// Prometheus metrics for the collector itself
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
