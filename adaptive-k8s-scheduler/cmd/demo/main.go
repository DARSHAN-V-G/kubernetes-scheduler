package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/action"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/detector"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/storage"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	namespace := flag.String("namespace", "ecommerce", "Kubernetes namespace containing the ecommerce pod")
	podName := flag.String("pod", "", "Exact pod name; if empty, select the first pod matching --selector")
	selector := flag.String("selector", "app=idle-checkpoint-demo", "Pod label selector used when --pod is empty")
	prometheusURL := flag.String("prometheus-url", "http://127.0.0.1:9090", "Prometheus HTTP API URL")
	kubeconfig := flag.String("kubeconfig", os.Getenv("KUBECONFIG"), "Kubeconfig path; defaults to in-cluster or ~/.kube/config")
	samples := flag.Int("samples", 5, "Number of live Prometheus samples to collect")
	interval := flag.Duration("interval", 10*time.Second, "Delay between live samples")
	timeWindow := flag.Duration("timewindow", 0, "Time window duration to watch the pod (e.g. 30s, 60s, 2m); automatically calculates sample count")
	watchWindow := flag.Duration("watch-window", 0, "Alias for --timewindow")
	idleQPSThreshold := flag.Float64("idle-qps-threshold", 0.1, "Maximum QPS treated as idle; accommodates Prometheus rate noise")
	containerName := flag.String("container", "", "Container to reclaim; required for multi-container pods")
	execute := flag.Bool("execute", false, "Execute the selected reclaim action after an IDLE classification")
	flag.Parse()

	chosenWindow := *timeWindow
	if *watchWindow > 0 {
		chosenWindow = *watchWindow
	}
	if chosenWindow > 0 {
		if *interval <= 0 {
			*interval = 5 * time.Second
		}
		computed := int(chosenWindow / *interval)
		if computed < 1 {
			computed = 1
		}
		*samples = computed
		fmt.Printf("Configured watch time window: %s (%d samples with %s interval)\n", chosenWindow, *samples, *interval)
	}

	ctx := context.Background()
	config, err := buildKubeConfig(*kubeconfig)
	if err != nil {
		fatal(err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		fatal(err)
	}
	prometheus, err := metrics.NewPrometheusClient(*prometheusURL, zap.NewNop())
	if err != nil {
		fatal(err)
	}

	pod, err := findPod(ctx, client, *namespace, *podName, *selector)
	if err != nil {
		fatal(err)
	}
	if pod.Status.Phase != corev1.PodRunning {
		fatal(fmt.Errorf("pod %s/%s is %s, not Running", pod.Namespace, pod.Name, pod.Status.Phase))
	}

	podMetrics := podMetricsFromKubernetesPod(pod)
	selectedContainer, err := selectContainer(podMetrics, *containerName)
	if err != nil {
		fatal(err)
	}
	window := metrics.NewMetricWindow(*samples)
	var firstInactive time.Time
	var latestProfile *analyzer.WorkloadProfile

	fmt.Printf("Live ecommerce pod: %s/%s\n", pod.Namespace, pod.Name)
	fmt.Printf("Prometheus: %s\n", *prometheusURL)
	fmt.Printf("Collecting %d real samples...\n", *samples)

	for sampleIndex := 0; sampleIndex < *samples; sampleIndex++ {
		telemetry, err := prometheus.FetchClusterTelemetry(ctx)
		if err != nil {
			fatal(err)
		}
		if !applyTelemetry(podMetrics, telemetry) {
			fatal(fmt.Errorf("required CPU or memory telemetry is missing for pod %s/%s", pod.Namespace, pod.Name))
		}

		inactive := podMetrics.TotalUsageCPUMillicores <= 20 &&
			podMetrics.TotalNetworkBytesSec <= 10240 &&
			podMetrics.RequestQPS <= *idleQPSThreshold
		if inactive {
			if firstInactive.IsZero() {
				firstInactive = time.Now()
			}
			podMetrics.IdleDuration = time.Since(firstInactive)
		} else {
			firstInactive = time.Time{}
			podMetrics.IdleDuration = 0
		}

		window.AddSample(metrics.MetricSample{
			Timestamp:          time.Now(),
			CPUMillicores:      podMetrics.TotalUsageCPUMillicores,
			MemoryWorkingSet:   podMetrics.TotalUsageMemoryBytes,
			MemoryRSS:          podMetrics.TotalUsageRSSBytes,
			NetworkBytesPerSec: podMetrics.TotalNetworkBytesSec,
			RequestQPS:         podMetrics.RequestQPS,
		})
		analyzerConfig := analyzer.DefaultConfig()
		analyzerConfig.IdleQPSThreshold = *idleQPSThreshold
		latestProfile = analyzer.Analyze(podMetrics, window, analyzerConfig)
		detectorConfig := detector.DefaultConfig()
		detectorConfig.QPSIdleThreshold = *idleQPSThreshold
		classification := detector.Classify(latestProfile, detectorConfig)

		fmt.Printf("sample %d/%d: cpu=%.2fm memory=%s network=%.2f B/s qps=%.3f idle=%s class=%s\n",
			sampleIndex+1, *samples,
			podMetrics.TotalUsageCPUMillicores,
			formatBytes(podMetrics.TotalUsageMemoryBytes),
			podMetrics.TotalNetworkBytesSec,
			podMetrics.RequestQPS,
			podMetrics.IdleDuration.Round(time.Second),
			classification.Class,
		)

		if sampleIndex+1 < *samples {
			time.Sleep(*interval)
		}
	}

	detectorConfig := detector.DefaultConfig()
	detectorConfig.QPSIdleThreshold = *idleQPSThreshold
	classification := detector.Classify(latestProfile, detectorConfig)
	if classification.Class != detector.ClassIdle {
		fmt.Printf("result: class=%s action=%s score=0.000\n", classification.Class, decision.ActionKeep)
		fmt.Println("No Kubernetes mutation was performed: detector rejected the pod because it was not continuously idle at the final sample.")
		return
	}
	decisionResult := decision.NewEngine(nil).Evaluate(latestProfile, podMetrics)
	fmt.Printf("result: class=%s action=%s score=%.3f\n", classification.Class, decisionResult.Action, decisionResult.Score)
	fmt.Printf("decision factors: cpu=%.3f memory=%.3f idle=%.3f benefit=%.3f checkpoint=%.3f\n",
		decisionResult.Scores.CPU,
		decisionResult.Scores.Memory,
		decisionResult.Scores.Idle,
		decisionResult.Scores.Benefit,
		decisionResult.Scores.Checkpoint,
	)
	if !*execute {
		fmt.Println("No Kubernetes mutation was performed; use --execute after reviewing the live result.")
		return
	}
	if decisionResult.Action == decision.ActionKeep {
		fmt.Println("No Kubernetes mutation was performed: decision is KEEP.")
		return
	}
	if classification.Class != detector.ClassIdle {
		fatal(fmt.Errorf("refusing to mutate %s/%s: detector class is %s, not IDLE", pod.Namespace, pod.Name, classification.Class))
	}
	if decisionResult.Action == decision.ActionSoftReclaim {
		container := podMetrics.Containers[selectedContainer]
		rightSize := action.NewSoftReclaimer(nil, zap.NewNop()).CalculateRightSizing(container, 1.20)
		fmt.Printf("soft-reclaim preview: target=%dm CPU, %s memory; frees=%dm CPU, %s memory\n",
			rightSize.TargetCPUMillicores,
			formatBytes(rightSize.TargetMemoryBytes),
			rightSize.FreedCPUMillicores,
			formatBytes(rightSize.FreedMemoryBytes),
		)
	}

	result, err := executeAction(ctx, config, client, podMetrics, decisionResult, selectedContainer)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("executed: action=%s success=%t message=%s\n", result.Action, result.Success, result.Message)
}

