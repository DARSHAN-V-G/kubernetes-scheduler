package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestMetricsCollector_IdleDurationTracking(t *testing.T) {
	// Create fake Kubernetes clientset
	clientset := fake.NewSimpleClientset()

	// Create test pod in fake API server
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "idle-worker",
			Labels:    map[string]string{"app": "worker"},
		},
		Spec: corev1.PodSpec{
			NodeName: "worker-node-1",
			Containers: []corev1.Container{
				{
					Name:  "worker",
					Image: "worker:latest",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase:    corev1.PodRunning,
			QOSClass: corev1.PodQOSBurstable,
		},
	}

	_, err := clientset.CoreV1().Pods("test-ns").Create(context.Background(), testPod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create fake pod: %v", err)
	}

	mockProm := &mockQuerier{
		responses: make(map[string]model.Value),
	}

	// 1. Initial State: Idle metrics (CPU = 5m, Net = 100 bytes/s, QPS = 0)
	mockProm.responses[Queries.ContainerCPU] = model.Vector{
		&model.Sample{
			Metric: model.Metric{"namespace": "test-ns", "pod": "idle-worker", "container": "worker"},
			Value:  5.0, // 5 millicores (below 20m threshold)
		},
	}
	mockProm.responses[Queries.ContainerMemWorking] = model.Vector{
		&model.Sample{
			Metric: model.Metric{"namespace": "test-ns", "pod": "idle-worker", "container": "worker"},
			Value:  30 * 1024 * 1024,
		},
	}
	mockProm.responses[Queries.PodNetworkRx] = model.Vector{
		&model.Sample{
			Metric: model.Metric{"namespace": "test-ns", "pod": "idle-worker"},
			Value:  50.0,
		},
	}
	mockProm.responses[Queries.PodNetworkTx] = model.Vector{
		&model.Sample{
			Metric: model.Metric{"namespace": "test-ns", "pod": "idle-worker"},
			Value:  50.0,
		},
	}
	mockProm.responses[Queries.PodAppQPS] = model.Vector{
		&model.Sample{
			Metric: model.Metric{"namespace": "test-ns", "pod": "idle-worker"},
			Value:  0.0,
		},
	}

	cfg := &CollectorConfig{
		ScrapeInterval:   100 * time.Millisecond,
		HTTPTimeout:      500 * time.Millisecond,
		WindowSize:       5,
		IdleCPUThreshold: 20.0,
		IdleNetThreshold: 1024.0,
		IdleQPSThreshold: 0.1,
		IdleMinDuration:  50 * time.Millisecond, // Short for testing
	}

	cache := NewMetricsCache(cfg.WindowSize)
	promClient := NewPrometheusClientWithQuerier(mockProm, zap.NewNop())
	collector := NewMetricsCollector(cfg, cache, promClient, clientset, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start informers
	if err := collector.informers.Start(ctx); err != nil {
		t.Fatalf("failed to start informers: %v", err)
	}

	// First collection cycle
	collector.collectTelemetry(ctx)

	// Wait 60ms to let idle duration exceed IdleMinDuration
	time.Sleep(60 * time.Millisecond)

	// Second collection cycle
	collector.collectTelemetry(ctx)

	pod, found := cache.GetPod("test-ns", "idle-worker")
	if !found {
		t.Fatalf("expected pod to be in cache")
	}

	if pod.TotalUsageCPUMillicores != 5.0 {
		t.Fatalf("expected 5.0 millicores CPU, got %f", pod.TotalUsageCPUMillicores)
	}
	if pod.IdleDuration <= 0 {
		t.Fatalf("expected IdleDuration > 0, got %v", pod.IdleDuration)
	}
	if !pod.IsIdle {
		t.Fatalf("expected pod to be marked IsIdle=true, got false (duration: %v)", pod.IdleDuration)
	}

	// 2. Activity burst arrives (CPU jumps to 150m)
	mockProm.responses[Queries.ContainerCPU] = model.Vector{
		&model.Sample{
			Metric: model.Metric{"namespace": "test-ns", "pod": "idle-worker", "container": "worker"},
			Value:  150.0, // Exceeds threshold!
		},
	}

	// Third collection cycle
	collector.collectTelemetry(ctx)

	podActive, _ := cache.GetPod("test-ns", "idle-worker")
	if podActive.TotalUsageCPUMillicores != 150.0 {
		t.Fatalf("expected 150.0 millicores CPU, got %f", podActive.TotalUsageCPUMillicores)
	}
	if podActive.IdleDuration != 0 {
		t.Fatalf("expected IdleDuration to reset to 0 upon activity, got %v", podActive.IdleDuration)
	}
	if podActive.IsIdle {
		t.Fatalf("expected IsIdle=false upon activity, got true")
	}
}
