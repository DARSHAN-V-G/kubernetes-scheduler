package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Config struct {
	TargetDefault   string
	TargetAdaptive  string
	Mode            string // "realtime" or "timewarp"
	TimeWarpFactor  float64
	StatsInterval   time.Duration
	MetricsFile     string
	RequestsFile    string
	OverallDuration time.Duration
	SimulateBursts  bool
}

type RequestLogEntry struct {
	Timestamp       string  `json:"timestamp"`
	Target          string  `json:"target"`
	TargetURL       string  `json:"target_url"`
	Method          string  `json:"method"`
	Path            string  `json:"path"`
	StatusCode      int     `json:"status_code"`
	StatusText      string  `json:"status_text"`
	DurationMs      float64 `json:"duration_ms"`
	IsTimeout       bool    `json:"is_timeout"`
	ResponseSnippet string  `json:"response_snippet"`
}

type RequestTracker struct {
	mu         sync.Mutex
	entries    []RequestLogEntry
	maxEntries int
	filePath   string
	dirty      bool
}

func NewRequestTracker(filePath string, maxEntries int) *RequestTracker {
	return &RequestTracker{
		entries:    make([]RequestLogEntry, 0, maxEntries),
		maxEntries: maxEntries,
		filePath:   filePath,
	}
}

func (rt *RequestTracker) Record(entry RequestLogEntry) {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Prepend entry so newest is at the top
	rt.entries = append([]RequestLogEntry{entry}, rt.entries...)
	if len(rt.entries) > rt.maxEntries {
		rt.entries = rt.entries[:rt.maxEntries]
	}
	rt.dirty = true
}

func (rt *RequestTracker) Flush() {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	if !rt.dirty || rt.filePath == "" {
		rt.mu.Unlock()
		return
	}
	snapshot := make([]RequestLogEntry, len(rt.entries))
	copy(snapshot, rt.entries)
	rt.dirty = false
	filePath := rt.filePath
	rt.mu.Unlock()

	data, err := json.MarshalIndent(map[string]interface{}{
		"updated_at": time.Now().UTC().Format(time.RFC3339),
		"requests":   snapshot,
	}, "", "  ")

	if err == nil {
		tmpPath := filePath + ".tmp"
		if err := os.WriteFile(tmpPath, data, 0644); err == nil {
			_ = os.Rename(tmpPath, filePath)
		}
	}
}


type MetricsCollector struct {
	mu sync.Mutex

	TotalDispatched uint64

	// Default scheduler stats
	DefSuccess   uint64
	DefErrors    uint64
	DefTimeouts  uint64
	DefLatencies []float64

	// Adaptive scheduler stats
	AdpSuccess   uint64
	AdpErrors    uint64
	AdpTimeouts  uint64
	AdpLatencies []float64

	csvWriter *csv.Writer
	csvFile   *os.File
}

func NewMetricsCollector(filePath string) (*MetricsCollector, error) {
	f, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	w := csv.NewWriter(f)
	// CSV Header
	_ = w.Write([]string{
		"timestamp", "sim_hour", "regime", "target_qps",
		"def_success", "def_errors", "def_timeouts", "def_p50_ms", "def_p95_ms",
		"adp_success", "adp_errors", "adp_timeouts", "adp_p50_ms", "adp_p95_ms",
	})
	w.Flush()

	return &MetricsCollector{
		DefLatencies: make([]float64, 0, 5000),
		AdpLatencies: make([]float64, 0, 5000),
		csvWriter:    w,
		csvFile:      f,
	}, nil
}

func (m *MetricsCollector) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.csvWriter != nil {
		m.csvWriter.Flush()
	}
	if m.csvFile != nil {
		_ = m.csvFile.Close()
	}
}