func executeAction(ctx context.Context, config *rest.Config, client kubernetes.Interface, pod *metrics.PodMetrics, decisionResult decision.DecisionResult, containerName string) (*action.ActionResult, error) {
	if decisionResult.Action == decision.ActionKeep {
		return nil, fmt.Errorf("decision is KEEP; no action to execute")
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create dynamic Kubernetes client: %w", err)
	}
	recordWriter := action.NewDynamicCheckpointRecordWriter(dynamicClient)
	softReclaimer := action.NewSoftReclaimer(client, zap.NewNop())
	evictor := action.NewK8sPodEvictor(client, zap.NewNop())
	kubeletClient, err := action.NewHTTPKubeletClient(client, config, true, zap.NewNop())
	if err != nil {
		return nil, fmt.Errorf("create kubelet client: %w", err)
	}
	validator := action.NewCheckpointValidator(
		storage.NewLocalFSStorage(action.CheckpointDirectory()),
		zap.NewNop(),
	)
	manager := action.NewActionManager(kubeletClient, validator, evictor, softReclaimer, recordWriter, zap.NewNop())
	return manager.Execute(ctx, action.ActionRequest{
		Pod:           pod,
		Decision:      decisionResult,
		ContainerName: containerName,
	})
}

func selectContainer(pod *metrics.PodMetrics, requested string) (string, error) {
	if requested != "" {
		if _, exists := pod.Containers[requested]; !exists {
			return "", fmt.Errorf("container %q not found in pod %s/%s", requested, pod.Namespace, pod.Name)
		}
		return requested, nil
	}
	if len(pod.Containers) != 1 {
		return "", fmt.Errorf("pod %s/%s has %d containers; specify --container", pod.Namespace, pod.Name, len(pod.Containers))
	}
	for name := range pod.Containers {
		return name, nil
	}
	return "", fmt.Errorf("pod %s/%s has no containers", pod.Namespace, pod.Name)
}

