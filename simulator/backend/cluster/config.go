// Package cluster provides runtime-cluster integration for the Active Workload mode.
// It loads and validates reclaim_policy.json and converts it into a decision.Policy.
package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
)

// ReclaimConfig is the on-disk representation of config/reclaim_policy.json.
type ReclaimConfig struct {
	Thresholds    ThresholdConfig    `json:"thresholds"`
	Weights       WeightConfig       `json:"weights"`
	Normalization NormalizationConfig `json:"normalization"`
	Safety        SafetyConfig       `json:"safety"`
}

// ThresholdConfig holds reclaim action score thresholds.
type ThresholdConfig struct {
	FullReclaim float64 `json:"full_reclaim"`
	SoftReclaim float64 `json:"soft_reclaim"`
}

// WeightConfig holds the 9-factor scoring weights.
type WeightConfig struct {
	CPU        float64 `json:"cpu"`
	Memory     float64 `json:"memory"`
	Idle       float64 `json:"idle"`
	Benefit    float64 `json:"benefit"`
	Replica    float64 `json:"replica"`
	Priority   float64 `json:"priority"`
	PDB        float64 `json:"pdb"`
	State      float64 `json:"state"`
	Checkpoint float64 `json:"checkpoint"`
}

// NormalizationConfig holds normalization bounds for scoring factors.
type NormalizationConfig struct {
	IdleMaxDurationSec  float64 `json:"idle_max_duration_sec"`
	BenefitMaxCPUMillis float64 `json:"benefit_max_cpu_millis"`
	BenefitMaxMemBytes  float64 `json:"benefit_max_mem_bytes"`
}

// SafetyConfig holds safety gate parameters.
type SafetyConfig struct {
	MaxPriorityForReclaim int32 `json:"max_priority_for_reclaim"`
	MinReplicasRequired   int32 `json:"min_replicas_required"`
}

// LoadReclaimConfig reads and validates a ReclaimConfig from the given JSON file path.
func LoadReclaimConfig(path string) (*ReclaimConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reclaim config: cannot read %q: %w", path, err)
	}

	var cfg ReclaimConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("reclaim config: malformed JSON in %q: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("reclaim config: validation failed: %w", err)
	}

	return &cfg, nil
}

// Validate checks all fields for correctness.
func (c *ReclaimConfig) Validate() error {
	var errs []error

	if c.Thresholds.FullReclaim < 0 || c.Thresholds.FullReclaim > 1 {
		errs = append(errs, fmt.Errorf("thresholds.full_reclaim %.4f not in [0,1]", c.Thresholds.FullReclaim))
	}
	if c.Thresholds.SoftReclaim < 0 || c.Thresholds.SoftReclaim > 1 {
		errs = append(errs, fmt.Errorf("thresholds.soft_reclaim %.4f not in [0,1]", c.Thresholds.SoftReclaim))
	}
	if c.Thresholds.SoftReclaim > c.Thresholds.FullReclaim {
		errs = append(errs, fmt.Errorf("soft_reclaim %.4f must be <= full_reclaim %.4f",
			c.Thresholds.SoftReclaim, c.Thresholds.FullReclaim))
	}

	w := c.Weights
	weights := map[string]float64{
		"cpu": w.CPU, "memory": w.Memory, "idle": w.Idle,
		"benefit": w.Benefit, "replica": w.Replica, "priority": w.Priority,
		"pdb": w.PDB, "state": w.State, "checkpoint": w.Checkpoint,
	}
	sum := 0.0
	for name, v := range weights {
		if v < 0 {
			errs = append(errs, fmt.Errorf("weight %q is negative (%.4f)", name, v))
		}
		sum += v
	}

	if math.Abs(sum-1.0) > 0.01 {
		errs = append(errs, fmt.Errorf("weights sum to %.4f, must be within 0.01 of 1.0", sum))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ToPolicy converts a validated ReclaimConfig into a *decision.Policy.
func (c *ReclaimConfig) ToPolicy() *decision.Policy {
	p := decision.DefaultPolicy()

	p.FullReclaimScoreThreshold = c.Thresholds.FullReclaim
	p.SoftReclaimScoreThreshold = c.Thresholds.SoftReclaim

	p.WeightCPU = c.Weights.CPU
	p.WeightMemory = c.Weights.Memory
	p.WeightIdle = c.Weights.Idle
	p.WeightBenefit = c.Weights.Benefit
	p.WeightReplica = c.Weights.Replica
	p.WeightPriority = c.Weights.Priority
	p.WeightPDB = c.Weights.PDB
	p.WeightState = c.Weights.State
	p.WeightCheckpoint = c.Weights.Checkpoint

	if c.Normalization.IdleMaxDurationSec > 0 {
		p.IdleMaxDurationSec = c.Normalization.IdleMaxDurationSec
	}
	if c.Normalization.BenefitMaxCPUMillis > 0 {
		p.BenefitMaxCPUMillis = c.Normalization.BenefitMaxCPUMillis
	}
	if c.Normalization.BenefitMaxMemBytes > 0 {
		p.BenefitMaxMemBytes = c.Normalization.BenefitMaxMemBytes
	}

	if c.Safety.MaxPriorityForReclaim > 0 {
		p.MaxPriorityForReclaim = c.Safety.MaxPriorityForReclaim
	}
	if c.Safety.MinReplicasRequired >= 0 {
		p.MinReplicasRequired = c.Safety.MinReplicasRequired
	}

	return p
}

// FromPolicy converts a *decision.Policy into a ReclaimConfig representation.
func FromPolicy(p *decision.Policy) *ReclaimConfig {
	if p == nil {
		p = decision.DefaultPolicy()
	}
	return &ReclaimConfig{
		Thresholds: ThresholdConfig{
			FullReclaim: p.FullReclaimScoreThreshold,
			SoftReclaim: p.SoftReclaimScoreThreshold,
		},
		Weights: WeightConfig{
			CPU:        p.WeightCPU,
			Memory:     p.WeightMemory,
			Idle:       p.WeightIdle,
			Benefit:    p.WeightBenefit,
			Replica:    p.WeightReplica,
			Priority:   p.WeightPriority,
			PDB:        p.WeightPDB,
			State:      p.WeightState,
			Checkpoint: p.WeightCheckpoint,
		},
		Normalization: NormalizationConfig{
			IdleMaxDurationSec:  p.IdleMaxDurationSec,
			BenefitMaxCPUMillis: p.BenefitMaxCPUMillis,
			BenefitMaxMemBytes:  p.BenefitMaxMemBytes,
		},
		Safety: SafetyConfig{
			MaxPriorityForReclaim: p.MaxPriorityForReclaim,
			MinReplicasRequired:   p.MinReplicasRequired,
		},
	}
}

// DefaultReclaimConfig returns in-memory defaults matching the ML-trained adaptive scheduler policy.
func DefaultReclaimConfig() *ReclaimConfig {
	return FromPolicy(decision.DefaultPolicy())
}
