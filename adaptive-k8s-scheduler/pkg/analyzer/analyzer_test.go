package analyzer

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// makePod builds a minimal PodMetrics for tests. Accepts requested CPU (millicores)
// and memory (bytes), actual usage CPU (millicores) and memory (bytes).
func makePod(reqCPU int64, reqMem int64, usageCPU float64, usageMem int64) *metrics.PodMetrics {
	return &metrics.PodMetrics{
		Namespace:               "test-ns",
		Name:                    "test-pod",
		Phase:                   corev1.PodRunning,
		TotalRequestedCPUMillis: reqCPU,
		TotalRequestedMemory:    reqMem,
		TotalUsageCPUMillicores: usageCPU,
		TotalUsageMemoryBytes:   usageMem,
		IsIdle:                  false,
		IdleDuration:            0,
		LastUpdated:             time.Now(),
	}
}

// makeWindow creates a MetricWindow and adds the provided CPU samples with default 50MB memory.
func makeWindow(cpuValues []float64) *metrics.MetricWindow {
	return makeWindowWithMem(cpuValues, 50*1024*1024)
}

// makeWindowWithMem creates a MetricWindow with specified CPU values and memory working set.
func makeWindowWithMem(cpuValues []float64, memBytes int64) *metrics.MetricWindow {
	w := metrics.NewMetricWindow(len(cpuValues) + 1)
	now := time.Now()
	for i, cpu := range cpuValues {
		w.AddSample(metrics.MetricSample{
			Timestamp:          now.Add(time.Duration(i) * 10 * time.Second),
			CPUMillicores:      cpu,
			MemoryWorkingSet:   memBytes,
			NetworkBytesPerSec: 100,
			RequestQPS:         0,
		})
	}
	return w
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestAnalyze_SingleSample(t *testing.T) {
	pod := makePod(1000, 512*1024*1024, 100, 100*1024*1024)
	w := makeWindow([]float64{100})

	profile := Analyze(pod, w, DefaultConfig())

	if profile.PodNamespace != "test-ns" {
		t.Errorf("expected namespace test-ns, got %s", profile.PodNamespace)
	}
	if profile.CPUUtilStatus != UtilizationAvailable {
		t.Error("expected CPUUtilStatus to be Available")
	}
	// 100m / 1000m = 0.10
	if profile.CPUUtilization != 0.10 {
		t.Errorf("expected CPUUtilization 0.10, got %f", profile.CPUUtilization)
	}
	if profile.SampleCount != 1 {
		t.Errorf("expected SampleCount 1, got %d", profile.SampleCount)
	}
}

func TestAnalyze_CPUUtilization_CorrectRatio(t *testing.T) {
	tests := []struct {
		name       string
		reqCPU     int64
		usageCPU   float64
		wantUtil   float64
		wantStatus UtilizationStatus
	}{
		{"10% utilization", 1000, 100, 0.10, UtilizationAvailable},
		{"100% utilization", 500, 500, 1.00, UtilizationAvailable},
		{"over 100%", 200, 300, 1.50, UtilizationAvailable},
		{"zero request BestEffort", 0, 50, 0, UtilizationUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pod := makePod(tc.reqCPU, 512*1024*1024, tc.usageCPU, 50*1024*1024)
			profile := Analyze(pod, makeWindow([]float64{tc.usageCPU}), DefaultConfig())

			if profile.CPUUtilStatus != tc.wantStatus {
				t.Errorf("CPUUtilStatus: got %v, want %v", profile.CPUUtilStatus, tc.wantStatus)
			}
			if tc.wantStatus == UtilizationAvailable && profile.CPUUtilization != tc.wantUtil {
				t.Errorf("CPUUtilization: got %f, want %f", profile.CPUUtilization, tc.wantUtil)
			}
		})
	}
}

func TestAnalyze_MemoryUtilization_ZeroRequest(t *testing.T) {
	pod := makePod(500, 0, 100, 200*1024*1024)
	profile := Analyze(pod, makeWindow([]float64{100}), DefaultConfig())

	if profile.MemoryUtilStatus != UtilizationUnavailable {
		t.Errorf("expected MemoryUtilStatus Unavailable for zero request, got %v", profile.MemoryUtilStatus)
	}
}