func findPod(ctx context.Context, client kubernetes.Interface, namespace, podName, selector string) (*corev1.Pod, error) {
	if podName != "" {
		return client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	}
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no pods found in %s with selector %q", namespace, selector)
	}
	return &pods.Items[0], nil
}

func podMetricsFromKubernetesPod(pod *corev1.Pod) *metrics.PodMetrics {
	result := &metrics.PodMetrics{
		Namespace: pod.Namespace, Name: pod.Name, UID: pod.UID,
		NodeName: pod.Spec.NodeName, Phase: pod.Status.Phase,
		RestartPolicy: pod.Spec.RestartPolicy, OwnerReferences: pod.OwnerReferences,
		Labels: pod.Labels, Annotations: pod.Annotations,
		QoSClass: pod.Status.QOSClass, PriorityClassName: pod.Spec.PriorityClassName,
		DisruptionsAllowed: -1, Containers: make(map[string]*metrics.ContainerMetrics),
	}
	if len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].State.Terminated != nil {
		result.ContainerExitCode = pod.Status.ContainerStatuses[0].State.Terminated.ExitCode
	}
	if snapshot, err := json.Marshal(pod.Spec); err == nil {
		result.PodSpecSnapshot = string(snapshot)
	}
	if pod.Spec.Priority != nil {
		result.Priority = *pod.Spec.Priority
	}
	for _, container := range pod.Spec.Containers {
		requestedCPU := container.Resources.Requests.Cpu().MilliValue()
		requestedMemory := container.Resources.Requests.Memory().Value()
		limitCPU := container.Resources.Limits.Cpu().MilliValue()
		limitMemory := container.Resources.Limits.Memory().Value()
		result.TotalRequestedCPUMillis += requestedCPU
		result.TotalRequestedMemory += requestedMemory
		result.TotalLimitCPUMillis += limitCPU
		result.TotalLimitMemory += limitMemory
		result.Containers[container.Name] = &metrics.ContainerMetrics{
			Name: container.Name, Image: container.Image,
			RequestedCPUMillis: requestedCPU, RequestedMemoryBytes: requestedMemory,
			LimitCPUMillis: limitCPU, LimitMemoryBytes: limitMemory,
		}
	}
	return result
}

func applyTelemetry(pod *metrics.PodMetrics, telemetry *metrics.PrometheusTelemetry) bool {
	podTotalCPU := 0.0
	podTotalMemory := int64(0)
	podTotalRSS := int64(0)
	allReady := true
	for name, container := range pod.Containers {
		key := metrics.ContainerKey(pod.Namespace, pod.Name, name)
		cpu, cpuOK := telemetry.ContainerCPU[key]
		memory, memoryOK := telemetry.ContainerMemWorking[key]
		if !cpuOK || !memoryOK {
			container.TelemetryReady = false
			allReady = false
			continue
		}
		container.UsageCPUMillicores = cpu
		container.UsageMemoryBytes = memory
		container.UsageRSSBytes = telemetry.ContainerMemRSS[key]
		container.TelemetryReady = true
		podTotalCPU += container.UsageCPUMillicores
		podTotalMemory += container.UsageMemoryBytes
		podTotalRSS += container.UsageRSSBytes
	}
	pod.TotalUsageCPUMillicores = podTotalCPU
	pod.TotalUsageMemoryBytes = podTotalMemory
	pod.TotalUsageRSSBytes = podTotalRSS
	key := metrics.PodKey(pod.Namespace, pod.Name)
	pod.NetworkRxBytesPerSec = telemetry.PodNetworkRx[key]
	pod.NetworkTxBytesPerSec = telemetry.PodNetworkTx[key]
	pod.TotalNetworkBytesSec = pod.NetworkRxBytesPerSec + pod.NetworkTxBytesPerSec
	pod.RequestQPS = telemetry.PodQPS[key]
	pod.TelemetryReady = allReady
	return allReady
}

func buildKubeConfig(path string) (*rest.Config, error) {
	if path != "" {
		return clientcmd.BuildConfigFromFlags("", path)
	}
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return clientcmd.BuildConfigFromFlags("", home+"/.kube/config")
}

func formatBytes(value int64) string {
	units := []string{"B", "KiB", "MiB", "GiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	return fmt.Sprintf("%.2f%s", amount, units[unit])
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "demo error: %v\n", err)
	os.Exit(1)
}
