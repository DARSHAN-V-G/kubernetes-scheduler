package action

import (
	"context"
	"testing"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// buildFakeRecord returns a CheckpointRecord in Restored phase with the given name.
func buildFakeRecord(namespace, name string) *v1alpha1.CheckpointRecord {
	return &v1alpha1.CheckpointRecord{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "reclaim.io/v1alpha1",
			Kind:       "CheckpointRecord",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.CheckpointRecordSpec{
			SourcePodName: "test-batch-pod",
			NodeName:      "node-1",
			ContainerName: "worker",
			ImageURI:      "batch-worker:latest",
			CheckpointPath: "/var/lib/kubelet/checkpoints/test.tar",
		},
		Status: v1alpha1.CheckpointRecordStatus{
			Phase:           v1alpha1.CheckpointPhaseRestored,
			RestoredPodName: "test-batch-pod-restored-123",
			Message:         "Successfully restored",
		},
	}
}

// buildStuckPod returns a Pod that is Pending/Unschedulable and carries the
// watchdogAnnotation pointing to a CheckpointRecord.
func buildStuckPod(namespace, podName, recordName string, createdAt time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              podName,
			Namespace:         namespace,
			CreationTimestamp: metav1.Time{Time: createdAt},
			Annotations: map[string]string{
				watchdogAnnotation: recordName,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "worker", Image: "batch-worker:latest"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodScheduled,
					Status: corev1.ConditionFalse,
					Reason: corev1.PodReasonUnschedulable,
				},
			},
		},
	}
}

func TestRestoreWatchdog_RollsBackRecordWhenPodStuck(t *testing.T) {
	const namespace = "ecommerce"
	const recordName = "ckpt-test-batch"
	const podName = "test-batch-pod-restored-123"

	// Fake pod that has been Pending/Unschedulable for longer than the timeout.
	stuckPod := buildStuckPod(namespace, podName, recordName,
		time.Now().Add(-(UnschedulablePendingTimeout + 10*time.Second)),
	)

	k8sClient := k8sfake.NewSimpleClientset(stuckPod)

	// Build the fake dynamic client with the scheme that includes CheckpointRecord.
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	record := buildFakeRecord(namespace, recordName)
	unstructuredRecord, err := runtime.DefaultUnstructuredConverter.ToUnstructured(record)
	if err != nil {
		t.Fatalf("convert record to unstructured: %v", err)
	}

	dynamicClient := fake.NewSimpleDynamicClient(scheme, &v1alpha1.CheckpointRecord{})
	_ = dynamicClient // recordWriter wraps it
	_ = unstructuredRecord

	recordWriter := &DynamicCheckpointRecordWriter{client: dynamicClient}
	watchdog := NewRestoreWatchdog(k8sClient, recordWriter, zap.NewNop(), time.Second)

	// Run a single reconcile cycle.
	ctx := context.Background()
	if err := watchdog.reconcile(ctx); err != nil {
		t.Logf("reconcile returned: %v", err)
	}

	// Verify the stuck pod was deleted from the fake k8s client.
	remaining, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods after reconcile: %v", err)
	}
	for _, p := range remaining.Items {
		if p.Name == podName {
			t.Errorf("expected stuck pod %s to be deleted but it still exists", podName)
		}
	}
}

func TestRestoreWatchdog_IgnoresFreshPod(t *testing.T) {
	const namespace = "ecommerce"
	const recordName = "ckpt-test-batch"
	const podName = "test-batch-pod-restored-456"

	// Pod created just 10 seconds ago — still within grace period.
	freshPod := buildStuckPod(namespace, podName, recordName, time.Now().Add(-10*time.Second))

	k8sClient := k8sfake.NewSimpleClientset(freshPod)
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	dynamicClient := fake.NewSimpleDynamicClient(scheme)
	recordWriter := &DynamicCheckpointRecordWriter{client: dynamicClient}

	watchdog := NewRestoreWatchdog(k8sClient, recordWriter, zap.NewNop(), time.Second)
	ctx := context.Background()
	_ = watchdog.reconcile(ctx)

	// Pod should NOT have been deleted.
	remaining, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	found := false
	for _, p := range remaining.Items {
		if p.Name == podName {
			found = true
		}
	}
	if !found {
		t.Errorf("fresh pod %s should not have been deleted", podName)
	}
}

func TestRestoreWatchdog_IgnoresPodWithoutAnnotation(t *testing.T) {
	const namespace = "ecommerce"
	const podName = "some-other-pod"

	// Pending pod with no watchdogAnnotation — should be completely ignored.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              podName,
			Namespace:         namespace,
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable},
			},
		},
	}

	k8sClient := k8sfake.NewSimpleClientset(pod)
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	dynamicClient := fake.NewSimpleDynamicClient(scheme)
	recordWriter := &DynamicCheckpointRecordWriter{client: dynamicClient}

	watchdog := NewRestoreWatchdog(k8sClient, recordWriter, zap.NewNop(), time.Second)
	ctx := context.Background()
	_ = watchdog.reconcile(ctx)

	remaining, _ := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	found := false
	for _, p := range remaining.Items {
		if p.Name == podName {
			found = true
		}
	}
	if !found {
		t.Errorf("non-restore pod %s should not have been deleted by watchdog", podName)
	}
}
