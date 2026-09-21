// Package detector implements the Idle Classifier — the second stage of the
// intelligence pipeline. It consumes a WorkloadProfile from the Analyzer and
// applies the project idle-classification policy to produce a ClassificationResult.
//
// Classification outcomes:
//   - ACTIVE:    any signal (CPU, memory, QPS, network) is above its idle threshold.
//   - LOW_USAGE: all signals are below thresholds but duration/sample requirements are not met.
//   - IDLE:      all signals below thresholds AND duration >= MinIdleDuration
//                AND samples >= MinSampleCount AND IsConsistentlyIdle is true.
//
// The Detector does NOT recompute IdleDuration — it uses the value from the
// WorkloadProfile, which was carried through from the Metrics Collector.
package detector

import (
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
)

// Classify evaluates a WorkloadProfile against the idle classification policy
// and returns an explainable ClassificationResult.
//
// This is a pure function: deterministic, no I/O, no side effects.
// If cfg is nil, DefaultConfig() is used.
func Classify(profile *analyzer.WorkloadProfile, cfg *Config) ClassificationResult {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	result := ClassificationResult{
		IdleDuration: profile.IdleDuration,
		ClassifiedAt: time.Now(),
	}

	// ── Evaluate each signal independently ────────────────────────────────────

	cpuActive, cpuReason := signalCPU(profile, cfg)
	memActive, memReason := signalMemory(profile, cfg)
	qpsActive, qpsReason := signalQPS(profile, cfg)
	netActive, netReason := signalNetwork(profile, cfg)

	result.Reasons = append(result.Reasons, cpuReason, memReason, qpsReason, netReason)

	// ── Any active signal → ACTIVE ─────────────────────────────────────────────

	if cpuActive || memActive || qpsActive || netActive {
		result.Class = ClassActive
		return result
	}

	// ── All signals below threshold — check duration and consistency ───────────

	durationMet := profile.IdleDuration >= cfg.MinIdleDuration
	samplesMet := profile.SampleCount >= cfg.MinSampleCount
	consistencyMet := profile.IsConsistentlyIdle

	if durationMet && samplesMet && consistencyMet {
		result.Class = ClassIdle
		result.Reasons = append(result.Reasons,
			fmt.Sprintf("idle duration satisfied: %v >= %v",
				profile.IdleDuration.Round(time.Second), cfg.MinIdleDuration),
			fmt.Sprintf("sample count satisfied: %d >= %d",
				profile.SampleCount, cfg.MinSampleCount),
			"all window samples consistently below idle thresholds",
		)
		return result
	}

	// ── LOW_USAGE: signals fine but not yet sustained ─────────────────────────

	result.Class = ClassLowUsage
	if !durationMet {
		result.Reasons = append(result.Reasons,
			fmt.Sprintf("idle duration not yet met: %v < %v",
				profile.IdleDuration.Round(time.Second), cfg.MinIdleDuration))
	}
	if !samplesMet {
		result.Reasons = append(result.Reasons,
			fmt.Sprintf("insufficient samples: %d < %d (need more observations)",
				profile.SampleCount, cfg.MinSampleCount))
	}
	if !consistencyMet {
		result.Reasons = append(result.Reasons,
			"window not consistently below thresholds (burst observed within window)")
	}

	return result
}

// signalCPU evaluates the CPU utilization signal.
// Returns (active=true, reason) if CPU is above the idle threshold.
//
// BestEffort pods (CPUUtilStatus == UtilizationUnavailable) are treated as NOT
// active: they have no scheduling budget to protect, so the CPU signal cannot
// trigger ACTIVE classification for them.
func signalCPU(profile *analyzer.WorkloadProfile, cfg *Config) (active bool, reason string) {
	if profile.CPUUtilStatus == analyzer.UtilizationUnavailable {
		return false, "cpu: unavailable (BestEffort pod, no CPU request)"
	}
	if profile.CPUUtilization >= cfg.CPUIdleThresholdPct {
		return true, fmt.Sprintf("cpu: active — utilization %.1f%% >= threshold %.1f%%",
			profile.CPUUtilization*100, cfg.CPUIdleThresholdPct*100)
	}
	return false, fmt.Sprintf("cpu: idle — utilization %.1f%% < threshold %.1f%%",
		profile.CPUUtilization*100, cfg.CPUIdleThresholdPct*100)
}

// signalMemory evaluates the memory utilization signal.
// Same BestEffort handling as signalCPU.
func signalMemory(profile *analyzer.WorkloadProfile, cfg *Config) (active bool, reason string) {
	if profile.MemoryUtilStatus == analyzer.UtilizationUnavailable {
		return false, "memory: unavailable (BestEffort pod, no memory request)"
	}
	if profile.MemoryUtilization >= cfg.MemoryIdleThresholdPct {
		return true, fmt.Sprintf("memory: active — utilization %.1f%% >= threshold %.1f%%",
			profile.MemoryUtilization*100, cfg.MemoryIdleThresholdPct*100)
	}
	return false, fmt.Sprintf("memory: idle — utilization %.1f%% < threshold %.1f%%",
		profile.MemoryUtilization*100, cfg.MemoryIdleThresholdPct*100)
}

// signalQPS evaluates the request rate signal.
// Any QPS above the idle threshold (default 0.0) indicates activity.
func signalQPS(profile *analyzer.WorkloadProfile, cfg *Config) (active bool, reason string) {
	if profile.AvgQPS > cfg.QPSIdleThreshold {
		return true, fmt.Sprintf("qps: active — avg %.3f req/s > threshold %.3f",
			profile.AvgQPS, cfg.QPSIdleThreshold)
	}
	return false, fmt.Sprintf("qps: idle — avg %.3f req/s <= threshold %.3f",
		profile.AvgQPS, cfg.QPSIdleThreshold)
}

// signalNetwork evaluates the network I/O signal.
// Network above the threshold indicates the workload is receiving or sending traffic.
func signalNetwork(profile *analyzer.WorkloadProfile, cfg *Config) (active bool, reason string) {
	if profile.AvgNetworkBytesPerSec >= cfg.NetIdleThresholdBytes {
		return true, fmt.Sprintf("network: active — avg %.0f bytes/s >= threshold %.0f bytes/s",
			profile.AvgNetworkBytesPerSec, cfg.NetIdleThresholdBytes)
	}
	return false, fmt.Sprintf("network: idle — avg %.0f bytes/s < threshold %.0f bytes/s",
		profile.AvgNetworkBytesPerSec, cfg.NetIdleThresholdBytes)
}
