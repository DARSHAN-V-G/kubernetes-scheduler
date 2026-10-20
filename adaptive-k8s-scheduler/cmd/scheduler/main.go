package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/action"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/activator"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/config"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/dependency"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/detector"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/scheduler"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/storage"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
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
	store := storage.NewLocalFSStorage(action.CheckpointDirectory())
	validator := action.NewCheckpointValidator(store, logger)
	kubeletClient, _ := action.NewHTTPKubeletClient(clientset, k8sConfig, true, logger)
	dynamicClient, err := dynamic.NewForConfig(k8sConfig)
	if err != nil {
		logger.Fatal("Failed to create dynamic Kubernetes client", zap.Error(err))
	}
	evictor := action.NewK8sPodEvictor(clientset, logger)
	softReclaimer := action.NewSoftReclaimer(clientset, logger)
	recordWriter := action.NewDynamicCheckpointRecordWriter(dynamicClient)
	actionMgr := action.NewActionManager(kubeletClient, validator, evictor, softReclaimer, recordWriter, logger)
	actionMgr.SetGracefulReclaimer(action.NewGracefulReclaimer(clientset, recordWriter, logger))
	actionMgr.SetK8sClient(clientset)
	restoreEngine := action.NewRestoreEngine(clientset, logger)
	restoreEngine.SetStatusWriter(recordWriter)

	// Decision Policy: checks dynamic WEIGHTS_FILE_PATH, local ml/artifacts, or ConfigMap mount
	weightsPath := os.Getenv("WEIGHTS_FILE_PATH")
	if weightsPath == "" {
		if _, statErr := os.Stat("ml/artifacts/trained_weights.json"); statErr == nil {
			weightsPath = "ml/artifacts/trained_weights.json"
		} else if _, statErr := os.Stat("../ml/artifacts/trained_weights.json"); statErr == nil {
			weightsPath = "../ml/artifacts/trained_weights.json"
		} else {
			weightsPath = "/etc/scheduler/trained_weights.json"
		}
	}
	var decisionPolicy *decision.Policy
	if _, statErr := os.Stat(weightsPath); statErr == nil {
		loadedPolicy, loadErr := decision.LoadPolicyFromWeightsFile(weightsPath)
		if loadErr != nil {
			logger.Warn("Failed to load weights from file, falling back to compiled DefaultPolicy",
				zap.String("weightsFile", weightsPath),
				zap.Error(loadErr),
			)
			decisionPolicy = decision.DefaultPolicy()
		} else {
			logger.Info("Successfully loaded data-backed weights and thresholds from file",
				zap.String("weightsFile", weightsPath),
				zap.Float64("WeightBenefit", loadedPolicy.WeightBenefit),
				zap.Float64("WeightMemory", loadedPolicy.WeightMemory),
				zap.Float64("WeightCPU", loadedPolicy.WeightCPU),
				zap.Float64("SoftThreshold", loadedPolicy.SoftReclaimScoreThreshold),
				zap.Float64("FullThreshold", loadedPolicy.FullReclaimScoreThreshold),
			)
			decisionPolicy = loadedPolicy
		}
	} else {
		decisionPolicy = decision.DefaultPolicy()
		logger.Info("Using compiled DefaultPolicy weights and thresholds",
			zap.Float64("WeightBenefit", decisionPolicy.WeightBenefit),
			zap.Float64("WeightMemory", decisionPolicy.WeightMemory),
			zap.Float64("WeightCPU", decisionPolicy.WeightCPU),
			zap.Float64("SoftThreshold", decisionPolicy.SoftReclaimScoreThreshold),
			zap.Float64("FullThreshold", decisionPolicy.FullReclaimScoreThreshold),
		)
	}

	decisionEngine := decision.NewEngine(decisionPolicy)
	detectorConfig := detector.DefaultConfig()
	var reclaimCooldownMu sync.Mutex
	reclaimCooldown := make(map[string]time.Time)

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

	// Demand-triggered restore loop. A checkpoint is restored only when a pending
	// pod exists in the same namespace, avoiding unsolicited duplicate workloads.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := restoreForPendingDemand(ctx, clientset, recordWriter, restoreEngine, logger); err != nil {
					logger.Warn("Demand-triggered restore check failed", zap.Error(err))
				}
			}
		}
	}()

	var cycleCounter uint64 = 0

	// 6. Start HTTP Server
	server := startHTTPServer(
		cfg.Collector.HTTPPort,
		cache,
		adaptiveSched,
		restoreEngine,
		recordWriter,
		os.Getenv("RESTORE_API_TOKEN"),
		decisionEngine,
		detectorConfig,
		schedCfg.SchedulerName,
		reclaimCooldown,
		&reclaimCooldownMu,
		&cycleCounter,
		logger,
	)

	// 6b. Start Demand Activator & Scale-from-Zero Buffer Gateway
	cooldownRecorder := &schedulerCooldownAdapter{
		mu:    &reclaimCooldownMu,
		store: reclaimCooldown,
	}
	readinessChecker := activator.NewK8sServiceReadinessChecker(clientset, logger)
	activatorCfg := activator.DefaultConfig()
	activatorServer := activator.NewActivatorServer(activatorCfg, clientset, readinessChecker, restoreEngine, recordWriter, cooldownRecorder, logger)

	activatorHTTP := &http.Server{
		Addr:    fmt.Sprintf(":%d", activatorCfg.Port),
		Handler: activatorServer,
	}
	go func() {
		logger.Info("Demand Activator & Scale-from-Zero Buffer Gateway listening", zap.Int("port", activatorCfg.Port))
		if err := activatorHTTP.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Activator server failed", zap.Error(err))
		}
	}()

	// 6c. Start Restore Watchdog: detects CRIU-restored bare pods that are stuck
	// Pending/Unschedulable (insufficient node resources) and rolls their
	// CheckpointRecord back to Ready so the restore can be retried later.
	restoreWatchdog := action.NewRestoreWatchdog(clientset, recordWriter, logger, 30*time.Second)
	go restoreWatchdog.Run(ctx)

	// 7. Start Metrics Collector in background
	go func() {
		if err := collector.Start(ctx); err != nil {
			logger.Error("Collector terminated with error", zap.Error(err))
		}
	}()

	// 8. Start Reclamation Engine Loop in background
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cycleCounter++
				allPods := cache.GetAllPods()
				for _, pod := range allPods {
					// Strictly monitor and evaluate only workload pods targeted to adaptive-scheduler
					if pod.Namespace == "adaptive-scheduler" || pod.Namespace == "kube-system" {
						continue
					}
					if pod.SchedulerName != schedCfg.SchedulerName && !strings.Contains(pod.Namespace, "adaptive") {
						continue
					}

					podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
					serviceName := pod.Labels["app"]
					if serviceName == "" {
						serviceName = pod.Name
					}
					svcKey := fmt.Sprintf("%s/%s", pod.Namespace, serviceName)

					reclaimCooldownMu.Lock()
					lastReclaimed, recentlyReclaimed := reclaimCooldown[podKey]
					if !recentlyReclaimed {
						lastReclaimed, recentlyReclaimed = reclaimCooldown[svcKey]
					}
					inCooldown := recentlyReclaimed && time.Since(lastReclaimed) < 2*time.Minute
					if inCooldown {
						reclaimCooldownMu.Unlock()
					} else {
						if recentlyReclaimed {
							delete(reclaimCooldown, podKey)
							delete(reclaimCooldown, svcKey)
						}
						reclaimCooldownMu.Unlock()
					}

					window, _ := cache.GetWindow(pod.Namespace, pod.Name)
					profile := analyzer.Analyze(pod, window, nil)
					classification := detector.Classify(profile, detectorConfig)
					decisionResult := decisionEngine.Evaluate(profile, pod)

					// Log every 10-second evaluation cycle for full visibility
					logger.Info(fmt.Sprintf("[CYCLE #%03d | 10s] Pod Evaluation", cycleCounter),
						zap.String("pod", podKey),
						zap.String("stage1_candidate", classification.Class.String()),
						zap.Bool("is_candidate", classification.Class == detector.ClassIdle),
						zap.Float64("cpu_milli", pod.TotalUsageCPUMillicores),
						zap.Duration("idle_dur", pod.IdleDuration),
						zap.Int("samples", profile.SampleCount),
						zap.Bool("consistent", profile.IsConsistentlyIdle),
						zap.Float64("score", decisionResult.Score),
						zap.String("action", decisionResult.Action.String()),
						zap.Bool("in_cooldown", inCooldown),
					)

					if inCooldown {
						continue
					}
					if classification.Class != detector.ClassIdle {
						continue
					}

					if decisionResult.Action == decision.ActionFullReclaim || decisionResult.Action == decision.ActionSoftReclaim {
						logger.Info("Reclamation Engine evaluating action",
							zap.String("pod", podKey),
							zap.String("action", decisionResult.Action.String()),
							zap.Float64("score", decisionResult.Score),
						)
						req := action.ActionRequest{
							Pod:      pod,
							Decision: decisionResult,
						}
						res, err := actionMgr.Execute(ctx, req)
						if err != nil {
							logger.Error("Reclamation action failed", zap.Error(err))
						} else {
							reclaimCooldownMu.Lock()
							reclaimCooldown[podKey] = time.Now()
							reclaimCooldownMu.Unlock()
							if pod.NodeName != "" {
								adaptiveSched.MarkNodeReclaimed(pod.NodeName)
							}
							logger.Info("Reclamation action succeeded", zap.String("message", res.Message))
						}
					}
				}
			}
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

	// Graceful shutdown HTTP Servers
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
	activatorHTTP.Shutdown(shutdownCtx)

	logger.Info("Adaptive Scheduler shutdown cleanly")
}

