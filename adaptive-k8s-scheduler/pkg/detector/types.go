package detector

import "time"

// WorkloadClass is the classification assigned to a workload by the Idle Detector.
type WorkloadClass int

const (
	// ClassActive means the workload is consuming resources above idle thresholds.
	// It must NOT be forwarded to the Reclamation Decision Engine.
	ClassActive WorkloadClass = iota

	// ClassLowUsage means all signal thresholds pass but the workload has not been
	// idle long enough, or consistently enough, to qualify as IDLE. Continue monitoring.
	ClassLowUsage

	// ClassIdle means the workload has been sustainably inactive across all signals
	// for at least MinIdleDuration. It is a candidate for the Decision Engine.
	ClassIdle
)

// String returns a human-readable label for the WorkloadClass.
func (c WorkloadClass) String() string {
	switch c {
	case ClassActive:
		return "ACTIVE"
	case ClassLowUsage:
		return "LOW_USAGE"
	case ClassIdle:
		return "IDLE"
	default:
		return "UNKNOWN"
	}
}

// ClassificationResult is the output of the Idle Detector.
type ClassificationResult struct {
	// Class is the classification verdict.
	Class WorkloadClass

	// Reasons describes why each signal contributed to the classification.
	// Populated for all classes to enable explainability and logging.
	Reasons []string

	// IdleDuration is the idle duration at classification time (from the WorkloadProfile).
	IdleDuration time.Duration

	// ClassifiedAt is the timestamp when the classification was performed.
	ClassifiedAt time.Time
}
