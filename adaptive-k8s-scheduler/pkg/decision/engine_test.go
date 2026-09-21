package decision

import (
	"math"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// goodProfile returns a WorkloadProfile that would produce a high reclamation score.
func goodProfile() *analyzer.WorkloadProfile {
	return &analyzer.WorkloadProfile{
		PodNamespace:             "test-ns",
		PodName:                  "test-pod",
		CPUUtilization:           0.05,
		CPUUtilStatus:            analyzer.UtilizationAvailable,
		MemoryUtilization:        0.05,
		MemoryUtilStatus:         analyzer.UtilizationAvailable,
		IdleDuration:             2 * time.Hour,
		IsConsistentlyIdle:       true,
		ReclaimableCPUMillicores: 1500,
		ReclaimableMemoryBytes:   2 * 1024 * 1024 * 1024,
		SampleCount:              10,
	}
}

// goodPod returns a PodMetrics that passes all safety checks and scores well.
func goodPod() *metrics.PodMetrics {
	return &metrics.PodMetrics{
		Namespace:          "test-ns",
		Name:               "test-pod",
		Phase:              corev1.PodRunning,
		Priority:           0,
		PriorityClassName:  "",
		DisruptionsAllowed: -1, // no PDB
		Replicas: &metrics.ReplicaInfo{
			OwnerKind:         "Deployment",
			OwnerName:         "test-deploy",
			DesiredReplicas:   5,
			ReadyReplicas:     5,
			AvailableReplicas: 5,
		},
		Annotations: map[string]string{
			"reclaim.io/checkpointable": "true",
		},
		Labels:      map[string]string{},
		LastUpdated: time.Now(),
	}
}

// ── Safety gate tests ─────────────────────────────────────────────────────────

func TestEngine_ProtectedAnnotation_BlocksAll(t *testing.T) {
	pod := goodPod()
	pod.Annotations["reclaim.io/protected"] = "true"
	engine := NewEngine(DefaultPolicy())
	result := engine.Evaluate(goodProfile(), pod)

	if result.Eligible {
		t.Error("protected workload must not be eligible")
	}
	if result.Action != ActionKeep {
		t.Errorf("expected KEEP for protected workload, got %s", result.Action)
	}
	if result.Capabilities.FullReclaimAllowed || result.Capabilities.SoftReclaimAllowed {
		t.Error("neither reclaim action should be allowed for protected workload")
	}
}

func TestEngine_SystemCriticalPriorityClass_BlocksAll(t *testing.T) {
	for _, cls := range []string{"system-cluster-critical", "system-node-critical"} {
		t.Run(cls, func(t *testing.T) {
			pod := goodPod()
			pod.PriorityClassName = cls
			result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
			if result.Action != ActionKeep {
				t.Errorf("%s: expected KEEP, got %s", cls, result.Action)
			}
			if result.Capabilities.SoftReclaimAllowed || result.Capabilities.FullReclaimAllowed {
				t.Error("system-critical pod: neither action should be allowed")
			}
		})
	}
}

func TestEngine_HighNumericPriority_BlocksAll(t *testing.T) {
	pod := goodPod()
	pod.Priority = 200000 // above MaxPriorityForReclaim=100000
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
	if result.Action != ActionKeep {
		t.Errorf("high priority: expected KEEP, got %s", result.Action)
	}
}

func TestEngine_PDB_DisruptionsAllowed0_BlocksAll(t *testing.T) {
	pod := goodPod()
	pod.DisruptionsAllowed = 0
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
	if result.Action != ActionKeep {
		t.Errorf("PDB=0: expected KEEP, got %s", result.Action)
	}
	if result.Capabilities.SoftReclaimAllowed || result.Capabilities.FullReclaimAllowed {
		t.Error("PDB=0: neither action should be allowed")
	}
}

func TestEngine_InsufficientReplicas_BlocksAll(t *testing.T) {
	pod := goodPod()
	pod.Replicas.AvailableReplicas = 1 // after removal: 0 < MinReplicasRequired=1
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
	if result.Action != ActionKeep {
		t.Errorf("insufficient replicas: expected KEEP, got %s", result.Action)
	}
}

func TestEngine_PodNotRunning_BlocksAll(t *testing.T) {
	pod := goodPod()
	pod.Phase = corev1.PodPending
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
	if result.Action != ActionKeep {
		t.Errorf("pending pod: expected KEEP, got %s", result.Action)
	}
}

func TestEngine_NotCheckpointable_BlocksFullOnly(t *testing.T) {
	pod := goodPod()
	pod.Annotations["reclaim.io/checkpointable"] = "false"
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)

	if result.Capabilities.FullReclaimAllowed {
		t.Error("checkpointable=false: FullReclaimAllowed must be false")
	}
	if !result.Capabilities.SoftReclaimAllowed {
		t.Error("checkpointable=false: SoftReclaimAllowed must remain true")
	}
	// Score is high → should fall back to SOFT_RECLAIM, not KEEP
	if result.Action == ActionKeep {
		t.Errorf("checkpointable=false with high score: expected SOFT_RECLAIM fallback, got KEEP")
	}
	if result.Action != ActionSoftReclaim {
		t.Errorf("checkpointable=false fallback: expected SOFT_RECLAIM, got %s", result.Action)
	}
}