func restoreForPendingDemand(
	ctx context.Context,
	client kubernetes.Interface,
	records *action.DynamicCheckpointRecordWriter,
	restoreEngine *action.RestoreEngine,
	logger *zap.Logger,
) error {
	pending, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "status.phase=Pending"})
	if err != nil {
		return fmt.Errorf("list pending pods: %w", err)
	}

	for _, pendingPod := range pending.Items {
		readyRecords, err := records.ListReady(ctx, pendingPod.Namespace)
		if err != nil {
			return err
		}
		record := matchingRestoreRecord(pendingPod, readyRecords)
		if record == nil || hasRestoredWorkload(ctx, client, pendingPod.Namespace, record.Spec.SourcePodName) {
			continue
		}
		logger.Info("Pending demand detected; restoring checkpointed workload",
			zap.String("pendingPod", fmt.Sprintf("%s/%s", pendingPod.Namespace, pendingPod.Name)),
			zap.String("checkpointRecord", record.Name),
		)

		// Restore unready dependencies in cascading order if declared
		deps := dependency.ParseDependencies(pendingPod.Namespace, pendingPod.Annotations)
		for _, dep := range deps {
			if !hasRestoredWorkload(ctx, client, dep.Namespace, dep.Name) {
				for _, depRec := range readyRecords {
					if depRec.Spec.SourcePodName == dep.Name || depRec.Name == dep.Name {
						logger.Info("Cascading restoration for dependency", zap.String("dependency", dep.String()))
						_, _ = restoreEngine.RestorePod(ctx, depRec)
						break
					}
				}
			}
		}

		if _, err := restoreEngine.RestorePod(ctx, record); err != nil {
			return fmt.Errorf("restore checkpoint %s/%s: %w", record.Namespace, record.Name, err)
		}
		return nil
	}

	// Also check queue-driven demand for batch-worker if it has a Ready checkpoint
	readyAdaptive, err := records.ListReady(ctx, "test-application-adaptive")
	if err == nil {
		for _, rec := range readyAdaptive {
			if strings.Contains(rec.Spec.SourcePodName, "batch-worker") || strings.Contains(rec.Spec.OwnerName, "batch-worker") {
				if checkRedisQueueNonEmpty("cache.test-application-adaptive.svc.cluster.local:6379", "queue:batch-tasks") {
					logger.Info("Queue demand detected for batch-worker; restoring deployment",
						zap.String("checkpointRecord", rec.Name),
					)
					_, _ = restoreEngine.RestorePod(ctx, rec)
				}
			}
		}
	}

	return nil
}

