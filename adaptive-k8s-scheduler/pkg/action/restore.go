package action

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"go.uber.org/zap"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RestoreEngine handles reconstitution of pods from CheckpointRecord CRDs.
type RestoreEngine struct {
	client       kubernetes.Interface
	logger       *zap.Logger
	statusWriter CheckpointStatusWriter
}

type CheckpointStatusWriter interface {
	UpdateStatus(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error)
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

func (r *RestoreEngine) SetStatusWriter(writer CheckpointStatusWriter) {
	r.statusWriter = writer
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
	podSpec.NodeName = ""

	labels := map[string]string{
		"app.kubernetes.io/restored-from": record.Name,
		"reclaim.io/source-pod":           record.Spec.SourcePodName,
	}
	for key, value := range record.Spec.PodLabelsSnapshot {
		labels[key] = value
	}

	restoredPodName := fmt.Sprintf("%s-restored-%d", record.Spec.SourcePodName, time.Now().Unix())
	if len(restoredPodName) > 63 {
		restoredPodName = restoredPodName[:63]
	}

	restoredPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoredPodName,
			Namespace: record.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"reclaim.io/checkpoint-path":   record.Spec.CheckpointPath,
				"reclaim.io/checkpoint-sha256": record.Spec.ChecksumSHA256,
				"reclaim.io/restored-at":       time.Now().Format(time.RFC3339),
				// watchdogAnnotation links this pod back to its CheckpointRecord so that
				// RestoreWatchdog can roll back the record if the pod gets stuck Unschedulable.
				watchdogAnnotation: record.Name,
			},
		},
		Spec: podSpec,
	}

	return restoredPod, nil
}

// RestorePod reconstitutes the pod and creates it in the cluster.
func (r *RestoreEngine) RestorePod(ctx context.Context, record *v1alpha1.CheckpointRecord) (*corev1.Pod, error) {
	if record == nil {
		return nil, fmt.Errorf("checkpoint record is nil")
	}
	if record.Status.Phase != "" && record.Status.Phase != v1alpha1.CheckpointPhaseCheckpointed && record.Status.Phase != v1alpha1.CheckpointPhaseReady && record.Status.Phase != v1alpha1.CheckpointPhaseRestoring {
		return nil, fmt.Errorf("checkpoint record %s is not Ready: %s", record.Name, record.Status.Phase)
	}

	record.Status.Phase = v1alpha1.CheckpointPhaseRestoring
	record.Status.Message = "Restore requested"
	if r.statusWriter != nil {
		updated, err := r.statusWriter.UpdateStatus(ctx, record)
		if err != nil {
			return nil, err
		}
		record.ResourceVersion = updated.ResourceVersion
	}

	if record.Spec.OwnerKind == "Deployment" && record.Spec.OwnerName != "" {
		replicas := int32(1)
		if _, err := r.client.AppsV1().Deployments(record.Namespace).UpdateScale(ctx, record.Spec.OwnerName, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: record.Spec.OwnerName, Namespace: record.Namespace}, Spec: autoscalingv1.ScaleSpec{Replicas: replicas}}, metav1.UpdateOptions{}); err != nil {
			if statusErr := r.markFailed(ctx, record, err); statusErr != nil {
				return nil, fmt.Errorf("scale Deployment: %w; failed to persist Failed status: %v", err, statusErr)
			}
			return nil, fmt.Errorf("scale Deployment: %w", err)
		}
		now := metav1.Now()
		record.Status.Phase = v1alpha1.CheckpointPhaseRestored
		record.Status.RestoredPodName = record.Spec.OwnerName
		record.Status.RestoredAt = &now
		record.Status.Message = fmt.Sprintf("Redeployment requested for Deployment %s", record.Spec.OwnerName)
		if r.statusWriter != nil {
			updated, err := r.statusWriter.UpdateStatus(ctx, record)
			if err != nil {
				return nil, err
			}
			record.ResourceVersion = updated.ResourceVersion
		}
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: record.Spec.OwnerName, Namespace: record.Namespace}}, nil
	}

	restoredPod, err := r.BuildRestoredPodSpec(record)
	if err != nil {
		if statusErr := r.markFailed(ctx, record, err); statusErr != nil {
			return nil, fmt.Errorf("build restored pod: %w; failed to persist Failed status: %v", err, statusErr)
		}
		return nil, err
	}

	r.logger.Info("Creating restored pod from checkpoint",
		zap.String("record", record.Name),
		zap.String("newPod", fmt.Sprintf("%s/%s", record.Namespace, restoredPod.Name)),
		zap.String("checkpointPath", record.Spec.CheckpointPath),
	)

	createdPod, err := r.client.CoreV1().Pods(record.Namespace).Create(ctx, restoredPod, metav1.CreateOptions{})
	if err != nil {
		statusErr := r.markFailed(ctx, record, err)
		if statusErr != nil {
			return nil, fmt.Errorf("failed to create restored pod %s/%s: %w; failed to persist Failed status: %v", record.Namespace, restoredPod.Name, err, statusErr)
		}
		return nil, fmt.Errorf("failed to create restored pod %s/%s: %w", record.Namespace, restoredPod.Name, err)
	}

	now := metav1.Now()
	record.Status.Phase = v1alpha1.CheckpointPhaseRestored
	record.Status.RestoredPodName = createdPod.Name
	record.Status.RestoredAt = &now
	record.Status.Message = fmt.Sprintf("Successfully restored as pod %s", createdPod.Name)
	if r.statusWriter != nil {
		updated, err := r.statusWriter.UpdateStatus(ctx, record)
		if err != nil {
			return nil, err
		}
		record.ResourceVersion = updated.ResourceVersion
	}

	return createdPod, nil
}

func (r *RestoreEngine) markFailed(ctx context.Context, record *v1alpha1.CheckpointRecord, cause error) error {
	record.Status.Phase = v1alpha1.CheckpointPhaseFailed
	record.Status.FailureReason = cause.Error()
	record.Status.Message = "Restore failed"
	if r.statusWriter != nil {
		updated, err := r.statusWriter.UpdateStatus(ctx, record)
		if err != nil {
			return err
		}
		record.ResourceVersion = updated.ResourceVersion
	}
	return nil
}
