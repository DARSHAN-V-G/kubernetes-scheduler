package decision

import (
	"sync"
	"time"
)

// History is a thread-safe in-memory store of Decision Engine results.
// It is maintained for observability and future adaptive feedback.
//
// IMPORTANT: In this version, history does NOT influence scoring or decisions.
// The Engine records results AFTER deciding. It never reads from History during
// Evaluate(). Future adaptive feedback iterations may introduce read paths here,
// but only after the deterministic baseline has been validated experimentally.
type History struct {
	mu      sync.RWMutex
	records map[string]*HistoryRecord
}

// HistoryRecord holds the decision history for a single pod.
type HistoryRecord struct {
	// PodKey is "namespace/name".
	PodKey string
	// LastDecision is the most recent DecisionResult for this pod.
	LastDecision DecisionResult
	// DecisionCount is the total number of decisions recorded for this pod.
	DecisionCount int
	// UpdatedAt is when the record was last updated.
	UpdatedAt time.Time
}

// NewHistory creates a new empty decision history store.
func NewHistory() *History {
	return &History{
		records: make(map[string]*HistoryRecord),
	}
}

// Record stores or updates the decision result for a pod.
// Called by Engine.Evaluate() after every decision.
// This method has no effect on the decision just made or future scoring.
func (h *History) Record(result DecisionResult) {
	key := result.PodNamespace + "/" + result.PodName

	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, found := h.records[key]; found {
		existing.LastDecision = result
		existing.DecisionCount++
		existing.UpdatedAt = time.Now()
		return
	}

	h.records[key] = &HistoryRecord{
		PodKey:        key,
		LastDecision:  result,
		DecisionCount: 1,
		UpdatedAt:     time.Now(),
	}
}

// Get retrieves the history record for a pod by "namespace/name" key.
func (h *History) Get(podKey string) (HistoryRecord, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	record, found := h.records[podKey]
	if !found {
		return HistoryRecord{}, false
	}
	return *record, true
}

// Len returns the number of distinct pods tracked in history.
func (h *History) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.records)
}
