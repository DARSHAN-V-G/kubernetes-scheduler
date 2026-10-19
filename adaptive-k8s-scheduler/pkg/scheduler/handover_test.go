package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
)

func TestHandoverManager_StandalonePod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-standalone-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app": "batch-job",
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName: "adaptive-scheduler",
			Containers: []corev1.Container{
				{
					Name:  "worker",
					Image: "alpine:latest",
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(pod)
	fakeRecorder := record.NewFakeRecorder(10)
	mgr := NewHandoverManager(fakeClient, fakeRecorder, "default-scheduler", nil)

	ctx := context.Background()
	err := mgr.HandoverPod(ctx, pod, "0/1 nodes available: insufficient real physical headroom")
	if err != nil {
		t.Fatalf("HandoverPod failed: %v", err)
	}

	// Verify new pod exists in client with default-scheduler
	recreatedPod, err := fakeClient.CoreV1().Pods("default").Get(ctx, "test-standalone-pod", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed fetching recreated pod: %v", err)
	}

	if recreatedPod.Spec.SchedulerName != "default-scheduler" {
		t.Errorf("expected schedulerName 'default-scheduler', got '%s'", recreatedPod.Spec.SchedulerName)
	}

	if recreatedPod.Annotations["reclaim.io/handed-over-from"] != "adaptive-scheduler" {
		t.Errorf("expected handover annotation on recreated pod, got %v", recreatedPod.Annotations)
	}

	// Verify event was recorded
	select {
	case event := <-fakeRecorder.Events:
		if event == "" {
			t.Errorf("expected non-empty event")
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timed out waiting for handover event")
	}
}

func TestHandoverManager_DeploymentOwnedPod(t *testing.T) {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-server",
			Namespace: "ecommerce",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					SchedulerName: "adaptive-scheduler",
				},
			},
		},
	}

	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-server-rs-123",
			Namespace: "ecommerce",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "Deployment",
					Name: "api-server",
				},
			},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-server-pod-xyz",
			Namespace: "ecommerce",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind: "ReplicaSet",
					Name: "api-server-rs-123",
				},
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName: "adaptive-scheduler",
		},
	}

	fakeClient := fake.NewSimpleClientset(deployment, rs, pod)
	fakeRecorder := record.NewFakeRecorder(10)
	mgr := NewHandoverManager(fakeClient, fakeRecorder, "default-scheduler", nil)

	ctx := context.Background()
	err := mgr.HandoverPod(ctx, pod, "insufficient physical memory")
	if err != nil {
		t.Fatalf("HandoverPod failed for deployment: %v", err)
	}

	// Verify deployment template was updated
	updatedDeploy, err := fakeClient.AppsV1().Deployments("ecommerce").Get(ctx, "api-server", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("failed fetching updated deployment: %v", err)
	}

	if updatedDeploy.Spec.Template.Spec.SchedulerName != "default-scheduler" {
		t.Errorf("expected deployment template schedulerName 'default-scheduler', got '%s'",
			updatedDeploy.Spec.Template.Spec.SchedulerName)
	}
}

func TestHandoverManager_HasReclaimableCandidates(t *testing.T) {
	cache := metrics.NewMetricsCache(10)
	mgr := NewHandoverManager(nil, nil, "default-scheduler", nil)

	// No pods -> false
	if mgr.HasReclaimableCandidates(cache) {
		t.Errorf("expected false for empty cache")
	}

	// Active pod with high CPU usage -> false
	cache.SetPod(&metrics.PodMetrics{
		Namespace:                "default",
		Name:                     "heavy-app",
		Phase:                    corev1.PodRunning,
		TotalUsageCPUMillicores: 500.0,
	})
	if mgr.HasReclaimableCandidates(cache) {
		t.Errorf("expected false for high-usage pod")
	}

	// Idle pod with low CPU usage -> true
	cache.SetPod(&metrics.PodMetrics{
		Namespace:                "default",
		Name:                     "idle-app",
		Phase:                    corev1.PodRunning,
		TotalUsageCPUMillicores: 5.0,
	})
	if !mgr.HasReclaimableCandidates(cache) {
		t.Errorf("expected true when idle pod with 5m CPU exists")
	}
}
