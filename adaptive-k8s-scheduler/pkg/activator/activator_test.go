package activator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBufferHTTPRequest(t *testing.T) {
	bodyData := []byte(`{"event":"test-payload","value":42}`)
	req, err := http.NewRequest("POST", "/api/v1/orders?priority=high", bytes.NewReader(bodyData))
	if err != nil {
		t.Fatalf("failed creating request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom-Trace", "trace-12345")
	req.Host = "backend-api.ecommerce.svc.cluster.local:3000"

	buffered, err := BufferHTTPRequest(req, 1024*1024)
	if err != nil {
		t.Fatalf("BufferHTTPRequest failed: %v", err)
	}

	if buffered.Method != "POST" {
		t.Errorf("expected POST, got %s", buffered.Method)
	}
	if buffered.URL != "/api/v1/orders?priority=high" {
		t.Errorf("expected URL with query, got %s", buffered.URL)
	}
	if string(buffered.Body) != string(bodyData) {
		t.Errorf("body mismatch: got %s, expected %s", string(buffered.Body), string(bodyData))
	}
	if buffered.Header.Get("X-Custom-Trace") != "trace-12345" {
		t.Errorf("header missing or corrupted")
	}

	// Replay request
	replayed, err := buffered.ToHTTPRequest(context.Background(), "http://10.96.0.1:3000")
	if err != nil {
		t.Fatalf("ToHTTPRequest failed: %v", err)
	}

	if replayed.URL.String() != "http://10.96.0.1:3000/api/v1/orders?priority=high" {
		t.Errorf("unexpected replayed target URL: %s", replayed.URL.String())
	}
	replayedBody, _ := io.ReadAll(replayed.Body)
	if string(replayedBody) != string(bodyData) {
		t.Errorf("replayed body mismatch: %s", string(replayedBody))
	}
}

func TestSingleFlightDeduplication(t *testing.T) {
	group := NewSingleFlightGroup()
	var execCount int32
	var wg sync.WaitGroup

	numConcurrent := 20
	wg.Add(numConcurrent)

	for i := 0; i < numConcurrent; i++ {
		go func() {
			defer wg.Done()
			err := group.Do("ecommerce/backend-api", func() error {
				atomic.AddInt32(&execCount, 1)
				time.Sleep(50 * time.Millisecond) // simulate CRIU restore
				return nil
			})
			if err != nil {
				t.Errorf("single flight error: %v", err)
			}
		}()
	}

	wg.Wait()

	if execCount != 1 {
		t.Errorf("expected exactly 1 execution for 20 concurrent requests, got %d", execCount)
	}
}

func TestParseTargetService(t *testing.T) {
	server := &ActivatorServer{
		cfg:    DefaultConfig(),
		logger: zap.NewNop(),
	}

	// 1. Via X-Target-Service header
	req1, _ := http.NewRequest("GET", "/api/health", nil)
	req1.Header.Set("X-Target-Service", "ecommerce/backend-api:3000")
	target1, err := server.ParseTargetService(req1)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if target1.Namespace != "ecommerce" || target1.Name != "backend-api" || target1.Port != 3000 {
		t.Errorf("target1 mismatch: %+v", target1)
	}

	// 2. Via Host header
	req2, _ := http.NewRequest("GET", "/api/products", nil)
	req2.Host = "frontend.ecommerce.svc.cluster.local:8080"
	target2, err := server.ParseTargetService(req2)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if target2.Name != "frontend" || target2.Namespace != "ecommerce" || target2.Port != 8080 {
		t.Errorf("target2 mismatch: %+v", target2)
	}

	// 3. Via query param
	req3, _ := http.NewRequest("GET", "/test?target_service=analytics:3001", nil)
	target3, err := server.ParseTargetService(req3)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if target3.Name != "analytics" || target3.Port != 3001 {
		t.Errorf("target3 mismatch: %+v", target3)
	}
}

type mockReadinessChecker struct {
	isReady bool
	target  string
}

func (m *mockReadinessChecker) IsServiceReady(ctx context.Context, namespace, serviceName string) (bool, error) {
	return m.isReady, nil
}

func (m *mockReadinessChecker) WaitUntilServiceReady(ctx context.Context, namespace, serviceName string, timeout time.Duration) error {
	m.isReady = true
	return nil
}

func (m *mockReadinessChecker) GetServiceTargetURL(namespace, serviceName string, defaultPort int) string {
	return m.target
}

func TestActivatorDirectProxyWhenLive(t *testing.T) {
	// Start a backend dummy HTTP server
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Handled", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("live-response-data"))
	}))
	defer backendServer.Close()

	readiness := &mockReadinessChecker{
		isReady: true,
		target:  backendServer.URL,
	}

	activator := &ActivatorServer{
		cfg:          DefaultConfig(),
		readiness:    readiness,
		singleFlight: NewSingleFlightGroup(),
		logger:       zap.NewNop(),
		httpClient:   backendServer.Client(),
	}

	req, _ := http.NewRequest("GET", "/api/v1/status", nil)
	req.Header.Set("X-Target-Service", "ecommerce/backend-api:3000")
	rec := httptest.NewRecorder()

	activator.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Header().Get("X-Backend-Handled") != "true" {
		t.Errorf("backend header missing")
	}
	if rec.Body.String() != "live-response-data" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

type mockCooldownRecorder struct {
	records map[string]time.Duration
}

func (m *mockCooldownRecorder) RecordCooldown(namespace, podOrServiceName string, duration time.Duration) {
	if m.records == nil {
		m.records = make(map[string]time.Duration)
	}
	key := fmt.Sprintf("%s/%s", namespace, podOrServiceName)
	m.records[key] = duration
}

func TestActivatorAutoStartBuffering(t *testing.T) {
	// Target backend server starts, but readiness initially reports false
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(fmt.Sprintf("created: %s", string(body))))
	}))
	defer backendServer.Close()

	readiness := &mockReadinessChecker{
		isReady: false, // initially dormant
		target:  backendServer.URL,
	}

	cooldown := &mockCooldownRecorder{}
	cfg := DefaultConfig()
	cfg.ReadinessTimeout = 2 * time.Second

	activator := &ActivatorServer{
		cfg:          cfg,
		readiness:    readiness,
		cooldown:     cooldown,
		singleFlight: NewSingleFlightGroup(),
		logger:       zap.NewNop(),
		httpClient:   backendServer.Client(),
	}

	req, _ := http.NewRequest("POST", "/api/v1/orders", bytes.NewReader([]byte("order-101")))
	req.Header.Set("X-Target-Service", "ecommerce/backend-api:3000")
	rec := httptest.NewRecorder()

	activator.ServeHTTP(rec, req)

	// Since mock readiness becomes ready inside WaitUntilServiceReady, request completes
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d. Body: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "created: order-101" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}

	// Verify cooldown was recorded
	if cooldown.records["ecommerce/backend-api"] != cfg.WarmupCooldown {
		t.Errorf("cooldown not recorded for backend-api: %+v", cooldown.records)
	}
}
