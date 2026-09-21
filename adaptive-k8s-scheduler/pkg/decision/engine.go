// Package decision implements the Multi-Criteria Reclamation Decision Engine —
// the third and final stage of the intelligence pipeline.
//
// The engine evaluates IDLE workload candidates and produces a DecisionResult that
// is fully explainable. It does NOT execute any reclamation action; that is the
// responsibility of a future Action Manager.
//
// Pipeline within Evaluate():
//  1. Safety / capability evaluation (hard constraints — score not involved)
//  2. If no action allowed → return KEEP immediately
//  3. Compute individual R_* factor scores [0, 1]
//  4. Compute weighted composite score
//  5. Select action based on score thresholds AND capability flags
//  6. Record to history (observability only — does NOT influence current decision)
package decision

import (
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// Engine is the Multi-Criteria Reclamation Decision Engine.
type Engine struct {
	policy  *Policy
	history *History
}

// NewEngine constructs a Decision Engine with the given policy.
// If policy is nil, DefaultPolicy() is used.
func NewEngine(policy *Policy) *Engine {
	if policy == nil {
		policy = DefaultPolicy()
	}
	return &Engine{
		policy:  policy,
		history: NewHistory(),
	}
}

// Evaluate runs the complete decision pipeline for an IDLE workload candidate.
// It is safe to call concurrently.
func (e *Engine) Evaluate(profile *analyzer.WorkloadProfile, pod *metrics.PodMetrics) DecisionResult {
	result := DecisionResult{
		PodNamespace:   pod.Namespace,
		PodName:        pod.Name,
		Classification: "IDLE",
		DecidedAt:      time.Now(),
	}

	// ── Step 1: Safety / capability evaluation ────────────────────────────────
	// Hard constraints first — score is not computed until capabilities are known.

	caps := evaluateCapabilities(pod, e.policy)
	result.Capabilities = caps
	result.RejectionReasons = append(result.RejectionReasons, caps.Reasons...)

	// If no action is allowed at all, return KEEP immediately without scoring.
	if !caps.FullReclaimAllowed && !caps.SoftReclaimAllowed {
		result.Eligible = false
		result.Action = ActionKeep
		result.Reasons = append(result.Reasons, "all reclamation blocked by safety constraints — KEEP")
		e.history.Record(result)
		return result
	}

	// ── Step 2: Compute individual R_* factor scores ──────────────────────────

	ownerKind := podOwnerKind(pod)

	factors := IndividualScores{
		CPU:        ScoreCPU(profile.CPUUtilization, profile.CPUUtilStatus),
		Memory:     ScoreMemory(profile.MemoryUtilization, profile.MemoryUtilStatus),
		Idle:       ScoreIdle(profile.IdleDuration, e.policy.IdleMaxDurationSec),
		Benefit:    ScoreBenefit(profile.ReclaimableCPUMillicores, profile.ReclaimableMemoryBytes, e.policy.BenefitMaxCPUMillis, e.policy.BenefitMaxMemBytes),
		Replica:    ScoreReplica(pod.Replicas),
		Priority:   ScorePriority(pod.Priority, e.policy.MaxPriorityForReclaim),
		PDB:        ScorePDB(pod.DisruptionsAllowed),
		State:      ScoreState(pod.Phase, ownerKind),
		Checkpoint: ScoreCheckpoint(pod.Annotations, e.policy.CheckpointableAnnotation),
	}
	result.Scores = factors

	// ── Step 3: Compute weighted composite score ──────────────────────────────

	score := ComputeScore(factors, e.policy)
	result.Score = score

	// ── Step 4: Action selection ──────────────────────────────────────────────
	// Applies score thresholds AND capability flags together.
	// A high score does NOT override a capability constraint.

	action, selectionReasons := selectAction(score, caps, e.policy)
	result.Action = action
	result.Eligible = action != ActionKeep

	result.Reasons = append(result.Reasons, selectionReasons...)
	result.Reasons = append(result.Reasons,
		fmt.Sprintf("composite score: %.4f", score),
		fmt.Sprintf(
			"R_CPU=%.3f R_Mem=%.3f R_Idle=%.3f R_Benefit=%.3f R_Replica=%.3f R_Priority=%.3f R_PDB=%.3f R_State=%.3f R_Checkpoint=%.3f",
			factors.CPU, factors.Memory, factors.Idle, factors.Benefit,
			factors.Replica, factors.Priority, factors.PDB, factors.State, factors.Checkpoint,
		),
	)

	// ── Step 5: Record to history (observability — does not change this result) ─

	e.history.Record(result)

	return result
}

// selectAction maps score + capability flags to an action.
//
// Logic (from implementation plan, Correction 6):
//
//	score >= FullThreshold AND FullReclaimAllowed              → FULL_RECLAIM
//	score >= FullThreshold AND !Full AND SoftAllowed           → SOFT_RECLAIM (capability fallback)
//	score >= FullThreshold AND !Full AND !Soft                 → KEEP
//	score >= SoftThreshold AND SoftReclaimAllowed              → SOFT_RECLAIM
//	score >= SoftThreshold AND !Soft AND FullAllowed           → FULL_RECLAIM (capability fallback)
//	score >= SoftThreshold AND !Soft AND !Full                 → KEEP
//	score < SoftThreshold                                      → KEEP
func selectAction(score float64, caps Capabilities, policy *Policy) (Action, []string) {
	var reasons []string

	if score >= policy.FullReclaimScoreThreshold {
		if caps.FullReclaimAllowed {
			reasons = append(reasons, fmt.Sprintf(
				"score %.4f >= full-reclaim threshold %.2f and full reclaim is allowed",
				score, policy.FullReclaimScoreThreshold,
			))
			return ActionFullReclaim, reasons
		}
		if caps.SoftReclaimAllowed {
			reasons = append(reasons, fmt.Sprintf(
				"score %.4f >= full-reclaim threshold %.2f but full reclaim is unavailable; falling back to soft reclaim",
				score, policy.FullReclaimScoreThreshold,
			))
			return ActionSoftReclaim, reasons
		}
		reasons = append(reasons, "score above full-reclaim threshold but no reclaim action is available")
		return ActionKeep, reasons
	}

	if score >= policy.SoftReclaimScoreThreshold {
		if caps.SoftReclaimAllowed {
			reasons = append(reasons, fmt.Sprintf(
				"score %.4f >= soft-reclaim threshold %.2f and soft reclaim is allowed",
				score, policy.SoftReclaimScoreThreshold,
			))
			return ActionSoftReclaim, reasons
		}
		if caps.FullReclaimAllowed {
			reasons = append(reasons, fmt.Sprintf(
				"score %.4f >= soft-reclaim threshold %.2f but soft reclaim is unavailable; falling back to full reclaim",
				score, policy.SoftReclaimScoreThreshold,
			))
			return ActionFullReclaim, reasons
		}
		reasons = append(reasons, "score above soft-reclaim threshold but no reclaim action is available")
		return ActionKeep, reasons
	}

	reasons = append(reasons, fmt.Sprintf(
		"score %.4f < soft-reclaim threshold %.2f — KEEP",
		score, policy.SoftReclaimScoreThreshold,
	))
	return ActionKeep, reasons
}

// podOwnerKind returns the owning controller kind for the pod, or "" for standalone pods.
func podOwnerKind(pod *metrics.PodMetrics) string {
	if pod.Replicas == nil {
		return ""
	}
	return pod.Replicas.OwnerKind
}

// Policy returns the engine active policy (for inspection/testing).
func (e *Engine) Policy() *Policy {
	return e.policy
}

// DecisionHistory returns the engine decision history store.
func (e *Engine) DecisionHistory() *History {
	return e.history
}
