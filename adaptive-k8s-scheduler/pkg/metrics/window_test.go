package metrics

import (
	"testing"
	"time"
)

func TestMetricWindow_AddSampleAndCount(t *testing.T) {
	window := NewMetricWindow(3)
	if window.Count() != 0 {
		t.Fatalf("expected initial count 0, got %d", window.Count())
	}

	now := time.Now()
	window.AddSample(MetricSample{Timestamp: now, CPUMillicores: 10})
	window.AddSample(MetricSample{Timestamp: now.Add(time.Second), CPUMillicores: 20})
	if window.Count() != 2 {
		t.Fatalf("expected count 2, got %d", window.Count())
	}

	window.AddSample(MetricSample{Timestamp: now.Add(2 * time.Second), CPUMillicores: 30})
	if window.Count() != 3 {
		t.Fatalf("expected count 3, got %d", window.Count())
	}

	// Adding 4th sample should wrap around and keep count at capacity 3
	window.AddSample(MetricSample{Timestamp: now.Add(3 * time.Second), CPUMillicores: 40})
	if window.Count() != 3 {
		t.Fatalf("expected count 3 after wrap-around, got %d", window.Count())
	}

	samples := window.GetSamples()
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}
	// Oldest should be 20, newest should be 40
	if samples[0].CPUMillicores != 20 || samples[1].CPUMillicores != 30 || samples[2].CPUMillicores != 40 {
		t.Fatalf("unexpected sample order: %+v", samples)
	}

	latest, ok := window.LatestSample()
	if !ok || latest.CPUMillicores != 40 {
		t.Fatalf("expected latest sample 40, got %+v", latest)
	}
}

func TestMetricWindow_ComputeStats(t *testing.T) {
	window := NewMetricWindow(5)
	now := time.Now()

	window.AddSample(MetricSample{Timestamp: now, CPUMillicores: 10, MemoryWorkingSet: 100, NetworkBytesPerSec: 1000, RequestQPS: 5})
	window.AddSample(MetricSample{Timestamp: now.Add(time.Second), CPUMillicores: 20, MemoryWorkingSet: 200, NetworkBytesPerSec: 2000, RequestQPS: 10})
	window.AddSample(MetricSample{Timestamp: now.Add(2 * time.Second), CPUMillicores: 30, MemoryWorkingSet: 300, NetworkBytesPerSec: 3000, RequestQPS: 15})

	stats := window.ComputeStats(0.5)
	if stats.Count != 3 {
		t.Fatalf("expected count 3, got %d", stats.Count)
	}
	if stats.AverageCPU != 20.0 {
		t.Fatalf("expected avg CPU 20.0, got %f", stats.AverageCPU)
	}
	if stats.PeakCPU != 30.0 {
		t.Fatalf("expected peak CPU 30.0, got %f", stats.PeakCPU)
	}
	if stats.AverageMemory != 200 {
		t.Fatalf("expected avg memory 200, got %d", stats.AverageMemory)
	}
	if stats.PeakMemory != 300 {
		t.Fatalf("expected peak memory 300, got %d", stats.PeakMemory)
	}
	if stats.AverageNetworkBytes != 2000.0 {
		t.Fatalf("expected avg network 2000.0, got %f", stats.AverageNetworkBytes)
	}
	if stats.AverageQPS != 10.0 {
		t.Fatalf("expected avg QPS 10.0, got %f", stats.AverageQPS)
	}
}

func TestMetricWindow_IsConsistentlyBelowThresholds(t *testing.T) {
	window := NewMetricWindow(3)
	now := time.Now()

	window.AddSample(MetricSample{Timestamp: now, CPUMillicores: 10, NetworkBytesPerSec: 100, RequestQPS: 0})
	window.AddSample(MetricSample{Timestamp: now.Add(time.Second), CPUMillicores: 15, NetworkBytesPerSec: 200, RequestQPS: 0})

	// Both are below (CPU <= 20, Net <= 500, QPS <= 1)
	if !window.IsConsistentlyBelowThresholds(20, 500, 1) {
		t.Fatal("expected samples to be below thresholds")
	}

	// Exceeds on CPU threshold
	if window.IsConsistentlyBelowThresholds(12, 500, 1) {
		t.Fatal("expected sample with CPU 15 to exceed threshold 12")
	}

	// Add high network burst
	window.AddSample(MetricSample{Timestamp: now.Add(2 * time.Second), CPUMillicores: 10, NetworkBytesPerSec: 5000, RequestQPS: 0})
	if window.IsConsistentlyBelowThresholds(20, 500, 1) {
		t.Fatal("expected burst sample to fail threshold")
	}
}