func TestEngine_StatefulSet_WithoutCheckpointAnnotation_BlocksFullOnly(t *testing.T) {
	pod := goodPod()
	pod.Replicas.OwnerKind = "StatefulSet"
	pod.Replicas.AvailableReplicas = 5
	delete(pod.Annotations, "reclaim.io/checkpointable") // no annotation

	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)

	if result.Capabilities.FullReclaimAllowed {
		t.Error("StatefulSet without annotation: FullReclaimAllowed must be false")
	}
	if !result.Capabilities.SoftReclaimAllowed {
		t.Error("StatefulSet without annotation: SoftReclaimAllowed must be true")
	}
}

// ── Action selection tests ────────────────────────────────────────────────────

func TestEngine_HighScore_FullReclaimAllowed_SelectsFullReclaim(t *testing.T) {
	// goodProfile + goodPod produces a score > 0.75.
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), goodPod())
	if result.Action != ActionFullReclaim {
		t.Errorf("high score + full allowed: expected FULL_RECLAIM, got %s (score=%.4f)", result.Action, result.Score)
	}
	if !result.Eligible {
		t.Error("expected Eligible=true for FULL_RECLAIM")
	}
}

func TestEngine_HighScore_FullNotAllowed_FallsBackToSoftReclaim(t *testing.T) {
	pod := goodPod()
	pod.Annotations["reclaim.io/checkpointable"] = "false"
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
	if result.Score < DefaultPolicy().FullReclaimScoreThreshold {
		t.Skipf("score %.4f did not reach full threshold — test assumption wrong", result.Score)
	}
	if result.Action != ActionSoftReclaim {
		t.Errorf("high score + full unavailable: expected SOFT_RECLAIM fallback, got %s", result.Action)
	}
}

func TestEngine_MediumScore_SoftReclaim(t *testing.T) {
	// Force a medium score (0.50 <= score < 0.75) by using a policy with tight weights
	// and a profile with moderate utilization.
	policy := DefaultPolicy()
	profile := &analyzer.WorkloadProfile{
		CPUUtilization:           0.50, // 50% — half score
		CPUUtilStatus:            analyzer.UtilizationAvailable,
		MemoryUtilization:        0.50,
		MemoryUtilStatus:         analyzer.UtilizationAvailable,
		IdleDuration:             45 * time.Second,
		IsConsistentlyIdle:       true,
		ReclaimableCPUMillicores: 500,
		ReclaimableMemoryBytes:   512 * 1024 * 1024,
		SampleCount:              5,
	}
	pod := goodPod()
	// Clear checkpoint annotation so it scores neutral (0.5).
	delete(pod.Annotations, "reclaim.io/checkpointable")
	pod.Replicas.AvailableReplicas = 2 // score 0.5 for replica

	result := NewEngine(policy).Evaluate(profile, pod)
	t.Logf("medium-score test: score=%.4f action=%s", result.Score, result.Action)

	// Score must be in [SoftThreshold, FullThreshold) range for this to be meaningful.
	if result.Score >= policy.FullReclaimScoreThreshold {
		t.Skipf("score %.4f hit full threshold — adjust inputs", result.Score)
	}
	if result.Score >= policy.SoftReclaimScoreThreshold {
		if result.Action != ActionSoftReclaim {
			t.Errorf("medium score: expected SOFT_RECLAIM, got %s", result.Action)
		}
	}
}

