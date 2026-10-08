package cluster

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
)

func TestDefaultReclaimConfig_MatchesPolicy(t *testing.T) {
	expectedPolicy := decision.DefaultPolicy()
	cfg := DefaultReclaimConfig()

	if cfg.Thresholds.FullReclaim != expectedPolicy.FullReclaimScoreThreshold {
		t.Errorf("expected full_reclaim=%v, got %v", expectedPolicy.FullReclaimScoreThreshold, cfg.Thresholds.FullReclaim)
	}
	if cfg.Thresholds.SoftReclaim != expectedPolicy.SoftReclaimScoreThreshold {
		t.Errorf("expected soft_reclaim=%v, got %v", expectedPolicy.SoftReclaimScoreThreshold, cfg.Thresholds.SoftReclaim)
	}
	if cfg.Weights.CPU != expectedPolicy.WeightCPU {
		t.Errorf("expected CPU weight=%v, got %v", expectedPolicy.WeightCPU, cfg.Weights.CPU)
	}
	if cfg.Weights.Memory != expectedPolicy.WeightMemory {
		t.Errorf("expected Memory weight=%v, got %v", expectedPolicy.WeightMemory, cfg.Weights.Memory)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("DefaultReclaimConfig failed validation: %v", err)
	}
}

func TestReclaimConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ReclaimConfig)
		wantErr bool
	}{
		{
			name:    "valid default",
			mutate:  func(c *ReclaimConfig) {},
			wantErr: false,
		},
		{
			name: "negative weight",
			mutate: func(c *ReclaimConfig) {
				c.Weights.CPU = -0.1
				c.Weights.Memory = 0.5
			},
			wantErr: true,
		},
		{
			name: "weights do not sum to 1",
			mutate: func(c *ReclaimConfig) {
				c.Weights.CPU = 0.5
			},
			wantErr: true,
		},
		{
			name: "full reclaim threshold > 1",
			mutate: func(c *ReclaimConfig) {
				c.Thresholds.FullReclaim = 1.2
			},
			wantErr: true,
		},
		{
			name: "soft > full threshold",
			mutate: func(c *ReclaimConfig) {
				c.Thresholds.SoftReclaim = 0.8
				c.Thresholds.FullReclaim = 0.7
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultReclaimConfig()
			tc.mutate(cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestLoadReclaimConfig_MalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	badFile := filepath.Join(tmpDir, "bad.json")
	if err := os.WriteFile(badFile, []byte("{ bad json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadReclaimConfig(badFile)
	if err == nil {
		t.Fatal("expected error for malformed json, got nil")
	}
}
