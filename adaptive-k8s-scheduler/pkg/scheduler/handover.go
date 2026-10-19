package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
)

// HandoverManager orchestrates delegating unschedulable workloads from
// adaptive-scheduler to the standard Kubernetes default-scheduler.
type HandoverManager struct {
	client               kubernetes.Interface
	eventRecorder        record.EventRecorder
	defaultSchedulerName string
	logger               *zap.Logger
}

// NewHandoverManager creates a new HandoverManager.
func NewHandoverManager(
	client kubernetes.Interface,
	eventRecorder record.EventRecorder,
	defaultSchedulerName string,
	logger *zap.Logger,
) *HandoverManager {
	if defaultSchedulerName == "" {
		defaultSchedulerName = "default-scheduler"
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &HandoverManager{
		client:               client,
		eventRecorder:        eventRecorder,
		defaultSchedulerName: defaultSchedulerName,
		logger:               logger,
	}
}

// HasReclaimableCandidates checks if any running pod in the cluster could be
// reclaimed to free up node headroom (i.e. low CPU/memory usage and not protected).
func (h *HandoverManager) HasReclaimableCandidates(cache *metrics.MetricsCache) bool {
	if cache == nil {
		return false
	}
	allPods := cache.GetAllPods()
	for _, pod := range allPods {
		if pod.Phase != corev1.PodRunning {
			continue
		}
		if pod.Annotations != nil && pod.Annotations["reclaim.io/protected"] == "true" {
			continue
		}
		// Consider candidate if CPU usage is under 30 millicores or memory utilization is low
		if pod.TotalUsageCPUMillicores < 30.0 {
			return true
		}
	}
	return false
}

// HandoverPod transfers an unschedulable pod to the default-scheduler.
// For Deployment/ReplicaSet-owned pods, it updates the parent template so the
// controller reconciles new pods with default-scheduler.
// For standalone pods, it deletes and recreates the pod with spec.schedulerName: default-scheduler.
func (h *HandoverManager) HandoverPod(ctx context.Context, pod *corev1.Pod, reason string) error {
	if pod == nil {
		return fmt.Errorf("pod is nil")
	}

	podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	h.logger.Info("Initiating workload handover to default-scheduler",
		zap.String("pod", podKey),
		zap.String("destinationScheduler", h.defaultSchedulerName),
		zap.String("reason", reason),
	)

	// Check if pod is managed by a Deployment or ReplicaSet
	var deploymentOwner string
	var rsOwner string

	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			rsOwner = owner.Name
			break
		} else if owner.Kind == "Deployment" {
			deploymentOwner = owner.Name
			break
		}
	}

	// If owned by ReplicaSet, find its parent Deployment if any
	if rsOwner != "" && deploymentOwner == "" && h.client != nil {
		rs, err := h.client.AppsV1().ReplicaSets(pod.Namespace).Get(ctx, rsOwner, metav1.GetOptions{})
		if err == nil {
			for _, owner := range rs.OwnerReferences {
				if owner.Kind == "Deployment" {
					deploymentOwner = owner.Name
					break
				}
			}
		}
	}

	// 1. If Deployment-owned, patch Deployment template
	if deploymentOwner != "" && h.client != nil {
		return h.handoverDeployment(ctx, pod, deploymentOwner, reason)
	}

	// 2. If ReplicaSet-owned without Deployment, patch ReplicaSet template
	if rsOwner != "" && h.client != nil {
		return h.handoverReplicaSet(ctx, pod, rsOwner, reason)
	}

	// 3. Otherwise standalone pod: delete and recreate with default-scheduler
	return h.handoverStandalonePod(ctx, pod, reason)
}

func (h *HandoverManager) handoverDeployment(ctx context.Context, pod *corev1.Pod, deploymentName, reason string) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]string{
						"reclaim.io/handed-over-from": "adaptive-scheduler",
						"reclaim.io/handover-reason":  reason,
						"reclaim.io/handed-over-at":   time.Now().Format(time.RFC3339),
					},
				},
				"spec": map[string]interface{}{
					"schedulerName": h.defaultSchedulerName,
				},
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal deployment patch: %w", err)
	}

	_, err = h.client.AppsV1().Deployments(pod.Namespace).Patch(
		ctx,
		deploymentName,
		k8stypes.StrategicMergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch deployment %s/%s template schedulerName: %w", pod.Namespace, deploymentName, err)
	}

	// Delete the stuck pending pod so the ReplicaSet immediately spawns a new one under default-scheduler
	zero := int64(0)
	_ = h.client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
	})

	h.recordHandoverEvent(pod, reason)
	h.logger.Info("Deployment template successfully handed over to default-scheduler",
		zap.String("namespace", pod.Namespace),
		zap.String("deployment", deploymentName),
	)
	return nil
}

func (h *HandoverManager) handoverReplicaSet(ctx context.Context, pod *corev1.Pod, rsName, reason string) error {
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"schedulerName": h.defaultSchedulerName,
				},
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal replicaset patch: %w", err)
	}

	_, err = h.client.AppsV1().ReplicaSets(pod.Namespace).Patch(
		ctx,
		rsName,
		k8stypes.StrategicMergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		return fmt.Errorf("patch replicaset %s/%s template schedulerName: %w", pod.Namespace, rsName, err)
	}

	zero := int64(0)
	_ = h.client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
	})

	h.recordHandoverEvent(pod, reason)
	return nil
}

func (h *HandoverManager) handoverStandalonePod(ctx context.Context, pod *corev1.Pod, reason string) error {
	if h.client == nil {
		return fmt.Errorf("kubernetes client is nil")
	}

	// Build replacement pod spec with default-scheduler
	newPodSpec := pod.Spec.DeepCopy()
	newPodSpec.SchedulerName = h.defaultSchedulerName
	newPodSpec.NodeName = ""

	newAnnotations := make(map[string]string)
	for k, v := range pod.Annotations {
		newAnnotations[k] = v
	}
	newAnnotations["reclaim.io/handed-over-from"] = "adaptive-scheduler"
	newAnnotations["reclaim.io/handover-reason"] = reason
	newAnnotations["reclaim.io/handed-over-at"] = time.Now().Format(time.RFC3339)

	newLabels := make(map[string]string)
	for k, v := range pod.Labels {
		newLabels[k] = v
	}

	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        pod.Name,
			Namespace:   pod.Namespace,
			Labels:      newLabels,
			Annotations: newAnnotations,
		},
		Spec: *newPodSpec,
	}

	// Delete old pod
	zero := int64(0)
	err := h.client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
	})
	if err != nil {
		h.logger.Warn("Failed deleting old pod during handover (continuing recreation)", zap.Error(err))
	}

	// Create replacement pod under default-scheduler
	createdPod, err := h.client.CoreV1().Pods(pod.Namespace).Create(ctx, newPod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("recreate pod %s/%s under default-scheduler: %w", pod.Namespace, pod.Name, err)
	}

	h.recordHandoverEvent(createdPod, reason)
	h.logger.Info("Standalone pod successfully recreated under default-scheduler",
		zap.String("pod", fmt.Sprintf("%s/%s", createdPod.Namespace, createdPod.Name)),
		zap.String("schedulerName", createdPod.Spec.SchedulerName),
	)

	return nil
}

func (h *HandoverManager) recordHandoverEvent(pod *corev1.Pod, reason string) {
	if h.eventRecorder != nil && pod != nil {
		h.eventRecorder.Eventf(
			pod,
			corev1.EventTypeNormal,
			"HandoverToDefaultScheduler",
			"No nodes have sufficient headroom; handed over to %s: %s",
			h.defaultSchedulerName,
			reason,
		)
	}
}