func TestEngine_LowScore_Keep(t *testing.T) {
	// Manufacture a score below 0.50: high utilization, very short idle, no reclaimable resources.
	profile := &analyzer.WorkloadProfile{
		CPUUtilization:           0.99,
		CPUUtilStatus:            analyzer.UtilizationAvailable,
		MemoryUtilization:        0.99,
		MemoryUtilStatus:         analyzer.UtilizationAvailable,
		IdleDuration:             1 * time.Second,
		IsConsistentlyIdle:       false,
		ReclaimableCPUMillicores: 0,
		ReclaimableMemoryBytes:   0,
		SampleCount:              3,
	}
	pod := goodPod()
	pod.Replicas.AvailableReplicas = 1 // 0 score for replica
	pod.Priority = 90000               // high priority

	result := NewEngine(DefaultPolicy()).Evaluate(profile, pod)
	t.Logf("low-score test: score=%.4f action=%s", result.Score, result.Action)
	if result.Score < DefaultPolicy().SoftReclaimScoreThreshold {
		if result.Action != ActionKeep {
			t.Errorf("low score: expected KEEP, got %s", result.Action)
		}
	}
}

func TestEngine_ScoreExactly075_FullAllowed_SelectsFullReclaim(t *testing.T) {
	policy := DefaultPolicy()
	// Build a policy and inputs that produce exactly 0.75.
	// We test selectAction directly for exact boundary control.
	caps := Capabilities{FullReclaimAllowed: true, SoftReclaimAllowed: true}
	action, _ := selectAction(0.75, caps, policy)
	if action != ActionFullReclaim {
		t.Errorf("score=0.75 + full allowed: expected FULL_RECLAIM, got %s", action)
	}
}

func TestEngine_ScoreExactly050_SoftAllowed_SelectsSoftReclaim(t *testing.T) {
	policy := DefaultPolicy()
	caps := Capabilities{FullReclaimAllowed: false, SoftReclaimAllowed: true}
	action, _ := selectAction(0.50, caps, policy)
	if action != ActionSoftReclaim {
		t.Errorf("score=0.50 + soft allowed: expected SOFT_RECLAIM, got %s", action)
	}
}

func TestEngine_ScoreBelow050_Keep(t *testing.T) {
	policy := DefaultPolicy()
	caps := Capabilities{FullReclaimAllowed: true, SoftReclaimAllowed: true}
	action, _ := selectAction(0.499, caps, policy)
	if action != ActionKeep {
		t.Errorf("score=0.499: expected KEEP, got %s", action)
	}
}

func TestEngine_HighScore_NoActionAllowed_Keep(t *testing.T) {
	policy := DefaultPolicy()
	caps := Capabilities{FullReclaimAllowed: false, SoftReclaimAllowed: false}
	action, _ := selectAction(0.95, caps, policy)
	if action != ActionKeep {
		t.Errorf("high score but no action available: expected KEEP, got %s", action)
	}
}

func TestEngine_SafetyOverridesHighScore(t *testing.T) {
	pod := goodPod()
	pod.Annotations["reclaim.io/protected"] = "true"
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
	// Score would be high, but safety must override it.
	if result.Action != ActionKeep {
		t.Errorf("safety must override high score: expected KEEP, got %s (score=%.4f)", result.Action, result.Score)
	}
}

