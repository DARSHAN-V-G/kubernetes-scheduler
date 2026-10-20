package decision

import (
	"os"
	"strconv"
)

// Policy holds all configurable thresholds, weights, and annotation keys for the
// Reclamation Decision Engine. Every numeric constant used in logic files MUST
// come from this struct — no magic numbers elsewhere.
type Policy struct {
	// ── Action selection score thresholds ─────────────────────────────────────

	// FullReclaimScoreThreshold: score >= this value selects FULL_RECLAIM
	// (subject to FullReclaimAllowed capability flag).
	FullReclaimScoreThreshold float64 // 0.75

	// SoftReclaimScoreThreshold: score >= this value selects SOFT_RECLAIM
	// (subject to SoftReclaimAllowed capability flag).
	SoftReclaimScoreThreshold float64 // 0.50

	// ── Scoring weights (must sum to 1.0) ─────────────────────────────────────

	WeightCPU        float64 // 0.20
	WeightMemory     float64 // 0.20
	WeightIdle       float64 // 0.15
	WeightBenefit    float64 // 0.15
	WeightReplica    float64 // 0.10
	WeightPriority   float64 // 0.05
	WeightPDB        float64 // 0.05
	WeightState      float64 // 0.05
	WeightCheckpoint float64 // 0.05

	// ── Safety thresholds ─────────────────────────────────────────────────────

	// MaxPriorityForReclaim: pods with numeric Priority >= this value are protected.
	// system-cluster-critical and system-node-critical are blocked by name regardless.
	MaxPriorityForReclaim int32 // 100000

	// MinReplicasRequired: minimum available replicas that must remain after removing
	// this pod. If AvailableReplicas-1 < MinReplicasRequired, all reclamation is blocked.
	MinReplicasRequired int32 // 1

	// ── Scoring normalization parameters ──────────────────────────────────────

	// IdleMaxDurationSec: idle duration (seconds) that maps to R_Idle = 1.0.
	IdleMaxDurationSec float64 // 3600.0 (1 hour)

	// BenefitMaxCPUMillis: reclaimable CPU (millicores) that maps to full CPU benefit.
	BenefitMaxCPUMillis float64 // 2000.0

	// BenefitMaxMemBytes: reclaimable memory (bytes) that maps to full memory benefit.
	BenefitMaxMemBytes float64 // 4 GiB

	// ── Annotation keys ───────────────────────────────────────────────────────

	// CheckpointableAnnotation controls CRIU compatibility.
	// "true"  → full reclaim supported
	// "false" → full reclaim blocked, soft reclaim still allowed
	// absent  → neutral (0.5 score, no restriction)
	CheckpointableAnnotation string // "reclaim.io/checkpointable"

	// ProtectedAnnotation blocks ALL reclamation when set to "true".
	// This is different from "not checkpointable" — a protected workload must never
	// be reclaimed regardless of score.
	ProtectedAnnotation string // "reclaim.io/protected"
}

// DefaultPolicy returns the standard policy matching the project implementation plan.
func DefaultPolicy() *Policy {
	p := &Policy{
		FullReclaimScoreThreshold: 0.5927,
		SoftReclaimScoreThreshold: 0.3361,

		// Data-backed weights derived via Shannon's Entropy Weight Method (EWM) on Google Borg traces:
		WeightCPU:        0.199702,
		WeightMemory:     0.225841,
		WeightBenefit:    0.409373,
		WeightPriority:   0.063951,
		WeightState:      0.098443,
		WeightReplica:    0.002690,
		WeightIdle:       0.0,
		WeightPDB:        0.0,
		WeightCheckpoint: 0.0,

		MaxPriorityForReclaim: 100000,
		MinReplicasRequired:   1,

		IdleMaxDurationSec:  60.0,
		BenefitMaxCPUMillis: 2000.0,
		BenefitMaxMemBytes:  4 * 1024 * 1024 * 1024, // 4 GiB

		CheckpointableAnnotation: "reclaim.io/checkpointable",
		ProtectedAnnotation:      "reclaim.io/protected",
	}

	if val := os.Getenv("FULL_RECLAIM_THRESHOLD"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
			p.FullReclaimScoreThreshold = f
		}
	}
	if val := os.Getenv("SOFT_RECLAIM_THRESHOLD"); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
			p.SoftReclaimScoreThreshold = f
		}
	}

	return p
}
