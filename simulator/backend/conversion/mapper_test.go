package conversion

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"simulator/backend/models"
)

func TestToPodMetricsAndWindow(t *testing.T) {
	sw := models.SyntheticWorkload{
		Name:                 "test-workload",
		Namespace:            "default",
		NodeName:             "node-1",
		Phase:                "Running",
		QoSClass:             "Burstable",
		Priority:             100,
		PriorityClassName:    "default-priority",
		DisruptionsAllowed:   2,
		OwnerKind:            "Deployment",
		OwnerName:            "test-deploy",
		DesiredReplicas:      3,
		ReadyReplicas:        3,
		AvailableReplicas:    3,
		RequestedCPUMillis:   1000,
		LimitCPUMillis:       2000,
		RequestedMemoryBytes: 1024 * 1024 * 1024,
		LimitMemoryBytes:     2048 * 1024 * 1024,
		UsageCPUMillicores:   25.5,
		UsageMemoryBytes:     50 * 1024 * 1024,
		NetworkBytesPerSec:   150.0,
		RequestQPS:           0.5,
		IdleDurationSeconds:  120,
		IsIdle:               true,
		Labels:               map[string]string{"app": "test"},
		Annotations:          map[string]string{"note": "synthetic"},
	}

	pod, window := ToPodMetricsAndWindow(sw)

	if pod.Name != sw.Name || pod.Namespace != sw.Namespace || pod.NodeName != sw.NodeName {
		t.Errorf("pod identity mismatch: got %s/%s on %s", pod.Namespace, pod.Name, pod.NodeName)
	}
	if pod.Phase != corev1.PodRunning {
		t.Errorf("expected phase PodRunning, got %v", pod.Phase)
	}
	if pod.TotalRequestedCPUMillis != sw.RequestedCPUMillis {
		t.Errorf("expected CPU request %d, got %d", sw.RequestedCPUMillis, pod.TotalRequestedCPUMillis)
	}
	if pod.TotalUsageCPUMillicores != sw.UsageCPUMillicores {
		t.Errorf("expected CPU usage %f, got %f", sw.UsageCPUMillicores, pod.TotalUsageCPUMillicores)
	}
	if pod.IdleDuration != 120*time.Second {
		t.Errorf("expected idle duration 120s, got %v", pod.IdleDuration)
	}
	if !pod.IsIdle {
		t.Errorf("expected pod.IsIdle to be true")
	}
	if pod.Replicas == nil || pod.Replicas.OwnerKind != "Deployment" || pod.Replicas.AvailableReplicas != 3 {
		t.Errorf("replicas mapping failed: %+v", pod.Replicas)
	}
	if pod.Labels["app"] != "test" || pod.Annotations["note"] != "synthetic" {
		t.Errorf("labels or annotations mismatch")
	}

	if window == nil {
		t.Fatalf("expected non-nil MetricWindow")
	}
	if window.Count() != 5 {
		t.Errorf("expected 5 samples in window, got %d", window.Count())
	}
}

func TestToNodeMetrics(t *testing.T) {
	sn := models.SyntheticNode{
		Name:                     "node-1",
		TotalCapacityCPUMillis:   4000,
		TotalCapacityMemoryBytes: 8192 * 1024 * 1024,
		AllocatableCPUMillis:     3800,
		AllocatableMemoryBytes:   7800 * 1024 * 1024,
		ActualUsageCPUMillicores: 1200.0,
		ActualUsageMemoryBytes:   2048 * 1024 * 1024,
		IsReady:                  true,
	}

	node := ToNodeMetrics(sn)
	if node.Name != sn.Name {
		t.Errorf("expected node name %s, got %s", sn.Name, node.Name)
	}
	if !node.IsReady {
		t.Errorf("expected node to be ready")
	}
	expectedRealFreeCPU := float64(sn.AllocatableCPUMillis) - sn.ActualUsageCPUMillicores
	if node.RealFreeCPUMillicores != expectedRealFreeCPU {
		t.Errorf("expected real free CPU %f, got %f", expectedRealFreeCPU, node.RealFreeCPUMillicores)
	}
}
