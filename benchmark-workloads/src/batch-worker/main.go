package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
)

type BatchTask struct {
	JobID      string `json:"job_id"`
	CreatedAt  string `json:"created_at"`
	MatrixDim  int    `json:"matrix_dim"`
	Iterations int    `json:"iterations"`
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

var (
	retainedBuffer []byte
	retainedLock   sync.Mutex
)

// simulateHeavyCompute simulates bursty CPU and RAM usage typical of analytics/ML workloads
func simulateHeavyCompute(task BatchTask) {
	log.Printf("[Worker ACTIVE] Processing batch job %s (MatrixDim: %d, Iterations: %d)...",
		task.JobID, task.MatrixDim, task.Iterations)

	start := time.Now()

	// 1. Memory Burst: Allocate & retain ~100MB byte buffer to spike container working set memory
	bufferSize := 100 * 1024 * 1024 // 100 MB
	retainedLock.Lock()
	if len(retainedBuffer) < bufferSize {
		retainedBuffer = make([]byte, bufferSize)
		for i := 0; i < len(retainedBuffer); i += 4096 {
			retainedBuffer[i] = byte(i % 256) // Touch each page to ensure physical RSS allocation
		}
	}
	retainedLock.Unlock()

	// 2. CPU Burst: Perform iterative SHA-256 rounds for ~100ms
	targetDuration := 100 * time.Millisecond
	var lastHash string
	seed := fmt.Sprintf("%s-%d", task.JobID, time.Now().UnixNano())

	for time.Since(start) < targetDuration {
		h := sha256.New()
		h.Write([]byte(seed))
		h.Write(retainedBuffer[:1024])
		sum := h.Sum(nil)
		lastHash = hex.EncodeToString(sum)
		seed = lastHash
	}

	log.Printf("[Worker COMPLETED] Job %s finished in %v. Hash: %.16s... (100MB memory retained in heap, worker now idle)",
		task.JobID, time.Since(start), lastHash)
}

var istLocation *time.Location

func init() {
	var err error
	istLocation, err = time.LoadLocation("Asia/Kolkata")
	if err != nil {
		istLocation = time.FixedZone("IST", 5*3600+30*60)
	}
	time.Local = istLocation
}

func main() {
	redisAddr := getEnv("REDIS_ADDR", "cache:6379")
	log.Printf("[%s] Starting Batch Worker daemon. Connecting to Redis queue at %s...",
		time.Now().In(istLocation).Format("15:04:05 IST"), redisAddr)

	rdb := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  0, // indefinite read timeout for blocking operations
		WriteTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling for graceful shutdown / CRIU preparation
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Printf("[%s] Received termination signal. Gracefully exiting worker loop...",
			time.Now().In(istLocation).Format("15:04:05 IST"))
		cancel()
	}()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[%s] Batch Worker stopped gracefully.", time.Now().In(istLocation).Format("15:04:05 IST"))
			return
		default:
			// BLPop blocks indefinitely (timeout: 0) on Redis server.
			// This is a true zero-overhead blocking call that generates ZERO network packets
			// and zero CPU usage while waiting, allowing the pod to reliably qualify as IDLE.
			results, err := rdb.BLPop(ctx, 0, "queue:batch-tasks").Result()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[%s] Redis queue listener error: %v. Retrying in 2s...",
					time.Now().In(istLocation).Format("15:04:05 IST"), err)
				time.Sleep(2 * time.Second)
				continue
			}

			// results[0] = list key ("queue:batch-tasks"), results[1] = JSON payload
			if len(results) >= 2 {
				var task BatchTask
				if err := json.Unmarshal([]byte(results[1]), &task); err == nil {
					simulateHeavyCompute(task)
				} else {
					log.Printf("[%s] Failed to decode task JSON: %v",
						time.Now().In(istLocation).Format("15:04:05 IST"), err)
				}
			}
		}
	}
}
