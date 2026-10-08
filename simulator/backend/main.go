package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"simulator/backend/handlers"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}

	handler := handlers.NewAPIHandler()
	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/health", handler.HandleHealth)
	mux.HandleFunc("/api/presets", handler.HandlePresets)
	mux.HandleFunc("/api/presets/", handler.HandlePresets)
	mux.HandleFunc("/api/simulate", handler.HandleSimulate)
	mux.HandleFunc("/api/simulate/workload", handler.HandleSimulateWorkload)

	// Active Workload / Cluster Routes
	mux.HandleFunc("/api/cluster/status", handler.Cluster().HandleClusterStatus)
	mux.HandleFunc("/api/workloads", handler.Cluster().HandleWorkloads)
	mux.HandleFunc("/api/reclaim/config", handler.Cluster().HandleReclaimConfig)
	mux.HandleFunc("/api/workloads/checkpoint", handler.Cluster().HandleCheckpoint)
	mux.HandleFunc("/api/workloads/restore", handler.Cluster().HandleRestore)

	// Live Traffic & Demand Simulation Routes
	mux.HandleFunc("/api/traffic/start", handler.Traffic().HandleTrafficStart)
	mux.HandleFunc("/api/traffic/stop", handler.Traffic().HandleTrafficStop)
	mux.HandleFunc("/api/traffic/burst", handler.Traffic().HandleTrafficBurst)
	mux.HandleFunc("/api/traffic/status", handler.Traffic().HandleTrafficStatus)
	mux.HandleFunc("/api/traffic/wake", handler.Traffic().HandleTrafficWake)

	// Resolve frontend static files directory
	frontendDir := findFrontendDir()
	if frontendDir != "" {
		log.Printf("[Simulator] Serving frontend assets from: %s", frontendDir)
		fs := http.FileServer(http.Dir(frontendDir))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			fs.ServeHTTP(w, r)
		})
	} else {
		log.Printf("[Simulator] Warning: frontend directory not found. Serving API only.")
	}

	addr := fmt.Sprintf(":%s", port)
	log.Printf("=================================================================")
	log.Printf("  Kubernetes Intelligence Simulator & Dashboard Running")
	log.Printf("  URL: http://localhost:%s", port)
	log.Printf("  API: http://localhost:%s/api/health", port)
	log.Printf("  Pipeline: Analyzer -> Detector -> Decision Engine (Go)")
	log.Printf("=================================================================")

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// findFrontendDir searches common relative paths for the simulator/frontend directory.
func findFrontendDir() string {
	candidates := []string{
		"simulator/frontend",
		"./frontend",
		"../frontend",
		"../../frontend",
	}

	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			info, err := os.Stat(abs)
			if err == nil && info.IsDir() {
				// Verify index.html exists
				if _, err := os.Stat(filepath.Join(abs, "index.html")); err == nil {
					return abs
				}
			}
		}
	}

	// Also check relative to executable if available
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, c := range candidates {
			target := filepath.Join(dir, c)
			if _, err := os.Stat(filepath.Join(target, "index.html")); err == nil {
				return target
			}
		}
	}

	return ""
}
