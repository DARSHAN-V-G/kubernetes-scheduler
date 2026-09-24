package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
)

// AdaptiveScheduler manages pod scheduling queue, filtering, scoring, and binding.
type AdaptiveScheduler struct {
	config         *SchedulerConfig
	client         kubernetes.Interface
	metricsCache   *metrics.MetricsCache
	filter         *HeadroomFilter
	scorer         *BinPackScorer
	eventRecorder  record.EventRecorder
	logger         *zap.Logger

	podQueue       chan *corev1.Pod
	reclaimedNodes map[string]time.Time
	reclaimedMu    sync.RWMutex
}

// NewAdaptiveScheduler creates a new AdaptiveScheduler instance.
func NewAdaptiveScheduler(
	cfg *SchedulerConfig,
	client kubernetes.Interface,
	cache *metrics.MetricsCache,
	recorder record.EventRecorder,
	logger *zap.Logger,
) *AdaptiveScheduler {
	if cfg == nil {
		cfg = DefaultSchedulerConfig()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &AdaptiveScheduler{
		config:         cfg,
		client:         client,
		metricsCache:   cache,
		filter:         NewHeadroomFilter(cfg),
		scorer:         NewBinPackScorer(cfg),
		eventRecorder:  recorder,
		logger:         logger,
		podQueue:       make(chan *corev1.Pod, 256),
		reclaimedNodes: make(map[string]time.Time),
	}
}

// MarkNodeReclaimed records that a node had resources reclaimed, awarding placement priority.
func (s *AdaptiveScheduler) MarkNodeReclaimed(nodeName string) {
	s.reclaimedMu.Lock()
	defer s.reclaimedMu.Unlock()
	s.reclaimedNodes[nodeName] = time.Now()
}

// getReclaimedNodesMap returns a map of nodes reclaimed within the last 15 minutes.
func (s *AdaptiveScheduler) getReclaimedNodesMap() map[string]bool {
	s.reclaimedMu.Lock()
	defer s.reclaimedMu.Unlock()

	cutoff := time.Now().Add(-15 * time.Minute)
	result := make(map[string]bool)

	for node, ts := range s.reclaimedNodes {
		if ts.After(cutoff) {
			result[node] = true
		} else {
			delete(s.reclaimedNodes, node)
		}
	}
	return result
}

// Start launches the pod informer and the scheduling worker loop.
func (s *AdaptiveScheduler) Start(ctx context.Context) error {
	s.logger.Info("Starting Adaptive Kubernetes Scheduler",
		zap.String("schedulerName", s.config.SchedulerName),
		zap.Float64("headroomBuffer", s.config.HeadroomSafetyBuffer),
		zap.Float64("utilCeiling", s.config.TargetUtilizationCeiling),
	)

	// Set up informer for unassigned pods
	factory := informers.NewSharedInformerFactory(s.client, s.config.ResyncPeriod)
	podInformer := factory.Core().V1().Pods().Informer()

	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if pod, ok := obj.(*corev1.Pod); ok {
				s.enqueueIfTargeted(pod)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if pod, ok := newObj.(*corev1.Pod); ok {
				s.enqueueIfTargeted(pod)
			}
		},
	})

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced) {
		return fmt.Errorf("failed to sync scheduler pod informer")
	}

	s.logger.Info("Pod informer synced; running scheduling loop...")

	// Worker loop
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Stopping Adaptive Scheduler worker loop...")
			return nil
		case pod := <-s.podQueue:
			s.scheduleOne(ctx, pod)
		}
	}
}

// enqueueIfTargeted inspects whether the pod targets this custom scheduler and is unscheduled.
func (s *AdaptiveScheduler) enqueueIfTargeted(pod *corev1.Pod) {
	if pod == nil {
		return
	}
	if pod.Spec.SchedulerName == s.config.SchedulerName && pod.Spec.NodeName == "" && pod.DeletionTimestamp == nil {
		select {
		case s.podQueue <- pod:
		default:
			s.logger.Warn("Scheduler queue full, dropping pod enqueue (will retry on next sync)",
				zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
			)
		}
	}
}

// SchedulePod performs the complete filter and score cycle for a pod, returning the chosen node.
func (s *AdaptiveScheduler) SchedulePod(pod *corev1.Pod) (string, error) {
	snapshot := s.metricsCache.GetSnapshot()
	if snapshot == nil || len(snapshot.Nodes) == 0 {
		return "", fmt.Errorf("no nodes available in telemetry cache snapshot")
	}

	// Step 1: Filter Phase (Real Physical Headroom)
	var eligibleNodes []*metrics.NodeMetrics
	filterReasons := make(map[string]string)

	for _, node := range snapshot.Nodes {
		res := s.filter.Filter(pod, node)
		if res.Eligible {
			eligibleNodes = append(eligibleNodes, node)
		} else {
			filterReasons[node.Name] = res.Reason
		}
	}

	if len(eligibleNodes) == 0 {
		return "", fmt.Errorf("0/%d nodes available: %v", len(snapshot.Nodes), filterReasons)
	}

	// Step 2: Score Phase (Real-Load Bin-Packing & Fragmentation)
	reclaimedMap := s.getReclaimedNodesMap()
	var bestNode string
	var highestScore float64 = -1.0
	var bestDetails string

	for _, node := range eligibleNodes {
		scored := s.scorer.Score(pod, node, reclaimedMap)
		if scored.Score > highestScore {
			highestScore = scored.Score
			bestNode = node.Name
			bestDetails = scored.Details
		}
	}

	s.logger.Info("Evaluated candidate nodes for placement",
		zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
		zap.String("selectedNode", bestNode),
		zap.Float64("score", highestScore),
		zap.String("details", bestDetails),
	)

	return bestNode, nil
}

// scheduleOne processes a single pending pod: select node and execute binding.
func (s *AdaptiveScheduler) scheduleOne(ctx context.Context, pod *corev1.Pod) {
	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	s.logger.Info("Attempting to schedule pod", zap.String("pod", podKey))

	selectedNode, err := s.SchedulePod(pod)
	if err != nil {
		s.logger.Warn("Failed to schedule pod", zap.String("pod", podKey), zap.Error(err))
		if s.eventRecorder != nil {
			s.eventRecorder.Eventf(pod, corev1.EventTypeWarning, "FailedScheduling", "%v", err)
		}
		return
	}

	// Step 3: Bind Phase
	binding := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: selectedNode,
		},
	}

	err = s.client.CoreV1().Pods(pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
	if err != nil {
		s.logger.Error("Failed to bind pod to node",
			zap.String("pod", podKey),
			zap.String("targetNode", selectedNode),
			zap.Error(err),
		)
		if s.eventRecorder != nil {
			s.eventRecorder.Eventf(pod, corev1.EventTypeWarning, "FailedBinding", "failed to bind to node %s: %v", selectedNode, err)
		}
		return
	}

	s.logger.Info("Successfully scheduled and bound pod",
		zap.String("pod", podKey),
		zap.String("node", selectedNode),
	)

	if s.eventRecorder != nil {
		s.eventRecorder.Eventf(pod, corev1.EventTypeNormal, "Scheduled", "Successfully assigned %s to %s via adaptive-scheduler", podKey, selectedNode)
	}
}
