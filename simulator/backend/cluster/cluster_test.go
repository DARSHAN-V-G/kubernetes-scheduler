package cluster

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

)

func TestStateStore_Transitions(t *testing.T) {
	store := NewStateStore()
	ns, name := "ecommerce", "workload-a"

	// 1. Initial state defaults to RUNNING
	st := store.Get(ns, name)
	if st.State != StateRunning {
		t.Errorf("expected initial state RUNNING, got %v", st.State)
	}

	// 2. Candidate decision
	store.RecordDecision(ns, name, 0.82, "FULL_RECLAIM", true, []string{"Idle duration high"})
	st = store.Get(ns, name)
	if st.State != StateCandidate {
		t.Errorf("expected state CANDIDATE, got %v", st.State)
	}
	if st.Score != 0.82 {
		t.Errorf("expected score 0.82, got %v", st.Score)
	}

	// 3. Checkpointing
	store.SetState(ns, name, StateCheckpointing, "Starting checkpoint", "")
	st = store.Get(ns, name)
	if st.State != StateCheckpointing {
		t.Errorf("expected state CHECKPOINTING, got %v", st.State)
	}

	// 4. Checkpointed
	archive := "/var/lib/kubelet/checkpoints/chk-123.tar"
	store.RecordCheckpoint(ns, name, archive)
	store.SetState(ns, name, StateCheckpointed, "Archive ready", "")
	st = store.Get(ns, name)
	if st.State != StateCheckpointed || st.CheckpointPath != archive {
		t.Errorf("expected CHECKPOINTED with archive %s, got %v, %s", archive, st.State, st.CheckpointPath)
	}

	// 5. Reclaimed
	store.SetState(ns, name, StateReclaimed, "Resources reclaimed", "")
	st = store.Get(ns, name)
	if st.State != StateReclaimed || st.ReclaimedAt == nil {
		t.Errorf("expected RECLAIMED with timestamp, got %v, %v", st.State, st.ReclaimedAt)
	}

	// 6. Restored
	store.SetState(ns, name, StateRestored, "Pod restored", "")
	st = store.Get(ns, name)
	if st.State != StateRestored || st.RestoredAt == nil {
		t.Errorf("expected RESTORED with timestamp, got %v, %v", st.State, st.RestoredAt)
	}
}

func TestLifecycleCoordinator_SafetyBlocking(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	store := NewStateStore()
	criuMgr := NewCRIUManager(fakeClient, nil, nil)
	coord := NewLifecycleCoordinator(fakeClient, criuMgr, store, nil)
	ctx := context.Background()

	// Create a protected pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "protected-worker",
			Namespace: "ecommerce",
			Annotations: map[string]string{
				"reclaim.io/protected": "true",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	_, err := fakeClient.CoreV1().Pods("ecommerce").Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Attempt reclamation on protected workload
	_, err = coord.ExecuteReclamation(ctx, "ecommerce", "protected-worker")
	if err == nil {
		t.Fatal("expected reclamation to fail for protected workload, got nil")
	}

	st := store.Get("ecommerce", "protected-worker")
	if st.State != StateFailed {
		t.Errorf("expected StateFailed, got %v", st.State)
	}
}

func TestCRIUManager_StatusStructure(t *testing.T) {
	mgr := NewCRIUManager(fake.NewSimpleClientset(), nil, nil)
	status := mgr.CheckStatus(context.Background())
	// Status returns a valid structure without crashing
	if status.Details == "" {
		t.Error("expected status details to be populated")
	}
}
