# Adaptive Kubernetes Scheduler & Runtime Reclamation Controller

A runtime-aware, adaptive Kubernetes scheduler and centralized reclamation system designed for dynamic physical headroom scheduling, real-time workload telemetry, safe autonomous resource reclamation (via soft in-place resize and full CRIU checkpointing), and demand-triggered restoration.

---

## Architecture Overview

Traditional Kubernetes scheduling decisions rely exclusively on static, declarative reservations (`pod.spec.containers[*].resources.requests`). When workloads are over-provisioned and sit idle, nodes suffer from artificial exhaustion while physical CPU and memory sit wasted.

This project addresses this inefficiency through an integrated, multi-tier architecture:

```mermaid
flowchart TB
    subgraph TelemetryLayer["1. Telemetry & Ingestion Layer"]
        cAdvisor["cAdvisor (Kubelet :10250)"] -->|Scrapes All Nodes| Prom["Co-located Prometheus (:9090)"]
        Prom -->|Scrapes via Loopback| Collector["Metrics Collector & Cache"]
        APIServer["Kubernetes API Server"] -->|Informers (Pods, Nodes, PDBs, Replicas)| Collector
    end

    subgraph SchedulerFramework["2. Adaptive Placement Engine"]
        PendingPod["Pending Pod (spec.schedulerName: adaptive-scheduler)"] --> Filter["Filter: Real Physical Free Headroom"]
        Filter --> Scorer["Score: Bin-Packing & Resource Balance"]
        Scorer --> Bind["API Server Node Binding"]
    end

    subgraph ReclaimEngine["3. Autonomous Reclamation & Restore"]
        Collector --> Analyzer["Workload Analyzer (P95, Trend)"]
        Analyzer --> Detector["Idle Detector (Multi-signal Window)"]
        Detector --> Decision["Decision Engine (Safety Gates & Quota Benefits)"]
        Decision -->|0.50 <= Score < 0.75| Soft["Soft Reclaim: Dynamic In-Place Resize"]
        Decision -->|Score >= 0.75| Full["Full Reclaim: Kubelet CRIU Checkpoint & Eviction"]
        Full --> Record["CheckpointRecord CRD"]
        Record --> Restore["Demand-Triggered Restore Engine"]
    end
```

### Core Features

1. **Co-Located Single-Pod Deployment**:
   * The scheduler binary and a dedicated Prometheus instance run inside the **same pod** (`READY 2/2`) in `kube-system`.
   * Queries run via localhost loopback (`http://127.0.0.1:9090`), eliminating network latency, cross-node traffic, and DNS overhead.
2. **Real Physical Headroom Scheduling**:
   * Rejects nodes that appear available on paper but are physically saturated by noisy neighbors:
     $$\text{RealFreeHeadroom} = \text{Allocatable} - \text{ActualPhysicalUsage}$$
3. **Bin-Packing with Utilization Ceiling & Resource Balance**:
   * Optimizes node density up to an 85% ceiling (penalizing hotspots above 85%).
   * Balances CPU and memory ratios to minimize stranded capacity.
   * Awards bonus points to nodes that recently reclaimed idle capacity.
4. **Multi-Signal Idle Detection**:
   * Evaluates CPU (millicores), Memory (RSS & working set), Network I/O (bytes/sec), and Request QPS (or packet rate) across configurable sliding windows.
5. **Two-Tier Reclamation Strategy**:
   * **Soft Reclaim (In-Place Resize)**: Dynamically scales down CPU and memory allocations via Kubernetes in-place resize without restarting the container.
   * **Full Reclaim (CRIU Checkpoint + Evict)**: Calls the Kubelet Container Checkpoint API, archives process memory state into a tarball, records metadata in a `CheckpointRecord` CRD, and evicts the idle pod to free node capacity.
6. **Demand-Triggered Restore**:
   * Detects pending pods requesting restoration and automatically reconstitutes workloads from verified checkpoint images.

---

## Directory Structure

