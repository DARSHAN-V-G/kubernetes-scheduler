package detector

import (
	"testing"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// idleProfile builds a fully-idle WorkloadProfile (all signals below default thresholds).
func idleProfile() *analyzer.WorkloadProfile {
	return &analyzer.WorkloadProfile{
		PodNamespace:          "ns",
		PodName:               "pod",
		CPUUtilization:        0.05, // 5% — below 30% threshold
		CPUUtilStatus:         analyzer.UtilizationAvailable,
		MemoryUtilization:     0.10, // 10% — below 30% threshold
		MemoryUtilStatus:      analyzer.UtilizationAvailable,
		AvgQPS:                0,
		AvgNetworkBytesPerSec: 100,              // below 10240 threshold
		IdleDuration:          60 * time.Second, // above 30s minimum
		IsConsistentlyIdle:    true,
		SampleCount:           5, // above minimum 3
	}
}

// ── Table-driven classification tests ─────────────────────────────────────────

func TestClassify_Active_HighCPU(t *testing.T) {
	p := idleProfile()
	p.CPUUtilization = 0.80 // 80% — well above 30% threshold
	result := Classify(p, DefaultConfig())
	if result.Class != ClassActive {
		t.Errorf("high CPU: expected ACTIVE, got %s", result.Class)
	}
}

func TestClassify_Active_QPSAboveZero(t *testing.T) {
	// CPU and memory are idle, but QPS > 0 → workload is serving requests → ACTIVE.
	p := idleProfile()
	p.AvgQPS = 1.5
	result := Classify(p, DefaultConfig())
	if result.Class != ClassActive {
		t.Errorf("QPS > 0: expected ACTIVE, got %s", result.Class)
	}
}

func TestClassify_Active_HighNetwork(t *testing.T) {
	p := idleProfile()
	p.AvgNetworkBytesPerSec = 50000 // 50 KB/s — above 10240 threshold
	result := Classify(p, DefaultConfig())
	if result.Class != ClassActive {
		t.Errorf("high network: expected ACTIVE, got %s", result.Class)
	}
}

func TestClassify_Active_HighMemory(t *testing.T) {
	p := idleProfile()
	p.MemoryUtilization = 0.70 // 70% — above 30% threshold
	result := Classify(p, DefaultConfig())
	if result.Class != ClassActive {
		t.Errorf("high memory: expected ACTIVE, got %s", result.Class)
	}
}

func TestClassify_LowUsage_DurationNotMet(t *testing.T) {
	p := idleProfile()
	p.IdleDuration = 10 * time.Second // below 30s minimum
	result := Classify(p, DefaultConfig())
	if result.Class != ClassLowUsage {
		t.Errorf("short duration: expected LOW_USAGE, got %s", result.Class)
	}
}

func TestClassify_LowUsage_InsufficientSamples(t *testing.T) {
	p := idleProfile()
	p.SampleCount = 2 // below minimum 3
	result := Classify(p, DefaultConfig())
	if result.Class != ClassLowUsage {
		t.Errorf("insufficient samples: expected LOW_USAGE, got %s", result.Class)
	}
}

func TestClassify_LowUsage_NotConsistentlyIdle(t *testing.T) {
	p := idleProfile()
	p.IsConsistentlyIdle = false // window had a burst
	result := Classify(p, DefaultConfig())
	if result.Class != ClassLowUsage {
		t.Errorf("inconsistent idle: expected LOW_USAGE, got %s", result.Class)
	}
}

func TestClassify_Idle_AllConditionsMet(t *testing.T) {
	p := idleProfile()
	result := Classify(p, DefaultConfig())
	if result.Class != ClassIdle {
		t.Errorf("fully idle profile: expected IDLE, got %s\nReasons: %v", result.Class, result.Reasons)
	}
}

func TestClassify_CPUThresholdBoundary_ExactlyAtThreshold_NotIdle(t *testing.T) {
	// CPU utilization == threshold → NOT below threshold → ACTIVE.
	p := idleProfile()
	p.CPUUtilization = 0.30 // exactly at threshold
	result := Classify(p, DefaultConfig())
	if result.Class != ClassActive {
		t.Errorf("CPU exactly at threshold: expected ACTIVE, got %s", result.Class)
	}
}

func TestClassify_CPUThresholdBoundary_JustBelowThreshold(t *testing.T) {
	p := idleProfile()
	p.CPUUtilization = 0.299 // just below threshold
	result := Classify(p, DefaultConfig())
	// CPU signal is fine; all other conditions met → IDLE.
	if result.Class != ClassIdle {
		t.Errorf("CPU just below threshold: expected IDLE, got %s", result.Class)
	}
}

func TestClassify_MemoryThresholdBoundary_ExactlyAtThreshold_NotIdle(t *testing.T) {
	p := idleProfile()
	p.MemoryUtilization = 0.30 // exactly at threshold → ACTIVE
	result := Classify(p, DefaultConfig())
	if result.Class != ClassActive {
		t.Errorf("memory exactly at threshold: expected ACTIVE, got %s", result.Class)
	}
}

func TestClassify_IdleDurationBoundary_ExactlyAtMinimum(t *testing.T) {
	p := idleProfile()
	p.IdleDuration = 30 * time.Second // exactly at minimum → should qualify
	result := Classify(p, DefaultConfig())
	if result.Class != ClassIdle {
		t.Errorf("idle duration exactly at minimum: expected IDLE, got %s", result.Class)
	}
}

func TestClassify_BestEffortCPU_NotTreatedAsActive(t *testing.T) {
	// BestEffort pod has no CPU request → UtilizationUnavailable.
	// The CPU signal must NOT cause ACTIVE classification.
	p := idleProfile()
	p.CPUUtilization = 0
	p.CPUUtilStatus = analyzer.UtilizationUnavailable
	result := Classify(p, DefaultConfig())
	// Should still be IDLE (all other conditions met).
	if result.Class != ClassIdle {
		t.Errorf("BestEffort CPU unavailable: expected IDLE, got %s", result.Class)
	}
}

func TestClassify_BestEffortMemory_NotTreatedAsActive(t *testing.T) {
	p := idleProfile()
	p.MemoryUtilization = 0
	p.MemoryUtilStatus = analyzer.UtilizationUnavailable
	result := Classify(p, DefaultConfig())
	if result.Class != ClassIdle {
		t.Errorf("BestEffort memory unavailable: expected IDLE, got %s", result.Class)
	}
}

func TestClassify_ReasonsAlwaysPopulated(t *testing.T) {
	cases := []*analyzer.WorkloadProfile{
		idleProfile(),
		func() *analyzer.WorkloadProfile { p := idleProfile(); p.CPUUtilization = 0.9; return p }(),
		func() *analyzer.WorkloadProfile { p := idleProfile(); p.IdleDuration = 5 * time.Second; return p }(),
	}
	for _, p := range cases {
		result := Classify(p, DefaultConfig())
		if len(result.Reasons) == 0 {
			t.Errorf("class %s: expected non-empty Reasons", result.Class)
		}
	}
}

func TestClassify_IdleDurationInResult(t *testing.T) {
	p := idleProfile()
	p.IdleDuration = 2 * time.Minute
	result := Classify(p, DefaultConfig())
	if result.IdleDuration != 2*time.Minute {
		t.Errorf("IdleDuration not passed through: got %v, want 2m", result.IdleDuration)
	}
}

func TestClassify_CustomConfig(t *testing.T) {
	// Use a very low idle threshold so workload is classified ACTIVE.
	cfg := &Config{
		CPUIdleThresholdPct:    0.01, // 1%
		MemoryIdleThresholdPct: 0.30,
		QPSIdleThreshold:       0.0,
		NetIdleThresholdBytes:  10240,
		MinIdleDuration:        5 * time.Second,
		MinSampleCount:         1,
	}
	p := idleProfile()
	p.CPUUtilization = 0.05 // 5% — above custom 1% threshold → ACTIVE
	result := Classify(p, cfg)
	if result.Class != ClassActive {
		t.Errorf("custom threshold: expected ACTIVE, got %s", result.Class)
	}
}
