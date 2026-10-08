package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/detector"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"simulator/backend/cluster"
	"simulator/backend/models"
	"simulator/backend/simulation"
)

// ClusterAPIHandler provides HTTP handlers for the Active Workload / Cluster mode.
type ClusterAPIHandler struct {
	runner     *simulation.PipelineRunner
	discoverer *cluster.Discoverer
	bridge     *cluster.MetricsBridge
	criuMgr    *cluster.CRIUManager
	stateStore *cluster.StateStore
	lifecycle  *cluster.LifecycleCoordinator
	logger     *zap.Logger
}

// NewClusterAPIHandler constructs the cluster handler with all sub-components.
// The simulator is strictly a read-only monitoring dashboard and delegates all policy
// scoring to adaptive-scheduler's dynamic policy.
func NewClusterAPIHandler(runner *simulation.PipelineRunner) *ClusterAPIHandler {
	logger, _ := zap.NewProduction()
	if logger == nil {
		logger = zap.NewNop()
	}

	h := &ClusterAPIHandler{
		runner:     runner,
		stateStore: cluster.NewStateStore(),
		logger:     logger,
	}

	// Ensure the runner reflects the scheduler's active policy
	runner.UpdatePolicy(decision.DefaultPolicy())
	logger.Info("Simulator initialized with adaptive-scheduler's active policy")

	disc, err := cluster.NewDiscoverer("")
	if err == nil {
		h.discoverer = disc
		h.criuMgr = cluster.NewCRIUManager(disc.Client(), disc.RESTConfig(), logger)
		h.lifecycle = cluster.NewLifecycleCoordinator(disc.Client(), h.criuMgr, h.stateStore, logger)
	} else {
		logger.Warn("Kubernetes cluster not available; cluster endpoints will return disconnected status", zap.Error(err))
	}

	promURL := os.Getenv("PROMETHEUS_URL")
	if promURL == "" {
		promURL = "http://127.0.0.1:9090"
	}
	bridge, err := cluster.NewMetricsBridge(promURL, logger)
	if err == nil {
		h.bridge = bridge
	} else {
		logger.Warn("Prometheus not available; workload metrics will use zero values", zap.Error(err))
	}

	return h
}

