package action

import (
	"context"
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// UnschedulablePendingTimeout is how long a restored pod may stay Pending/Unschedulable
	// before the watchdog declares the restore attempt failed and recycles the CheckpointRecord.
	UnschedulablePendingTimeout = 2 * time.Minute

	// watchdogAnnotation is stamped on pods created by RestorePod so the watchdog
	// can identify them without listing all pods in the cluster.
	watchdogAnnotation = "reclaim.io/checkpoint-record-name"
)

// RestoreWatchdog monitors bare pods that were created by RestorePod (CRIU path).
// If such a pod stays Pending/Unschedulable longer than UnschedulablePendingTimeout,
// the watchdog:
//  1. Deletes the stuck pod (reclaims the reservation slot).
//  2. Rolls the associated CheckpointRecord back to phase Ready so the next
//     restore attempt can succeed once resources are available.
type RestoreWatchdog struct {
	k8sClient    kubernetes.Interface
	recordWriter *DynamicCheckpointRecordWriter
	logger       *zap.Logger
	interval     time.Duration
}

// NewRestoreWatchdog creates a watchdog with the given poll interval.
// A zero interval defaults to 30 seconds.
func NewRestoreWatchdog(
	k8sClient kubernetes.Interface,
	recordWriter *DynamicCheckpointRecordWriter,
	logger *zap.Logger,
	interval time.Duration,
) *RestoreWatchdog {
	if logger == nil {
		logger = zap.NewNop()
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &RestoreWatchdog{
		k8sClient:    k8sClient,
		recordWriter: recordWriter,
		logger:       logger,
		interval:     interval,
	}
}

// Run blocks and polls on the given interval until ctx is cancelled.
// Call it in a dedicated goroutine.
func (w *RestoreWatchdog) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	w.logger.Info("RestoreWatchdog started",
		zap.Duration("pollInterval", w.interval),
		zap.Duration("pendingTimeout", UnschedulablePendingTimeout),
	)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("RestoreWatchdog stopping")
			return
		case <-ticker.C:
			if err := w.reconcile(ctx); err != nil {
				w.logger.Warn("RestoreWatchdog reconcile error", zap.Error(err))
			}
		}
	}
}

// reconcile scans all namespaces for restored pods that are stuck and rolls
// back their CheckpointRecord.
func (w *RestoreWatchdog) reconcile(ctx context.Context) error {
	// List Pending pods cluster-wide that carry our annotation.
	pods, err := w.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Pending",
	})
	if err != nil {
		return fmt.Errorf("list pending pods: %w", err)
	}

	for i := range pods.Items {
		pod := &pods.Items[i]

		// Only care about pods created by our RestoreEngine (bare CRIU pods).
		recordName, ok := pod.Annotations[watchdogAnnotation]
		if !ok {
			continue
		}

		if !w.isUnschedulable(pod) {
			continue
		}

		// Check how long it has been Pending.
		age := time.Since(pod.CreationTimestamp.Time)
		if age < UnschedulablePendingTimeout {
			w.logger.Debug("Restored pod is Unschedulable but within grace period",
				zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
				zap.Duration("age", age),
			)
			continue
		}

		w.logger.Warn("Restored pod stuck Unschedulable beyond timeout; recycling",
			zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
			zap.String("checkpointRecord", recordName),
			zap.Duration("pendingFor", age),
		)

		if err := w.recyclePod(ctx, pod, recordName); err != nil {
			w.logger.Error("Failed to recycle stuck restored pod",
				zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
				zap.Error(err),
			)
		}
	}

	return nil
}

// isUnschedulable returns true when the pod has the PodScheduled condition set
// to False with reason Unschedulable, OR when no node has been assigned yet.
func (w *RestoreWatchdog) isUnschedulable(pod *corev1.Pod) bool {
	if pod.Spec.NodeName != "" {
		// Already scheduled to a node; may still be starting.
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled &&
			cond.Status == corev1.ConditionFalse &&
			cond.Reason == corev1.PodReasonUnschedulable {
			return true
		}
	}
	// Treat a pod with no node assignment and no conditions yet as potentially
	// unschedulable (e.g., if the scheduler hasn't processed it yet).
	// We still gate on the timeout above, so this won't fire prematurely.
	return len(pod.Status.Conditions) == 0 && pod.Spec.NodeName == ""
}

// recyclePod deletes the stuck pod and rolls the CheckpointRecord back to Ready.
func (w *RestoreWatchdog) recyclePod(ctx context.Context, pod *corev1.Pod, recordName string) error {
	// 1. Delete the stuck pod so it stops consuming scheduler resources.
	gracePeriod := int64(0)
	if err := w.k8sClient.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	}); err != nil {
		return fmt.Errorf("delete stuck pod %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	w.logger.Info("Deleted stuck restored pod",
		zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
	)

	// 2. Fetch the CheckpointRecord.
	if w.recordWriter == nil {
		return fmt.Errorf("recordWriter is nil; cannot roll back CheckpointRecord %s/%s", pod.Namespace, recordName)
	}
	record, err := w.recordWriter.Get(ctx, pod.Namespace, recordName)
	if err != nil {
		return fmt.Errorf("get CheckpointRecord %s/%s: %w", pod.Namespace, recordName, err)
	}

	// 3. Only roll back if the record is in Restored/Restoring (the states we set
	//    prematurely). If it's already Failed/Expired, leave it alone.
	if record.Status.Phase != v1alpha1.CheckpointPhaseRestored &&
		record.Status.Phase != v1alpha1.CheckpointPhaseRestoring {
		w.logger.Info("CheckpointRecord phase is not Restored/Restoring; skipping rollback",
			zap.String("record", recordName),
			zap.String("phase", string(record.Status.Phase)),
		)
		return nil
	}

	// 4. Roll back to Ready so the next restore attempt can trigger.
	record.Status.Phase = v1alpha1.CheckpointPhaseReady
	record.Status.RestoredPodName = ""
	record.Status.RestoredAt = nil
	record.Status.FailureReason = fmt.Sprintf(
		"restore attempt failed: pod %s/%s was Unschedulable for %s (insufficient node resources); rolled back to Ready",
		pod.Namespace, pod.Name, UnschedulablePendingTimeout,
	)
	record.Status.Message = "Rolled back to Ready after Unschedulable restore pod exceeded timeout"

	if _, err := w.recordWriter.UpdateStatus(ctx, record); err != nil {
		return fmt.Errorf("roll back CheckpointRecord %s/%s to Ready: %w", pod.Namespace, recordName, err)
	}

	w.logger.Info("CheckpointRecord rolled back to Ready",
		zap.String("record", fmt.Sprintf("%s/%s", pod.Namespace, recordName)),
		zap.String("stuckPod", pod.Name),
	)

	return nil
}