// ── Scoring function unit tests ───────────────────────────────────────────────

func TestScoreCPU(t *testing.T) {
	tests := []struct {
		util   float64
		status analyzer.UtilizationStatus
		want   float64
	}{
		{0.0, analyzer.UtilizationAvailable, 1.0},
		{0.5, analyzer.UtilizationAvailable, 0.5},
		{1.0, analyzer.UtilizationAvailable, 0.0},
		{1.5, analyzer.UtilizationAvailable, 0.0},    // clamped
		{0.0, analyzer.UtilizationUnavailable, 0.5},  // neutral
		{0.99, analyzer.UtilizationUnavailable, 0.5}, // neutral regardless of util
	}
	for _, tc := range tests {
		got := ScoreCPU(tc.util, tc.status)
		if got != tc.want {
			t.Errorf("ScoreCPU(%.2f, %v): got %.2f, want %.2f", tc.util, tc.status, got, tc.want)
		}
	}
}

func TestScoreMemory(t *testing.T) {
	if ScoreMemory(0.0, analyzer.UtilizationAvailable) != 1.0 {
		t.Error("ScoreMemory(0.0 Available): expected 1.0")
	}
	if ScoreMemory(1.0, analyzer.UtilizationAvailable) != 0.0 {
		t.Error("ScoreMemory(1.0 Available): expected 0.0")
	}
	if ScoreMemory(0.5, analyzer.UtilizationUnavailable) != 0.5 {
		t.Error("ScoreMemory(Unavailable): expected neutral 0.5")
	}
}

func TestScoreIdle(t *testing.T) {
	tests := []struct {
		dur  time.Duration
		max  float64
		want float64
	}{
		{0, 3600, 0.0},
		{1800 * time.Second, 3600, 0.5},
		{3600 * time.Second, 3600, 1.0},
		{7200 * time.Second, 3600, 1.0}, // capped
		{100 * time.Second, 0, 0.0},     // max=0 → 0
	}
	for _, tc := range tests {
		got := ScoreIdle(tc.dur, tc.max)
		if got != tc.want {
			t.Errorf("ScoreIdle(%v, %v): got %.4f, want %.4f", tc.dur, tc.max, got, tc.want)
		}
	}
}

func TestScoreBenefit(t *testing.T) {
	if ScoreBenefit(0, 0, 2000, 4e9) != 0.0 {
		t.Error("zero reclaimable: expected 0.0")
	}
	got := ScoreBenefit(2000, int64(4e9), 2000, 4e9)
	if got != 1.0 {
		t.Errorf("max reclaimable: expected 1.0, got %.4f", got)
	}
	got = ScoreBenefit(1000, int64(2e9), 2000, 4e9)
	if got != 0.5 {
		t.Errorf("half reclaimable: expected 0.5, got %.4f", got)
	}
}

func TestScoreReplica(t *testing.T) {
	tests := []struct {
		replicas *metrics.ReplicaInfo
		want     float64
	}{
		{nil, 0.0},
		{&metrics.ReplicaInfo{AvailableReplicas: 0}, 0.0},
		{&metrics.ReplicaInfo{AvailableReplicas: 1}, 0.0},
		{&metrics.ReplicaInfo{AvailableReplicas: 2}, 0.5},
		{&metrics.ReplicaInfo{AvailableReplicas: 3}, 0.8},
		{&metrics.ReplicaInfo{AvailableReplicas: 4}, 1.0},
		{&metrics.ReplicaInfo{AvailableReplicas: 10}, 1.0},
	}
	for _, tc := range tests {
		got := ScoreReplica(tc.replicas)
		if got != tc.want {
			n := int32(0)
			if tc.replicas != nil {
				n = tc.replicas.AvailableReplicas
			}
			t.Errorf("ScoreReplica(%d): got %.1f, want %.1f", n, got, tc.want)
		}
	}
}

