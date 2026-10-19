package action

import (
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// ActionRequest defines the input to the Action Manager.
type ActionRequest struct {
	Pod           *metrics.PodMetrics     `json:"pod"`
	Decision      decision.DecisionResult `json:"decision"`
	ContainerName string                  `json:"containerName,omitempty"` // Default container to target, or empty for first
	Reason        string                  `json:"reason,omitempty"`
}

// ActionResult captures the outcome of executing an action.
type ActionResult struct {
	PodNamespace         string          `json:"podNamespace"`
	PodName              string          `json:"podName"`
	Action               decision.Action `json:"action"`
	Success              bool            `json:"success"`
	CheckpointRecordName string          `json:"checkpointRecordName,omitempty"`
	CheckpointPath       string          `json:"checkpointPath,omitempty"`
	CheckpointSizeBytes  int64           `json:"checkpointSizeBytes,omitempty"`
	ChecksumSHA256       string          `json:"checksumSha256,omitempty"`
	FreedCPUMillicores   float64         `json:"freedCpuMillicores,omitempty"`
	FreedMemoryBytes     int64           `json:"freedMemoryBytes,omitempty"`
	Duration             time.Duration   `json:"duration"`
	ExecutedAt           time.Time       `json:"executedAt"`
	Message              string          `json:"message,omitempty"`
	Error                string          `json:"error,omitempty"`
}

// CheckpointResponse represents the JSON response returned by Kubelet's checkpoint API.
type CheckpointResponse struct {
	Items     []string             `json:"items"` // Paths to created checkpoint tarballs
	Artifacts []CheckpointArtifact `json:"artifacts,omitempty"`
	SizeBytes int64                `json:"sizeBytes,omitempty"`
}

// CheckpointArtifact identifies a checkpoint archive returned by a kubelet.
type CheckpointArtifact struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
}

func (r *CheckpointResponse) Artifact() CheckpointArtifact {
	if r == nil {
		return CheckpointArtifact{}
	}
	if len(r.Artifacts) > 0 {
		return r.Artifacts[0]
	}
	if len(r.Items) > 0 {
		return CheckpointArtifact{Path: r.Items[0], SizeBytes: r.SizeBytes}
	}
	return CheckpointArtifact{}
}
