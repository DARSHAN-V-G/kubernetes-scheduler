package scheduler

import (
	"fmt"
	"math"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	corev1 "k8s.io/api/core/v1"
)

// BinPackScorer evaluates nodes to achieve optimal packing density and minimize fragmentation.
type BinPackScorer struct {
	config *SchedulerConfig
}

// NewBinPackScorer initializes a new bin-pack scorer.
func NewBinPackScorer(cfg *SchedulerConfig) *BinPackScorer {
	if cfg == nil {
		cfg = DefaultSchedulerConfig()
	}
	return &BinPackScorer{
		config: cfg,
	}
}

// Score computes a placement score [0, 100] for an eligible node.
func (s *BinPackScorer) Score(pod *corev1.Pod, node *metrics.NodeMetrics, reclaimedNodes map[string]bool) NodeScore {
	reqCPU, reqMem := PodRequests(pod)

	if node.AllocatableCPUMillis <= 0 || node.AllocatableMemoryBytes <= 0 {
		return NodeScore{
			NodeName: node.Name,
			Score:    0,
			Details:  "invalid node allocatable capacity",
		}
	}

	// 1. Post-placement Physical Utilization
	postUsageCPU := node.ActualUsageCPUMillicores + float64(reqCPU)
	postUsageMem := float64(node.ActualUsageMemoryBytes + reqMem)

	cpuUtil := postUsageCPU / float64(node.AllocatableCPUMillis)
	memUtil := postUsageMem / float64(node.AllocatableMemoryBytes)

	// Clamp to [0.0, 1.0]
	if cpuUtil > 1.0 {
		cpuUtil = 1.0
	}
	if memUtil > 1.0 {
		memUtil = 1.0
	}

	// 2. Bin-Packing Score Component
	// Preference curve: higher utilization scores higher up to the ceiling (e.g. 85%).
	// If utilization exceeds ceiling, a penalty is applied to avoid thermal/OOM hotspots.
	cpuScore := s.utilizationScore(cpuUtil)
	memScore := s.utilizationScore(memUtil)

	weightedUtilScore := (cpuScore * s.config.WeightCPU) + (memScore * s.config.WeightMemory)

	// 3. Resource Balance Component (Minimizing stranded resources)
	// Balance is penalized if one resource is 90% full while the other is only 10% full.
	utilDelta := math.Abs(cpuUtil - memUtil)
	balanceScore := (1.0 - utilDelta) * 20.0 // Up to 20 points for perfect balance

	// 4. Reclaimed Capacity Preference Bonus
	reclaimBonus := 0.0
	if reclaimedNodes != nil && reclaimedNodes[node.Name] {
		reclaimBonus = s.config.ReclaimedNodeBonus
	}

	// 5. Composite Normalized Score [0, 100]
	finalScore := (weightedUtilScore * 0.70) + balanceScore + reclaimBonus
	if finalScore > 100.0 {
		finalScore = 100.0
	}
	if finalScore < 0.0 {
		finalScore = 0.0
	}

	details := fmt.Sprintf(
		"cpuUtil=%.1f%% (score=%.1f), memUtil=%.1f%% (score=%.1f), balance=%.1f, reclaimBonus=%.1f",
		cpuUtil*100, cpuScore, memUtil*100, memScore, balanceScore, reclaimBonus,
	)

	return NodeScore{
		NodeName: node.Name,
		Score:    finalScore,
		Details:  details,
	}
}

// utilizationScore computes a packing score [0, 100] based on post-placement utilization.
func (s *BinPackScorer) utilizationScore(util float64) float64 {
	ceiling := s.config.TargetUtilizationCeiling
	if ceiling <= 0 || ceiling > 1.0 {
		ceiling = 0.85
	}

	if util <= ceiling {
		// Linear increase up to ceiling: 0% -> 20pts, ceiling% -> 100pts
		return 20.0 + (util/ceiling)*80.0
	}

	// Taper off sharply above ceiling to prevent node overload
	overage := (util - ceiling) / (1.0 - ceiling)
	return 100.0 - (overage * 50.0) // 100% util drops to 50pts
}
