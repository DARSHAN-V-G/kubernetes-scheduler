package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"simulator/backend/simulation"
)

func TestHandleReclaimConfig_ReadOnly(t *testing.T) {
	runner := simulation.NewPipelineRunner()
	handler := NewClusterAPIHandler(runner)

	// 1. GET /api/reclaim/config
	getReq := httptest.NewRequest(http.MethodGet, "/api/reclaim/config", nil)
	getW := httptest.NewRecorder()
	handler.HandleReclaimConfig(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 on GET, got %d", getW.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if _, ok := resp["thresholds"]; !ok {
		t.Errorf("expected thresholds in response, got %v", resp)
	}

	// 2. PUT /api/reclaim/config should be rejected (Method Not Allowed)
	putReq := httptest.NewRequest(http.MethodPut, "/api/reclaim/config", bytes.NewReader([]byte("{}")))
	putW := httptest.NewRecorder()
	handler.HandleReclaimConfig(putW, putReq)

	if putW.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 on PUT, got %d", putW.Code)
	}
}

func TestHandleClusterStatus(t *testing.T) {
	runner := simulation.NewPipelineRunner()
	handler := NewClusterAPIHandler(runner)

	req := httptest.NewRequest(http.MethodGet, "/api/cluster/status", nil)
	w := httptest.NewRecorder()
	handler.HandleClusterStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed decoding status response: %v", err)
	}

	if resp["mode"] != "ACTIVE_CLUSTER" {
		t.Errorf("expected mode ACTIVE_CLUSTER, got %v", resp["mode"])
	}
}