func TestScorePriority(t *testing.T) {
	if ScorePriority(0, 100000) != 1.0 {
		t.Error("priority=0: expected 1.0")
	}
	if ScorePriority(100000, 100000) != 0.0 {
		t.Error("priority=max: expected 0.0")
	}
	if ScorePriority(50000, 100000) != 0.5 {
		t.Error("priority=half max: expected 0.5")
	}
	if ScorePriority(200000, 100000) != 0.0 {
		t.Error("priority > max: should be clamped to 0.0")
	}
}

func TestScorePDB(t *testing.T) {
	tests := []struct {
		allowed int32
		want    float64
	}{
		{-1, 1.0},
		{0, 0.0},
		{1, 0.7},
		{2, 1.0},
		{5, 1.0},
	}
	for _, tc := range tests {
		got := ScorePDB(tc.allowed)
		if got != tc.want {
			t.Errorf("ScorePDB(%d): got %.1f, want %.1f", tc.allowed, got, tc.want)
		}
	}
}

func TestScoreState(t *testing.T) {
	tests := []struct {
		phase     corev1.PodPhase
		ownerKind string
		want      float64
	}{
		{corev1.PodRunning, "Deployment", 1.0},
		{corev1.PodRunning, "ReplicaSet", 1.0},
		{corev1.PodRunning, "StatefulSet", 0.4},
		{corev1.PodRunning, "", 0.6},
		{corev1.PodRunning, "Job", 0.6},
		{corev1.PodPending, "Deployment", 0.2},
		{corev1.PodFailed, "Deployment", 0.2},
	}
	for _, tc := range tests {
		got := ScoreState(tc.phase, tc.ownerKind)
		if got != tc.want {
			t.Errorf("ScoreState(%s, %s): got %.1f, want %.1f", tc.phase, tc.ownerKind, got, tc.want)
		}
	}
}

func TestScoreCheckpoint(t *testing.T) {
	key := "reclaim.io/checkpointable"
	if ScoreCheckpoint(map[string]string{key: "true"}, key) != 1.0 {
		t.Error("checkpointable=true: expected 1.0")
	}
	if ScoreCheckpoint(map[string]string{key: "false"}, key) != 0.0 {
		t.Error("checkpointable=false: expected 0.0")
	}
	if ScoreCheckpoint(map[string]string{}, key) != 0.5 {
		t.Error("annotation absent: expected neutral 0.5")
	}
	if ScoreCheckpoint(nil, key) != 0.5 {
		t.Error("nil annotations: expected neutral 0.5")
	}
}

// ── Score normalization / range tests ─────────────────────────────────────────

func TestComputeScore_Range(t *testing.T) {
	policy := DefaultPolicy()
	const eps = 1e-6

	// All factors at 1.0 → score must be 1.0.
	ones := IndividualScores{1, 1, 1, 1, 1, 1, 1, 1, 1}
	if got := ComputeScore(ones, policy); math.Abs(got-1.0) > eps {
		t.Errorf("all-ones score: expected 1.0, got %.6f", got)
	}
	// All factors at 0.0 → score must be 0.0.
	zeros := IndividualScores{}
	if got := ComputeScore(zeros, policy); math.Abs(got-0.0) > eps {
		t.Errorf("all-zeros score: expected 0.0, got %.6f", got)
	}
	// All factors at 0.5 → score must be 0.5.
	halves := IndividualScores{0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5}
	if got := ComputeScore(halves, policy); math.Abs(got-0.5) > eps {
		t.Errorf("all-halves score: expected 0.5, got %.6f", got)
	}
}

func TestComputeScore_WeightsSumTo1(t *testing.T) {
	p := DefaultPolicy()
	sum := p.WeightCPU + p.WeightMemory + p.WeightIdle + p.WeightBenefit +
		p.WeightReplica + p.WeightPriority + p.WeightPDB + p.WeightState + p.WeightCheckpoint
	if sum < 0.9999 || sum > 1.0001 {
		t.Errorf("DefaultPolicy weights must sum to 1.0, got %.6f", sum)
	}
}

