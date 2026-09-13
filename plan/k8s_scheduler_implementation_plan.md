# Implementation Plan: Runtime-Aware Resource Reclamation & Adaptive Kubernetes Scheduler

## Goal Description
Implement a production-ready, extensible Kubernetes scheduling and resource reclamation system based on the architectural specifications in `architecutre.png` and `reclaimationengine.png`. 

The system enables **Runtime-Aware Resource Reclamation and Adaptive Scheduling**:
1. **Dynamic Metric Ingestion**: Aggregates container- and pod-level CPU, Memory, Network I/O, Request/QPS, and Idle duration alongside K8s control plane state (Priority, QoS, PDBs, Replicas).
2. **Workload Analysis & Idle Detection**: Distinguishes active vs low-usage vs genuinely idle workloads using configurable multi-signal sliding windows.
3. **Multi-Criteria Reclamation Decision Engine**: Evaluates safety gates (PDB, priority class, minimum replica thresholds, checkpoint compatibility) and reclamation gains (CPU/RAM freed vs checkpoint overhead).
4. **Action Manager (Full vs Soft Reclaim)**:
   - **Full Reclaim (CRIU Checkpoint & Suspend)**: Triggers Kubelet/containerd checkpoint API, verifies artifact integrity, saves checkpoint to persistent storage, suspends/evicts the pod to release scheduler quota, and provides an automatic or trigger-based restore mechanism.
   - **Soft Reclaim (Dynamic In-Place Right-Sizing)**: In-place Pod resize (K8s 1.27+ `resizePolicy`) to adjust requests down without killing the container.
5. **Custom Adaptive Scheduler Plugin**: K8s Scheduling Framework plugin (`Filter`, `Score`, `Reserve`, `PreScore`) that schedules pending pods into reclaimed capacity with bin-packing preference.

---

## User Review Required

> [!IMPORTANT]
> **Container Checkpointing (CRIU) Requirements in Kubernetes**:
> Kubernetes native Container Checkpointing (`POST /checkpoint`) was introduced as an alpha feature in K8s 1.25+ (`ContainerCheckpoint` feature gate enabled in Kubelet) and requires CRIU installed on worker nodes and containerd runtime. If running on local Kind/Docker Desktop, the Kind node image must have CRIU enabled and Kubelet configured with `--feature-gates=ContainerCheckpoint=true`.

> [!WARNING]
> **Stateful vs Stateless Workloads**:
> Full reclaim via CRIU is best suited for batch jobs, machine learning training/eval checkpoints, cold developer workspaces, and long-running batch workers. Network-connected TCP services (like open websockets or DB connections) may experience socket resets during restore unless coordinated. We will enforce safe filtering via annotation tags (e.g., `reclaim.simplens.io/checkpointable: "true"` or workload type detection).

---

## Architecture & Component Breakdown

```mermaid
flowchart TB
    subgraph K8sCluster["Kubernetes Cluster"]
        direction TB
        APIServer["K8s API Server"]
        Kubelet["Kubelet (CRIU / Checkpoint API)"]
        Workloads["Worker Pods (Apps / Batch)"]
    end

    subgraph MonitoringLayer["Monitoring & Telemetry Layer"]
        cAdvisor["cAdvisor / Node Exporter"]
        Prom["Prometheus / VictoriaMetrics"]
        CollectorDaemon["Metrics Collector Cache (In-Memory)"]
    end

    subgraph ControllerPlane["Runtime-Aware Scheduler & Controller"]
        direction TB
        Analyzer["1. Workload Analyzer"]
        Detector["2. Idle Classifier & Detector"]
        DecisionEngine["3. Multi-Criteria Reclaim Engine"]
        ActionMgr["4. Action Manager"]
        FeedbackLoop["5. Feedback & History Store (SQLite/CRD)"]
    end

    subgraph SchedulerFramework["Custom K8s Scheduler"]
        FilterPlugin["Filter: Hard Constraints & PDBs"]
        ScorePlugin["Score: Real-Load Bin-Packing"]
    end

    subgraph StorageLayer["Checkpoint & Metadata Storage"]
        CheckpointPVC["PV / S3 / Local Disk (Checkpoints)"]
    end

    Kubelet -->|Metrics| cAdvisor --> Prom
    APIServer -->|Informers (Pods, PDB, Nodes)| CollectorDaemon
    Prom -->|PromQL Vector Sync| CollectorDaemon
    CollectorDaemon --> Analyzer --> Detector --> DecisionEngine
    DecisionEngine -->|Safe & Beneficial| ActionMgr
    ActionMgr -->|POST /checkpoint| Kubelet
    Kubelet -->|Checkpoint Tarball| CheckpointPVC
    ActionMgr -->|Delete/Evict Pod| APIServer
    ActionMgr -->|Soft Reclaim: In-Place Patch| APIServer
    ActionMgr -->|Record History| FeedbackLoop
    FeedbackLoop -.->|Update Heuristics| DecisionEngine
    SchedulerFramework <--> APIServer
```

