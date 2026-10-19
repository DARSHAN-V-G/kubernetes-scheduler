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

	// 2. CPU Burst: Perform iterative SHA-256 rounds for ~5 seconds
	targetDuration := 5 * time.Second
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

func main() {
	redisAddr := getEnv("REDIS_ADDR", "cache:6379")
	log.Printf("Starting Batch Worker daemon. Connecting to Redis queue at %s...", redisAddr)

	rdb := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling for graceful shutdown / CRIU preparation
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Println("Received termination signal. Gracefully exiting worker loop...")
		cancel()
	}()

	for {
		select {
		case <-ctx.Done():
			log.Println("Batch Worker stopped gracefully.")
			return
		default:
			// BLPop blocks for up to 5 seconds waiting for a new task
			results, err := rdb.BLPop(ctx, 5*time.Second, "queue:batch-tasks").Result()
			if err != nil {
				if err == redis.Nil {
					// Timeout with no tasks -> completely idle
					continue
				}
				if ctx.Err() != nil {
					return
				}
				log.Printf("Waiting for tasks on 'queue:batch-tasks' (Redis status: %v)...", err)
				time.Sleep(2 * time.Second)
				continue
			}

			// results[0] = list key ("queue:batch-tasks"), results[1] = JSON payload
			if len(results) >= 2 {
				var task BatchTask
				if err := json.Unmarshal([]byte(results[1]), &task); err == nil {
					simulateHeavyCompute(task)
				} else {
					log.Printf("Failed to decode task JSON: %v", err)
				}
			}
		}
	}
}
