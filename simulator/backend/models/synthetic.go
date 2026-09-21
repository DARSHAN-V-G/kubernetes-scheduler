package models

import (
	corev1 "k8s.io/api/core/v1"
)

// SyntheticNode represents a simulated Kubernetes node.
type SyntheticNode struct {
	Name                     string  `json:"name"`
	TotalCapacityCPUMillis   int64   `json:"totalCapacityCpuMillis"`
	TotalCapacityMemoryBytes int64   `json:"totalCapacityMemoryBytes"`
	AllocatableCPUMillis     int64   `json:"allocatableCpuMillis"`
	AllocatableMemoryBytes   int64   `json:"allocatableMemoryBytes"`
	ActualUsageCPUMillicores float64 `json:"actualUsageCpuMillicores"`
	ActualUsageMemoryBytes   int64   `json:"actualUsageMemoryBytes"`
	IsReady                  bool    `json:"isReady"`
}

// SyntheticWorkload represents a simulated Kubernetes Pod with declarative specs,
// control-plane metadata, and runtime telemetry.
type SyntheticWorkload struct {
	Name                 string            `json:"name"`
	Namespace            string            `json:"namespace"`
	NodeName             string            `json:"nodeName"`
	Phase                string            `json:"phase"`              // Running, Pending, Failed, Succeeded
	QoSClass             string            `json:"qosClass"`           // Guaranteed, Burstable, BestEffort
	Priority             int32             `json:"priority"`           // Numeric priority
	PriorityClassName    string            `json:"priorityClassName"`  // e.g. system-cluster-critical
	DisruptionsAllowed   int32             `json:"disruptionsAllowed"` // -1 = no PDB, 0 = blocked, >0 = allowed
	OwnerKind            string            `json:"ownerKind"`          // Deployment, ReplicaSet, StatefulSet, DaemonSet
	OwnerName            string            `json:"ownerName"`
	DesiredReplicas      int32             `json:"desiredReplicas"`
	ReadyReplicas        int32             `json:"readyReplicas"`
	AvailableReplicas    int32             `json:"availableReplicas"`
	RequestedCPUMillis   int64             `json:"requestedCpuMillis"`
	LimitCPUMillis       int64             `json:"limitCpuMillis"`
	RequestedMemoryBytes int64             `json:"requestedMemoryBytes"`
	LimitMemoryBytes     int64             `json:"limitMemoryBytes"`
	UsageCPUMillicores   float64           `json:"usageCpuMillicores"`
	UsageMemoryBytes     int64             `json:"usageMemoryBytes"`
	NetworkBytesPerSec   float64           `json:"networkBytesPerSec"`
	RequestQPS           float64           `json:"requestQps"`
	IdleDurationSeconds  int64             `json:"idleDurationSeconds"`
	IsIdle               bool              `json:"isIdle"`
	Labels               map[string]string `json:"labels,omitempty"`
	Annotations          map[string]string `json:"annotations,omitempty"`
}

// ClusterModel encapsulates a full synthetic cluster containing nodes and workloads.
type ClusterModel struct {
	Nodes     []SyntheticNode     `json:"nodes"`
	Workloads []SyntheticWorkload `json:"workloads"`
}

// GetPodPhase returns corev1.PodPhase corresponding to workload phase string.
func (w *SyntheticWorkload) GetPodPhase() corev1.PodPhase {
	switch w.Phase {
	case string(corev1.PodPending):
		return corev1.PodPending
	case string(corev1.PodSucceeded):
		return corev1.PodSucceeded
	case string(corev1.PodFailed):
		return corev1.PodFailed
	default:
		return corev1.PodRunning
	}
}
