# Complete Demo & Walkthrough Guide: Adaptive Kubernetes Scheduler & Simulator

This guide provides step-by-step instructions to demonstrate the complete system, from the **75-scenario intelligence simulator** to the **live Kubernetes cluster with real Prometheus telemetry, real CRIU checkpointing, resource reclamation, and pod restoration**.

---

## Architecture & Demo Highlights

The system operates in two distinct, coordinated modes accessible from a single unified dashboard:

```text
┌────────────────────────────────────────────────────────────────────────┐
│                   Unified Dashboard (http://localhost:8082)            │
│       [ Scenario Simulator ]         [ Active Workload / Cluster ]     │
└────────────────────────────────────────────────────────────────────────┘
                    │                                      │
     ┌──────────────┴──────────────┐        ┌──────────────┴──────────────┐
     ▼                             ▼        ▼                             ▼
Mode 1: Preset Scenarios (75 Tests)         Mode 2: Real Kubernetes Cluster (Live)
- Synthetic cluster topologies              - Kind control plane + CRIU v4.1.1
- 9-Factor ridge regression scoring         - Real Prometheus physical telemetry
- Immediate multi-scenario exploration      - Safe CRIU Checkpoint API via Kubelet
- Zero cluster impact / dry-run             - Real resource reclamation & Pod restore
```

---

## Prerequisites & Quick Health Check

Before starting the demo, ensure the local Kind cluster and supporting services are active.

### 1. Verify Kubernetes Cluster & Node Status
```bash
kubectl get nodes -o wide
```
**Expected Output:**
```text
NAME                             STATUS   ROLES           AGE   VERSION
adaptive-cluster-control-plane   Ready    control-plane   24h   v1.37.0
```

### 2. Verify CRIU Installation on the Node
```bash
docker exec adaptive-cluster-control-plane criu check
```
**Expected Output:**
```text
Looks good.
```

### 3. Verify Prometheus Port-Forwarding
Prometheus scrapes real node and container metrics (cAdvisor) inside the cluster. If not already forwarded, run:
```bash
kubectl port-forward -n ecommerce svc/prometheus 9090:9090 &
curl -s http://127.0.0.1:9090/-/healthy
```
**Expected Output:**
```text
Prometheus Server is Healthy.
```

### 4. Check Workloads in `ecommerce` Namespace
```bash
kubectl get pods -n ecommerce -o wide
```
**Expected Output:**
```text
NAME                          READY   STATUS    AGE   IP           NODE
prometheus-5db68fcd8c-c69j9   1/1     Running   1h    10.244.0.7   adaptive-cluster-control-plane
prometheus-5db68fcd8c-whgk8   1/1     Running   1h    10.244.0.8   adaptive-cluster-control-plane
test-hostnet-worker           1/1     Running   1h    172.18.0.2   adaptive-cluster-control-plane
test-idle-worker-restored     1/1     Running   10m   10.244.0.9   adaptive-cluster-control-plane
```

> [!TIP]
> If `test-idle-worker` is not present, you can deploy a fresh idle candidate anytime using:
> ```bash
> kubectl apply -f - <<EOF
> apiVersion: v1
> kind: Pod
> metadata:
>   name: test-idle-worker
>   namespace: ecommerce
>   labels:
>     app: test-idle-worker
>   annotations:
>     reclaim.io/checkpointable: "true"
> spec:
>   containers:
>   - name: worker
>     image: alpine:3.20
>     command: ["/bin/sh", "-c", "i=0; while true; do echo \$i; i=\$((i+1)); sleep 1; done"]
>     resources:
>       requests:
>         cpu: "100m"
>         memory: "64Mi"
> EOF
> ```

---

## Step 1: Launch the Simulator Backend & Dashboard

1. Navigate to the simulator directory and start the server:
   ```bash
   cd /home/kavin/Work/kubernetes-scheduler/simulator
   go run ./backend
   ```
   *(Or run the precompiled binary: `/tmp/sim-backend`)*

