package cluster

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// WorkloadLifecycleState represents the real state machine phase.
type WorkloadLifecycleState string

const (
	StateRunning       WorkloadLifecycleState = "RUNNING"
	StateCandidate     WorkloadLifecycleState = "CANDIDATE"
	StateCheckpointing WorkloadLifecycleState = "CHECKPOINTING"
	StateCheckpointed  WorkloadLifecycleState = "CHECKPOINTED"
	StateReclaimed     WorkloadLifecycleState = "RECLAIMED"
	StateRestoring     WorkloadLifecycleState = "RESTORING"
	StateRestored      WorkloadLifecycleState = "RESTORED"
	StateFailed        WorkloadLifecycleState = "RECLAMATION_FAILED"
)

// WorkloadClusterState maintains the dynamic lifecycle state of an active workload.
type WorkloadClusterState struct {
	Namespace       string                 `json:"namespace"`
	Name            string                 `json:"name"`
	State           WorkloadLifecycleState `json:"state"`
	StateDetail     string                 `json:"stateDetail,omitempty"`
	Score           float64                `json:"score"`
	Action          string                 `json:"action"`
	DecisionReasons []string               `json:"decisionReasons,omitempty"`
	SafetyPassed    bool                   `json:"safetyPassed"`
	CheckpointPath  string                 `json:"checkpointPath,omitempty"`
	CheckpointedAt  *time.Time             `json:"checkpointedAt,omitempty"`
	ReclaimedAt     *time.Time             `json:"reclaimedAt,omitempty"`
	RestoredAt      *time.Time             `json:"restoredAt,omitempty"`
	LastError       string                 `json:"lastError,omitempty"`
	SnapshotPod     *corev1.Pod            `json:"-"`
	UpdatedAt       time.Time              `json:"updatedAt"`
}

// StateStore provides thread-safe in-memory caching of workload lifecycle transitions.
type StateStore struct {
	mu     sync.RWMutex
	states map[string]*WorkloadClusterState
}

// NewStateStore initializes an empty StateStore.
func NewStateStore() *StateStore {
	return &StateStore{
		states: make(map[string]*WorkloadClusterState),
	}
}

func stateKey(namespace, name string) string {
	return namespace + "/" + name
}

// Get retrieves state or creates a default RUNNING state.
func (s *StateStore) Get(namespace, name string) *WorkloadClusterState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := stateKey(namespace, name)
	if item, ok := s.states[key]; ok {
		copy := *item
		return &copy
	}
	return &WorkloadClusterState{
		Namespace: namespace,
		Name:      name,
		State:     StateRunning,
		UpdatedAt: time.Now(),
	}
}

// GetAll returns all tracked workload states.
func (s *StateStore) GetAll() map[string]*WorkloadClusterState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make(map[string]*WorkloadClusterState, len(s.states))
	for k, v := range s.states {
		copy := *v
		copied[k] = &copy
	}
	return copied
}

// SetState updates the lifecycle state and details for a workload.
func (s *StateStore) SetState(namespace, name string, state WorkloadLifecycleState, detail, errStr string) *WorkloadClusterState {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateKey(namespace, name)
	item, ok := s.states[key]
	if !ok {
		item = &WorkloadClusterState{
			Namespace: namespace,
			Name:      name,
		}
		s.states[key] = item
	}

	item.State = state
	item.StateDetail = detail
	item.LastError = errStr
	item.UpdatedAt = time.Now()
	now := time.Now()

	switch state {
	case StateCheckpointed:
		item.CheckpointedAt = &now
	case StateReclaimed:
		item.ReclaimedAt = &now
	case StateRestored:
		item.RestoredAt = &now
	}

	copy := *item
	return &copy
}

// RecordDecision updates the candidate state and score from pipeline evaluation.
func (s *StateStore) RecordDecision(namespace, name string, score float64, action string, passed bool, reasons []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateKey(namespace, name)
	item, ok := s.states[key]
	if !ok {
		item = &WorkloadClusterState{
			Namespace: namespace,
			Name:      name,
			State:     StateRunning,
		}
		s.states[key] = item
	}

	item.Score = score
	item.Action = action
	item.SafetyPassed = passed
	item.DecisionReasons = reasons
	item.UpdatedAt = time.Now()

	if item.State == StateRunning && action != "KEEP" {
		item.State = StateCandidate
		item.StateDetail = "Selected as reclamation candidate by decision engine"
	} else if item.State == StateCandidate && action == "KEEP" {
		item.State = StateRunning
		item.StateDetail = "Workload retained (KEEP)"
	}
}

// RecordCheckpoint sets the checkpoint archive path.
func (s *StateStore) RecordCheckpoint(namespace, name, archivePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateKey(namespace, name)
	if item, ok := s.states[key]; ok {
		item.CheckpointPath = archivePath
		now := time.Now()
		item.CheckpointedAt = &now
		item.UpdatedAt = now
	}
}

// SaveSnapshot saves the original pod spec for restoration.
func (s *StateStore) SaveSnapshot(namespace, name string, pod *corev1.Pod) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateKey(namespace, name)
	if item, ok := s.states[key]; ok {
		if pod != nil {
			item.SnapshotPod = pod.DeepCopy()
		}
	}
}