---

## Proposed Low-Level Design & Directory Structure

We will implement this project in Go (standard for Kubernetes controllers & scheduler plugins) under `k8s-scheduler/`.

```
k8s-scheduler/
├── cmd/
│   ├── controller/               # Main entrypoint for Reclamation Controller & Analyzer
│   │   └── main.go
│   ├── scheduler/                # Custom K8s Scheduler binary (Scheduling Framework)
│   │   └── main.go
│   └── restore-agent/            # Sidecar/Daemon or webhook to restore checkpointed pods
│       └── main.go
├── api/
│   └── v1alpha1/                 # Custom Resource Definitions (CRDs)
│       ├── types.go              # ReclaimPolicy, CheckpointRecord, WorkloadProfile
│       └── zz_generated.deepcopy.go
├── pkg/
│   ├── config/                   # Configuration loader & CLI flags
│   │   └── config.go
│   ├── metrics/                  # Monitoring Layer
│   │   ├── collector.go          # Background poller (Prometheus + Metrics Server)
│   │   ├── cache.go              # Thread-safe in-memory cache for fast lookups
│   │   └── prometheus_client.go  # Optimized PromQL queries (CPU, RAM, Net I/O)
│   ├── analyzer/                 # Component 1: Workload Analyzer
│   │   ├── analyzer.go           # Synthesizes raw metrics into sliding-window usage
│   │   └── window.go             # Exponential moving average / time-series window
│   ├── detector/                 # Component 2: Idle Classification & Detector
│   │   ├── classifier.go         # Classifies Active vs Low-Usage vs Idle
│   │   └── rules.go              # QPS == 0, CPU < threshold, Idle duration > T_idle
│   ├── decision/                 # Component 3: Multi-Criteria Decision Engine
│   │   ├── engine.go             # Main evaluator pipeline
│   │   ├── safety_checks.go      # Priority, PDB, Replica availability, QoS
│   │   ├── benefit_evaluator.go  # Net freed quota vs cost ratio
│   │   └── history.go            # Past success rates, restore duration tracking
│   ├── action/                   # Component 4: Action Manager
│   │   ├── manager.go            # Orchestrates Full Reclaim vs Soft Reclaim
│   │   ├── criu_checkpoint.go    # Interacts with Kubelet Container Checkpoint API
│   │   ├── soft_reclaim.go       # In-place resource right-sizing (PATCH Pod.spec)
│   │   └── restore.go            # Pod reconstitution & CRIU restore execution
│   ├── storage/                  # Storage abstraction for checkpoint images
│   │   ├── storage.go            # Storage interface (LocalFS, NFS, S3)
│   │   └── localfs.go
│   └── scheduler/                # Kubernetes Scheduling Framework Plugins
│       ├── plugin.go             # Plugin registration
│       ├── filter.go             # Filters nodes with actual headroom
│       └── score.go              # Scores nodes based on bin-packing & low fragmentation
├── deployments/
│   ├── crds/                     # YAML definitions for CRDs
│   │   ├── reclaimpolicy.yaml
│   │   └── checkpointrecord.yaml
│   ├── rbac.yaml                 # ClusterRole, RoleBinding, ServiceAccount
│   ├── controller-deployment.yaml# Deployment for controller & collector
│   └── scheduler-deployment.yaml # Deployment for custom scheduler
├── test/
│   ├── e2e/                      # End-to-end integration tests on Kind
│   └── mocks/                    # Mock Kubelet, Prometheus, and APIServer clients
├── go.mod
└── go.sum
```