2. Confirm the server is running:
   ```text
   =================================================================
     Kubernetes Intelligence Simulator & Dashboard Running
     URL: http://localhost:8082
     API: http://localhost:8082/api/health
     Pipeline: Analyzer -> Detector -> Decision Engine (Go)
   =================================================================
   ```

3. Open your browser to: **`http://localhost:8082`**

---

## Step 2: Demo Mode 1 — Scenario Simulator (75 Presets)

1. Click on **`Scenario Simulator`** in the top navigation bar.
2. Select any preset scenario from the dropdown:
   - **`scenario-01`**: Standard baseline mixed cluster.
   - **`scenario-15`**: Extreme CPU exhaustion with idle batch jobs.
   - **`scenario-45`**: Multi-signal conflicting telemetry (low CPU, high network).
   - **`scenario-72`**: Protected workloads and strict Pod Disruption Budgets (`PDB`).
3. Click **`Run Simulation`**:
   - The topology view renders nodes, assigned pods, and utilization gauges.
   - Click on any workload row or card to open the **Intelligence Factor Drawer**.
   - Show the 9 normalized sub-scores ($R_1 \dots R_9$):
     - $R_1$ (CPU Utilization)
     - $R_2$ (Memory Utilization)
     - $R_3$ (Idle Duration)
     - $R_4$ (Reclaim Benefit)
     - $R_5$ (Replica Safety)
     - $R_6$ (Priority Class)
     - $R_7$ (Pod Disruption Budget)
     - $R_8$ (Lifecycle State)
     - $R_9$ (Checkpoint Compatibility)

---

## Step 3: Demo Mode 2 — Active Workload / Real Cluster

Click on **`Active Workload / Cluster`** in the top navigation bar.

### What the Screen Displays:
- **Cluster Status Card**:
  - Cluster Mode: `ACTIVE_CLUSTER`
  - Kubernetes Version: `v1.37.0`
  - Ready Nodes: `1 / 1`
- **CRIU Status Card**:
  - Version: `CRIU 4.1.1`
  - Self-Test: `Passing (Looks good)`
- **Prometheus Status Card**:
  - Endpoint: `http://127.0.0.1:9090`
  - Metrics Scraped: `26+ active telemetry streams`
- **Live Workloads Table**:
  - Real pods discovered directly from the `ecommerce` namespace.
  - Live CPU millicores and memory working set fetched from Prometheus cAdvisor metrics.
  - Real-time classification (`IDLE`, `ACTIVE`, `LOW_USAGE`).
  - Decision Engine Composite Score & Action (`KEEP`, `SOFT_RECLAIM`, `FULL_RECLAIM`).
  - Lifecycle state machine badges: `RUNNING` $\rightarrow$ `CANDIDATE` $\rightarrow$ `CHECKPOINTING` $\rightarrow$ `CHECKPOINTED` $\rightarrow$ `RECLAIMED` $\rightarrow$ `RESTORED`.

---

## Step 4: Live Checkpoint & Reclamation Demonstration

You can perform and display this directly from the **Dashboard UI** or via **terminal curl commands**.

### Option A: Via Dashboard UI
1. Locate `test-idle-worker` in the Active Workloads table.
2. Observe its state badge: `CANDIDATE`, Score: `~0.7301`.
3. Click the orange **`Checkpoint & Reclaim`** button in the Action column.
4. Watch the state transitions live on screen:
   - `CHECKPOINTING...` (Invoking Kubelet CRIU API)
   - `CHECKPOINTED` (Archive generated)
   - `RECLAIMED` (Pod evicted, resources reclaimed)
5. The button changes to a green **`Restore Workload`** button.

### Option B: Via Terminal / API
Run the following curl command:
```bash
curl -s -X POST http://localhost:8082/api/workloads/checkpoint \
  -H "Content-Type: application/json" \
  -d '{"namespace":"ecommerce","name":"test-idle-worker"}' | python3 -m json.tool
```

