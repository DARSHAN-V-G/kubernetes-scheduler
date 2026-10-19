package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

type Config struct {
	Port        string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	RedisAddr   string
}

var (
	db  *sql.DB
	rdb *redis.Client
	ctx = context.Background()
)

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

func initDB(cfg Config) error {
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable connect_timeout=5",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName)
	
	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Create tables if not exists
	schema := `
	CREATE TABLE IF NOT EXISTS vehicle_telemetry (
		id SERIAL PRIMARY KEY,
		vehicle_id VARCHAR(64) NOT NULL,
		speed FLOAT NOT NULL,
		engine_temp FLOAT NOT NULL,
		battery_level FLOAT NOT NULL,
		latitude FLOAT NOT NULL,
		longitude FLOAT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS fleet_orders (
		id SERIAL PRIMARY KEY,
		order_ref VARCHAR(64) NOT NULL,
		customer_id VARCHAR(64) NOT NULL,
		total_amount FLOAT NOT NULL,
		status VARCHAR(32) DEFAULT 'DISPATCHED',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err = db.Exec(schema)
	return err
}

func initRedis(cfg Config) {
	rdb = redis.NewClient(&redis.Options{
		Addr:         cfg.RedisAddr,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		PoolSize:     10,
	})
}

// Handlers
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "healthy",
		"component": "core-api",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

func handleCatalog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// 1. Check Redis Cache
	cached, err := rdb.Get(ctx, "fleet:catalog").Result()
	if err == nil && cached != "" {
		w.Header().Set("X-Cache", "HIT")
		w.Write([]byte(cached))
		return
	}

	// 2. Cache miss: construct simulated catalog
	catalog := map[string]interface{}{
		"vehicles": []map[string]interface{}{
			{"id": "V-101", "model": "Electric Hauler", "range_km": 420, "status": "AVAILABLE"},
			{"id": "V-102", "model": "Urban Van", "range_km": 280, "status": "IN_TRANSIT"},
			{"id": "V-103", "model": "Autonomous Pod", "range_km": 350, "status": "CHARGING"},
			{"id": "V-104", "model": "Heavy Rig Prime", "range_km": 600, "status": "AVAILABLE"},
		},
		"total_active": 4,
		"cached_at":    time.Now().UTC().Format(time.RFC3339),
	}
	bytes, _ := json.Marshal(catalog)

	// Save to Redis with 45s TTL
	_ = rdb.Set(ctx, "fleet:catalog", string(bytes), 45*time.Second).Err()
	w.Header().Set("X-Cache", "MISS")
	w.Write(bytes)
}

func handleTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	type TelemetryPayload struct {
		VehicleID    string  `json:"vehicle_id"`
		Speed        float64 `json:"speed"`
		EngineTemp   float64 `json:"engine_temp"`
		BatteryLevel float64 `json:"battery_level"`
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
	}

	var p TelemetryPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		// Provide randomized fallback if empty
		p = TelemetryPayload{
			VehicleID:    fmt.Sprintf("V-%d", rand.Intn(100)+1),
			Speed:        45.0 + rand.Float64()*40,
			EngineTemp:   85.0 + rand.Float64()*15,
			BatteryLevel: 60.0 + rand.Float64()*40,
			Latitude:     37.7749 + (rand.Float64()-0.5)*0.1,
			Longitude:    -122.4194 + (rand.Float64()-0.5)*0.1,
		}
	}

	// Insert into DB
	var rowID int
	query := `INSERT INTO vehicle_telemetry (vehicle_id, speed, engine_temp, battery_level, latitude, longitude)
	          VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`
	err := db.QueryRow(query, p.VehicleID, p.Speed, p.EngineTemp, p.BatteryLevel, p.Latitude, p.Longitude).Scan(&rowID)
	if err != nil {
		log.Printf("[DB Error] Telemetry insert failed: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "recorded",
		"record_id":  rowID,
		"vehicle_id": p.VehicleID,
	})
}

func handleCheckout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	type OrderPayload struct {
		CustomerID string  `json:"customer_id"`
		Amount     float64 `json:"amount"`
	}

	var o OrderPayload
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		o = OrderPayload{
			CustomerID: fmt.Sprintf("CUST-%04d", rand.Intn(9999)),
			Amount:     99.50 + rand.Float64()*250,
		}
	}
	orderRef := fmt.Sprintf("ORD-%d-%04d", time.Now().Unix(), rand.Intn(1000))

	var orderID int
	query := `INSERT INTO fleet_orders (order_ref, customer_id, total_amount) VALUES ($1, $2, $3) RETURNING id`
	err := db.QueryRow(query, orderRef, o.CustomerID, o.Amount).Scan(&orderID)
	if err != nil {
		log.Printf("[DB Error] Order insert failed: %v", err)
	}

	// Invalidate catalog cache
	_ = rdb.Del(ctx, "fleet:catalog").Err()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "order_dispatched",
		"order_id":    orderID,
		"order_ref":   orderRef,
		"amount":      o.Amount,
		"customer_id": o.CustomerID,
	})
}

func handleProcessBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	jobID := fmt.Sprintf("JOB-%d-%04d", time.Now().UnixNano(), rand.Intn(9999))
	taskPayload := map[string]interface{}{
		"job_id":     jobID,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"matrix_dim": 800,
		"iterations": 1500,
	}
	data, _ := json.Marshal(taskPayload)

	// Push task to Redis queue for Batch Worker
	err := rdb.RPush(ctx, "queue:batch-tasks", string(data)).Err()
	if err != nil {
		http.Error(w, fmt.Sprintf("Redis enqueue error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "enqueued",
		"job_id":  jobID,
		"message": "Task queued for async batch-worker execution",
	})
}

func main() {
	cfg := Config{
		Port:       getEnv("PORT", "3000"),
		DBHost:     getEnv("DB_HOST", "datastore"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", "postgres"),
		DBName:     getEnv("DB_NAME", "benchmark_fleet"),
		RedisAddr:  getEnv("REDIS_ADDR", "cache:6379"),
	}

	log.Printf("Starting Core API on :%s (DB: %s:%s, Redis: %s)", cfg.Port, cfg.DBHost, cfg.DBPort, cfg.RedisAddr)

	initRedis(cfg)
	
	// Retrying DB connection on startup (accommodates cold start of datastore)
	for i := 0; i < 15; i++ {
		err := initDB(cfg)
		if err == nil {
			log.Println("Database connection and schema verified.")
			break
		}
		log.Printf("Waiting for database (%v)... attempt %d/15", err, i+1)
		time.Sleep(2 * time.Second)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/api/v1/health", handleHealth)
	mux.HandleFunc("/api/v1/catalog", handleCatalog)
	mux.HandleFunc("/api/v1/fleet/status", handleCatalog)
	mux.HandleFunc("/api/v1/telemetry/record", handleTelemetry)
	mux.HandleFunc("/api/v1/orders/checkout", handleCheckout)
	mux.HandleFunc("/api/v1/worker/process-batch", handleProcessBatch)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server ListenAndServe failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("Shutting down Core API gracefully...")
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxTimeout)
}
