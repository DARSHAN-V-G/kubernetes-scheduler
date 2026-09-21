package v1alpha1

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add v1alpha1 types to scheme: %v", err)
	}

	knownTypes := scheme.KnownTypes(GroupVersion)
	if _, ok := knownTypes["ReclaimPolicy"]; !ok {
		t.Errorf("ReclaimPolicy not registered in scheme")
	}
	if _, ok := knownTypes["CheckpointRecord"]; !ok {
		t.Errorf("CheckpointRecord not registered in scheme")
	}
}

func TestReclaimPolicySerializationAndDeepCopy(t *testing.T) {
	cpuPct := 0.04
	memPct := 0.08
	qps := 0.01
	netBytes := 5120.0
	minReplicas := int32(2)
	maxPriority := int32(50000)
	fullThresh := 0.80
	softThresh := 0.45
	wCPU := 0.25

	policy := &ReclaimPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "reclaim.io/v1alpha1",
			Kind:       "ReclaimPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "ecommerce",
			Labels: map[string]string{
				"tier": "backend",
			},
		},
		Spec: ReclaimPolicySpec{
			IdleDurationThreshold:  "15m",
			CPUIdleThresholdPct:    &cpuPct,
			MemoryIdleThresholdPct: &memPct,
			QPSIdleThreshold:       &qps,
			NetIdleThresholdBytes:  &netBytes,
			MinReplicasRequired:    &minReplicas,
			MaxPriorityForReclaim:  &maxPriority,
			ExemptPriorityClasses:  []string{"system-cluster-critical"},
			ActionType:             ActionTypeAdaptive,
			Weights: &ScoringWeights{
				WeightCPU: &wCPU,
			},
			ScoreThresholds: &ScoreThresholds{
				FullReclaimThreshold: &fullThresh,
				SoftReclaimThreshold: &softThresh,
			},
		},
		Status: ReclaimPolicyStatus{
			ObservedGeneration: 1,
			ActiveReclamations: 0,
			TotalReclaimedCPU:  "500m",
		},
	}

	// 1. Test JSON roundtrip
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("Failed to marshal ReclaimPolicy: %v", err)
	}

	var decoded ReclaimPolicy
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal ReclaimPolicy: %v", err)
	}

	if decoded.Spec.IdleDurationThreshold != "15m" {
		t.Errorf("Expected IdleDurationThreshold '15m', got %s", decoded.Spec.IdleDurationThreshold)
	}
	if decoded.Spec.ActionType != ActionTypeAdaptive {
		t.Errorf("Expected ActionType Adaptive, got %s", decoded.Spec.ActionType)
	}

	// 2. Test DeepCopy
	copied := policy.DeepCopy()
	if copied == nil {
		t.Fatalf("DeepCopy returned nil")
	}
	if copied.Name != policy.Name || copied.Namespace != policy.Namespace {
		t.Errorf("Copied metadata does not match")
	}

	// Verify deep mutation independence
	*copied.Spec.CPUIdleThresholdPct = 0.99
	if *policy.Spec.CPUIdleThresholdPct == 0.99 {
		t.Errorf("DeepCopy shared pointer for CPUIdleThresholdPct")
	}

	obj := policy.DeepCopyObject()
	if _, ok := obj.(*ReclaimPolicy); !ok {
		t.Errorf("DeepCopyObject did not return *ReclaimPolicy")
	}
}

