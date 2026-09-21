package analyzer

// Config holds all tunable parameters for the Workload Analyzer.
// Values here are used exclusively by the Analyzer itself — the IsConsistentlyIdle
// re-validation uses these thresholds, independently from the Detector thresholds.
type Config struct {
	// EMAAlpha is the exponential moving average smoothing factor passed to
	// MetricWindow.ComputeStats. Valid range: (0, 1]. Default: 0.3.
	EMAAlpha float64

	// TrendRisingDelta: if (secondHalfAvg - firstHalfAvg) / mean > TrendRisingDelta,
	// the CPU trend is classified as RISING. Default: 0.20 (20% relative change).
	TrendRisingDelta float64

	// TrendFallingDelta: if (firstHalfAvg - secondHalfAvg) / mean > TrendFallingDelta,
	// the CPU trend is classified as FALLING. Default: 0.20.
	TrendFallingDelta float64

	// IdleCPUMillicores is the per-sample CPU threshold (millicores) used when calling
	// MetricWindow.IsConsistentlyBelowThresholds to validate IsConsistentlyIdle.
	// Default: 20.0
	IdleCPUMillicores float64

	// IdleNetBytesPerSec is the per-sample network threshold (bytes/sec) for the
	// consistency check. Default: 10240.0 (10 KB/s).
	IdleNetBytesPerSec float64

	// IdleQPSThreshold is the per-sample QPS threshold for the consistency check.
	// Default: 0.0.
	IdleQPSThreshold float64
}

// DefaultConfig returns the standard Analyzer configuration matching the
// project implementation plan defaults.
func DefaultConfig() *Config {
	return &Config{
		EMAAlpha:           0.3,
		TrendRisingDelta:   0.20,
		TrendFallingDelta:  0.20,
		IdleCPUMillicores:  20.0,
		IdleNetBytesPerSec: 10240.0,
		IdleQPSThreshold:   0.0,
	}
}
