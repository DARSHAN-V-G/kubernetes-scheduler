package decision

import (
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// ── Individual Factor Functions ───────────────────────────────────────────────
//
// Each function is independently testable, has no side effects, and returns a
// normalized float64 in [0, 1].
// Raw inputs → normalization → [0,1] output → weighted by Policy in ComputeScore.

// ScoreCPU computes R_CPU: lower CPU utilization → higher score (more reclaimable).
//
// When status is UtilizationUnavailable (BestEffort pod with no CPU request),
// a neutral score of 0.5 is returned. This is an explicitly documented assumption:
// we have no denominator to evaluate idleness, so we neither reward nor penalize.
//
// Returns: [0, 1]
func ScoreCPU(cpuUtil float64, status analyzer.UtilizationStatus) float64 {
	if status == analyzer.UtilizationUnavailable {
		return 0.5 // neutral: no request-based denominator available
	}
	return clamp(1.0-cpuUtil, 0, 1)
}

// ScoreMemory computes R_Memory: lower memory utilization → higher score.
// Same UtilizationUnavailable handling as ScoreCPU.
//
// Returns: [0, 1]
func ScoreMemory(memUtil float64, status analyzer.UtilizationStatus) float64 {
	if status == analyzer.UtilizationUnavailable {
		return 0.5 // neutral: no request-based denominator available
	}
	return clamp(1.0-memUtil, 0, 1)
}

// ScoreIdle computes R_Idle: longer sustained idle duration → higher score.
// Normalized against idleMaxSec; capped at 1.0. Zero duration → 0.0.
//
// Returns: [0, 1]
func ScoreIdle(idleDuration time.Duration, idleMaxSec float64) float64 {
	if idleMaxSec <= 0 {
		return 0
	}
	return clamp(idleDuration.Seconds()/idleMaxSec, 0, 1)
}

// ScoreBenefit computes R_Benefit: greater reclaimable resource quota → higher score.
// Combines CPU and memory reclaimable estimates with equal weight (0.5 each):
//
//	= 0.5 * (reclaimCPU / maxCPU) + 0.5 * (reclaimMem / maxMem)
//
// Returns: [0, 1]
func ScoreBenefit(reclaimCPUMillicores float64, reclaimMemBytes int64, maxCPU, maxMem float64) float64 {
	var cpuFactor, memFactor float64
	if maxCPU > 0 {
		cpuFactor = clamp(reclaimCPUMillicores/maxCPU, 0, 1)
	}
	if maxMem > 0 {
		memFactor = clamp(float64(reclaimMemBytes)/maxMem, 0, 1)
	}
	return 0.5*cpuFactor + 0.5*memFactor
}

// ScoreReplica computes R_Replica: greater replica redundancy → safer reclamation.
// Nil Replicas (standalone pod) or single replica: score 0.0 — removing the only
// instance is the highest-risk scenario.
//
// Discrete mapping:
//
//	nil / 0  → 0.0
//	1        → 0.0
//	2        → 0.5
//	3        → 0.8
//	>= 4     → 1.0
//
// Returns: [0, 1]
func ScoreReplica(replicas *metrics.ReplicaInfo) float64 {
	if replicas == nil {
		return 0.0
	}
	switch {
	case replicas.AvailableReplicas <= 1:
		return 0.0
	case replicas.AvailableReplicas == 2:
		return 0.5
	case replicas.AvailableReplicas == 3:
		return 0.8
	default:
		return 1.0
	}
}

// ScorePriority computes R_Priority: lower pod priority → safer to reclaim → higher score.
//
//	= 1 - clamp(priority / maxPriority, 0, 1)
//
// maxPriority should be policy.MaxPriorityForReclaim.
//
// Returns: [0, 1]
func ScorePriority(priority int32, maxPriority int32) float64 {
	if maxPriority <= 0 {
		return 1.0
	}
	return clamp(1.0-float64(priority)/float64(maxPriority), 0, 1)
}

// ScorePDB computes R_PDB: more disruptions allowed → safer reclamation → higher score.
//
// Mapping:
//
//	-1   → 1.0  (no PDB applies to this pod — unrestricted)
//	0    → 0.0  (PDB blocks disruptions — defensive; safety check should catch this first)
//	1    → 0.7  (one disruption allowed — possible but cautious)
//	>= 2 → 1.0  (multiple disruptions allowed — safe)
//
// Returns: [0, 1]
func ScorePDB(disruptionsAllowed int32) float64 {
	switch {
	case disruptionsAllowed < 0:
		return 1.0
	case disruptionsAllowed == 0:
		return 0.0
	case disruptionsAllowed == 1:
		return 0.7
	default:
		return 1.0
	}
}

// ScoreState computes R_State: workload type and pod phase influence reclamation risk.
//
// Mapping:
//
//	Running + Deployment or ReplicaSet → 1.0 (stateless, controller will reschedule)
//	Running + StatefulSet              → 0.4 (stateful — higher risk)
//	Running + no owner (standalone)    → 0.6 (no controller; manageable but manual)
//	Running + other owner              → 0.6
//	Not Running                        → 0.2 (unexpected state — should not normally reach Decision Engine)
//
// Returns: [0, 1]
func ScoreState(phase corev1.PodPhase, ownerKind string) float64 {
	if phase != corev1.PodRunning {
		return 0.2
	}
	switch ownerKind {
	case "Deployment", "ReplicaSet":
		return 1.0
	case "StatefulSet":
		return 0.4
	default:
		return 0.6
	}
}

// ScoreCheckpoint computes R_Checkpoint: checkpoint/restore capability → higher score.
//
// Annotation value mapping (key = policy.CheckpointableAnnotation):
//
//	"true"   → 1.0  (explicitly checkpointable)
//	"false"  → 0.0  (not checkpointable; full reclaim will also be blocked by safety check)
//	absent   → 0.5  (unknown — neutral assumption)
//
// Returns: [0, 1]
func ScoreCheckpoint(annotations map[string]string, annotationKey string) float64 {
	val, exists := annotations[annotationKey]
	if !exists {
		return 0.5
	}
	if val == "true" {
		return 1.0
	}
	return 0.0
}

// ComputeScore calculates the weighted composite reclamation score.
// All weights are read from the Policy struct — never hardcoded here.
//
// Because all R_* values are in [0,1] and the weights in DefaultPolicy sum to 1.0,
// the result is guaranteed to be in [0, 1] for well-formed policies.
//
// Returns: [0, 1] (for valid Policy with weights summing to 1.0)
func ComputeScore(factors IndividualScores, policy *Policy) float64 {
	return policy.WeightCPU*factors.CPU +
		policy.WeightMemory*factors.Memory +
		policy.WeightIdle*factors.Idle +
		policy.WeightBenefit*factors.Benefit +
		policy.WeightReplica*factors.Replica +
		policy.WeightPriority*factors.Priority +
		policy.WeightPDB*factors.PDB +
		policy.WeightState*factors.State +
		policy.WeightCheckpoint*factors.Checkpoint
}

// clamp constrains a float64 to [min, max].
func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
