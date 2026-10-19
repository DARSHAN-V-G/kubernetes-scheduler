# Adaptive Kubernetes Scheduler & Centralized Telemetry Layer

A runtime-aware, adaptive Kubernetes scheduler and centralized resource monitoring system built for dynamic workload optimization, real physical headroom scheduling, and safe resource reclamation.

---

## Key Features

1. **Centralized Resource Telemetry**:
   * Scrapes runtime metrics directly from **Prometheus / cAdvisor** across all cluster nodes, pods, and containers.
   * Tracks **CPU** (millicores), **Memory** (working set & RSS bytes), **Network I/O** (Rx & Tx bandwidth), **Request/QPS** (HTTP QPS or packet rates), and **Idle Duration**.
2. **Kubernetes Control-Plane Ingestion**:
   * Uses `client-go` Informers to track declarative state: **Priority**, **QoS Class**, **PDBs (`disruptionsAllowed`)**, and owning controller **Replica Quorums** (Deployments, ReplicaSets, StatefulSets).
3. **Co-Located Single-Pod Architecture**:
   * The custom scheduler and a dedicated Prometheus server run **inside the same Kubernetes Pod** (`READY 2/2`).
   * Prometheus scrapes all nodes cluster-wide, while the scheduler's collector queries Prometheus over localhost loopback (`http://127.0.0.1:9090`), eliminating network latency, DNS overhead, and cluster partition risks.
4. **Independent Coexistence with Default Scheduler**:
   * Runs harmoniously alongside `kube-scheduler` in Kubernetes.
   * Workloads target this scheduler via `spec.schedulerName: adaptive-scheduler`.
   * Operates an isolated leader election lease (`adaptive-scheduler` in `kube-system`).
5. **Decoupled Architecture**:
   * Completely independent and generic. It can monitor and schedule any workload across the cluster. Applications like `cloud-ecommerce` serve strictly as external benchmarks.

---

## Directory Structure

```
adaptive-k8s-scheduler/
├── cmd/
│   └── metrics-collector/        # Standalone daemon & HTTP snapshot API
│       └── main.go
├── pkg/
│   ├── config/                   # Configuration flags & environment variable loader
│   │   └── config.go
│   ├── metrics/                  # Core telemetry & correlation package
│   │   ├── types.go              # Complete metric models (CPU, Mem, Net, QPS, Idle, K8s State)
│   │   ├── window.go             # Sliding-window ring buffer (EMA & P95)
│   │   ├── cache.go              # Thread-safe in-memory cache & headroom calculator
│   │   ├── prometheus_client.go  # Localhost PromQL vector scraper
│   │   ├── k8s_informer.go       # Informers (Nodes, Pods, PDBs, Replicas)
│   │   └── collector.go          # Central coordination & idle duration engine
├── deployments/                  # Production-ready Kubernetes manifests
│   ├── prometheus-configmap.yaml # Scrape config for co-located Prometheus
│   ├── rbac.yaml                 # ServiceAccount, ClusterRole, ClusterRoleBinding
│   ├── scheduler-deployment.yaml # Co-located 2-container Pod Deployment
│   └── example-workload.yaml     # Sample pod demonstrating spec.schedulerName routing
├── Dockerfile                    # Multi-stage Docker container build
├── go.mod                        # Go module
└── go.sum
```

---

## Co-Located Pod Architecture

```
+-------------------------------------------------------------------------+
|                  Custom Scheduler Pod (kube-system)                     |
|                                                                         |
|  +--------------------------------+  +--------------------------------+ |
|  | Container 1: prometheus        |  | Container 2: adaptive-scheduler| |
|  | (prom/prometheus:v2.45.0)     |  | (Custom Go binary)             | |
|  |                                |  |                                | |
|  | - Scrapes cAdvisor on all nodes|  | - Queries PromQL via loopback  | |
|  | - Short 2h TSDB retention      |  |   http://127.0.0.1:9090        | |
|  | - Listens on :9090             |  | - Listens on :8081             | |
|  +--------------------------------+  +--------------------------------+ |
|                  ^                                  ^                   |
|                  |                                  |                   |
+------------------|----------------------------------|-------------------+
                   | Scrapes /metrics/cadvisor        | Watches Pods/Nodes
                   v                                  v
        +----------------------+             +------------------+
        | All K8s Worker Nodes |             |  K8s API Server  |
        +----------------------+             +------------------+
```

