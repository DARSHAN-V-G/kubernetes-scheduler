package scheduler

import (
	"testing"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createTestPod(cpuStr, memStr string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-workload",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			SchedulerName: "adaptive-scheduler",
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpuStr),
							corev1.ResourceMemory: resource.MustParse(memStr),
						},
					},
				},
			},
		},
	}
}

func createTestNode(name string, allocCPU int64, allocMem int64, realFreeCPU float64, realFreeMem int64) *metrics.NodeMetrics {
	return &metrics.NodeMetrics{
		Name:                     name,
		AllocatableCPUMillis:     allocCPU,
		AllocatableMemoryBytes:   allocMem,
		TotalCapacityCPUMillis:   allocCPU,
		TotalCapacityMemoryBytes: allocMem,
		RealFreeCPUMillicores:    realFreeCPU,
		RealFreeMemoryBytes:      realFreeMem,
		ActualUsageCPUMillicores: float64(allocCPU) - realFreeCPU,
		ActualUsageMemoryBytes:   allocMem - realFreeMem,
		IsReady:                  true,
		HasMemoryPressure:        false,
		HasDiskPressure:          false,
		HasPIDPressure:           false,
		LastUpdated:              time.Now(),
	}
}

func TestHeadroomFilterConditions(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	filter := NewHeadroomFilter(cfg)
	pod := createTestPod("200m", "256Mi")

	// 1. Not Ready Node
	nodeNotReady := createTestNode("node-not-ready", 4000, 8*1024*1024*1024, 3000, 6*1024*1024*1024)
	nodeNotReady.IsReady = false
	res := filter.Filter(pod, nodeNotReady)
	if res.Eligible {
		t.Errorf("Expected NotReady node to be rejected")
	}

	// 2. Memory Pressure Node
	nodeMemPressure := createTestNode("node-mem-pressure", 4000, 8*1024*1024*1024, 3000, 6*1024*1024*1024)
	nodeMemPressure.HasMemoryPressure = true
	res = filter.Filter(pod, nodeMemPressure)
	if res.Eligible {
		t.Errorf("Expected node with MemoryPressure to be rejected")
	}
}

func TestHeadroomFilterRealVsDeclarative(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	filter := NewHeadroomFilter(cfg)
	pod := createTestPod("500m", "512Mi") // 500m CPU, 512MB RAM required

	// Node has plenty of declarative quota, but a noisy neighbor is physically consuming 95% CPU!
	// RealFreeCPU = 200m (< 500m required)
	noisyNode := createTestNode("noisy-node", 4000, 8*1024*1024*1024, 200.0, 6*1024*1024*1024)
	res := filter.Filter(pod, noisyNode)
	if res.Eligible {
		t.Errorf("Expected noisy node with insufficient real physical CPU to be rejected")
	}

	// Healthy Node with plenty of real physical headroom
	healthyNode := createTestNode("healthy-node", 4000, 8*1024*1024*1024, 2500.0, 5*1024*1024*1024)
	res = filter.Filter(pod, healthyNode)
	if !res.Eligible {
		t.Errorf("Expected healthy node to pass filter, got: %s", res.Reason)
	}
}

func TestBinPackScorerPackingPreference(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	scorer := NewBinPackScorer(cfg)
	pod := createTestPod("250m", "256Mi")

	// Node Low Load: 20% utilized
	nodeLow := createTestNode("node-low", 4000, 8*1024*1024*1024, 3200, int64(8*1024*1024*1024)*8/10)

	// Node Moderate Load: 65% utilized (better packing density without exceeding 85% ceiling)
	nodeMod := createTestNode("node-mod", 4000, 8*1024*1024*1024, 1400, int64(8*1024*1024*1024)*35/100)

	scoreLow := scorer.Score(pod, nodeLow, nil)
	scoreMod := scorer.Score(pod, nodeMod, nil)

	if scoreMod.Score <= scoreLow.Score {
		t.Errorf("Bin-pack scorer should prefer node with higher packing density: Mod=%.1f, Low=%.1f",
			scoreMod.Score, scoreLow.Score)
	}
}

func TestBinPackScorerReclaimedBonus(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	scorer := NewBinPackScorer(cfg)
	pod := createTestPod("200m", "200Mi")

	nodeA := createTestNode("node-a", 4000, 8*1024*1024*1024, 2000, 4*1024*1024*1024)
	nodeB := createTestNode("node-b", 4000, 8*1024*1024*1024, 2000, 4*1024*1024*1024)

	reclaimed := map[string]bool{"node-a": true}

	scoreA := scorer.Score(pod, nodeA, reclaimed)
	scoreB := scorer.Score(pod, nodeB, nil)

	if scoreA.Score <= scoreB.Score {
		t.Errorf("Expected node with reclaimed capacity to receive bonus score: A=%.1f, B=%.1f",
			scoreA.Score, scoreB.Score)
	}
}

func TestAdaptiveSchedulerSchedulePod(t *testing.T) {
	cache := metrics.NewMetricsCache(5)
	cfg := DefaultSchedulerConfig()
	scheduler := NewAdaptiveScheduler(cfg, nil, cache, nil, nil)

	// Populate cache with two nodes
	node1 := createTestNode("kind-worker-1", 4000, 8*1024*1024*1024, 100.0, 4*1024*1024*1024) // Insufficient CPU
	node2 := createTestNode("kind-worker-2", 4000, 8*1024*1024*1024, 3000.0, 5*1024*1024*1024) // Plentiful CPU

	cache.SetNode(node1)
	cache.SetNode(node2)

	pod := createTestPod("500m", "512Mi")

	bestNode, err := scheduler.SchedulePod(pod)
	if err != nil {
		t.Fatalf("SchedulePod failed: %v", err)
	}

	if bestNode != "kind-worker-2" {
		t.Errorf("Expected kind-worker-2 to be chosen, got %s", bestNode)
	}
}
