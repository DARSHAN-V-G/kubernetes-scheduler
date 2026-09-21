package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ActionType describes the mode of reclamation executed when a workload qualifies.
type ActionType string

const (
	// ActionTypeFullReclaim triggers CRIU checkpointing, integrity validation, and pod eviction.
	ActionTypeFullReclaim ActionType = "FullReclaim"

	// ActionTypeSoftReclaim triggers in-place pod resizing or declarative request right-sizing.
	ActionTypeSoftReclaim ActionType = "SoftReclaim"

	// ActionTypeAdaptive dynamically selects FullReclaim or SoftReclaim based on score and safety capabilities.
	ActionTypeAdaptive ActionType = "Adaptive"
)

// ScoringWeights defines custom weightings for the 9 multi-criteria evaluation factors.
// The sum of all non-zero weights should ideally equal 1.0.
type ScoringWeights struct {
	// WeightCPU is the importance of low CPU utilization (default: 0.20)
	WeightCPU *float64 `json:"weightCpu,omitempty"`

	// WeightMemory is the importance of low Memory utilization (default: 0.20)
	WeightMemory *float64 `json:"weightMemory,omitempty"`

	// WeightIdle is the importance of cumulative idle duration (default: 0.15)
	WeightIdle *float64 `json:"weightIdle,omitempty"`

	// WeightBenefit is the importance of net reclaimable resources freed (default: 0.15)
	WeightBenefit *float64 `json:"weightBenefit,omitempty"`

	// WeightReplica is the importance of replica quorum headroom (default: 0.10)
	WeightReplica *float64 `json:"weightReplica,omitempty"`

	// WeightPriority is the importance of low pod priority (default: 0.05)
	WeightPriority *float64 `json:"weightPriority,omitempty"`

	// WeightPDB is the importance of disruption budget availability (default: 0.05)
	WeightPDB *float64 `json:"weightPdb,omitempty"`

	// WeightState is the importance of stateless/batch lifecycle state (default: 0.05)
	WeightState *float64 `json:"weightState,omitempty"`

	// WeightCheckpoint is the importance of CRIU checkpointability (default: 0.05)
	WeightCheckpoint *float64 `json:"weightCheckpoint,omitempty"`
}

// ScoreThresholds defines composite score boundaries for selecting reclamation actions.
type ScoreThresholds struct {
	// FullReclaimThreshold is the minimum composite score [0, 1] required for Full Reclaim (default: 0.75).
	FullReclaimThreshold *float64 `json:"fullReclaimThreshold,omitempty"`

	// SoftReclaimThreshold is the minimum composite score [0, 1] required for Soft Reclaim (default: 0.50).
	SoftReclaimThreshold *float64 `json:"softReclaimThreshold,omitempty"`
}

// ReclaimPolicySpec defines the desired configuration and safety parameters of ReclaimPolicy.
type ReclaimPolicySpec struct {
	// IdleDurationThreshold is the minimum duration of continuous inactivity before a pod is considered idle (e.g. "10m", "1h").
	// +kubebuilder:default="10m"
	IdleDurationThreshold string `json:"idleDurationThreshold,omitempty"`

	// CPUIdleThresholdPct is the maximum CPU utilization percentage (0.0 to 1.0) to qualify as idle (default: 0.05 = 5%).
	CPUIdleThresholdPct *float64 `json:"cpuIdleThresholdPct,omitempty"`

	// MemoryIdleThresholdPct is the maximum Memory utilization percentage (0.0 to 1.0) to qualify as idle (default: 0.10 = 10%).
	MemoryIdleThresholdPct *float64 `json:"memoryIdleThresholdPct,omitempty"`

	// QPSIdleThreshold is the maximum average requests/sec to qualify as idle (default: 0.0).
	QPSIdleThreshold *float64 `json:"qpsIdleThreshold,omitempty"`

	// NetIdleThresholdBytes is the maximum network I/O bytes/sec to qualify as idle (default: 10240.0 = 10 KB/s).
	NetIdleThresholdBytes *float64 `json:"netIdleThresholdBytes,omitempty"`

	// MinReplicasRequired is the minimum number of available replicas that must remain active after reclaiming a pod (default: 1).
	MinReplicasRequired *int32 `json:"minReplicasRequired,omitempty"`

	// MaxPriorityForReclaim is the maximum priority value for pods eligible for reclamation (default: 100000).
	MaxPriorityForReclaim *int32 `json:"maxPriorityForReclaim,omitempty"`

	// ExemptPriorityClasses is a list of PriorityClass names that are exempt from any reclamation.
	ExemptPriorityClasses []string `json:"exemptPriorityClasses,omitempty"`

	// ActionType specifies the preferred reclamation action mode (FullReclaim, SoftReclaim, or Adaptive).
	// +kubebuilder:default="Adaptive"
	ActionType ActionType `json:"actionType,omitempty"`

	// Weights contains optional custom weightings for the 9 decision factors.
	Weights *ScoringWeights `json:"weights,omitempty"`

	// ScoreThresholds contains score boundaries for action selection.
	ScoreThresholds *ScoreThresholds `json:"scoreThresholds,omitempty"`
}

// ReclaimPolicyStatus defines the observed runtime status and metrics of ReclaimPolicy.
type ReclaimPolicyStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ActiveReclamations is the number of workloads currently undergoing reclamation under this policy.
	ActiveReclamations int32 `json:"activeReclamations,omitempty"`

	// TotalReclaimedCPU is the cumulative CPU capacity freed by this policy (e.g., "15400m").
	TotalReclaimedCPU string `json:"totalReclaimedCpu,omitempty"`

	// TotalReclaimedMemory is the cumulative Memory freed by this policy (e.g., "32Gi").
	TotalReclaimedMemory string `json:"totalReclaimedMemory,omitempty"`

	// Conditions represents the latest observations of the policy's state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Action",type="string",JSONPath=".spec.actionType",description="Reclamation Action Mode"
// +kubebuilder:printcolumn:name="IdleThreshold",type="string",JSONPath=".spec.idleDurationThreshold",description="Idle Duration Threshold"
// +kubebuilder:printcolumn:name="ActiveReclaims",type="integer",JSONPath=".status.activeReclamations",description="Active Reclamations"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ReclaimPolicy is the Schema for the reclaimpolicies API.
type ReclaimPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReclaimPolicySpec   `json:"spec,omitempty"`
	Status ReclaimPolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// ReclaimPolicyList contains a list of ReclaimPolicy.
type ReclaimPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReclaimPolicy `json:"items"`
}
