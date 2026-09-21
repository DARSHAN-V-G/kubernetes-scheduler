package simulation

import (
	"fmt"
	"testing"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/detector"
)

// TestAll75ScenariosIntegrity comprehensively validates all 75 scenarios against the authoritative Go pipeline:
// 1. Total count == 75
// 2. Numbers 1..75 are unique and complete
// 3. Scenario IDs are unique and formatted properly
// 4. Categories A-J match the specified numbering ranges
// 5. runner.ExecuteSimulation() successfully executes with non-nil outputs, valid classifications, and valid decisions
func TestAll75ScenariosIntegrity(t *testing.T) {
	runner := NewPipelineRunner()
	presets := AllPresets()

	// A. Total scenario count == 75
	if len(presets) != 75 {
		t.Fatalf("expected exactly 75 scenarios, got %d", len(presets))
	}

	seenNumbers := make(map[int]bool)
	seenIDs := make(map[string]bool)
	categoryCounts := make(map[string]int)

	expectedCategories := []string{
		"workload-state",
		"reclamation",
		"safety",
		"checkpoint",
		"activity",
		"resources",
		"priority",
		"replicas",
		"mixed",
		"complex-clusters",
	}

	for i, scenario := range presets {
		meta := scenario.Metadata
		expectedNum := i + 1

		// B & D. Numbers 1 through 75 exist and are sequential
		if meta.Number != expectedNum {
			t.Errorf("scenario index %d: expected number %d, got %d", i, expectedNum, meta.Number)
		}
		if seenNumbers[meta.Number] {
			t.Errorf("duplicate scenario number detected: %d", meta.Number)
		}
		seenNumbers[meta.Number] = true

		// C. Unique scenario IDs
		expectedID := fmt.Sprintf("scenario-%02d", meta.Number)
		if meta.ID != expectedID {
			t.Errorf("scenario %d: expected ID %q, got %q", meta.Number, expectedID, meta.ID)
		}
		if seenIDs[meta.ID] {
			t.Errorf("duplicate scenario ID detected: %q", meta.ID)
		}
		seenIDs[meta.ID] = true

		// F. Category assignment matches intended ranges
		var expectedCat string
		switch {
		case meta.Number >= 1 && meta.Number <= 9:
			expectedCat = "workload-state"
		case meta.Number >= 10 && meta.Number <= 17:
			expectedCat = "reclamation"
		case meta.Number >= 18 && meta.Number <= 26:
			expectedCat = "safety"
		case meta.Number >= 27 && meta.Number <= 32:
			expectedCat = "checkpoint"
		case meta.Number >= 33 && meta.Number <= 38:
			expectedCat = "activity"
		case meta.Number >= 39 && meta.Number <= 46:
			expectedCat = "resources"
		case meta.Number >= 47 && meta.Number <= 51:
			expectedCat = "priority"
		case meta.Number >= 52 && meta.Number <= 57:
			expectedCat = "replicas"
		case meta.Number >= 58 && meta.Number <= 66:
			expectedCat = "mixed"
		case meta.Number >= 67 && meta.Number <= 75:
			expectedCat = "complex-clusters"
		default:
			t.Errorf("scenario %d: out of valid range 1..75", meta.Number)
		}

		if meta.Category != expectedCat {
			t.Errorf("scenario %d (%s): expected category %q, got %q", meta.Number, meta.Name, expectedCat, meta.Category)
		}
		categoryCounts[meta.Category]++

		// G & H. Successful execution through runner.ExecuteSimulation()
		resp := runner.ExecuteSimulation(scenario.Cluster)
		if resp == nil {
			t.Fatalf("scenario %d: ExecuteSimulation returned nil", meta.Number)
		}

		if len(resp.Workloads) != len(scenario.Cluster.Workloads) {
			t.Fatalf("scenario %d: expected %d workload results, got %d", meta.Number, len(scenario.Cluster.Workloads), len(resp.Workloads))
		}

		for _, w := range resp.Workloads {
			if w.Name == "" {
				t.Errorf("scenario %d: workload result missing name", meta.Number)
			}
			// Validate classification is valid
			if w.Classification != detector.ClassActive &&
				w.Classification != detector.ClassLowUsage &&
				w.Classification != detector.ClassIdle {
				t.Errorf("scenario %d: invalid classification %v for workload %s", meta.Number, w.Classification, w.Name)
			}
			// Validate action is valid
			if w.Action != decision.ActionKeep &&
				w.Action != decision.ActionSoftReclaim &&
				w.Action != decision.ActionFullReclaim {
				t.Errorf("scenario %d: invalid decision action %v for workload %s", meta.Number, w.Action, w.Name)
			}
		}
	}

	// E. All 10 categories A-J are represented
	for _, cat := range expectedCategories {
		if count := categoryCounts[cat]; count == 0 {
			t.Errorf("category %q has 0 scenarios", cat)
		}
	}
}

// ── Targeted Behavioral Verification Tests ───────────────────────────────────