func checkRedisQueueNonEmpty(addr, queue string) bool {
	conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(1 * time.Second))
	cmd := fmt.Sprintf("*2\r\n$4\r\nLLEN\r\n$%d\r\n%s\r\n", len(queue), queue)
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return false
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil || len(line) < 2 || line[0] != ':' {
		return false
	}
	countStr := strings.TrimSpace(line[1:])
	count, _ := strconv.Atoi(countStr)
	return count > 0
}

type schedulerCooldownAdapter struct {
	mu    *sync.Mutex
	store map[string]time.Time
}

func (c *schedulerCooldownAdapter) RecordCooldown(namespace, podOrServiceName string, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := fmt.Sprintf("%s/%s", namespace, podOrServiceName)
	c.store[key] = time.Now().Add(duration)
}

func matchingRestoreRecord(pendingPod corev1.Pod, records []*v1alpha1.CheckpointRecord) *v1alpha1.CheckpointRecord {
	sourcePod := pendingPod.Annotations["reclaim.io/restore-source-pod"]
	if sourcePod == "" {
		sourcePod = pendingPod.Labels["reclaim.io/restore-source-pod"]
	}
	if sourcePod == "" {
		return nil
	}
	for _, record := range records {
		if record.Spec.SourcePodName == sourcePod {
			return record
		}
	}
	return nil
}