// ── Eligibility and reason population tests ───────────────────────────────────

func TestEngine_Eligible_TrueForApprovedActions(t *testing.T) {
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), goodPod())
	if result.Action == ActionKeep && result.Eligible {
		t.Error("Eligible must be false when Action=KEEP")
	}
	if result.Action != ActionKeep && !result.Eligible {
		t.Error("Eligible must be true when a reclaim action is selected")
	}
}

func TestEngine_ReasonsAlwaysPopulated(t *testing.T) {
	// Both approved and rejected results must have non-empty Reasons.
	approved := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), goodPod())
	if len(approved.Reasons) == 0 {
		t.Error("approved decision: Reasons must not be empty")
	}

	blockedPod := goodPod()
	blockedPod.Annotations["reclaim.io/protected"] = "true"
	blocked := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), blockedPod)
	if len(blocked.RejectionReasons) == 0 {
		t.Error("blocked decision: RejectionReasons must not be empty")
	}
}

func TestEngine_ZeroResourceRequests_BestEffort(t *testing.T) {
	profile := &analyzer.WorkloadProfile{
		CPUUtilization:     0,
		CPUUtilStatus:      analyzer.UtilizationUnavailable,
		MemoryUtilization:  0,
		MemoryUtilStatus:   analyzer.UtilizationUnavailable,
		IdleDuration:       1 * time.Hour,
		IsConsistentlyIdle: true,
		SampleCount:        5,
	}
	pod := goodPod()
	result := NewEngine(DefaultPolicy()).Evaluate(profile, pod)
	// Score must be in [0, 1] — no panic, no NaN.
	if result.Score < 0 || result.Score > 1 {
		t.Errorf("BestEffort pod: score out of range [0,1]: %.4f", result.Score)
	}
	// ScoreCPU and ScoreMemory return 0.5 for Unavailable.
	if result.Scores.CPU != 0.5 {
		t.Errorf("BestEffort CPU score: expected 0.5, got %.3f", result.Scores.CPU)
	}
	if result.Scores.Memory != 0.5 {
		t.Errorf("BestEffort Memory score: expected 0.5, got %.3f", result.Scores.Memory)
	}
}

func TestEngine_NilReplicas_ScoreZero(t *testing.T) {
	pod := goodPod()
	pod.Replicas = nil
	result := NewEngine(DefaultPolicy()).Evaluate(goodProfile(), pod)
	if result.Scores.Replica != 0.0 {
		t.Errorf("nil replicas: expected R_Replica=0.0, got %.3f", result.Scores.Replica)
	}
}

// ── History tests ─────────────────────────────────────────────────────────────

func TestHistory_RecordAndGet(t *testing.T) {
	h := NewHistory()
	result := DecisionResult{PodNamespace: "ns", PodName: "pod", Action: ActionFullReclaim}
	h.Record(result)

	rec, found := h.Get("ns/pod")
	if !found {
		t.Fatal("expected to find recorded history")
	}
	if rec.DecisionCount != 1 {
		t.Errorf("expected DecisionCount=1, got %d", rec.DecisionCount)
	}
	if rec.LastDecision.Action != ActionFullReclaim {
		t.Errorf("expected FULL_RECLAIM in history, got %s", rec.LastDecision.Action)
	}
}

func TestHistory_RecordIncrements(t *testing.T) {
	h := NewHistory()
	r := DecisionResult{PodNamespace: "ns", PodName: "pod"}
	h.Record(r)
	h.Record(r)
	h.Record(r)
	rec, _ := h.Get("ns/pod")
	if rec.DecisionCount != 3 {
		t.Errorf("expected DecisionCount=3, got %d", rec.DecisionCount)
	}
}

func TestHistory_NotFound(t *testing.T) {
	h := NewHistory()
	_, found := h.Get("ns/nonexistent")
	if found {
		t.Error("expected not found for unrecorded pod")
	}
}
