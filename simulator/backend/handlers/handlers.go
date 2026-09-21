package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"simulator/backend/models"
	"simulator/backend/simulation"
)

// APIHandler coordinates all REST endpoints for the simulator.
type APIHandler struct {
	runner *simulation.PipelineRunner
}

// NewAPIHandler constructs a handler with the shared PipelineRunner.
func NewAPIHandler() *APIHandler {
	return &APIHandler{
		runner: simulation.NewPipelineRunner(),
	}
}

// EnableCORS sets headers to allow local cross-origin development if needed.
func EnableCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

// HandleHealth returns the system health and intelligence pipeline version info.
func (h *APIHandler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	pol := h.runner.Policy()
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "healthy",
		"service": "adaptive-k8s-scheduler-simulator",
		"pipeline": map[string]string{
			"analyzer": "pkg/analyzer (authoritative)",
			"detector": "pkg/detector (authoritative)",
			"decision": "pkg/decision (authoritative)",
		},
		"policy": map[string]interface{}{
			"weights": map[string]float64{
				"CPU":        pol.WeightCPU,
				"Memory":     pol.WeightMemory,
				"Idle":       pol.WeightIdle,
				"Benefit":    pol.WeightBenefit,
				"Replica":    pol.WeightReplica,
				"Priority":   pol.WeightPriority,
				"PDB":        pol.WeightPDB,
				"State":      pol.WeightState,
				"Checkpoint": pol.WeightCheckpoint,
			},
			"thresholds": map[string]float64{
				"fullReclaim": pol.FullReclaimScoreThreshold,
				"softReclaim": pol.SoftReclaimScoreThreshold,
			},
			"annotations": map[string]string{
				"checkpointable": pol.CheckpointableAnnotation,
				"protected":      pol.ProtectedAnnotation,
			},
		},
	})
}

// HandleListPresets returns metadata for all available preset scenarios.
func (h *APIHandler) HandlePresets(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	// Check if a specific preset is requested via path: /api/presets/{id}
	path := strings.TrimPrefix(r.URL.Path, "/api/presets")
	path = strings.TrimPrefix(path, "/")

	w.Header().Set("Content-Type", "application/json")

	if path != "" {
		preset, err := simulation.GetPresetByID(path)
		if err != nil {
			http.Error(w, `{"error":"preset not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(preset)
		return
	}

	// Return list of all preset metadata
	presets := simulation.GetAllPresetMetadata()
	_ = json.NewEncoder(w).Encode(presets)
}

// HandleSimulate receives a ClusterModel and returns authoritative simulation results.
func (h *APIHandler) HandleSimulate(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var cluster models.ClusterModel
	if err := json.NewDecoder(r.Body).Decode(&cluster); err != nil {
		http.Error(w, `{"error":"invalid JSON body: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	resp := h.runner.ExecuteSimulation(cluster)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleSimulateWorkload evaluates a single workload on the fly.
func (h *APIHandler) HandleSimulateWorkload(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var sw models.SyntheticWorkload
	if err := json.NewDecoder(r.Body).Decode(&sw); err != nil {
		http.Error(w, `{"error":"invalid JSON body: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	result := h.runner.ExecuteSingleWorkload(sw)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