func hasRestoredWorkload(ctx context.Context, client kubernetes.Interface, namespace, sourcePod string) bool {
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "reclaim.io/source-pod=" + sourcePod,
	})
	return err == nil && len(pods.Items) > 0
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
	var cfg *rest.Config
	var err error
	if kubeconfigPath != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	} else if inClusterConfig, inClusterErr := rest.InClusterConfig(); inClusterErr == nil {
		cfg = inClusterConfig
	} else if home, homeErr := os.UserHomeDir(); homeErr == nil {
		defaultPath := fmt.Sprintf("%s/.kube/config", home)
		if _, statErr := os.Stat(defaultPath); statErr == nil {
			cfg, err = clientcmd.BuildConfigFromFlags("", defaultPath)
		}
	}
	if cfg == nil {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("could not locate valid kubeconfig")
	}

	// Prevent client-side throttling during high-frequency evaluation and reconciliation loops (default is 5 QPS / 10 Burst)
	if cfg.QPS < 50.0 {
		cfg.QPS = 50.0
	}
	if cfg.Burst < 100 {
		cfg.Burst = 100
	}
	return cfg, nil
}

type checkpointRecordReader interface {
	Get(context.Context, string, string) (*v1alpha1.CheckpointRecord, error)
}

type PodEvaluationDTO struct {
	PodName            string   `json:"pod_name"`
	Namespace          string   `json:"namespace"`
	SchedulerName      string   `json:"scheduler_name"`
	WorkloadKind       string   `json:"workload_kind"`
	Phase              string   `json:"phase"`
	NodeName           string   `json:"node_name"`
	InCooldown         bool     `json:"in_cooldown"`
	CooldownRemainingS float64  `json:"cooldown_remaining_sec"`

	// Stage 1: Candidate Qualification Gate
	IsCandidate        bool     `json:"is_candidate"`
	Stage1Class        string   `json:"stage1_classification"`
	CPUMillicores      float64  `json:"cpu_millicores"`
	CPUThresholdMillis float64  `json:"cpu_threshold_millicores"`
	CPUUtilPct         float64  `json:"cpu_util_pct"`
	MemoryWorkingSetMB float64  `json:"memory_working_set_mb"`
	MemoryRequestedMB  float64  `json:"memory_requested_mb"`
	MemoryUtilPct      float64  `json:"memory_util_pct"`
	NetworkBytesPerSec float64  `json:"network_bytes_sec"`
	QPS                float64  `json:"qps"`
	IdleDurationSec    float64  `json:"idle_duration_sec"`
	MinIdleDurationSec float64  `json:"min_idle_duration_sec"`
	DurationSatisfied  bool     `json:"duration_satisfied"`
	SampleCount        int      `json:"sample_count"`
	MinSamples         int      `json:"min_samples"`
	SamplesSatisfied   bool     `json:"samples_satisfied"`
	IsConsistentlyIdle bool     `json:"is_consistently_idle"`
	Stage1Reasons      []string `json:"stage1_reasons"`

	// Stage 2: Decision Scoring Engine
	CompositeScore     float64  `json:"composite_score"`
	DecisionAction     string   `json:"decision_action"`
	SoftThreshold      float64  `json:"soft_threshold"`
	FullThreshold      float64  `json:"full_threshold"`
	ScoreCPU           float64  `json:"score_cpu"`
	ScoreMemory        float64  `json:"score_mem"`
	ScoreBenefit       float64  `json:"score_benefit"`
	ScorePriority      float64  `json:"score_priority"`
	ScoreState         float64  `json:"score_state"`
	ScoreReplica       float64  `json:"score_replica"`
	FullReclaimAllowed bool     `json:"full_reclaim_allowed"`
	SoftReclaimAllowed bool     `json:"soft_reclaim_allowed"`
	RejectionReasons   []string `json:"rejection_reasons"`
	DecisionReasons    []string `json:"decision_reasons"`
}

