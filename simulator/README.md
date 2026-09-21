# Kubernetes Intelligence Simulator & Dashboard

A local web-based simulation tool for demonstrating and evaluating the intelligence pipeline of the **Adaptive Runtime-Aware Kubernetes Scheduler**.

---

## 1. Purpose

The simulator provides a visual interface for creating synthetic Kubernetes nodes and workloads, passing them through the real Go implementation of the intelligence pipeline:
- **`pkg/analyzer`**: Workload Analyzer
- **`pkg/detector`**: Idle Classifier
- **`pkg/decision`**: Multi-Criteria Reclamation Decision Engine

It allows operators and evaluators to inspect how decisions are formed, examine the 9 individual score factors ($R_1 \dots R_9$), observe dual-capability safety gating, and assess theoretical reclaimable cluster headroom.

> [!IMPORTANT]
> **Simulation Only — Zero Cluster Modifications**:
> The simulator does **NOT** execute CRIU checkpoints, pod evictions, container restarts, or Kubernetes API calls. All actions (`KEEP`, `SOFT_RECLAIM`, `FULL_RECLAIM`) are decision outputs generated for evaluation and demonstration.

---

## 2. Architecture

The simulator connects directly to the production Go intelligence packages:

```
Browser Dashboard (Vanilla JS + CSS)
         │  HTTP / JSON
         ▼
Simulator Backend (Go net/http on :8082)
         │  In-Memory Go Structs
         ▼
Synthetic Conversion (mapper.go)
         │  *metrics.PodMetrics + *metrics.MetricWindow
         ▼
┌─────────────────────────────────────────────────────────────┐
│             Production Intelligence Pipeline                │
│                                                             │
│   1. pkg/analyzer.Analyze()                                 │
│          ↓ WorkloadProfile                                  │
│   2. pkg/detector.Classify()                                │
│          ↓ ClassificationResult                             │
│   3. pkg/decision.Engine.Evaluate()                         │
│          ↓ DecisionResult                                   │
└─────────────────────────────────────────────────────────────┘
         │  DecisionResult + Scores + Explainability
         ▼
Dashboard Visualization (Topology, Badges, 9-Factor Drawer)
```

---

## 3. Getting Started

### Prerequisites
- Go 1.24+ installed on your system.

### Starting the Simulator

Navigate into the `simulator` directory and run:

```powershell
cd simulator
go run ./backend/main.go
```

*(Alternatively, from the workspace root: `go run -C simulator ./backend/main.go`)*

By default, the server starts on port `8082`:
- **Dashboard URL**: [http://localhost:8082](http://localhost:8082)
- **API Health**: [http://localhost:8082/api/health](http://localhost:8082/api/health)

*(Optional: Set `PORT=9000` environment variable to run on an alternative port).*

---

## 4. How to Run a Simulation

1. Open [http://localhost:8082](http://localhost:8082) in your browser.
2. Select a predefined scenario from the **Scenario Preset** dropdown (e.g. `Preset G: Mixed Cluster` or `Preset C: Strong Full-Reclaim Candidate`).
3. (Optional) Click **+ Add Node** or **+ Add Workload** to customize the synthetic cluster.
4. Click the prominent green **RUN SIMULATION** button.
5. Review:
   - **Cluster Telemetry Strip**: Aggregate active, low usage, idle counts, decisions, and theoretical reclaimable CPU/RAM.
   - **Node Cards**: Node capacity utilization and per-node theoretical headroom.
   - **Workload Table**: Individual workload verdicts, composite scores $[0.0, 1.0]$, and actions.
   - **Score Breakdown Drawer**: Click **Score Breakdown** on any row to expand the 9-factor scores and read positive/negative explainability reasons.

---

## 5. Preset Scenarios

| Preset | Name | Characteristics | Expected Outcome |
|---|---|---|---|
| **A** | Active Workload | High CPU (85%), ongoing QPS (180), recent activity | `ACTIVE` / `KEEP` |
| **B** | Low Usage | Low CPU/memory, but idle duration 15s < 60s minimum | `LOW_USAGE` / `KEEP` |
| **C** | Strong Full-Reclaim | 2h idle, CPU 5m/2000m, Mem 50MB/4GB, QPS 0, 4 replicas, PDB=2, Checkpointable | `IDLE` / `FULL_RECLAIM` |
| **D** | Protected Idle | Negligible usage, long idle, but `reclaim.io/protected="true"` | `IDLE` / `KEEP` (Safety gate) |
| **E** | PDB Blocked | Idle, but PDB `disruptionsAllowed = 0` | `IDLE` / `KEEP` (Disruption gate) |
| **F** | Checkpoint Unsupported | Idle StatefulSet without CRIU checkpoint annotation | `IDLE` / `SOFT_RECLAIM` (Fallback) |
| **G** | Mixed Cluster | 3 nodes, 20 heterogeneous workloads (web, batch, daemons, standby) | Comprehensive cluster benchmark |

---

## 6. Available Workload Fields

The simulator exposes fields mapped 1-to-1 with `metrics.PodMetrics`:
- **Identity**: Pod Name, Namespace, Node Name, Owner Kind (`Deployment`, `ReplicaSet`, `StatefulSet`, `DaemonSet`), Owner Name
- **Resource Allocations**: Requested CPU (m), Limit CPU (m), Requested Memory (MB), Limit Memory (MB)
- **Runtime Telemetry**: Actual CPU (m), Actual Memory Working Set (MB), Network (bytes/sec), Request QPS
- **Idle State**: Idle Duration (seconds), `isIdle` flag
- **Quorum & Policy**: Desired/Ready/Available Replicas, PDB `disruptionsAllowed` (-1 for none, 0 for blocked, >0 for allowed), Priority Class, Numeric Priority
- **Annotations**:
  - `reclaim.io/protected="true"`: Unconditionally blocks all reclamation actions.
  - `reclaim.io/checkpointable="true"`: Enables CRIU full reclamation eligibility.

---

## 7. Real Kubernetes Compatibility

The simulator is architected so the synthetic layer (`simulator/backend/conversion`) can be swapped with live Kubernetes client informers and metrics collectors without changing any logic in `pkg/analyzer`, `pkg/detector`, or `pkg/decision`.
