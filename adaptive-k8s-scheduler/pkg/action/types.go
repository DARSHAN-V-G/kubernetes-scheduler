package action

import (
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// ActionRequest defines the input to the Action Manager.
type ActionRequest struct {
	Pod           *metrics.PodMetrics    `json:"pod"`
	Decision      decision.DecisionResult `json:"decision"`
	ContainerName string                 `json:"containerName,omitempty"` // Default container to target, or empty for first
	Reason        string                 `json:"reason,omitempty"`
}

// ActionResult captures the outcome of executing an action.
type ActionResult struct {
	PodNamespace        string                 `json:"podNamespace"`
	PodName             string                 `json:"podName"`
	Action              decision.Action        `json:"action"`
	Success             bool                   `json:"success"`
	CheckpointRecordName string                `json:"checkpointRecordName,omitempty"`
	CheckpointPath      string                 `json:"checkpointPath,omitempty"`
	CheckpointSizeBytes int64                  `json:"checkpointSizeBytes,omitempty"`
	ChecksumSHA256      string                 `json:"checksumSha256,omitempty"`
	FreedCPUMillicores  float64                `json:"freedCpuMillicores,omitempty"`
	FreedMemoryBytes    int64                  `json:"freedMemoryBytes,omitempty"`
	Duration            time.Duration          `json:"duration"`
	ExecutedAt          time.Time              `json:"executedAt"`
	Message             string                 `json:"message,omitempty"`
	Error               string                 `json:"error,omitempty"`
}

// CheckpointResponse represents the JSON response returned by Kubelet's checkpoint API.
type CheckpointResponse struct {
	Items []string `json:"items"` // Paths to created checkpoint tarballs
}