func TestCheckpointRecordSerializationAndDeepCopy(t *testing.T) {
	now := metav1.Now()
	record := &CheckpointRecord{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "reclaim.io/v1alpha1",
			Kind:       "CheckpointRecord",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ckpt-analytics-worker-001",
			Namespace: "ecommerce",
		},
		Spec: CheckpointRecordSpec{
			SourcePodName:       "analytics-worker-xyz",
			SourcePodUID:        "1234-5678-90ab",
			NodeName:            "kind-worker-2",
			ContainerName:       "worker",
			ImageURI:            "worker:latest",
			CheckpointPath:      "/var/lib/kubelet/checkpoints/checkpoint-worker.tar",
			CheckpointSizeBytes: 10485760,
			ChecksumSHA256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			CapturedAt:          now,
			OriginalRequests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
			PodSpecSnapshot: `{"containers":[{"name":"worker","image":"worker:latest"}]}`,
		},
		Status: CheckpointRecordStatus{
			Phase:   CheckpointPhaseReady,
			Message: "Checkpoint verified and stored",
		},
	}

	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Failed to marshal CheckpointRecord: %v", err)
	}

	var decoded CheckpointRecord
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal CheckpointRecord: %v", err)
	}

	if decoded.Spec.SourcePodName != "analytics-worker-xyz" {
		t.Errorf("Expected sourcePodName 'analytics-worker-xyz', got %s", decoded.Spec.SourcePodName)
	}
	if decoded.Status.Phase != CheckpointPhaseReady {
		t.Errorf("Expected status phase Ready, got %s", decoded.Status.Phase)
	}

	// Test DeepCopy
	copied := record.DeepCopy()
	if copied == nil {
		t.Fatalf("DeepCopy returned nil")
	}
	if copied.Spec.CheckpointSizeBytes != 10485760 {
		t.Errorf("Expected size 10485760, got %d", copied.Spec.CheckpointSizeBytes)
	}
	copied.Spec.OriginalRequests[corev1.ResourceCPU] = resource.MustParse("1000m")
	origCPU := record.Spec.OriginalRequests[corev1.ResourceCPU]
	if origCPU.String() == "1000m" {
		t.Errorf("DeepCopy shared ResourceList map")
	}
}

func TestToDecisionPolicy(t *testing.T) {
	// Test nil spec returns defaults
	defaultPol := ToDecisionPolicy(nil)
	if defaultPol == nil {
		t.Fatalf("ToDecisionPolicy(nil) returned nil")
	}
	if defaultPol.FullReclaimScoreThreshold != 0.75 {
		t.Errorf("Expected default FullReclaimScoreThreshold 0.75, got %f", defaultPol.FullReclaimScoreThreshold)
	}

	// Test custom spec
	minR := int32(3)
	maxP := int32(80000)
	fullT := 0.85
	softT := 0.55
	wIdle := 0.30

	customSpec := &ReclaimPolicySpec{
		IdleDurationThreshold: "2h",
		MinReplicasRequired:   &minR,
		MaxPriorityForReclaim: &maxP,
		ScoreThresholds: &ScoreThresholds{
			FullReclaimThreshold: &fullT,
			SoftReclaimThreshold: &softT,
		},
		Weights: &ScoringWeights{
			WeightIdle: &wIdle,
		},
	}

	pol := ToDecisionPolicy(customSpec)
	if pol.MinReplicasRequired != 3 {
		t.Errorf("Expected MinReplicasRequired 3, got %d", pol.MinReplicasRequired)
	}
	if pol.MaxPriorityForReclaim != 80000 {
		t.Errorf("Expected MaxPriorityForReclaim 80000, got %d", pol.MaxPriorityForReclaim)
	}
	if pol.FullReclaimScoreThreshold != 0.85 {
		t.Errorf("Expected FullReclaimScoreThreshold 0.85, got %f", pol.FullReclaimScoreThreshold)
	}
	if pol.SoftReclaimScoreThreshold != 0.55 {
		t.Errorf("Expected SoftReclaimScoreThreshold 0.55, got %f", pol.SoftReclaimScoreThreshold)
	}
	if pol.WeightIdle != 0.30 {
		t.Errorf("Expected WeightIdle 0.30, got %f", pol.WeightIdle)
	}
	if pol.IdleMaxDurationSec != 7200.0 {
		t.Errorf("Expected IdleMaxDurationSec 7200, got %f", pol.IdleMaxDurationSec)
	}
}
