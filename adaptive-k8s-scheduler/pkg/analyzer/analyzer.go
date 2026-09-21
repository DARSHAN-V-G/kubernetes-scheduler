// Package analyzer transforms raw telemetry from the Metrics Collector into a
// higher-level WorkloadProfile for consumption by the Idle Detector and
// Reclamation Decision Engine.
//
// The Analyzer is a pure consumer: it does not collect metrics, does not track
// idle duration, and does not query Prometheus or the Kubernetes API.
// All inputs come from the existing metrics.PodMetrics and metrics.MetricWindow.
package analyzer

import (
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// Analyze produces a WorkloadProfile by interpreting a pod metrics snapshot and
// its associated sliding-window telemetry history.
//
// It is a pure function: deterministic, no side effects, no I/O, no mutations
// to its inputs. Safe to call concurrently.
//
// If window is nil (pod not yet observed by the collector), window-derived fields
// are left at zero values and IsConsistentlyIdle is false.
//
// If cfg is nil, DefaultConfig() is used.
func Analyze(pod *metrics.PodMetrics, window *metrics.MetricWindow, cfg *Config) *WorkloadProfile {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	profile := &WorkloadProfile{
		PodNamespace: pod.Namespace,
		PodName:      pod.Name,
		AnalyzedAt:   time.Now(),
	}

	// ── Utilization ───────────────────────────────────────────────────────────

	profile.CPUUtilization, profile.CPUUtilStatus = computeUtilization(
		pod.TotalUsageCPUMillicores,
		float64(pod.TotalRequestedCPUMillis),
	)
	profile.MemoryUtilization, profile.MemoryUtilStatus = computeUtilization(
		float64(pod.TotalUsageMemoryBytes),
		float64(pod.TotalRequestedMemory),
	)

	// ── Window statistics ─────────────────────────────────────────────────────

	if window != nil {
		stats := window.ComputeStats(cfg.EMAAlpha)

		profile.AvgCPUMillicores = stats.AverageCPU
		profile.PeakCPUMillicores = stats.PeakCPU
		profile.AvgMemoryBytes = stats.AverageMemory
		profile.PeakMemoryBytes = stats.PeakMemory
		profile.AvgNetworkBytesPerSec = stats.AverageNetworkBytes
		profile.AvgQPS = stats.AverageQPS
		profile.SampleCount = stats.Count
		profile.WindowDuration = window.Duration()

		// ── Trend ─────────────────────────────────────────────────────────────

		profile.CPUTrend = computeTrend(
			window.GetSamples(),
			cfg.TrendRisingDelta,
			cfg.TrendFallingDelta,
		)

		// ── IsConsistentlyIdle re-validation ──────────────────────────────────
		// Uses the Analyzer own thresholds (not the collector thresholds) to confirm
		// that ALL samples in the current window are below idle thresholds.
		profile.IsConsistentlyIdle = window.IsConsistentlyBelowThresholds(
			cfg.IdleCPUMillicores,
			cfg.IdleNetBytesPerSec,
			cfg.IdleQPSThreshold,
		)
	}

	// ── Idle state passthrough from collector (NOT recomputed) ────────────────

	profile.IdleDuration = pod.IdleDuration
	profile.CollectorIsIdle = pod.IsIdle

	// ── Reclaimable resource estimates ────────────────────────────────────────

	profile.ReclaimableCPUMillicores = reclaimableCPU(
		float64(pod.TotalRequestedCPUMillis),
		profile.AvgCPUMillicores,
	)
	profile.ReclaimableMemoryBytes = reclaimableMemory(
		pod.TotalRequestedMemory,
		profile.AvgMemoryBytes,
	)

	return profile
}

// computeUtilization returns actual/requested and a status flag.
// Returns UtilizationUnavailable when requested <= 0 (BestEffort / no quota set).
// No node-level capacity fallback is used; downstream components must handle Unavailable.
func computeUtilization(actual, requested float64) (float64, UtilizationStatus) {
	if requested <= 0 {
		return 0, UtilizationUnavailable
	}
	return actual / requested, UtilizationAvailable
}

// computeTrend compares average CPU in the first and second halves of the sample window.
// A relative change greater than risingDelta identifies RISING; greater than fallingDelta
// identifies FALLING. Returns TrendStable for short windows or flat usage.
func computeTrend(samples []metrics.MetricSample, risingDelta, fallingDelta float64) Trend {
	if len(samples) < 2 {
		return TrendStable
	}

	mid := len(samples) / 2
	firstAvg := sampleAvgCPU(samples[:mid])
	secondAvg := sampleAvgCPU(samples[mid:])

	mean := (firstAvg + secondAvg) / 2.0
	if mean <= 0 {
		// Both halves are near zero — no meaningful trend.
		return TrendStable
	}

	relativeDelta := (secondAvg - firstAvg) / mean
	switch {
	case relativeDelta > risingDelta:
		return TrendRising
	case relativeDelta < -fallingDelta:
		return TrendFalling
	default:
		return TrendStable
	}
}

// sampleAvgCPU computes the arithmetic mean CPU (millicores) of a sample slice.
func sampleAvgCPU(samples []metrics.MetricSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += s.CPUMillicores
	}
	return sum / float64(len(samples))
}

// reclaimableCPU estimates the CPU millicores that could be freed if requests were
// right-sized to actual average usage. Returns 0 if actual usage exceeds requested.
func reclaimableCPU(requested, avgUsed float64) float64 {
	if r := requested - avgUsed; r > 0 {
		return r
	}
	return 0
}

// reclaimableMemory estimates the memory bytes that could be freed if requests were
// right-sized to actual average usage. Returns 0 if actual usage exceeds requested.
func reclaimableMemory(requested, avgUsed int64) int64 {
	if r := requested - avgUsed; r > 0 {
		return r
	}
	return 0
}
