package detector

import (
	"os"
	"strconv"
	"time"
)

// Config holds all tunable thresholds for the Idle Classifier / Detector.
// Initial values correspond to the project implementation plan specification.
// All values in logic files must be read from this struct — never hardcoded inline.
type Config struct {
	// CPUIdleThresholdPct is the maximum CPU utilization (fraction of requested,
	// e.g. 0.30 = 30%) below which the CPU signal is considered idle.
	CPUIdleThresholdPct float64 // default: 0.30

	// MemoryIdleThresholdPct is the maximum memory utilization (fraction of requested)
	// below which the memory signal is considered idle.
	MemoryIdleThresholdPct float64 // default: 0.80

	// QPSIdleThreshold is the maximum average QPS at which the QPS signal is idle.
	// Calibrated to 2.0 to filter internal Kubelet readiness/liveness health checks.
	QPSIdleThreshold float64 // default: 2.0

	// NetIdleThresholdBytes is the maximum average network bytes/sec below which
	// the network signal is considered idle.
	NetIdleThresholdBytes float64 // default: 15360.0 (15 KB/s)

	// MinIdleDuration is the minimum continuous idle time required before a workload
	// can be classified as IDLE rather than LOW_USAGE.
	MinIdleDuration time.Duration // default: 30s

	// MinSampleCount is the minimum number of window samples required before the
	// detector will classify a workload as IDLE. Prevents premature classification
	// when observation is insufficient.
	MinSampleCount int // default: 3
}

// DefaultConfig returns the standard Detector configuration matching calibrated defaults
// and environment variable overrides.
func DefaultConfig() *Config {
	cfg := &Config{
		CPUIdleThresholdPct:    0.30,
		MemoryIdleThresholdPct: 0.80,
		QPSIdleThreshold:       2.0,
		NetIdleThresholdBytes:  15360.0,
		MinIdleDuration:        30 * time.Second,
		MinSampleCount:         3,
	}

	if val := os.Getenv("IDLE_CPU_THRESHOLD_PCT"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
			cfg.CPUIdleThresholdPct = f
		}
	}
	if val := os.Getenv("IDLE_MEM_THRESHOLD_PCT"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
			cfg.MemoryIdleThresholdPct = f
		}
	}
	if val := os.Getenv("IDLE_QPS_THRESHOLD"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil && f >= 0 {
			cfg.QPSIdleThreshold = f
		}
	}
	if val := os.Getenv("IDLE_NET_THRESHOLD_BYTES"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil && f >= 0 {
			cfg.NetIdleThresholdBytes = f
		}
	}
	if val := os.Getenv("IDLE_MIN_DURATION_SECONDS"); val != "" {
		if s, err := strconv.Atoi(val); err == nil && s > 0 {
			cfg.MinIdleDuration = time.Duration(s) * time.Second
		}
	}

	return cfg
}