---

## Detailed Step-by-Step Implementation Strategy

### Phase 1: CRD Definitions & Data Models (`api/v1alpha1`)
Define the declarative schema for cluster administrators to configure policies and track checkpoints:

1. **`ReclaimPolicy` CRD**:
   - `spec.idleDurationThreshold`: Duration (e.g. `10m`, `30m`) before candidate qualification.
   - `spec.cpuThreshold`: CPU utilization below which container is considered idle (e.g. `0.02` cores).
   - `spec.memoryThreshold`: Minimum memory delta to justify reclamation.
   - `spec.minReplicasAvailable`: Protect services from falling below required quorum.
   - `spec.exemptPriorityClasses`: Skip system-critical workloads (e.g., `system-cluster-critical`).
   - `spec.actionType`: `FullReclaim` (CRIU Checkpoint + Terminate), `SoftReclaim` (Downscale requests), or `Adaptive`.
2. **`CheckpointRecord` CRD**:
   - Stores checkpoint metadata: `sourcePodName`, `namespace`, `nodeName`, `imageURI`, `checkpointPath`, `checkpointSizeBytes`, `capturedAt`, `originalResources`, `status` (`Ready`, `Restoring`, `Expired`).

### Phase 2: High-Performance Monitoring & Metrics Ingestion (`pkg/metrics`)
To ensure scheduling decisions run in sub-millisecond latencies without blocking on network round-trips:
1. **Prometheus Vector Scraper**:
   - Background worker queries Prometheus every `N` seconds (default: 10s).
   - Collects 3 core vector queries:
     - `sum(rate(container_cpu_usage_seconds_total{container!=""}[2m])) by (pod, namespace)`
     - `sum(container_memory_working_set_bytes{container!=""}) by (pod, namespace)`
     - `sum(rate(container_network_receive_bytes_total[2m]) + rate(container_network_transmit_bytes_total[2m])) by (pod, namespace)`
2. **Client-Go Informer Caches**:
   - Pod informer: Watches all pods, extracts `QOSClass`, `Priority`, `PodStatus`, `ResourceRequirements`.
   - PDB informer: Maps Pod labels to matching `PodDisruptionBudget.Status.DisruptionsAllowed`.
   - Node informer: Tracks allocatable vs allocated vs actual used capacity.
3. **In-Memory Store (`MetricsCache`)**:
   - Thread-safe `sync.RWMutex` map indexed by `namespace/pod-name`.
   - Computes `IdleDuration` dynamically by tracking consecutive windows where CPU < threshold and Network traffic < threshold.

### Phase 3: Workload Analyzer & Idle Detector (`pkg/analyzer`, `pkg/detector`)
1. **Workload Categorization**:
   - **`ACTIVE`**: CPU or Network I/O above baseline, requests actively served.
   - **`LOW_USAGE`**: Intermittent bursts (e.g., periodic cron or heartbeat) within the last window.
   - **`IDLE`**: Sustained period $T \ge T_{\text{idle}}$ where CPU, Memory delta, and Network I/O are below thresholds and QPS == 0.
2. **Candidate Generation**:
   - Generates an `IdlePodCandidate` event forwarded to the Multi-Criteria Reclaim Engine.

### Phase 4: Multi-Criteria Reclamation Decision Engine (`pkg/decision`)
Implement the multi-stage evaluation pipeline shown in `reclaimationengine.png`:
1. **Safety & Policy Gate (Must Pass All)**:
   - **Priority Check**: Pod priority must be lower than threshold (never checkpoint critical pods).
   - **PDB Check**: `DisruptionsAllowed > 0`. If disrupting this pod breaks quorum, reject.
   - **Replica Availability**: If part of a Deployment/ReplicaSet, ensure `AvailableReplicas > MinReplicas`.
   - **Checkpoint Compatibility**: Pod must not mount host paths that CRIU cannot serialize; must have compatible container runtime.
