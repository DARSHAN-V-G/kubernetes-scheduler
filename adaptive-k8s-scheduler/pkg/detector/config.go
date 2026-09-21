package detector

import "time"

// Config holds all tunable thresholds for the Idle Classifier / Detector.
// Initial values correspond to the project implementation plan specification.
// All values in logic files must be read from this struct — never hardcoded inline.
type Config struct {
	// CPUIdleThresholdPct is the maximum CPU utilization (fraction of requested,
	// e.g. 0.30 = 30%) below which the CPU signal is considered idle.
	CPUIdleThresholdPct float64 // default: 0.30

	// MemoryIdleThresholdPct is the maximum memory utilization (fraction of requested)
	// below which the memory signal is considered idle.
	MemoryIdleThresholdPct float64 // default: 0.30

	// QPSIdleThreshold is the maximum average QPS at which the QPS signal is idle.
	// Strictly: AvgQPS <= QPSIdleThreshold → idle.
	QPSIdleThreshold float64 // default: 0.0

	// NetIdleThresholdBytes is the maximum average network bytes/sec below which
	// the network signal is considered idle.
	NetIdleThresholdBytes float64 // default: 10240.0 (10 KB/s)

	// MinIdleDuration is the minimum continuous idle time required before a workload
	// can be classified as IDLE rather than LOW_USAGE.
	MinIdleDuration time.Duration // default: 30s

	// MinSampleCount is the minimum number of window samples required before the
	// detector will classify a workload as IDLE. Prevents premature classification
	// when observation is insufficient.
	MinSampleCount int // default: 3
}

// DefaultConfig returns the standard Detector configuration matching the project
// implementation plan defaults.
func DefaultConfig() *Config {
	return &Config{
		CPUIdleThresholdPct:    0.30,
		MemoryIdleThresholdPct: 0.30,
		QPSIdleThreshold:       0.0,
		NetIdleThresholdBytes:  10240.0,
		MinIdleDuration:        30 * time.Second,
		MinSampleCount:         3,
	}
}