**Expected JSON Response:**
```json
{
  "state": {
    "namespace": "ecommerce",
    "name": "test-idle-worker",
    "state": "RECLAIMED",
    "stateDetail": "Workload stopped & resources reclaimed. Checkpoint: /var/lib/kubelet/checkpoints/checkpoint-test-idle-worker_ecommerce-worker-2026-09-30T06:39:06Z.tar",
    "score": 0.7301,
    "action": "FULL_RECLAIM",
    "safetyPassed": true,
    "checkpointPath": "/var/lib/kubelet/checkpoints/checkpoint-test-idle-worker_ecommerce-worker-2026-09-30T06:39:06Z.tar"
  },
  "success": true
}
```

### Verification in Cluster (Show the Audience):
1. **Verify the Real CRIU Tarball on the Host**:
   ```bash
   docker exec adaptive-cluster-control-plane ls -lh /var/lib/kubelet/checkpoints/
   ```
   *Output shows real archive:* `checkpoint-test-idle-worker_ecommerce-worker-....tar (~587 KB)`.

2. **Inspect Tarball Internal CRIU Process Images**:
   ```bash
   TARBALL=$(docker exec adaptive-cluster-control-plane ls /var/lib/kubelet/checkpoints/ | tail -n 1)
   docker exec adaptive-cluster-control-plane tar -tf /var/lib/kubelet/checkpoints/$TARBALL | head -12
   ```
   *Output:*
   ```text
   checkpoint/
   checkpoint/cgroup.img
   checkpoint/core-1.img
   checkpoint/descriptors.json
   checkpoint/fdinfo-2.img
   checkpoint/files.img
   checkpoint/fs-1.img
   checkpoint/inventory.img
   checkpoint/mm-1.img
   ```

3. **Verify Pod Eviction / Headroom Freed**:
   ```bash
   kubectl get pods -n ecommerce
   ```
   *Notice `test-idle-worker` is gone — resources have been freed back to the cluster.*

---

## Step 5: Live Workload Restoration Demonstration

### Option A: Via Dashboard UI
Click the green **`Restore Workload`** button on the reclaimed row.
- The badge transitions: `RESTORING` $\rightarrow$ `RESTORED` $\rightarrow$ `RUNNING`.

### Option B: Via Terminal / API
Run:
```bash
curl -s -X POST http://localhost:8082/api/workloads/restore \
  -H "Content-Type: application/json" \
  -d '{"namespace":"ecommerce","name":"test-idle-worker"}' | python3 -m json.tool
```

**Expected JSON Response:**
```json
{
  "state": {
    "namespace": "ecommerce",
    "name": "test-idle-worker",
    "state": "RESTORED",
    "stateDetail": "Workload restored as test-idle-worker-restored. Status: RUNNING",
    "action": "FULL_RECLAIM",
    "safetyPassed": true
  },
  "success": true
}
```

### Verification in Cluster:
```bash
kubectl get pods -n ecommerce -o wide
```
*Output shows the pod reconstituted and running:*
```text
NAME                          READY   STATUS    AGE   IP           NODE
test-idle-worker-restored     1/1     Running   15s   10.244.0.9   adaptive-cluster-control-plane
```

Check the pod annotations to demonstrate it was restored from the checkpoint:
```bash
kubectl get pod test-idle-worker-restored -n ecommerce -o jsonpath='{.metadata.annotations}' | python3 -m json.tool
```
*Look for:*
```json
{
  "reclaim.io/checkpointable": "true",
  "reclaim.io/restored-at": "2026-09-30T06:39:42Z",
  "reclaim.io/restored-from-checkpoint": "true"
}
```

---

## Step 6: Dynamic Policy Configuration Demonstration

The decision engine does not hardcode weights or thresholds. You can demonstrate live hot-reloading:

1. In the dashboard, click **`Reclaim Policy`** (top right of Active Cluster view).
2. The modal displays:
   - Full Reclaim Score Threshold (`0.75`)
   - Soft Reclaim Score Threshold (`0.50`)
   - 9 Factor Weights (CPU `0.20`, Memory `0.20`, Idle `0.15`, Benefit `0.15`, Replica `0.10`, Priority `0.05`, PDB `0.05`, State `0.05`, Checkpoint `0.05`)
   - Safety gates (`max_priority_for_reclaim`, `min_replicas_required`)
3. Modify a threshold (e.g. adjust full reclaim threshold to `0.70`) and click **Save Policy**.
4. Or perform via CLI:
   ```bash
   curl -s -X PUT http://localhost:8082/api/reclaim/config \
     -H "Content-Type: application/json" \
     -d '{
       "thresholds": {"full_reclaim": 0.70, "soft_reclaim": 0.50},
       "weights": {"cpu": 0.20, "memory": 0.20, "idle": 0.15, "benefit": 0.15, "replica": 0.10, "priority": 0.05, "pdb": 0.05, "state": 0.05, "checkpoint": 0.05},
       "normalization": {"idle_max_duration_sec": 60, "benefit_max_cpu_millis": 2000, "benefit_max_mem_bytes": 4294967296},
       "safety": {"max_priority_for_reclaim": 100000, "min_replicas_required": 0}
     }'
   ```
5. Click **Refresh** in the dashboard: the workloads are immediately re-evaluated against the new weights and thresholds in real-time without restarting the backend!

---

## Step 7: Run Automated Verification Tests

To demonstrate code correctness and system integrity, run the full test suites:

### 1. Simulator & Cluster Integration Tests (All Pass)
```bash
cd /home/kavin/Work/kubernetes-scheduler/simulator
go test -v ./...
```
*Coverage includes:*
- `simulator/backend/cluster`: FSM state transitions, safety gate blocking, config loading, CRIU status.
- `simulator/backend/handlers`: REST API endpoints, hot-reloading policy, CORS.
- `simulator/backend/conversion`: Metric window sliding buffer and pod mapper.
- `simulator/backend/simulation`: All 75 preset scenarios evaluated against authoritative Go packages.

### 2. Adaptive Scheduler Package Tests (All Pass)
```bash
cd /home/kavin/Work/kubernetes-scheduler/adaptive-k8s-scheduler
go test ./...
```
*Coverage includes:*
- `pkg/analyzer`: Sliding window P95 and linear regression trend calculation.
- `pkg/detector`: Multi-signal idle classifier.
- `pkg/decision`: Ridge-regression scoring engine, PDB and replica safety gates.
- `pkg/action`: Kubelet checkpoint client and in-place resize logic.
- `pkg/scheduler`: Adaptive physical headroom filter and bin-packing scorer.

---

## Summary of REST API Endpoints for Demo Reference

| HTTP Method | Endpoint | Description |
|:---|:---|:---|
| `GET` | `/api/health` | System health, authoritative pipeline packages, active policy weights |
| `GET` | `/api/presets` | Metadata for all 75 preset simulation scenarios |
| `POST` | `/api/simulate` | Evaluates a synthetic cluster model through the pipeline |
| `GET` | `/api/cluster/status` | Real Kubernetes version, nodes, CRIU health, Prometheus connection |
| `GET` | `/api/workloads` | Discovers live pods, enriches with Prometheus telemetry, and evaluates scores |
| `GET` | `/api/reclaim/config` | Reads `config/reclaim_policy.json` weights and thresholds |
| `PUT` | `/api/reclaim/config` | Hot-reloads weights, thresholds, and safety gates in memory and on disk |
| `POST` | `/api/workloads/checkpoint` | Triggers real CRIU container checkpoint and evicts pod on success |
| `POST` | `/api/workloads/restore` | Reconstitutes reclaimed pod into the cluster from checkpoint |
