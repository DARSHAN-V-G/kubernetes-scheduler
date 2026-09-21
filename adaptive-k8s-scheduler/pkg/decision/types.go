package decision

import "time"

// Action represents the reclamation action selected by the Decision Engine.
// The engine produces a decision — it does NOT execute the action.
// A future Action Manager consumes DecisionResult and carries out execution.
type Action int

const (
	// ActionKeep means no reclamation action should be taken.
	// Either the workload failed a safety check, or its score is below the soft threshold.
	ActionKeep Action = iota

	// ActionSoftReclaim means in-place resource right-sizing (request reduction)
	// is the selected action. No container restart or CRIU checkpoint involved.
	ActionSoftReclaim

	// ActionFullReclaim means checkpoint-and-suspend (CRIU) is the selected action.
	// Only selected when FullReclaimAllowed == true in Capabilities.
	ActionFullReclaim
)

// String returns a human-readable label for the Action.
func (a Action) String() string {
	switch a {
	case ActionSoftReclaim:
		return "SOFT_RECLAIM"
	case ActionFullReclaim:
		return "FULL_RECLAIM"
	default:
		return "KEEP"
	}
}

// IndividualScores holds the normalized [0, 1] score for each R_* factor.
// All fields are populated before the composite score is computed.
type IndividualScores struct {
	CPU        float64 // R_CPU
	Memory     float64 // R_Memory
	Idle       float64 // R_Idle
	Benefit    float64 // R_Benefit
	Replica    float64 // R_Replica
	Priority   float64 // R_Priority
	PDB        float64 // R_PDB
	State      float64 // R_State
	Checkpoint float64 // R_Checkpoint
}

// Capabilities describes which reclamation actions are permitted after the
// safety evaluation. Both flags may be false if all reclamation is blocked.
//
// "Not checkpointable" (FullReclaimAllowed=false, SoftReclaimAllowed=true) is
// fundamentally different from "Protected" (both false). These are kept separate.
type Capabilities struct {
	// FullReclaimAllowed is true if CRIU checkpoint-and-suspend is safe for this workload.
	FullReclaimAllowed bool
	// SoftReclaimAllowed is true if in-place resource right-sizing is safe.
	SoftReclaimAllowed bool
	// Reasons describes the restriction(s) applied, if any.
	Reasons []string
}

// DecisionResult is the complete, explainable output of the Decision Engine.
// It is the terminal output of the intelligence pipeline.
// No reclamation action is executed here — the Action Manager consumes this type.
type DecisionResult struct {
	PodNamespace string
	PodName      string

	// Classification that triggered this evaluation (always "IDLE" in normal flow).
	Classification string

	// Eligible is true when at least one reclamation action is safe AND the score
	// meets the soft-reclaim threshold. False means ActionKeep.
	Eligible bool

	// Capabilities shows which actions were permitted after safety evaluation.
	Capabilities Capabilities

	// Scores holds each individual R_* factor value [0, 1].
	Scores IndividualScores

	// Score is the weighted composite reclamation score [0, 1].
	Score float64

	// Action is the selected reclamation action.
	Action Action

	// Reasons describes positive factors that contributed to the selected action.
	Reasons []string

	// RejectionReasons describes safety failures or restrictions that blocked
	// reclamation or constrained available actions.
	RejectionReasons []string

	// DecidedAt is the timestamp when the decision was made.
	DecidedAt time.Time
}
