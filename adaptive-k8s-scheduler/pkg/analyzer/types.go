package analyzer

import "time"

// UtilizationStatus distinguishes a computed utilization ratio from a case where
// the ratio is mathematically undefined (BestEffort pod with zero resource requests).
// Downstream components handle UtilizationUnavailable explicitly — no fallback to
// node-level capacity is performed here.
type UtilizationStatus int

const (
	// UtilizationAvailable means the utilization ratio was computed successfully.
	UtilizationAvailable UtilizationStatus = iota

	// UtilizationUnavailable means TotalRequestedCPU or TotalRequestedMemory is zero
	// (BestEffort pod). The utilization field is meaningless in this case.
	// The Decision Engine assigns a neutral score (0.5) for Unavailable dimensions.
	UtilizationUnavailable
)

// Trend describes the directional movement of CPU usage over the observation window.
type Trend int

const (
	// TrendStable means CPU has not moved significantly across the window.
	TrendStable Trend = iota
	// TrendRising means CPU is increasing (second-half avg > first-half avg by more than delta).
	TrendRising
	// TrendFalling means CPU is decreasing (first-half avg > second-half avg by more than delta).
	TrendFalling
)

// String returns a human-readable label for the Trend.
func (t Trend) String() string {
	switch t {
	case TrendRising:
		return "RISING"
	case TrendFalling:
		return "FALLING"
	default:
		return "STABLE"
	}
}

// WorkloadProfile is the Analyzer output: a workload-level interpretation of raw
// telemetry produced by the Metrics Collector. It is a plain data structure with no
// behaviour — produced by Analyze() and consumed by the Idle Detector and Decision Engine.
//
// Design principle: the Analyzer does NOT collect metrics. It interprets what the
// Metrics Collector has already produced. IdleDuration and CollectorIsIdle are passed
// through from the collector unchanged; they are not recomputed here.
type WorkloadProfile struct {
	PodNamespace string
	PodName      string

	// ── Utilization relative to requested resource quota ──────────────────────

	// CPUUtilization is TotalUsageCPUMillicores / TotalRequestedCPUMillis.
	// Valid only when CPUUtilStatus == UtilizationAvailable.
	CPUUtilization float64
	// CPUUtilStatus is UtilizationUnavailable when TotalRequestedCPUMillis == 0 (BestEffort).
	CPUUtilStatus UtilizationStatus

	// MemoryUtilization is TotalUsageMemoryBytes / TotalRequestedMemory.
	// Valid only when MemoryUtilStatus == UtilizationAvailable.
	MemoryUtilization float64
	// MemoryUtilStatus is UtilizationUnavailable when TotalRequestedMemory == 0 (BestEffort).
	MemoryUtilStatus UtilizationStatus

	// ── Window-based statistics (delegated to MetricWindow.ComputeStats) ──────

	AvgCPUMillicores      float64
	PeakCPUMillicores     float64
	AvgMemoryBytes        int64
	PeakMemoryBytes       int64
	AvgNetworkBytesPerSec float64
	AvgQPS                float64

	// ── Trend ─────────────────────────────────────────────────────────────────

	// CPUTrend compares mean CPU in the first vs second half of the window.
	CPUTrend Trend

	// ── Idle state (passed through from PodMetrics — NOT recomputed) ──────────

	// IdleDuration is PodMetrics.IdleDuration exactly as tracked by the collector.
	IdleDuration time.Duration
	// CollectorIsIdle is PodMetrics.IsIdle exactly as set by the collector.
	CollectorIsIdle bool

	// IsConsistentlyIdle is independently validated by the Analyzer via the MetricWindow.
	// True when ALL samples in the current window are below the analyzer idle thresholds.
	// This is distinct from CollectorIsIdle which uses the collector own raw thresholds.
	IsConsistentlyIdle bool

	// ── Reclaimable resource estimates ────────────────────────────────────────

	// ReclaimableCPUMillicores estimates CPU quota that could be freed:
	// max(0, TotalRequestedCPUMillis - AvgCPUMillicores).
	ReclaimableCPUMillicores float64

	// ReclaimableMemoryBytes estimates memory quota that could be freed:
	// max(0, TotalRequestedMemory - AvgMemoryBytes).
	ReclaimableMemoryBytes int64

	// ── Window metadata ───────────────────────────────────────────────────────

	SampleCount    int
	WindowDuration time.Duration
	AnalyzedAt     time.Time
}