```
kubernetes-test-application/
├── .github/
│   └── workflows/
│       └── build-and-push.yaml    # GitHub Actions workflow for GHCR image build & push
├── adaptive-k8s-scheduler/        # Custom scheduler source code & manifests
│   ├── cmd/
│   │   ├── scheduler/             # Main scheduler & reclamation daemon
│   │   ├── metrics-collector/     # Standalone telemetry collector daemon
│   │   └── demo/                  # Interactive demo & evaluation CLI tool
│   ├── pkg/
│   │   ├── scheduler/             # Filter, Score, and Binding logic
│   │   ├── metrics/               # Telemetry collector, cache, and PromQL client
│   │   ├── analyzer/              # Metric trend and profile analyzer
│   │   ├── detector/              # Multi-signal idle classifier
│   │   ├── decision/              # Multi-criteria decision engine & safety gates
│   │   ├── action/                # Soft resize, Kubelet checkpoint client, and restore engine
│   │   ├── storage/               # LocalFS checkpoint tarball verification
│   │   └── config/                # Command-line flags & environment variable loader
│   ├── api/v1alpha1/              # CRD Go types (CheckpointRecord, ReclaimPolicy)
│   ├── deployments/               # Kubernetes deployment manifests & CRD specs
│   └── Dockerfile                 # Multi-stage Docker build for Go binaries
├── cloud-ecommerce/               # Microservices reference application for benchmarking
├── kind-config.yaml               # Kind cluster config enabling ContainerCheckpoint
└── README.md                      # Project documentation & scheduling guide
```

---

## Quickstart: Deploying the Scheduler

### Prerequisites
* Docker Engine / Docker Desktop
* `kubectl` (v1.27+)
* `kind` (v0.20+)

