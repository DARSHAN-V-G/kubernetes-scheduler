package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// TrafficSimulator coordinates simulated user traffic against the application.
type TrafficSimulator struct {
	mu           sync.Mutex
	running      bool
	targetURL    string
	requestsSent int64
	successCount int64
	errorCount   int64
	startedAt    time.Time
	lastActiveAt time.Time
	cancel       context.CancelFunc
	client       *http.Client
}

// NewTrafficSimulator creates a new traffic generator.
func NewTrafficSimulator() *TrafficSimulator {
	return &TrafficSimulator{
		targetURL: "http://localhost:8088",
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// Start launches a continuous background traffic generation loop.
func (s *TrafficSimulator) Start(targetURL string, concurrency int, intervalMs int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return false
	}

	if targetURL == "" {
		targetURL = "http://localhost:8088"
	}
	if concurrency <= 0 {
		concurrency = 3
	}
	if intervalMs <= 0 {
		intervalMs = 800
	}

	s.targetURL = targetURL
	s.running = true
	s.startedAt = time.Now()
	s.lastActiveAt = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	for i := 0; i < concurrency; i++ {
		go func(workerID int) {
			ticker := time.NewTicker(time.Duration(intervalMs) * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.executeSession(ctx)
				}
			}
		}(i)
	}

	return true
}

// Stop terminates active traffic workers.
func (s *TrafficSimulator) Stop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return false
	}

	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.running = false
	return true
}

// Burst sends a specified number of requests in parallel.
func (s *TrafficSimulator) Burst(targetURL string, count int) int {
	if targetURL == "" {
		targetURL = s.targetURL
	}
	if targetURL == "" {
		targetURL = "http://localhost:8088"
	}
	if count <= 0 {
		count = 20
	}

	var wg sync.WaitGroup
	var burstSuccess int64

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if s.executeSingleCall(ctx, targetURL) {
				atomic.AddInt64(&burstSuccess, 1)
			}
		}()
	}

	wg.Wait()
	return int(burstSuccess)
}

func (s *TrafficSimulator) executeSession(ctx context.Context) {
	s.mu.Lock()
	baseURL := s.targetURL
	s.mu.Unlock()

	atomic.AddInt64(&s.requestsSent, 1)
	s.mu.Lock()
	s.lastActiveAt = time.Now()
	s.mu.Unlock()

	randNum := rand.Intn(900000) + 100000
	username := fmt.Sprintf("user_%d", randNum)
	password := "SecretPass123!"
	email := fmt.Sprintf("%s@example.com", username)

	// 1. Health check or Catalog
	req1, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/products", nil)
	resp1, err1 := s.client.Do(req1)
	if err1 == nil && resp1.StatusCode < 400 {
		_ = resp1.Body.Close()
		atomic.AddInt64(&s.successCount, 1)
	} else {
		if resp1 != nil {
			_ = resp1.Body.Close()
		}
		atomic.AddInt64(&s.errorCount, 1)
		return
	}

	// 2. Register
	regPayload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
		"email":    email,
	})
	req2, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/users/register", bytes.NewBuffer(regPayload))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err2 := s.client.Do(req2)
	if err2 == nil && resp2.StatusCode < 400 {
		_ = resp2.Body.Close()
		atomic.AddInt64(&s.successCount, 1)
	} else {
		if resp2 != nil {
			_ = resp2.Body.Close()
		}
		atomic.AddInt64(&s.errorCount, 1)
	}

	// 3. Login
	loginPayload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	req3, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/users/login", bytes.NewBuffer(loginPayload))
	req3.Header.Set("Content-Type", "application/json")
	resp3, err3 := s.client.Do(req3)
	token := ""
	if err3 == nil && resp3.StatusCode < 400 {
		var lResult struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(resp3.Body).Decode(&lResult)
		_ = resp3.Body.Close()
		token = lResult.Token
		atomic.AddInt64(&s.successCount, 1)
	} else {
		if resp3 != nil {
			_ = resp3.Body.Close()
		}
		atomic.AddInt64(&s.errorCount, 1)
	}

	// 4. Place Order if token present
	if token != "" {
		orderPayload, _ := json.Marshal(map[string]interface{}{
			"items": []map[string]interface{}{
				{"product_id": "p1", "quantity": 1, "price": 29.99},
				{"product_id": "p2", "quantity": 1, "price": 14.99},
			},
			"total_amount": 44.98,
		})
		req4, _ := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/orders", bytes.NewBuffer(orderPayload))
		req4.Header.Set("Content-Type", "application/json")
		req4.Header.Set("Authorization", "Bearer "+token)
		resp4, err4 := s.client.Do(req4)
		if err4 == nil && resp4.StatusCode < 400 {
			_ = resp4.Body.Close()
			atomic.AddInt64(&s.successCount, 1)
		} else {
			if resp4 != nil {
				_ = resp4.Body.Close()
			}
			atomic.AddInt64(&s.errorCount, 1)
		}
	}
}

