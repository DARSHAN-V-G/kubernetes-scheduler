package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"simulator/backend/models"
	"simulator/backend/simulation"
)

func TestHandleHealth(t *testing.T) {
	h := NewAPIHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()

	h.HandleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}

	if body["status"] != "healthy" {
		t.Errorf("expected status 'healthy', got %v", body["status"])
	}
}

func TestHandlePresets(t *testing.T) {
	h := NewAPIHandler()

	// 1. List all presets
	reqList := httptest.NewRequest(http.MethodGet, "/api/presets", nil)
	wList := httptest.NewRecorder()
	h.HandlePresets(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", wList.Code)
	}

	var meta []simulation.PresetMetadata
	if err := json.Unmarshal(wList.Body.Bytes(), &meta); err != nil {
		t.Fatalf("failed to decode presets list: %v", err)
	}
	if len(meta) < 7 {
		t.Errorf("expected at least 7 presets, got %d", len(meta))
	}

	// 2. Get specific preset
	reqItem := httptest.NewRequest(http.MethodGet, "/api/presets/preset-c", nil)
	wItem := httptest.NewRecorder()
	h.HandlePresets(wItem, reqItem)

	if wItem.Code != http.StatusOK {
		t.Fatalf("expected 200 for preset-c, got %d", wItem.Code)
	}

	var scenario simulation.PresetScenario
	if err := json.Unmarshal(wItem.Body.Bytes(), &scenario); err != nil {
		t.Fatalf("failed to decode preset-c: %v", err)
	}
	if scenario.Metadata.ID != "scenario-03" {
		t.Errorf("expected scenario-03, got %s", scenario.Metadata.ID)
	}
}

func TestHandleSimulate(t *testing.T) {
	h := NewAPIHandler()

	cluster := models.ClusterModel{
		Nodes: []models.SyntheticNode{
			{Name: "node-1", TotalCapacityCPUMillis: 4000, TotalCapacityMemoryBytes: 8192 * 1024 * 1024, IsReady: true},
		},
		Workloads: []models.SyntheticWorkload{
			{
				Name:                 "test-pod",
				Namespace:            "default",
				NodeName:             "node-1",
				Phase:                "Running",
				RequestedCPUMillis:   1000,
				RequestedMemoryBytes: 1024 * 1024 * 1024,
				UsageCPUMillicores:   800.0,
				UsageMemoryBytes:     500 * 1024 * 1024,
				RequestQPS:           100.0,
				IdleDurationSeconds:  0,
				IsIdle:               false,
			},
		},
	}

	data, err := json.Marshal(cluster)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/simulate", bytes.NewReader(data))
	w := httptest.NewRecorder()

	h.HandleSimulate(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.SimulationResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response error: %v", err)
	}

	if len(resp.Workloads) != 1 {
		t.Fatalf("expected 1 workload result, got %d", len(resp.Workloads))
	}
	if resp.Workloads[0].Name != "test-pod" {
		t.Errorf("expected workload name 'test-pod', got %s", resp.Workloads[0].Name)
	}
}