func (m *MetricsCollector) RecordResult(target string, statusCode int, duration time.Duration, isTimeout bool) {
	durMs := float64(duration.Microseconds()) / 1000.0

	m.mu.Lock()
	defer m.mu.Unlock()

	if target == "default" {
		if isTimeout {
			m.DefTimeouts++
		} else if statusCode >= 200 && statusCode < 400 {
			m.DefSuccess++
			m.DefLatencies = append(m.DefLatencies, durMs)
		} else {
			m.DefErrors++
		}
	} else {
		if isTimeout {
			m.AdpTimeouts++
		} else if statusCode >= 200 && statusCode < 400 {
			m.AdpSuccess++
			m.AdpLatencies = append(m.AdpLatencies, durMs)
		} else {
			m.AdpErrors++
		}
	}
}

func calcPercentile(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0.0
	}
	// Quick approximate sort/index
	idx := int(float64(len(values)-1) * percentile)
	// For small slices, simple partial sort
	for i := 0; i <= idx; i++ {
		minIdx := i
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[minIdx] {
				minIdx = j
			}
		}
		values[i], values[minIdx] = values[minIdx], values[i]
	}
	return values[idx]
}

func (m *MetricsCollector) SnapshotAndFlush(simHour float64, regime string, targetQPS float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	defP50 := calcPercentile(m.DefLatencies, 0.50)
	defP95 := calcPercentile(m.DefLatencies, 0.95)
	adpP50 := calcPercentile(m.AdpLatencies, 0.50)
	adpP95 := calcPercentile(m.AdpLatencies, 0.95)

	ts := time.Now().UTC().Format(time.RFC3339)

	// Write to CSV
	_ = m.csvWriter.Write([]string{
		ts,
		fmt.Sprintf("%.2f", simHour),
		regime,
		fmt.Sprintf("%.2f", targetQPS),
		fmt.Sprintf("%d", m.DefSuccess),
		fmt.Sprintf("%d", m.DefErrors),
		fmt.Sprintf("%d", m.DefTimeouts),
		fmt.Sprintf("%.2f", defP50),
		fmt.Sprintf("%.2f", defP95),
		fmt.Sprintf("%d", m.AdpSuccess),
		fmt.Sprintf("%d", m.AdpErrors),
		fmt.Sprintf("%d", m.AdpTimeouts),
		fmt.Sprintf("%.2f", adpP50),
		fmt.Sprintf("%.2f", adpP95),
	})
	m.csvWriter.Flush()

	log.Printf("[METRICS] SimHour: %05.2f | Regime: %-16s | QPS: %5.1f | Def: [OK:%d, Err:%d, P95:%.1fms] | Adp: [OK:%d, Err:%d, P95:%.1fms]",
		simHour, regime, targetQPS,
		m.DefSuccess, m.DefErrors+m.DefTimeouts, defP95,
		m.AdpSuccess, m.AdpErrors+m.AdpTimeouts, adpP95)

	// Reset latency buffers for the next interval
	m.DefLatencies = m.DefLatencies[:0]
	m.AdpLatencies = m.AdpLatencies[:0]
}

// CalculateDiurnalRate calculates arrival rate λ(t) based on simulated hour of day [0, 24)
func CalculateDiurnalRate(hourOfDay float64, simulateBursts bool) (float64, string) {
	h := math.Mod(hourOfDay, 24.0)

	var baseRate float64
	var regime string

	switch {
	case h >= 23.0 || h < 7.0:
		// Regime 1: Deep Night Quiescence (0.02 - 0.05 req/s)
		baseRate = 0.03
		regime = "Night Quiescence"
	case h >= 7.0 && h < 9.5:
		// Regime 2: Morning Ramp (2.0 - 8.0 req/s)
		progress := (h - 7.0) / 2.5
		baseRate = 2.0 + progress*6.0
		regime = "Morning Ramp"
	case h >= 9.5 && h < 16.5:
		// Regime 3: Peak Business Hours (18.0 - 28.0 req/s with harmonic oscillation)
		harmonic := math.Sin((h-9.5)/7.0*math.Pi) * 8.0
		baseRate = 18.0 + harmonic
		regime = "Peak Business"
	case h >= 16.5 && h < 19.5:
		// Regime 4: Afternoon Lull (6.0 - 12.0 req/s)
		decay := 1.0 - (h-16.5)/3.0
		baseRate = 6.0 + decay*6.0
		regime = "Afternoon Lull"
	default:
		// Regime 5: Evening Surge (25.0 - 40.0 req/s)
		baseRate = 30.0 + math.Sin((h-19.5)/3.5*math.Pi)*10.0
		regime = "Evening Surge"
	}

	// 5% chance of a localized Poisson flash burst
	if simulateBursts && rand.Float64() < 0.05 && baseRate > 1.0 {
		baseRate *= 2.5
		regime += " (Burst)"
	}

	return math.Max(0.02, baseRate), regime
}