2. **Reclamation Benefit Check**:
   - Calculate $\text{Benefit} = (\text{CPU\_Request} \times W_{cpu}) + (\text{Mem\_Request} \times W_{mem})$.
   - Check if cluster has pending pods that can immediately utilize the freed capacity.
3. **Historical Insights Weighting**:
   - Query past `CheckpointRecord` entries. If pod's previous checkpoint took $> 60$s or failed, de-prioritize full checkpoint in favor of soft reclaim.

### Phase 5: Action Manager & CRIU Checkpointing (`pkg/action`)
1. **Full Reclaim Workflow**:
   - Issue authenticated request to Kubelet API:
     `POST /checkpoint/{namespace}/{pod}/{container}`
   - Kubelet invokes containerd checkpoint API via CRIU. Output tarball is written to `/var/lib/kubelet/checkpoints/` or shared storage PVC.
   - **Integrity Verification**: Verify tarball exists, contains `checkpoint/descriptors.json` and valid memory dumps, and calculate SHA256 checksum.
   - **Free Capacity**: Create `CheckpointRecord` CRD, then gracefully delete or evict the pod via Kubernetes Eviction API (`/api/v1/namespaces/{ns}/pods/{pod}/eviction`).
2. **Soft Reclaim (Right-Sizing)**:
   - When checkpointing is unsafe or benefit is moderate:
   - Apply in-place pod resize patch: `PATCH /api/v1/namespaces/{ns}/pods/{pod}/resize` or update Deployment resource requests down to match actual peak usage with a safety margin (e.g., $1.2 \times \text{P95}$).
3. **Restoration Workflow (`Restore Engine`)**:
   - Triggered on demand (e.g. ingress traffic trigger, webhook, manual resume, or schedule queue).
   - Restores pod spec pointing to checkpoint volume, mounting the checkpoint archive, and resuming state.

### Phase 6: Custom Kubernetes Scheduler Plugin (`pkg/scheduler`)
Implement a plugin for `k8s.io/kubernetes/pkg/scheduler/framework`:
1. **Filter Plugin**:
   - Rejects nodes where **actual runtime memory/CPU** plus the incoming pod request would exceed physical capacity (prevents noisy-neighbor OOM kills).
2. **Score Plugin**:
   - Prioritizes nodes where idle workloads were reclaimed or nodes with high resource fragmentation to achieve optimal packing density.

---

## Verification Plan

### Automated Tests
1. **Unit Tests (`go test ./pkg/...`)**:
   - Test metric cache concurrency and eviction.
   - Test idle detection window calculations.
   - Test decision engine safety matrix (PDB allowed = 0 vs > 0, priority classes).
   - Test soft-reclaim patch generator.
2. **Mock Kubelet Checkpoint Integration Tests**:
   - Mock Kubelet checkpoint endpoint to verify tarball checksum verification and error handling.

### End-to-End Cluster Verification (Kind Cluster)
1. **Cluster Setup**:
   - Boot local Kind cluster with `ContainerCheckpoint` feature gate enabled in `k8s/kind-config.yaml`.
   - Deploy Prometheus and node-exporter / metrics-server.
2. **Workload Scenario Execution**:
   - Deploy sample workloads:
     - Workload A: Active HTTP service (should stay ACTIVE).
     - Workload B: Idle batch worker with high CPU reservation (should trigger candidate -> Checkpoint -> Reclaim).
     - Workload C: Workload with PDB `maxUnavailable: 0` (should be rejected by safety gate).
   - Verify pod B gets checkpointed, `CheckpointRecord` CRD is created, and pod B is terminated.
   - Deploy a pending workload and confirm it is scheduled into the freed capacity.
   - Trigger restore for Workload B and verify it resumes.