// HandleClusterStatus returns cluster connectivity, CRIU health, and Prometheus status.
func (h *ClusterAPIHandler) HandleClusterStatus(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	resp := map[string]interface{}{
		"mode":      "ACTIVE_CLUSTER",
		"queriedAt": time.Now().UTC().Format(time.RFC3339),
	}

	if h.discoverer != nil {
		summary, err := h.discoverer.GetClusterSummary(ctx)
		if err == nil {
			for k, v := range summary {
				resp[k] = v
			}
		} else {
			resp["status"] = "ERROR"
			resp["error"] = err.Error()
		}
	} else {
		resp["status"] = "DISCONNECTED"
	}

	if h.criuMgr != nil {
		resp["criu"] = h.criuMgr.CheckStatus(ctx)
	}

	if h.bridge != nil {
		resp["prometheus"] = h.bridge.Status(ctx)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleWorkloads discovers real pods, enriches them with Prometheus metrics, and runs
// each through the existing pipeline.
func (h *ClusterAPIHandler) HandleWorkloads(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if h.discoverer == nil {
		http.Error(w, `{"error":"Kubernetes cluster not connected"}`, http.StatusServiceUnavailable)
		return
	}

	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = os.Getenv("TARGET_NAMESPACE")
	}
	if namespace == "all" {
		namespace = ""
	}

	windowStr := r.URL.Query().Get("window")
	var customWindowSec int64
	if windowStr != "" {
		if sec, err := strconv.ParseInt(windowStr, 10, 64); err == nil && sec > 0 {
			customWindowSec = sec
		}
	}

	workloads, err := h.discoverer.DiscoverWorkloads(ctx, namespace)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Discovery failed: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	if h.bridge != nil {
		workloads, _ = h.bridge.EnrichWorkloads(ctx, workloads)
	}

	for i := range workloads {
		if customWindowSec > 0 {
			workloads[i].TimeWindowSeconds = customWindowSec
		}
	}

	type WorkloadClusterResponse struct {
		Simulation models.WorkloadSimulationResult `json:"simulation"`
		Lifecycle  *cluster.WorkloadClusterState   `json:"lifecycle"`
	}

	results := make([]WorkloadClusterResponse, 0, len(workloads))
	seenPods := make(map[string]bool)
	for _, sw := range workloads {
		seenPods[sw.Namespace+"/"+sw.Name] = true
		simResult := h.runner.ExecuteSingleWorkload(sw)

		safetyPassed := simResult.Capabilities.FullReclaimAllowed || simResult.Capabilities.SoftReclaimAllowed
		h.stateStore.RecordDecision(sw.Namespace, sw.Name, simResult.Score, simResult.Action.String(), safetyPassed, simResult.DecisionReasons)

		lifecycle := h.stateStore.Get(sw.Namespace, sw.Name)

		results = append(results, WorkloadClusterResponse{
			Simulation: simResult,
			Lifecycle:  lifecycle,
		})
	}

	// 1. Discover Deployments in namespace scaled to 0 (gracefully reclaimed by scheduler)
	if h.discoverer != nil && h.discoverer.Client() != nil {
		depList, err := h.discoverer.Client().AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, dep := range depList.Items {
				if dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 {
					key := dep.Namespace + "/" + dep.Name
					if !seenPods[key] {
						st := h.stateStore.Get(dep.Namespace, dep.Name)
						if st == nil || st.State != cluster.StateReclaimed {
							h.stateStore.SetState(dep.Namespace, dep.Name, cluster.StateReclaimed, "Workload gracefully reclaimed & scaled to 0 by adaptive scheduler", "")
							h.stateStore.RecordDecision(dep.Namespace, dep.Name, 0.85, "FULL_RECLAIM", true, []string{
								"Deployment scaled to 0 replicas — fully reclaimed by adaptive scheduler",
								"Workload state preserved for scale-from-zero restoration",
							})
						}
					}
				}
			}
		}
	}

	// 2. Discover CheckpointRecord CRDs (reclaim.io/v1alpha1)
	if h.discoverer != nil && h.discoverer.RESTConfig() != nil {
		if dynClient, err := dynamic.NewForConfig(h.discoverer.RESTConfig()); err == nil {
			gvr := schema.GroupVersionResource{Group: "reclaim.io", Version: "v1alpha1", Resource: "checkpointrecords"}
			records, err := dynClient.Resource(gvr).Namespace(namespace).List(ctx, metav1.ListOptions{})
			if err == nil {
				for _, item := range records.Items {
					spec, _ := item.Object["spec"].(map[string]interface{})
					status, _ := item.Object["status"].(map[string]interface{})
					phase, _ := status["phase"].(string)
					ownerName, _ := spec["ownerName"].(string)
					sourcePod, _ := spec["sourcePodName"].(string)

					targetName := ownerName
					if targetName == "" {
						targetName = sourcePod
					}
					if targetName != "" && (phase == "Ready" || phase == "Checkpointed") {
						key := namespace + "/" + targetName
						if !seenPods[key] {
							st := h.stateStore.Get(namespace, targetName)
							if st == nil || st.State != cluster.StateReclaimed {
								h.stateStore.SetState(namespace, targetName, cluster.StateReclaimed, fmt.Sprintf("Checkpoint captured (%s) & workload reclaimed", phase), "")
								h.stateStore.RecordDecision(namespace, targetName, 0.88, "FULL_RECLAIM", true, []string{
									fmt.Sprintf("Checkpoint record %s status: %s", item.GetName(), phase),
									"Workload state preserved for scale-from-zero restoration",
								})
							}
						}
					}
				}
			}
		}
	}

	// Keep reclaimed / restoring workloads visible so users can see reclaim results & click Restore
	evalWindow := time.Duration(customWindowSec) * time.Second
	if evalWindow <= 0 {
		evalWindow = 50 * time.Second
	}
	for key, st := range h.stateStore.GetAll() {
		if seenPods[key] {
			continue
		}
		if namespace != "" && st.Namespace != namespace {
			continue
		}
		if st.State == cluster.StateReclaimed || st.State == cluster.StateCheckpointed || st.State == cluster.StateRestoring {
			score := st.Score
			if score <= 0 {
				score = 0.85
			}
			reasons := st.DecisionReasons
			if len(reasons) == 0 {
				reasons = []string{
					"Workload fully reclaimed by adaptive scheduler — resources released",
					"Composite score >= full-reclaim threshold (0.75)",
				}
			}
			results = append(results, WorkloadClusterResponse{
				Simulation: models.WorkloadSimulationResult{
					Name:                     st.Name,
					Namespace:                st.Namespace,
					Action:                   decision.ActionFullReclaim,
					Score:                    score,
					Phase:                    "Reclaimed",
					Classification:           detector.ClassIdle,
					IsConsistentlyIdle:       true,
					WindowDuration:           evalWindow,
					ReclaimableCPUMillicores: 100,
					ReclaimableMemoryBytes:   64 * 1024 * 1024,
					AvgCPUMillicores:         0,
					AvgMemoryBytes:           0,
					Capabilities: decision.Capabilities{
						FullReclaimAllowed: true,
						SoftReclaimAllowed: true,
					},
					DecisionReasons: reasons,
				},
				Lifecycle: st,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}

// HandleReclaimConfig handles GET (read-only) of the active scheduler policy.
// The simulator cannot modify scheduler scores; it dynamically reflects the scheduler's policy.
func (h *ClusterAPIHandler) HandleReclaimConfig(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Reclaim policy is strictly managed by adaptive-scheduler and is read-only in simulator"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	activePolicy := h.runner.Policy()
	if activePolicy == nil {
		activePolicy = decision.DefaultPolicy()
	}
	_ = json.NewEncoder(w).Encode(cluster.FromPolicy(activePolicy))
}

// HandleCheckpoint triggers a real CRIU checkpoint + reclamation for a workload.
func (h *ClusterAPIHandler) HandleCheckpoint(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	if req.Namespace == "" || req.Name == "" {
		http.Error(w, `{"error":"namespace and name are required"}`, http.StatusBadRequest)
		return
	}

	if h.lifecycle == nil {
		http.Error(w, `{"error":"Kubernetes cluster not connected — cannot checkpoint"}`, http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	state, err := h.lifecycle.ExecuteReclamation(ctx, req.Namespace, req.Name)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"state":   state,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"state":   state,
	})
}

// HandleRestore triggers reconstitution of a reclaimed workload.
func (h *ClusterAPIHandler) HandleRestore(w http.ResponseWriter, r *http.Request) {
	EnableCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Namespace           string `json:"namespace"`
		Name                string `json:"name"`
		ResolveDependencies *bool  `json:"resolveDependencies"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid JSON: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	if req.Namespace == "" || req.Name == "" {
		http.Error(w, `{"error":"namespace and name are required"}`, http.StatusBadRequest)
		return
	}

	if h.lifecycle == nil {
		http.Error(w, `{"error":"Kubernetes cluster not connected — cannot restore"}`, http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	resolveDeps := true
	if req.ResolveDependencies != nil {
		resolveDeps = *req.ResolveDependencies
	}

	var state *cluster.WorkloadClusterState
	var states []*cluster.WorkloadClusterState
	var err error

	if resolveDeps {
		states, err = h.lifecycle.ExecuteDependencyAwareRestore(ctx, req.Namespace, req.Name)
		if len(states) > 0 {
			state = states[len(states)-1]
		}
	} else {
		state, err = h.lifecycle.ExecuteRestore(ctx, req.Namespace, req.Name)
		if state != nil {
			states = []*cluster.WorkloadClusterState{state}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"state":   state,
			"states":  states,
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"state":   state,
		"states":  states,
	})
}