type RequestPayload struct {
	Method string
	Path   string
	Body   []byte
}

func GenerateEndpointRequest(elapsedSec float64) RequestPayload {
	// 60-Second Phased Batch Burst Cycle:
	// - 0s to 20s: Batch Burst Phase (Jobs dispatched to spike worker memory to 100+ MB)
	// - 20s to 60s: Quiescent Phase (0 batch jobs; worker becomes IDLE > 10s holding 100MB -> FULL_RECLAIM)
	cyclePos := math.Mod(elapsedSec, 60.0)
	isBatchBurstWindow := (cyclePos < 20.0)

	roll := rand.Float64()

	if isBatchBurstWindow && roll < 0.35 {
		// In batch burst window: spike batch-worker
		body, _ := json.Marshal(map[string]interface{}{
			"matrix_dim": 600,
			"iterations": 1000,
		})
		return RequestPayload{
			Method: "POST",
			Path:   "/api/v1/worker/process-batch",
			Body:   body,
		}
	}

	// Quiescent / standard microservice transactions
	if roll < 0.70 {
		// 70% Light Reads: Cache & Gateway hit
		paths := []string{"/api/v1/fleet/status", "/api/v1/catalog", "/healthz"}
		return RequestPayload{
			Method: "GET",
			Path:   paths[rand.Intn(len(paths))],
			Body:   nil,
		}
	} else {
		// 30% Write Transactions: Core API & Datastore hit
		if rand.Float64() < 0.5 {
			body, _ := json.Marshal(map[string]interface{}{
				"vehicle_id":    fmt.Sprintf("FLEET-%03d", rand.Intn(50)+1),
				"speed":         30.0 + rand.Float64()*50.0,
				"engine_temp":   82.0 + rand.Float64()*18.0,
				"battery_level": 50.0 + rand.Float64()*50.0,
			})
			return RequestPayload{
				Method: "POST",
				Path:   "/api/v1/telemetry/record",
				Body:   body,
			}
		} else {
			body, _ := json.Marshal(map[string]interface{}{
				"customer_id": fmt.Sprintf("CLIENT-%04d", rand.Intn(500)+1),
				"amount":      120.0 + rand.Float64()*300.0,
			})
			return RequestPayload{
				Method: "POST",
				Path:   "/api/v1/orders/checkout",
				Body:   body,
			}
		}
	}
}

