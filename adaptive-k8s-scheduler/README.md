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