### 1. Create a Kind Cluster with Container Checkpointing
Use the provided [`kind-config.yaml`](file:///D:/College%20Projects/FinalYearProject1/kubernetes-test-application/kind-config.yaml) to enable the Kubelet `ContainerCheckpoint` feature gate and mount the checkpoints volume:

```bash
kind create cluster --name adaptive-cluster --config kind-config.yaml
```

*(Optional for Linux/bare-metal CRIU testing)*: Install `criu` on the worker nodes:
```bash
docker exec -it adaptive-cluster-worker apt-get update && docker exec -it adaptive-cluster-worker apt-get install -y criu
docker exec -it adaptive-cluster-worker2 apt-get update && docker exec -it adaptive-cluster-worker2 apt-get install -y criu
```

### 2. Build or Pull the Scheduler Image
You can pull the official image built by GitHub Actions:
```bash
docker pull ghcr.io/adhish-krishna/adaptive-scheduler:latest
kind load docker-image ghcr.io/adhish-krishna/adaptive-scheduler:latest --name adaptive-cluster
```

Or build locally:
```bash
cd adaptive-k8s-scheduler
docker build -t adaptive-scheduler:latest .
kind load docker-image adaptive-scheduler:latest --name adaptive-cluster
```

### 3. Deploy CRDs, RBAC, and the Scheduler Pod
```bash
# 1. Install Custom Resource Definitions
kubectl apply -f deployments/crds/reclaim.io_checkpointrecords.yaml \
              -f deployments/crds/reclaim.io_reclaimpolicies.yaml

kubectl wait --for=condition=Established --timeout=60s \
  crd/checkpointrecords.reclaim.io crd/reclaimpolicies.reclaim.io

# 2. Apply RBAC permissions
kubectl apply -f deployments/rbac.yaml

# 3. Deploy Prometheus ConfigMap and Co-located Scheduler
kubectl apply -f deployments/prometheus-configmap.yaml
kubectl apply -f deployments/scheduler-deployment.yaml

# 4. Verify the scheduler is Running with 2/2 containers Ready
kubectl get pods -n kube-system -l app=adaptive-scheduler
```

---

## How to Schedule Pods Using the Custom Scheduler

To route any workload to the Adaptive Scheduler instead of the default Kubernetes scheduler, set:
```yaml
spec:
  schedulerName: adaptive-scheduler
```

### Example 1: Standard Application Pod

This pod routes directly to the adaptive scheduler. The scheduler checks the actual physical CPU and memory headroom of all candidate nodes before placing it.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: web-frontend
  namespace: default
  labels:
    app: web-frontend
spec:
  schedulerName: adaptive-scheduler
  containers:
    - name: nginx
      image: nginx:alpine
      resources:
        requests:
          cpu: "100m"
          memory: "128Mi"
        limits:
          cpu: "250m"
          memory: "256Mi"
```

Apply and verify:
```bash
kubectl apply -f web-frontend.yaml
kubectl get pod web-frontend -o wide
# Check scheduler logs to see the filter and bin-pack scoring evaluation
kubectl logs -n kube-system -l app=adaptive-scheduler -c adaptive-scheduler --tail=20
```

---

### Example 2: Deployment with Replicas (Replica-Quorum Aware)

The reclamation engine is aware of replica quorums. When evaluating a pod for reclamation, it verifies that removing or modifying it will not violate the minimum available replica threshold (`MinReplicasRequired: 1`).

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: order-processing-service
  namespace: default
spec:
  replicas: 3
  selector:
    matchLabels:
      app: order-processor
  template:
    metadata:
      labels:
        app: order-processor
    spec:
      schedulerName: adaptive-scheduler
      containers:
        - name: processor
          image: python:3.11-slim
          command: ["python", "-c", "import time; [time.sleep(1) for _ in iter(int, 1)]"]
          resources:
            requests:
              cpu: "250m"
              memory: "256Mi"
            limits:
              cpu: "500m"
              memory: "512Mi"
```

Apply:
```bash
kubectl apply -f order-processor.yaml
kubectl get pods -l app=order-processor -o wide
```

---

### Example 3: Checkpointable Batch Worker (`reclaim.io/checkpointable: "true"`)

For batch workers, machine learning jobs, or background data crunchers, annotate the pod with `reclaim.io/checkpointable: "true"`. This gives the Decision Engine permission to checkpoint the container via CRIU if it remains idle.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: batch-analytics-worker
  namespace: default
  labels:
    app: batch-worker
  annotations:
    reclaim.io/checkpointable: "true"
spec:
  schedulerName: adaptive-scheduler
  restartPolicy: Never
  containers:
    - name: analytics-runner
      image: alpine:3.20
      command: ["/bin/sh", "-c", "exec sleep 86400"]
      resources:
        requests:
          cpu: "1000m"
          memory: "1Gi"
        limits:
          cpu: "2000m"
          memory: "2Gi"
```

**What happens when this pod stays idle?**
1. Telemetry detects sustained zero CPU, network, and QPS ($T \ge 30\text{s}$).
2. The Decision Engine scores the candidate. High requested quota ($1000\text{m}$ CPU, $1\text{Gi}$ RAM) pushes the score $\ge 0.75$, selecting `FULL_RECLAIM`.
3. The Kubelet Checkpoint API is invoked.
4. A `CheckpointRecord` custom resource is persisted to the cluster.
5. The pod is evicted to return physical and declarative quota to the node.

---

### Example 4: Protected Workload (`reclaim.io/protected: "true"`)

If a workload must **never** be resized or evicted regardless of idleness, add the `reclaim.io/protected: "true"` annotation. The decision engine enforces this as an uncompromising hard safety gate.

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: critical-payment-gateway
  namespace: default
  annotations:
    reclaim.io/protected: "true"
spec:
  schedulerName: adaptive-scheduler
  containers:
    - name: payment-api
      image: alpine:3.20
      command: ["/bin/sh", "-c", "exec sleep 86400"]
      resources:
        requests:
          cpu: "500m"
          memory: "512Mi"
```

Even after hours of zero activity, the scheduler will mark the decision as `KEEP` and refuse both soft and full reclamation.

---

### Example 5: Demand-Triggered Restore of a Checkpointed Pod

When a workload has been checkpointed and evicted, its metadata is preserved in a `CheckpointRecord` custom resource (status: `Ready`).

To automatically restore and resume the workload when demand arises, submit a pending pod with the `reclaim.io/restore-source-pod` annotation:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: demand-trigger
  namespace: default
  annotations:
    reclaim.io/restore-source-pod: "batch-analytics-worker"
spec:
  schedulerName: adaptive-scheduler
  containers:
    - name: trigger
      image: alpine:3.20
      command: ["sleep", "10"]
```

**Controller Restoration Flow:**
1. The scheduler's demand-triggered restore loop (running every 10s) scans for pending demand.
2. It discovers `demand-trigger` targeting `batch-analytics-worker`.
3. It locates the matching `CheckpointRecord` in status `Ready`.
4. The `RestoreEngine` reconstructs the pod spec snapshot from the CRD, sets the checkpoint tarball mount annotations, and creates the restored pod (`<source-pod>-restored-<timestamp>`).
5. The `CheckpointRecord` status transitions from `Ready` $\to$ `Restoring` $\to$ `Restored`.

---

## Telemetry & Observability HTTP APIs

The scheduler exposes a REST API on port `8081` inside the cluster.

To inspect cluster-wide physical telemetry and scheduling snapshots:

```bash
# Port forward the scheduler API
kubectl port-forward -n kube-system deploy/adaptive-scheduler 8081:8081
```

| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/v1/snapshot` | `GET` | Returns unified cluster telemetry (all nodes, pods, real usage vs declarative requests) |
| `/api/v1/nodes` | `GET` | Real-time physical capacity, actual usage, and real free headroom per node |
| `/api/v1/pods` | `GET` | Container-level CPU (m), memory (working set & RSS), network I/O, QPS, and idle duration |
| `/healthz` | `GET` | Liveness health check (`200 OK`) |
| `/readyz` | `GET` | Readiness health check (`200 OK`) |
| `/metrics` | `GET` | Prometheus-compatible internal scheduler metrics |
| `/api/v1/checkpoints/:ns/:name/restore` | `POST` | Manual API-triggered restoration of a specific `CheckpointRecord` |

Querying the telemetry snapshot:
```bash
curl -s http://localhost:8081/api/v1/snapshot | jq .
```

---

## Reclamation Decision Matrix

The Decision Engine evaluates eight weighted factors to calculate a composite score $[0.0, 1.0]$:

$$\text{Score} = \sum (w_i \times R_i)$$

| Factor | Weight | Evaluation Logic |
| :--- | :--- | :--- |
| **$R_{\text{CPU}}$** | 20% | Lower actual CPU utilization relative to request yields a higher score |
| **$R_{\text{Memory}}$** | 20% | Lower actual RSS/working set memory relative to request yields a higher score |
| **$R_{\text{Idle}}$** | 15% | Normalized continuous inactivity duration ($T_{\text{idle}} / 3600\text{s}$) |
| **$R_{\text{Benefit}}$** | 15% | Quantity of CPU millicores and RAM bytes that would be freed |
| **$R_{\text{Replica}}$** | 10% | Redundancy safety: 0 for standalone pods, 0.5 for 2 replicas, 1.0 for $\ge 4$ replicas |
| **$R_{\text{Priority}}$** | 5% | Lower pod priority yields higher reclamation preference |
| **$R_{\text{PDB}}$** | 5% | Disruption budget availability (`disruptionsAllowed > 0`) |
| **$R_{\text{Checkpoint}}$** | 5% | Workload opt-in via `reclaim.io/checkpointable: "true"` |

### Action Thresholds:
* **Score $< 0.50$**: `KEEP` (workload is retained untouched).
* **$0.50 \le \text{Score} < 0.75$**: `SOFT_RECLAIM` (dynamic in-place resize via Kubernetes `/resize` subresource down to $1.2 \times \text{P95}$, zero downtime).
* **Score $\ge 0.75$**: `FULL_RECLAIM` (Kubelet CRIU checkpoint, `CheckpointRecord` CRD creation, and graceful eviction).

---

## GitHub Actions Workflow (CI/CD)

The repository includes an automated workflow at [`.github/workflows/build-and-push.yaml`](file:///D:/College%20Projects/FinalYearProject1/kubernetes-test-application/.github/workflows/build-and-push.yaml) that builds and publishes container images to the **GitHub Container Registry (GHCR)**:

* **Triggers**:
  * Pushes to `main` or `master` branches
  * Releases / version tags (`v*.*.*`)
  * Manual triggers via `workflow_dispatch`
* **Target Image**: `ghcr.io/<repository-owner>/adaptive-scheduler:latest`
* **Features**:
  * Multi-architecture image support via QEMU
  * Layer caching via GitHub Actions cache (`type=gha`) for fast builds
  * Automatic semver, branch, and commit SHA tag generation
