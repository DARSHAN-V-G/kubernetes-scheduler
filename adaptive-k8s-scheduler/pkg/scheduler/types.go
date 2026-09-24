package scheduler

import (
	"time"
)

// SchedulerConfig holds configuration parameters for the Adaptive Kubernetes Scheduler.
type SchedulerConfig struct {
	// SchedulerName is the name matched against pod.spec.schedulerName (default: "adaptive-scheduler").
	SchedulerName string `json:"schedulerName"`

	// LeaderElect enables leader election for HA deployments.
	LeaderElect bool `json:"leaderElect"`

	// LeaderElectResourceName is the lease name (default: "adaptive-scheduler").
	LeaderElectResourceName string `json:"leaderElectResourceName"`

	// LeaderElectNamespace is the namespace for the lease (default: "kube-system").
	LeaderElectNamespace string `json:"leaderElectNamespace"`

	// HeadroomSafetyBuffer ensures actual physical headroom exceeds requested resources by this multiplier (default: 1.05 = 5%).
	HeadroomSafetyBuffer float64 `json:"headroomSafetyBuffer"`

	// TargetUtilizationCeiling is the maximum desired physical utilization after placement (default: 0.85 = 85%).
	TargetUtilizationCeiling float64 `json:"targetUtilizationCeiling"`

	// WeightCPU is the scoring weight for CPU bin-packing (default: 0.50).
	WeightCPU float64 `json:"weightCpu"`

	// WeightMemory is the scoring weight for Memory bin-packing (default: 0.50).
	WeightMemory float64 `json:"weightMemory"`

	// ReclaimedNodeBonus is an incentive score bonus [0-20] awarded to nodes with recently reclaimed capacity.
	ReclaimedNodeBonus float64 `json:"reclaimedNodeBonus"`

	// ResyncPeriod is the pod informer resync period.
	ResyncPeriod time.Duration `json:"resyncPeriod"`
}

// DefaultSchedulerConfig returns standard production defaults.
func DefaultSchedulerConfig() *SchedulerConfig {
	return &SchedulerConfig{
		SchedulerName:            "adaptive-scheduler",
		LeaderElect:              true,
		LeaderElectResourceName: "adaptive-scheduler",
		LeaderElectNamespace:    "kube-system",
		HeadroomSafetyBuffer:     1.05,
		TargetUtilizationCeiling: 0.85,
		WeightCPU:                0.50,
		WeightMemory:             0.50,
		ReclaimedNodeBonus:       15.0,
		ResyncPeriod:             30 * time.Second,
	}
}

// FilterResult represents the filtering verdict for a single node.
type FilterResult struct {
	NodeName string `json:"nodeName"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

// NodeScore represents the computed bin-packing and placement score for an eligible node.
type NodeScore struct {
	NodeName string  `json:"nodeName"`
	Score    float64 `json:"score"` // Normalized [0, 100]
	Details  string  `json:"details"`
}