func (s *TrafficSimulator) executeSingleCall(ctx context.Context, baseURL string) bool {
	atomic.AddInt64(&s.requestsSent, 1)
	s.mu.Lock()
	s.lastActiveAt = time.Now()
	s.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/products", nil)
	if err != nil {
		atomic.AddInt64(&s.errorCount, 1)
		return false
	}

	resp, err := s.client.Do(req)
	if err != nil || resp.StatusCode >= 400 {
		if resp != nil {
			_ = resp.Body.Close()
		}
		atomic.AddInt64(&s.errorCount, 1)
		return false
	}

	_ = resp.Body.Close()
	atomic.AddInt64(&s.successCount, 1)
	return true
}

// Status returns current metrics for the traffic generator.
func (s *TrafficSimulator) Status() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	elapsedSec := 0
	qps := 0.0
	if s.running && !s.startedAt.IsZero() {
		elapsedSec = int(time.Since(s.startedAt).Seconds())
		if elapsedSec > 0 {
			qps = float64(atomic.LoadInt64(&s.requestsSent)) / float64(elapsedSec)
		}
	}

	return map[string]interface{}{
		"running":      s.running,
		"targetUrl":    s.targetURL,
		"requestsSent": atomic.LoadInt64(&s.requestsSent),
		"successCount": atomic.LoadInt64(&s.successCount),
		"errorCount":   atomic.LoadInt64(&s.errorCount),
		"elapsedSec":   elapsedSec,
		"currentQps":   fmt.Sprintf("%.1f", qps),
		"lastActiveAt": s.lastActiveAt.Format(time.RFC3339),
	}
}

// Wake triggers scale-from-zero activation through the Demand Activator.
func (s *TrafficSimulator) Wake(activatorURL, targetService string) (map[string]interface{}, error) {
	if activatorURL == "" {
		activatorURL = "http://localhost:8085"
	}
	if targetService == "" {
		targetService = "ecommerce/backend-api:3000"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "GET", activatorURL+"/api/health", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Target-Service", targetService)

	resp, err := s.client.Do(req)
	duration := time.Since(start)

	if err != nil {
		return map[string]interface{}{
			"success":     false,
			"error":       err.Error(),
			"durationSec": duration.Seconds(),
		}, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var bodyJSON map[string]interface{}
	_ = json.Unmarshal(bodyBytes, &bodyJSON)

	return map[string]interface{}{
		"success":     resp.StatusCode < 400,
		"statusCode":  resp.StatusCode,
		"durationSec": fmt.Sprintf("%.2fs", duration.Seconds()),
		"response":    bodyJSON,
	}, nil
}

// TrafficAPIHandler exposes HTTP REST endpoints for traffic generation.
type TrafficAPIHandler struct {
	sim *TrafficSimulator
}

// NewTrafficAPIHandler creates a new traffic API handler.
func NewTrafficAPIHandler() *TrafficAPIHandler {
	return &TrafficAPIHandler{
		sim: NewTrafficSimulator(),
	}
}

func (h *TrafficAPIHandler) HandleTrafficStart(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TargetURL   string `json:"targetUrl"`
		Concurrency int    `json:"concurrency"`
		IntervalMs  int    `json:"intervalMs"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	started := h.sim.Start(req.TargetURL, req.Concurrency, req.IntervalMs)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"started": started,
		"status":  h.sim.Status(),
	})
}

func (h *TrafficAPIHandler) HandleTrafficStop(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stopped := h.sim.Stop()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"stopped": stopped,
		"status":  h.sim.Status(),
	})
}

func (h *TrafficAPIHandler) HandleTrafficBurst(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TargetURL string `json:"targetUrl"`
		Count     int    `json:"count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	successful := h.sim.Burst(req.TargetURL, req.Count)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"requested":  req.Count,
		"successful": successful,
		"status":     h.sim.Status(),
	})
}

func (h *TrafficAPIHandler) HandleTrafficStatus(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.sim.Status())
}

func (h *TrafficAPIHandler) HandleTrafficWake(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ActivatorURL  string `json:"activatorUrl"`
		TargetService string `json:"targetService"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	result, err := h.sim.Wake(req.ActivatorURL, req.TargetService)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
	}
	_ = json.NewEncoder(w).Encode(result)
}