---

## Metrics Specification

| Metric | Source | PromQL / Extraction Logic |
| :--- | :--- | :--- |
| **Container CPU** | cAdvisor | `sum(rate(container_cpu_usage_seconds_total[2m])) * 1000` (millicores) |
| **Pod Total CPU** | Aggregation | $\sum \text{ContainerCPU}$ |
| **Container Memory** | cAdvisor | `container_memory_working_set_bytes` & `container_memory_rss` |
| **Node Real Headroom** | Calculation | $\text{Allocatable} - \text{ActualPhysicalUsage}$ |
| **Network I/O** | cAdvisor | `rate(container_network_receive_bytes_total[2m])` + `rate(container_network_transmit_bytes_total[2m])` |
| **Request / QPS** | Prom Scrape | `http_requests_total` rate (with packet rate fallback) |
| **Idle Duration** | Collector Engine | Cumulative time where $\text{CPU} < 20\text{m}$, $\text{Net} < 10\text{KB/s}$, $\text{QPS} \approx 0$ |
| **Priority** | K8s Spec | `pod.spec.priority` & `pod.spec.priorityClassName` |
| **QoS Class** | K8s Status | `pod.status.qosClass` (`Guaranteed`, `Burstable`, `BestEffort`) |
| **PDBs** | K8s Informer | `pdb.status.disruptionsAllowed` |
| **Replicas** | K8s Informer | Desired, Ready, and Available counts from owning Deployment/ReplicaSet |

---

## Deployment & Verification

Install the CRDs before applying sample custom resources. Kubernetes needs a short period to establish the new API kinds:

```bash
kubectl apply -f deployments/crds/reclaim.io_checkpointrecords.yaml \
   -f deployments/crds/reclaim.io_reclaimpolicies.yaml
kubectl wait --for=condition=Established --timeout=60s \
   crd/checkpointrecords.reclaim.io crd/reclaimpolicies.reclaim.io
kubectl apply -f deployments/crds/sample_checkpointrecord.yaml \
   -f deployments/crds/sample_reclaimpolicy.yaml
```

Because these CRDs use a status subresource, status fields in sample manifests may need to be applied separately:

```bash
kubectl patch checkpointrecord ckpt-analytics-worker-sample -n ecommerce \
   --subresource=status --type=merge \
   -p '{"status":{"phase":"Ready","message":"Sample checkpoint record ready for restoration"}}'
```

### Live ecommerce metrics demo

Port-forward the ecommerce Prometheus service and run the demo against a real pod:

```bash
kubectl -n ecommerce port-forward svc/prometheus 9090:9090
go run ./cmd/demo --namespace ecommerce --selector app=worker --prometheus-url http://127.0.0.1:9090
```

The command reads pod resource requests from Kubernetes and CPU, memory, network, and QPS values from Prometheus. It samples the real pod five times, computes idle duration from those observations, and runs the analyzer, detector, and decision engine. It is read-only and does not evict or resize pods.

To execute the selected action against the real pod, add `--execute`:

```bash
go run ./cmd/demo \
   --namespace ecommerce \
   --pod idle-checkpoint-demo \
   --prometheus-url http://127.0.0.1:9090 \
   --samples 7 \
   --interval 10s \
   --execute
```

The demo treats QPS at or below `0.1` as idle by default, matching the collector threshold and filtering small Prometheus rate noise. Override it with `--idle-qps-threshold` when needed.

Execution is refused unless the final detector result is `IDLE`. A `SOFT_RECLAIM` decision calls the Kubernetes pod resize subresource; a `FULL_RECLAIM` decision calls the Kubelet checkpoint API, validates the archive, persists a `CheckpointRecord`, and evicts the pod. Full reclaim additionally requires CRIU/Kubelet support and the checkpoint path to be accessible from the demo process.