func dispatchSingle(client *http.Client, targetBase string, targetName string, payload RequestPayload, metrics *MetricsCollector, tracker *RequestTracker) {
	reqUrl := targetBase + payload.Path
	var bodyReader *bytes.Reader
	if payload.Body != nil {
		bodyReader = bytes.NewReader(payload.Body)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}

	req, err := http.NewRequest(payload.Method, reqUrl, bodyReader)
	if err != nil {
		metrics.RecordResult(targetName, 0, 0, false)
		if tracker != nil {
			tracker.Record(RequestLogEntry{
				Timestamp:       time.Now().Format("15:04:05.000"),
				Target:          targetName,
				TargetURL:       reqUrl,
				Method:          payload.Method,
				Path:            payload.Path,
				StatusCode:      0,
				StatusText:      "Err Request Build",
				DurationMs:      0,
				IsTimeout:       false,
				ResponseSnippet: err.Error(),
			})
		}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AdaptiveBenchmark-StochasticSimulator/1.0")

	startTime := time.Now()
	dispatchTs := startTime.Format("15:04:05.000")
	resp, err := client.Do(req)
	duration := time.Since(startTime)
	durMs := float64(duration.Microseconds()) / 1000.0

	if err != nil {
		metrics.RecordResult(targetName, 0, duration, true)
		if tracker != nil {
			errStr := err.Error()
			statusCode := 504
			statusText := "Gateway Timeout"
			if strings.Contains(errStr, "connection refused") {
				statusCode = 502
				statusText = "Connection Refused (Port Offline)"
			} else if os.IsTimeout(err) || strings.Contains(errStr, "Client.Timeout") {
				statusCode = 504
				statusText = "Timeout (Activator / Cold-Start)"
			} else {
				statusCode = 500
				statusText = "Transport Error"
			}

			tracker.Record(RequestLogEntry{
				Timestamp:       dispatchTs,
				Target:          targetName,
				TargetURL:       reqUrl,
				Method:          payload.Method,
				Path:            payload.Path,
				StatusCode:      statusCode,
				StatusText:      statusText,
				DurationMs:      durMs,
				IsTimeout:       statusCode == 504,
				ResponseSnippet: errStr,
			})
		}
		return
	}

	defer resp.Body.Close()

	// Read response body snippet (up to 512 bytes)
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	respSnippet := strings.TrimSpace(string(bodyBytes))
	if len(respSnippet) > 200 {
		respSnippet = respSnippet[:197] + "..."
	}
	if respSnippet == "" {
		respSnippet = resp.Status
	}

	metrics.RecordResult(targetName, resp.StatusCode, duration, false)
	if tracker != nil {
		tracker.Record(RequestLogEntry{
			Timestamp:       dispatchTs,
			Target:          targetName,
			TargetURL:       reqUrl,
			Method:          payload.Method,
			Path:            payload.Path,
			StatusCode:      resp.StatusCode,
			StatusText:      resp.Status,
			DurationMs:      durMs,
			IsTimeout:       false,
			ResponseSnippet: respSnippet,
		})
	}
}

func main() {
	var cfg Config
	flag.StringVar(&cfg.TargetDefault, "target-default", "http://127.0.0.1:8080", "Base URL for default scheduler gateway")
	flag.StringVar(&cfg.TargetAdaptive, "target-adaptive", "http://127.0.0.1:8081", "Base URL for adaptive scheduler gateway")
	flag.StringVar(&cfg.Mode, "mode", "realtime", "Execution mode: 'realtime' (1x wall clock) or 'timewarp' (accelerated)")
	flag.Float64Var(&cfg.TimeWarpFactor, "timewarp-factor", 48.0, "Time acceleration factor (e.g. 48x = 24h cycle in 30 minutes)")
	flag.DurationVar(&cfg.StatsInterval, "stats-interval", 5*time.Second, "Interval between terminal status logs")
	flag.StringVar(&cfg.MetricsFile, "metrics-file", "benchmark_traffic_telemetry.csv", "Path for output metrics CSV")
	flag.StringVar(&cfg.RequestsFile, "requests-file", "recent_requests.json", "Path for output recent requests JSON (served to dashboard)")
	flag.DurationVar(&cfg.OverallDuration, "duration", 0, "Total benchmark duration (e.g. 96h, 30m; 0 = run until SIGINT)")
	flag.BoolVar(&cfg.SimulateBursts, "simulate-bursts", true, "Inject random Poisson bursts during peak periods")
	flag.Parse()

	log.Println("=================================================================")
	log.Println(" Industrial Stochastic Traffic Simulator & Dual Dispatcher")
	log.Printf(" Mode: %s (TimeWarp Factor: %.1fx)", cfg.Mode, cfg.TimeWarpFactor)
	log.Printf(" Target Default  : %s", cfg.TargetDefault)
	log.Printf(" Target Adaptive : %s", cfg.TargetAdaptive)
	log.Printf(" Metrics Output  : %s", cfg.MetricsFile)
	log.Printf(" Requests Output : %s", cfg.RequestsFile)
	log.Println("=================================================================")

	metrics, err := NewMetricsCollector(cfg.MetricsFile)
	if err != nil {
		log.Fatalf("Failed to initialize metrics CSV: %v", err)
	}
	defer metrics.Close()

	var reqTracker *RequestTracker
	if cfg.RequestsFile != "" {
		reqTracker = NewRequestTracker(cfg.RequestsFile, 80)
	}

	// High performance HTTP client with connection reuse & Demand Activator timeout accommodation
	transport := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   35 * time.Second, // Accommodates Demand Activator cold-start reconstitution without dropping
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.OverallDuration > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, cfg.OverallDuration)
		defer timeoutCancel()
		log.Printf("Benchmark scheduled to run for %v", cfg.OverallDuration)
	}

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stopChan
		log.Println("\nShutdown signal received. Flushing final metrics and exiting...")
		if reqTracker != nil {
			reqTracker.Flush()
		}
		cancel()
	}()

	// Periodic flusher for recent requests JSON (every 500ms)
	if reqTracker != nil {
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					reqTracker.Flush()
					return
				case <-ticker.C:
					reqTracker.Flush()
				}
			}
		}()
	}

	startTime := time.Now()
	lastStats := time.Now()
	var currentSimHour float64 = 8.0 // Start simulation at 08:00 AM (Morning Ramp)

	// Fixed worker pool for memory safety (< 30 MB RAM)
	workerPool := make(chan struct{}, 100)

	log.Println("Traffic simulation active. Generating continuous Poisson requests...")

	for {
		select {
		case <-ctx.Done():
			log.Println("Simulation completed.")
			if reqTracker != nil {
				reqTracker.Flush()
			}
			return
		default:
		}

		// Calculate current simulated hour
		elapsedReal := time.Since(startTime).Seconds()
		if cfg.Mode == "timewarp" {
			simulatedElapsedHours := (elapsedReal * cfg.TimeWarpFactor) / 3600.0
			currentSimHour = math.Mod(8.0+simulatedElapsedHours, 24.0)
		} else {
			// Real wall-clock time
			now := time.Now()
			currentSimHour = float64(now.Hour()) + float64(now.Minute())/60.0 + float64(now.Second())/3600.0
		}

		rateLambda, regime := CalculateDiurnalRate(currentSimHour, cfg.SimulateBursts)

		// Sample Poisson arrival interval Δt = -ln(U) / λ
		u := rand.Float64()
		if u <= 0.0 {
			u = 1e-6
		}
		interArrivalSec := -math.Log(u) / rateLambda

		// If in timewarp mode, compress wait duration
		if cfg.Mode == "timewarp" {
			interArrivalSec = interArrivalSec / cfg.TimeWarpFactor
		}
		sleepDuration := time.Duration(interArrivalSec * float64(time.Second))

		// Sleep until next stochastic arrival
		time.Sleep(sleepDuration)

		// Generate synthetic request
		payload := GenerateEndpointRequest(elapsedReal)
		atomic.AddUint64(&metrics.TotalDispatched, 1)

		// Dual mirrored dispatch in parallel goroutine
		workerPool <- struct{}{}
		go func(p RequestPayload) {
			defer func() { <-workerPool }()

			var wg sync.WaitGroup
			wg.Add(2)

			// 1. Dispatch to Default Scheduler
			go func() {
				defer wg.Done()
				dispatchSingle(httpClient, cfg.TargetDefault, "default", p, metrics, reqTracker)
			}()

			// 2. Dispatch to Adaptive Scheduler (Mirrored)
			go func() {
				defer wg.Done()
				dispatchSingle(httpClient, cfg.TargetAdaptive, "adaptive", p, metrics, reqTracker)
			}()

			wg.Wait()
		}(payload)

		// Periodic metrics flush and terminal logging
		if time.Since(lastStats) >= cfg.StatsInterval {
			metrics.SnapshotAndFlush(currentSimHour, regime, rateLambda)
			lastStats = time.Now()
		}
	}
}