type EvaluationsResponse struct {
	Timestamp   string             `json:"timestamp"`
	CycleNumber uint64             `json:"cycle_number"`
	Pods        []PodEvaluationDTO `json:"pods"`
}

func buildPodEvaluationDTO(
	pod *metrics.PodMetrics,
	cache *metrics.MetricsCache,
	decisionEngine *decision.Engine,
	detectorConfig *detector.Config,
	cooldownStore map[string]time.Time,
	cooldownMu *sync.Mutex,
) PodEvaluationDTO {
	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	var inCooldown bool
	var cooldownRemaining float64
	if cooldownMu != nil && cooldownStore != nil {
		cooldownMu.Lock()
		if lastReclaimed, exists := cooldownStore[podKey]; exists {
			elapsed := time.Since(lastReclaimed)
			if elapsed < 2*time.Minute {
				inCooldown = true
				cooldownRemaining = (2*time.Minute - elapsed).Seconds()
			}
		}
		cooldownMu.Unlock()
	}

	window, _ := cache.GetWindow(pod.Namespace, pod.Name)
	profile := analyzer.Analyze(pod, window, nil)
	classification := detector.Classify(profile, detectorConfig)
	decisionResult := decisionEngine.Evaluate(profile, pod)

	var reqMemMB, usedMemMB, memUtilPct float64
	if pod.TotalRequestedMemory > 0 {
		reqMemMB = float64(pod.TotalRequestedMemory) / (1024 * 1024)
		usedMemMB = float64(pod.TotalUsageMemoryBytes) / (1024 * 1024)
		memUtilPct = (float64(pod.TotalUsageMemoryBytes) / float64(pod.TotalRequestedMemory)) * 100
	}
	var cpuUtilPct float64
	if pod.TotalRequestedCPUMillis > 0 {
		cpuUtilPct = (pod.TotalUsageCPUMillicores / float64(pod.TotalRequestedCPUMillis)) * 100
	}

	workloadKind := "Pod"
	if pod.Replicas != nil && pod.Replicas.OwnerKind != "" {
		workloadKind = pod.Replicas.OwnerKind
	}

	policy := decisionEngine.Policy()

	return PodEvaluationDTO{
		PodName:            pod.Name,
		Namespace:          pod.Namespace,
		SchedulerName:      pod.SchedulerName,
		WorkloadKind:       workloadKind,
		Phase:              string(pod.Phase),
		NodeName:           pod.NodeName,
		InCooldown:         inCooldown,
		CooldownRemainingS: cooldownRemaining,
		IsCandidate:        classification.Class == detector.ClassIdle,
		Stage1Class:        classification.Class.String(),
		CPUMillicores:      pod.TotalUsageCPUMillicores,
		CPUThresholdMillis: 20.0,
		CPUUtilPct:         cpuUtilPct,
		MemoryWorkingSetMB: usedMemMB,
		MemoryRequestedMB:  reqMemMB,
		MemoryUtilPct:      memUtilPct,
		NetworkBytesPerSec: pod.TotalNetworkBytesSec,
		QPS:                pod.RequestQPS,
		IdleDurationSec:    pod.IdleDuration.Seconds(),
		MinIdleDurationSec: detectorConfig.MinIdleDuration.Seconds(),
		DurationSatisfied:  pod.IdleDuration >= detectorConfig.MinIdleDuration,
		SampleCount:        profile.SampleCount,
		MinSamples:         detectorConfig.MinSampleCount,
		SamplesSatisfied:   profile.SampleCount >= detectorConfig.MinSampleCount,
		IsConsistentlyIdle: profile.IsConsistentlyIdle,
		Stage1Reasons:      classification.Reasons,
		CompositeScore:     decisionResult.Score,
		DecisionAction:     decisionResult.Action.String(),
		SoftThreshold:      policy.SoftReclaimScoreThreshold,
		FullThreshold:      policy.FullReclaimScoreThreshold,
		ScoreCPU:           decisionResult.Scores.CPU,
		ScoreMemory:        decisionResult.Scores.Memory,
		ScoreBenefit:       decisionResult.Scores.Benefit,
		ScorePriority:      decisionResult.Scores.Priority,
		ScoreState:         decisionResult.Scores.State,
		ScoreReplica:       decisionResult.Scores.Replica,
		FullReclaimAllowed: decisionResult.Capabilities.FullReclaimAllowed,
		SoftReclaimAllowed: decisionResult.Capabilities.SoftReclaimAllowed,
		RejectionReasons:   decisionResult.RejectionReasons,
		DecisionReasons:    decisionResult.Reasons,
	}
}