func TestRunner_Targeted_ActiveWorkload(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-01")
	if err != nil {
		t.Fatalf("failed to fetch scenario-01: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassActive {
		t.Errorf("expected ACTIVE, got %v", w.Classification)
	}
	if w.Action != decision.ActionKeep {
		t.Errorf("expected KEEP, got %v", w.Action)
	}
}

func TestRunner_Targeted_LowUsageWorkload(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-02")
	if err != nil {
		t.Fatalf("failed to fetch scenario-02: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassLowUsage {
		t.Errorf("expected LOW_USAGE, got %v", w.Classification)
	}
	if w.Action != decision.ActionKeep {
		t.Errorf("expected KEEP, got %v", w.Action)
	}
}

func TestRunner_Targeted_StrongFullReclaim(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-03")
	if err != nil {
		t.Fatalf("failed to fetch scenario-03: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassIdle {
		t.Errorf("expected IDLE, got %v", w.Classification)
	}
	if w.Action != decision.ActionFullReclaim {
		t.Errorf("expected FULL_RECLAIM, got %v (score=%f)", w.Action, w.Score)
	}
	if !w.Capabilities.FullReclaimAllowed {
		t.Errorf("expected FullReclaimAllowed to be true")
	}
}

func TestRunner_Targeted_ProtectedWorkload(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-18")
	if err != nil {
		t.Fatalf("failed to fetch scenario-18: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassIdle {
		t.Errorf("expected IDLE, got %v", w.Classification)
	}
	if w.Action != decision.ActionKeep {
		t.Errorf("expected KEEP for protected workload, got %v", w.Action)
	}
	if w.Capabilities.FullReclaimAllowed || w.Capabilities.SoftReclaimAllowed {
		t.Errorf("expected both capabilities to be blocked by protected annotation")
	}
}

func TestRunner_Targeted_PDBBlocked(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-19")
	if err != nil {
		t.Fatalf("failed to fetch scenario-19: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassIdle {
		t.Errorf("expected IDLE, got %v", w.Classification)
	}
	if w.Action != decision.ActionKeep {
		t.Errorf("expected KEEP for PDB blocked workload, got %v", w.Action)
	}
}

func TestRunner_Targeted_CheckpointUnsupported(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-28")
	if err != nil {
		t.Fatalf("failed to fetch scenario-28: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassIdle {
		t.Errorf("expected IDLE, got %v", w.Classification)
	}
	// Full reclaim blocked due to stateful without checkpoint, falls back to SoftReclaim
	if w.Action != decision.ActionSoftReclaim {
		t.Errorf("expected SOFT_RECLAIM fallback, got %v (score=%f)", w.Action, w.Score)
	}
	if w.Capabilities.FullReclaimAllowed {
		t.Errorf("expected FullReclaimAllowed to be false for non-checkpointable StatefulSet")
	}
	if !w.Capabilities.SoftReclaimAllowed {
		t.Errorf("expected SoftReclaimAllowed to be true")
	}
}

func TestRunner_Targeted_HighPriority(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-23")
	if err != nil {
		t.Fatalf("failed to fetch scenario-23: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Action != decision.ActionKeep {
		t.Errorf("expected KEEP for high priority workload, got %v", w.Action)
	}
}

func TestRunner_Targeted_ReplicaSafety(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-21")
	if err != nil {
		t.Fatalf("failed to fetch scenario-21: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Action != decision.ActionKeep {
		t.Errorf("expected KEEP for replica quorum deficit, got %v", w.Action)
	}
}

func TestRunner_Targeted_ConflictingSignals_NetworkActive(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-33")
	if err != nil {
		t.Fatalf("failed to fetch scenario-33: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassActive {
		t.Errorf("expected ACTIVE due to network I/O override, got %v", w.Classification)
	}
	if w.Action != decision.ActionKeep {
		t.Errorf("expected KEEP, got %v", w.Action)
	}
}

func TestRunner_Targeted_ZeroResourceRequest(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-39")
	if err != nil {
		t.Fatalf("failed to fetch scenario-39: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	w := resp.Workloads[0]
	if w.Classification != detector.ClassIdle {
		t.Errorf("expected IDLE, got %v", w.Classification)
	}
	// Verify no panic or NaN
	if w.Score < 0 || w.Score > 1.0 {
		t.Errorf("invalid score for zero request: %f", w.Score)
	}
}

func TestRunner_Targeted_MixedCluster(t *testing.T) {
	runner := NewPipelineRunner()
	s, err := GetPresetByID("scenario-67")
	if err != nil {
		t.Fatalf("failed to fetch scenario-67: %v", err)
	}
	resp := runner.ExecuteSimulation(s.Cluster)
	if resp.ClusterSummary.TotalWorkloads != 6 {
		t.Errorf("expected 6 workloads in summary, got %d", resp.ClusterSummary.TotalWorkloads)
	}
	if resp.ClusterSummary.TotalCapacityCPUMillis <= 0 {
		t.Errorf("expected positive total capacity CPU, got %d", resp.ClusterSummary.TotalCapacityCPUMillis)
	}
	if resp.ClusterSummary.ProjectedAvailableCPUMillis <= 0 {
		t.Errorf("expected positive projected available CPU, got %f", resp.ClusterSummary.ProjectedAvailableCPUMillis)
	}
}