func TestAnalyze_ReclaimableResources(t *testing.T) {
	// Requested 1000m, average usage 200m → 800m reclaimable.
	// Requested 512MiB, average usage 50MiB → 462MiB reclaimable.
	cpuSamples := []float64{150, 200, 250} // avg = 200m
	pod := makePod(1000, 512*1024*1024, 200, 50*1024*1024)
	w := metrics.NewMetricWindow(10)
	now := time.Now()
	for i, cpu := range cpuSamples {
		w.AddSample(metrics.MetricSample{
			Timestamp:        now.Add(time.Duration(i) * 10 * time.Second),
			CPUMillicores:    cpu,
			MemoryWorkingSet: 50 * 1024 * 1024,
		})
	}

	profile := Analyze(pod, w, DefaultConfig())

	// AvgCPU from window stats ≈ 200m; reclaimable = 1000 - 200 = 800m.
	if profile.ReclaimableCPUMillicores <= 0 {
		t.Errorf("expected positive ReclaimableCPUMillicores, got %f", profile.ReclaimableCPUMillicores)
	}
	if profile.ReclaimableMemoryBytes <= 0 {
		t.Errorf("expected positive ReclaimableMemoryBytes, got %d", profile.ReclaimableMemoryBytes)
	}
}

func TestAnalyze_ReclaimableResources_ZeroWhenUsageExceedsRequest(t *testing.T) {
	// Usage exceeds request — reclaimable should be 0, not negative.
	pod := makePod(100, 64*1024*1024, 200, 200*1024*1024)
	profile := Analyze(pod, makeWindowWithMem([]float64{200}, 200*1024*1024), DefaultConfig())

	if profile.ReclaimableCPUMillicores != 0 {
		t.Errorf("expected 0 reclaimable CPU when usage exceeds request, got %f", profile.ReclaimableCPUMillicores)
	}
	if profile.ReclaimableMemoryBytes != 0 {
		t.Errorf("expected 0 reclaimable memory when usage exceeds request, got %d", profile.ReclaimableMemoryBytes)
	}
}

func TestAnalyze_CPUTrend_Rising(t *testing.T) {
	// Samples increasing strongly: 10, 10, 10, 100, 100, 100.
	// First half avg ≈ 10, second half avg ≈ 100 → RISING.
	samples := []float64{10, 10, 10, 100, 100, 100}
	pod := makePod(500, 256*1024*1024, 50, 50*1024*1024)
	w := metrics.NewMetricWindow(10)
	now := time.Now()
	for i, cpu := range samples {
		w.AddSample(metrics.MetricSample{
			Timestamp:     now.Add(time.Duration(i) * 10 * time.Second),
			CPUMillicores: cpu,
		})
	}

	profile := Analyze(pod, w, DefaultConfig())

	if profile.CPUTrend != TrendRising {
		t.Errorf("expected TrendRising, got %s", profile.CPUTrend)
	}
}

func TestAnalyze_CPUTrend_Falling(t *testing.T) {
	samples := []float64{100, 100, 100, 10, 10, 10}
	pod := makePod(500, 256*1024*1024, 50, 50*1024*1024)
	w := metrics.NewMetricWindow(10)
	now := time.Now()
	for i, cpu := range samples {
		w.AddSample(metrics.MetricSample{
			Timestamp:     now.Add(time.Duration(i) * 10 * time.Second),
			CPUMillicores: cpu,
		})
	}

	profile := Analyze(pod, w, DefaultConfig())

	if profile.CPUTrend != TrendFalling {
		t.Errorf("expected TrendFalling, got %s", profile.CPUTrend)
	}
}

func TestAnalyze_CPUTrend_Stable(t *testing.T) {
	samples := []float64{50, 52, 49, 51, 50, 48}
	pod := makePod(500, 256*1024*1024, 50, 50*1024*1024)
	w := metrics.NewMetricWindow(10)
	now := time.Now()
	for i, cpu := range samples {
		w.AddSample(metrics.MetricSample{
			Timestamp:     now.Add(time.Duration(i) * 10 * time.Second),
			CPUMillicores: cpu,
		})
	}

	profile := Analyze(pod, w, DefaultConfig())

	if profile.CPUTrend != TrendStable {
		t.Errorf("expected TrendStable, got %s", profile.CPUTrend)
	}
}