### Deliberately idle checkpoint demo

Deploy a workload with substantial reserved capacity but no traffic or work:

```bash
kubectl apply -f deployments/demo-idle-pod.yaml
kubectl get pod -n ecommerce -w idle-checkpoint-demo
```

Observe its real Prometheus values:

```bash
go run ./cmd/demo \
   --namespace ecommerce \
   --pod idle-checkpoint-demo \
   --prometheus-url http://127.0.0.1:9090 \
   --samples 7 \
   --interval 10s
```

The pod is eligible only after the production collector has continuously observed CPU <= 20m, network <= 10KB/s, and QPS <= 0.1 for the configured idle duration (default: 60s). The live demo command reports the values but does not mutate the pod. The production scheduler loop performs checkpoint and eviction only when CRIU, archive storage, record persistence, and safety checks are configured.

To remove the demo workload:

```bash
kubectl delete pod -n ecommerce idle-checkpoint-demo
```

Use an exact pod when needed:

```bash
go run ./cmd/demo --namespace ecommerce --pod worker-xxxxxxxxx-xxxxx --prometheus-url http://127.0.0.1:9090
```

### Request-triggered restore in Kubernetes

Checkpointing is lifecycle-sensitive: the kubelet/runtime may stop the source container after a successful checkpoint. The controller records the verified archive as `Checkpointed` before releasing a bare Pod. For Pods owned by a Deployment, Job, or other controller, lifecycle release is deferred so the owner cannot immediately recreate the source workload. `restartPolicy: Never` is recommended for one-shot checkpoint probes; `Always` or `OnFailure` can cause the controller to restart a source after runtime termination and should be tested only with an explicit owner strategy.

The restore spike is deliberately separate from the scheduler restore path. It asks K3s containerd to import a CRIU tar as an OCI image and creates a probe Pod only if containerd accepts the import. A CRIU checkpoint tar is not necessarily an OCI image, so rejection is reported as unsupported rather than treated as a successful restore:

```bash
go run ./cmd/restore-spike --tar /var/lib/rancher/k3s/agent/kubelet/checkpoints/checkpoint.tar
```

After deploying the scheduler and exposing its HTTP service, restore a `Ready` checkpoint record with:

```bash
curl -X POST \
   -H "Authorization: Bearer $RESTORE_API_TOKEN" \
   "http://localhost:8081/api/v1/checkpoints/<namespace>/<checkpoint-record>/restore"
```

The controller updates the record through `Restoring` to `Restored` (or `Failed`) and creates a pod from the stored specification. This is the simulation/portable restore path; real CRIU process-state restoration still requires a node-side runtime restore implementation and shared checkpoint storage.

Set `RESTORE_API_TOKEN` on the scheduler deployment before using the manual endpoint. The scheduler also checks for pending pods every 10 seconds. A pending pod must identify the workload to restore with `reclaim.io/restore-source-pod: <source-pod-name>`; the controller skips the request when no matching record exists or a replacement is already present. This automatic path currently recreates the pod from its saved specification; it does not resume the original process memory image.

### 1. Build and Load Container Image
```bash
cd adaptive-k8s-scheduler
docker build -t adaptive-scheduler:latest .
kind load docker-image adaptive-scheduler:latest
```

### 2. Deploy to Kubernetes
```bash
kubectl apply -f deployments/prometheus-configmap.yaml
kubectl apply -f deployments/rbac.yaml
kubectl apply -f deployments/scheduler-deployment.yaml
```

### 3. Verify Health & Snapshot Telemetry
Verify the pod is running with both containers ready (`2/2`):
```bash
kubectl get pods -n kube-system -l app=adaptive-scheduler
```

Port-forward and query the telemetry snapshot:
```bash
kubectl port-forward -n kube-system deploy/adaptive-scheduler 8081:8081

# Query unified cluster snapshot (Nodes, Pods, Containers, requests vs actual)
curl http://localhost:8081/api/v1/snapshot | jq .
```
