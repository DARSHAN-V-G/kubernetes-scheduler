package action

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RestoreEngine handles reconstitution of pods from CheckpointRecord CRDs.
type RestoreEngine struct {
	client kubernetes.Interface
	logger *zap.Logger
}

// NewRestoreEngine creates a new restoration engine.
func NewRestoreEngine(client kubernetes.Interface, logger *zap.Logger) *RestoreEngine {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &RestoreEngine{
		client: client,
		logger: logger,
	}
}

// BuildRestoredPodSpec constructs a new Pod object from a CheckpointRecord.
func (r *RestoreEngine) BuildRestoredPodSpec(record *v1alpha1.CheckpointRecord) (*corev1.Pod, error) {
	if record == nil {
		return nil, fmt.Errorf("checkpoint record is nil")
	}

	var podSpec corev1.PodSpec
	if record.Spec.PodSpecSnapshot != "" {
		if err := json.Unmarshal([]byte(record.Spec.PodSpecSnapshot), &podSpec); err != nil {
			r.logger.Warn("Failed parsing podSpecSnapshot; constructing fallback spec", zap.Error(err))
		}
	}

	// If snapshot wasn't available or had no containers, build standard single-container spec
	if len(podSpec.Containers) == 0 {
		podSpec = corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  record.Spec.ContainerName,
					Image: record.Spec.ImageURI,
					Resources: corev1.ResourceRequirements{
						Requests: record.Spec.OriginalRequests,
						Limits:   record.Spec.OriginalLimits,
					},
				},
			},
		}
	}

	restoredPodName := fmt.Sprintf("%s-restored-%d", record.Spec.SourcePodName, time.Now().Unix())
	if len(restoredPodName) > 63 {
		restoredPodName = restoredPodName[:63]
	}

	restoredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoredPodName,
			Namespace: record.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/restored-from": record.Name,
				"reclaim.io/source-pod":           record.Spec.SourcePodName,
			},
			Annotations: map[string]string{
				"reclaim.io/checkpoint-path":   record.Spec.CheckpointPath,
				"reclaim.io/checkpoint-sha256": record.Spec.ChecksumSHA256,
				"reclaim.io/restored-at":       time.Now().Format(time.RFC3339),
			},
		},
		Spec: podSpec,
	}

	return restoredPod, nil
}

// RestorePod reconstitutes the pod and creates it in the cluster.
func (r *RestoreEngine) RestorePod(ctx context.Context, record *v1alpha1.CheckpointRecord) (*corev1.Pod, error) {
	restoredPod, err := r.BuildRestoredPodSpec(record)
	if err != nil {
		return nil, err
	}

	r.logger.Info("Creating restored pod from checkpoint",
		zap.String("record", record.Name),
		zap.String("newPod", fmt.Sprintf("%s/%s", record.Namespace, restoredPod.Name)),
		zap.String("checkpointPath", record.Spec.CheckpointPath),
	)

	createdPod, err := r.client.CoreV1().Pods(record.Namespace).Create(ctx, restoredPod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create restored pod %s/%s: %w", record.Namespace, restoredPod.Name, err)
	}

	now := metav1.Now()
	record.Status.Phase = v1alpha1.CheckpointPhaseRestored
	record.Status.RestoredPodName = createdPod.Name
	record.Status.RestoredAt = &now
	record.Status.Message = fmt.Sprintf("Successfully restored as pod %s", createdPod.Name)

	return createdPod, nil
}
