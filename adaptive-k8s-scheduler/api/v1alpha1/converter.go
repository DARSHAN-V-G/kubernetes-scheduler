package v1alpha1

import (
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
)

// ToDecisionPolicy converts a Kubernetes ReclaimPolicySpec into a decision.Policy struct
// usable by the Multi-Criteria Reclamation Decision Engine.
// If spec is nil, decision.DefaultPolicy() is returned.
func ToDecisionPolicy(spec *ReclaimPolicySpec) *decision.Policy {
	policy := decision.DefaultPolicy()
	if spec == nil {
		return policy
	}

	// ── Safety and Priority Thresholds ────────────────────────────────────────
	if spec.MinReplicasRequired != nil {
		policy.MinReplicasRequired = *spec.MinReplicasRequired
	}
	if spec.MaxPriorityForReclaim != nil {
		policy.MaxPriorityForReclaim = *spec.MaxPriorityForReclaim
	}

	// ── Idle Duration Threshold Parsing ───────────────────────────────────────
	if spec.IdleDurationThreshold != "" {
		if dur, err := time.ParseDuration(spec.IdleDurationThreshold); err == nil && dur > 0 {
			policy.IdleMaxDurationSec = dur.Seconds()
		}
	}

	// ── Score Thresholds ──────────────────────────────────────────────────────
	if spec.ScoreThresholds != nil {
		if spec.ScoreThresholds.FullReclaimThreshold != nil {
			policy.FullReclaimScoreThreshold = *spec.ScoreThresholds.FullReclaimThreshold
		}
		if spec.ScoreThresholds.SoftReclaimThreshold != nil {
			policy.SoftReclaimScoreThreshold = *spec.ScoreThresholds.SoftReclaimThreshold
		}
	}

	// ── Custom Factor Weights ─────────────────────────────────────────────────
	if spec.Weights != nil {
		w := spec.Weights
		if w.WeightCPU != nil {
			policy.WeightCPU = *w.WeightCPU
		}
		if w.WeightMemory != nil {
			policy.WeightMemory = *w.WeightMemory
		}
		if w.WeightIdle != nil {
			policy.WeightIdle = *w.WeightIdle
		}
		if w.WeightBenefit != nil {
			policy.WeightBenefit = *w.WeightBenefit
		}
		if w.WeightReplica != nil {
			policy.WeightReplica = *w.WeightReplica
		}
		if w.WeightPriority != nil {
			policy.WeightPriority = *w.WeightPriority
		}
		if w.WeightPDB != nil {
			policy.WeightPDB = *w.WeightPDB
		}
		if w.WeightState != nil {
			policy.WeightState = *w.WeightState
		}
		if w.WeightCheckpoint != nil {
			policy.WeightCheckpoint = *w.WeightCheckpoint
		}
	}

	return policy
}