func TestAnalyze_IsConsistentlyIdle_True(t *testing.T) {
	// All samples well below default idle threshold (20m CPU, 10240 net, 0 QPS).
	pod := makePod(500, 256*1024*1024, 5, 20*1024*1024)
	w := metrics.NewMetricWindow(5)
	now := time.Now()
	for i := 0; i < 5; i++ {
		w.AddSample(metrics.MetricSample{
			Timestamp:          now.Add(time.Duration(i) * 10 * time.Second),
			CPUMillicores:      5,
			NetworkBytesPerSec: 100,
			RequestQPS:         0,
		})
	}

	profile := Analyze(pod, w, DefaultConfig())

	if !profile.IsConsistentlyIdle {
		t.Error("expected IsConsistentlyIdle true when all samples below thresholds")
	}
}

func TestAnalyze_IsConsistentlyIdle_False_WhenOneSampleAboveThreshold(t *testing.T) {
	pod := makePod(500, 256*1024*1024, 5, 20*1024*1024)
	w := metrics.NewMetricWindow(5)
	now := time.Now()
	// Four idle samples, one burst.
	for i := 0; i < 4; i++ {
		w.AddSample(metrics.MetricSample{
			Timestamp:     now.Add(time.Duration(i) * 10 * time.Second),
			CPUMillicores: 5,
		})
	}
	// One sample above idle CPU threshold.
	w.AddSample(metrics.MetricSample{
		Timestamp:     now.Add(40 * time.Second),
		CPUMillicores: 50, // above default 20m threshold
	})

	profile := Analyze(pod, w, DefaultConfig())

	if profile.IsConsistentlyIdle {
		t.Error("expected IsConsistentlyIdle false when one sample exceeds threshold")
	}
}

func TestAnalyze_IdleDurationPassthrough(t *testing.T) {
	// IdleDuration must equal exactly what the collector set — not recomputed.
	expectedDuration := 5 * time.Minute
	pod := makePod(500, 256*1024*1024, 5, 20*1024*1024)
	pod.IdleDuration = expectedDuration
	pod.IsIdle = true

	profile := Analyze(pod, makeWindow([]float64{5}), DefaultConfig())

	if profile.IdleDuration != expectedDuration {
		t.Errorf("IdleDuration passthrough failed: got %v, want %v", profile.IdleDuration, expectedDuration)
	}
	if !profile.CollectorIsIdle {
		t.Error("CollectorIsIdle passthrough failed: expected true")
	}
}

func TestAnalyze_NilWindow(t *testing.T) {
	// nil window should produce a valid profile with zero window fields.
	pod := makePod(1000, 512*1024*1024, 100, 100*1024*1024)

	profile := Analyze(pod, nil, DefaultConfig())

	if profile.SampleCount != 0 {
		t.Errorf("expected SampleCount 0 for nil window, got %d", profile.SampleCount)
	}
	if profile.IsConsistentlyIdle {
		t.Error("expected IsConsistentlyIdle false for nil window")
	}
	// Utilization is still computed from pod fields.
	if profile.CPUUtilStatus != UtilizationAvailable {
		t.Error("expected CPUUtilStatus Available even with nil window")
	}
}

func TestAnalyze_WindowDurationPassthrough(t *testing.T) {
	w := metrics.NewMetricWindow(5)
	now := time.Now()
	w.AddSample(metrics.MetricSample{Timestamp: now, CPUMillicores: 10})
	w.AddSample(metrics.MetricSample{Timestamp: now.Add(30 * time.Second), CPUMillicores: 10})

	pod := makePod(500, 256*1024*1024, 10, 50*1024*1024)
	profile := Analyze(pod, w, DefaultConfig())

	if profile.WindowDuration < 25*time.Second {
		t.Errorf("expected WindowDuration ~30s, got %v", profile.WindowDuration)
	}
}
