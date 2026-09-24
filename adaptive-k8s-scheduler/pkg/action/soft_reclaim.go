package action

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// SoftReclaimer performs in-place dynamic resource request downscaling.
type SoftReclaimer struct {
	client kubernetes.Interface
	logger *zap.Logger
}

// NewSoftReclaimer creates a new soft reclaimer.
func NewSoftReclaimer(client kubernetes.Interface, logger *zap.Logger) *SoftReclaimer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SoftReclaimer{
		client: client,
		logger: logger,
	}
}

// RightSizeResult contains the delta of capacity reclaimed through soft right-sizing.
type RightSizeResult struct {
	TargetCPUMillicores  int64 `json:"targetCpuMillicores"`
	TargetMemoryBytes    int64 `json:"targetMemoryBytes"`
	FreedCPUMillicores   int64 `json:"freedCpuMillicores"`
	FreedMemoryBytes     int64 `json:"freedMemoryBytes"`
}

// CalculateRightSizing determines new conservative resource requests based on actual telemetry
// plus a safety margin (e.g. 1.20 = 20% headroom above actual usage).
func (s *SoftReclaimer) CalculateRightSizing(cMetric *metrics.ContainerMetrics, safetyFactor float64) RightSizeResult {
	if safetyFactor <= 1.0 {
		safetyFactor = 1.20 // 20% buffer
	}

	// Floor safety: at least 10m CPU and 16Mi memory
	const minCPU = int64(10)
	const minMem = int64(16 * 1024 * 1024)

	targetCPU := int64(math.Ceil(cMetric.UsageCPUMillicores * safetyFactor))
	if targetCPU < minCPU {
		targetCPU = minCPU
	}

	targetMem := int64(float64(cMetric.UsageMemoryBytes) * safetyFactor)
	if targetMem < minMem {
		targetMem = minMem
	}

	freedCPU := cMetric.RequestedCPUMillis - targetCPU
	if freedCPU < 0 {
		freedCPU = 0
	}

	freedMem := cMetric.RequestedMemoryBytes - targetMem
	if freedMem < 0 {
		freedMem = 0
	}

	return RightSizeResult{
		TargetCPUMillicores: targetCPU,
		TargetMemoryBytes:   targetMem,
		FreedCPUMillicores:  freedCPU,
		FreedMemoryBytes:    freedMem,
	}
}

// ApplyInPlaceResize applies an in-place resource request patch to a container in a running pod.
func (s *SoftReclaimer) ApplyInPlaceResize(ctx context.Context, namespace, podName, containerName string, rightSize RightSizeResult) error {
	s.logger.Info("Applying in-place resource resize patch",
		zap.String("pod", fmt.Sprintf("%s/%s", namespace, podName)),
		zap.String("container", containerName),
		zap.Int64("newCpuMillis", rightSize.TargetCPUMillicores),
		zap.Int64("newMemoryBytes", rightSize.TargetMemoryBytes),
	)

	// In-place pod resize patch: updates spec.containers[?].resources.requests
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"containers": []map[string]interface{}{
				{
					"name": containerName,
					"resources": map[string]interface{}{
						"requests": map[string]interface{}{
							"cpu":    fmt.Sprintf("%dm", rightSize.TargetCPUMillicores),
							"memory": fmt.Sprintf("%d", rightSize.TargetMemoryBytes),
						},
					},
				},
			},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("failed constructing patch JSON: %w", err)
	}

	_, err = s.client.CoreV1().Pods(namespace).Patch(
		ctx,
		podName,
		k8stypes.StrategicMergePatchType,
		patchBytes,
		metav1.PatchOptions{},
		"resize", // Subresource for in-place resize (K8s 1.27+)
	)
	if err != nil {
		// Fallback to standard pod patch if subresource is not supported
		_, err = s.client.CoreV1().Pods(namespace).Patch(
			ctx,
			podName,
			k8stypes.StrategicMergePatchType,
			patchBytes,
			metav1.PatchOptions{},
		)
		if err != nil {
			return fmt.Errorf("in-place patch failed for pod %s/%s: %w", namespace, podName, err)
		}
	}

	s.logger.Info("In-place resource resize successfully applied",
		zap.String("pod", fmt.Sprintf("%s/%s", namespace, podName)),
		zap.Int64("freedCpuMillis", rightSize.FreedCPUMillicores),
		zap.Int64("freedMemoryBytes", rightSize.FreedMemoryBytes),
	)
	return nil
}