func startHTTPServer(
	port int,
	cache *metrics.MetricsCache,
	sched *scheduler.AdaptiveScheduler,
	restoreEngine *action.RestoreEngine,
	recordReader checkpointRecordReader,
	restoreToken string,
	decisionEngine *decision.Engine,
	detectorConfig *detector.Config,
	schedulerName string,
	reclaimCooldown map[string]time.Time,
	reclaimCooldownMu *sync.Mutex,
	cycleCounter *uint64,
	logger *zap.Logger,
) *http.Server {
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

	mux.HandleFunc("/api/v1/evaluations", func(w http.ResponseWriter, r *http.Request) {
		allPods := cache.GetAllPods()
		var evals []PodEvaluationDTO
		for _, pod := range allPods {
			if pod.Namespace == "adaptive-scheduler" || pod.Namespace == "kube-system" {
				continue
			}
			if pod.SchedulerName != schedulerName && !strings.Contains(pod.Namespace, "adaptive") {
				continue
			}
			dto := buildPodEvaluationDTO(pod, cache, decisionEngine, detectorConfig, reclaimCooldown, reclaimCooldownMu)
			evals = append(evals, dto)
		}
		var currCycle uint64
		if cycleCounter != nil {
			currCycle = *cycleCounter
		}
		resp := EvaluationsResponse{
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
			CycleNumber: currCycle,
			Pods:        evals,
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc("/api/v1/checkpoints/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || restoreEngine == nil || recordReader == nil || restoreToken == "" {
			http.Error(w, "restore endpoint unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+restoreToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 6 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "checkpoints" {
			http.Error(w, "expected /api/v1/checkpoints/{namespace}/{name}/restore", http.StatusNotFound)
			return
		}
		if parts[5] != "restore" {
			http.Error(w, "expected restore action", http.StatusNotFound)
			return
		}

		record, err := recordReader.Get(r.Context(), parts[3], parts[4])
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		pod, err := restoreEngine.RestorePod(r.Context(), record)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(pod)
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
