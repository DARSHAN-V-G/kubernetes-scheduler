# Architecture Deep-Dive: Metrics Collector & Request-Triggered Pod Restoration

This document provides a comprehensive technical analysis of two critical components in the **`adaptive-k8s-scheduler`** codebase:
1. **The Telemetry & Metrics Collector Engine**: Scrapes, correlates, and calculates real-time physical headroom and multi-signal idle status across cluster workloads.
2. **Pod Restoration from Checkpoint upon Receiving a Request**: How hibernated/checkpointed workloads are reconstituted on-demand when traffic arrives, including HTTP buffering, dependency DAG resolution, and the restore engine workflow.

---

## 1. Metrics Collector Architecture & Workflow

The metrics collection system bridges the gap between **declarative Kubernetes state** (from the K8s API server) and **real-time physical resource usage** (from Prometheus / cAdvisor).

### 1.1 Co-Located Pod Topology

In deployment (`deployments/scheduler-deployment.yaml`), the scheduler binary and Prometheus run as a **co-located dual-container Pod** (`READY 2/2`) inside the `kube-system` namespace:

```
+---------------------------------------------------------------------------------+
|                   Custom Scheduler Pod (kube-system)                            |
|                                                                                 |
|  +-------------------------------------+   +---------------------------------+  |
|  | Container 1: prometheus             |   | Container 2: adaptive-scheduler |  |
|  | (prom/prometheus:v2.45.0)          |   | (Custom Go binary)              |  |
|  |                                     |   |                                 |  |
|  | - Scrapes cAdvisor & node-exporter  |   | - Queries PromQL via loopback   |  |
|  |   across all cluster nodes          |   |   (http://127.0.0.1:9090)       |  |
|  | - Retention: 2 hours TSDB           |   | - Informer cache for K8s state  |  |
|  | - Listens on :9090                  |   | - HTTP Snapshot API on :8081    |  |
|  +-------------------------------------+   +---------------------------------+  |
|                     ^                                       ^                   |
+---------------------|---------------------------------------|-------------------+
                      | Scrapes cAdvisor (/metrics/cadvisor)  | Watches API Objects
                      v                                       v
         +-------------------------+             +-----------------------+
         |    Worker Nodes & Pods  |             | Kubernetes API Server |
         +-------------------------+             +-----------------------+
```

* **Zero Network Overhead**: Prometheus scrapes cluster nodes over the network, but the scheduler’s internal `MetricsCollector` scrapes Prometheus over `127.0.0.1:9090` (loopback). This eliminates cross-node latency, DNS resolution bottlenecks, and network partition risks.

---

### 1.2 Dual-Source Telemetry Ingestion Pipeline

The collector combines two parallel ingestion streams into a thread-safe unified model:

```
               [Kubernetes API Server]                 [Prometheus / cAdvisor]
                          │                                       │
            SharedInformers (30s resync)               PromQL Scrapes (10s ticker)
                          │                                       │
                          ▼                                       ▼
                 K8sInformerManager                       PrometheusClient
            - Pods, Nodes, Conditions               - Container CPU (millicores)
            - Requests & Limits                     - Memory (Working Set & RSS)
            - QoS Class & Priority                  - Network Rx/Tx Bandwidth
            - PDBs (disruptionsAllowed)             - Request QPS / Packet Rates
            - Controller Quorum (Replicas)          - Node Physical CPU & Memory
                          │                                       │
                          └───────────────────┬───────────────────┘
                                              │
                                              ▼
                                   MetricsCollector Loop
                                              │
                      ┌───────────────────────┴───────────────────────┐
                      │                                               │
                      ▼                                               ▼
         Multi-Signal Idle Detector                     Node Headroom Calculator
         - CPU <= 20m                                   - RealFreeCPU = Allocatable - Actual
         - Net <= 10KB/s                                - RealFreeMem = Allocatable - Actual
         - QPS <= 0.1
         - Tracks continuous IdleDuration
                      │                                               │
                      └───────────────────────┬───────────────────────┘
                                              │
                                              ▼
                                    MetricsCache (RWMutex)
                               - Window Ring Buffer (EMA & P95)
                               - Indexed by Pod, Node, and Namespace
                               - Cluster Snapshot API (/api/v1/snapshot)
```

#### A. Kubernetes Control-Plane Ingestion (`pkg/metrics/k8s_informer.go`)
Uses `client-go`'s `SharedInformerFactory` to observe:
* **Nodes**: Allocatable vs. Total Capacity for CPU and Memory, plus conditions (`NodeReady`, `NodeMemoryPressure`, `NodeDiskPressure`, `NodePIDPressure`).
* **Pods**: Declarative resource requests and limits per container, `spec.priority`, `status.qosClass`, labels, annotations, and owner references.
* **PodDisruptionBudgets (PDBs)**: Dynamically resolves `status.disruptionsAllowed` to determine if evicting or reclaiming a pod would violate disruption safety.
* **Controller Quorum**: Checks the parent Deployment, ReplicaSet, or StatefulSet to calculate whether desired replicas are currently available.

#### B. Prometheus Vector Client (`pkg/metrics/prometheus_client.go`)
Every collection cycle executes 8 PromQL queries concurrently in parallel goroutines:
1. **Container CPU Rate**:
   ```promql
   sum(rate(container_cpu_usage_seconds_total{container!="",container!="POD"}[2m])) by (namespace, pod, container) * 1000
   ```
2. **Container Memory Working Set**:
   ```promql
   sum(container_memory_working_set_bytes{container!="",container!="POD"}) by (namespace, pod, container)
   ```
3. **Container Memory RSS**:
   ```promql
   sum(container_memory_rss{container!="",container!="POD"}) by (namespace, pod, container)
   ```
4. **Pod Network Receive (Rx) Rate**:
   ```promql
   sum(rate(container_network_receive_bytes_total[2m])) by (namespace, pod)
   ```
5. **Pod Network Transmit (Tx) Rate**:
   ```promql
   sum(rate(container_network_transmit_bytes_total[2m])) by (namespace, pod)
   ```
6. **Application Request QPS (Primary)**:
   ```promql
   sum(rate(http_requests_total[2m])) by (namespace, pod)
   ```
   * *Fallback*: If no HTTP metrics exist, it queries network packet rates (`container_network_receive_packets_total` + `transmit_packets_total`).
7. **Node Physical CPU Usage**:
   ```promql
   sum(rate(node_cpu_seconds_total{mode!="idle"}[2m])) by (instance, node) * 1000
   ```
8. **Node Physical Memory Usage**:
   ```promql
   node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes
   ```

---

### 1.3 Correlation, Idle Tracking & Cache Synchronization (`pkg/metrics/collector.go`)

On each tick of `cfg.Collector.ScrapeInterval` (default `10s`), `MetricsCollector.collectTelemetry()` executes:

1. **Pod & Container Aggregation**:
   * Telemetry is correlated using composite keys: `namespace/pod/container` for containers and `namespace/pod` for pods.
   * If a container has not yet been scraped by Prometheus, the collector falls back to the pod's declared resource requests.
   * Computes pod total CPU, working set memory, RSS memory, and combined network bandwidth ($Rx + Tx$).

2. **Multi-Signal Idle Duration Tracking**:
   A workload is considered inactive if and only if **all three** dynamic signals stay below thresholds:
   $$\text{Inactive} \iff \text{CPU} \le 20\text{m} \ \land \ \text{Network} \le 10\text{ KB/s} \ \land \ \text{QPS} \le 0.1$$
   * If inactive, `IdleDuration = now.Sub(pod.LastActiveTime)`.
   * When `IdleDuration >= cfg.IdleMinDuration` (default `60s`), the pod is flagged as `IsIdle = true`.
   * If traffic spikes (any signal exceeds its threshold), `LastActiveTime` resets to `now`, `IdleDuration` resets to `0`, and `IsIdle = false`.

3. **Rolling Window Ring Buffer (`pkg/metrics/window.go`)**:
   * Appends each sample into a per-pod rolling window buffer (`MetricWindow`).
   * Computes Exponential Moving Averages (EMA) and 95th Percentile (P95) values to filter momentary transient spikes from true sustained workloads.

4. **Real Physical Node Headroom Calculation (`pkg/metrics/cache.go`)**:
   Traditional `kube-scheduler` makes decisions purely on static *requested* reservations. This collector calculates **actual physical headroom**:
   $$\text{RealFreeCPU} = \max(0, \ \text{AllocatableCPU} - \text{ActualUsageCPU})$$
   $$\text{RealFreeMemory} = \max(0, \ \text{AllocatableMemory} - \text{ActualUsageMemory})$$

5. **Pruning**:
   * Stale pods (e.g., completed jobs or deleted pods) that haven't been updated in 5 minutes are evicted from the cache.

---

## 2. Pod Restoration from Checkpoint upon Receiving a Request

When an idle pod is reclaimed via **`FULL_RECLAIM`**, its state is saved as a CRIU checkpoint archive and recorded in a custom resource (`CheckpointRecord` in `reclaim.io/v1alpha1`). The pod itself is evicted to release its compute quota.

When new demand arrives, the system restores the workload. The codebase supports **three mechanisms** for triggering restoration:
1. **The Scale-from-Zero Demand Activator (HTTP Gateway Proxy)** (`pkg/activator/`): Intercepts user HTTP traffic, buffers incoming requests, resolves dependencies, wakes the pod, and replays the traffic.
2. **Declarative Pending-Demand Loop** (`cmd/scheduler/main.go: restoreForPendingDemand`): Periodically matches unscheduled `Pending` pods requesting restoration with available checkpoint records.
3. **Direct Admin REST API Endpoint** (`POST /api/v1/checkpoints/{ns}/{name}/restore`): Authenticated HTTP webhook endpoint.

Below is the complete end-to-end breakdown of how request-triggered restoration works through the **Demand Activator**.

---

### 2.1 Scale-from-Zero Demand Activator Workflow

```
[Client Request]
       │
       ▼
[Activator Server :8083]
       │
       ├─► 1. Parse Target Service (from X-Target-Service, ?target_service, or Host header)
       │
       ├─► 2. Check Service Readiness (via EndpointSlices / Endpoints)
       │      │
       │      ├─► [Endpoints Ready] (Fast Path) ──► Proxy Directly to Pod ClusterIP
       │      │
       │      └─► [Endpoints == 0] (Dormant / Hibernated)
       │             │
       │             ▼
       ├─► 3. Buffer Incoming HTTP Request (Method, URL, Headers, Body up to 10MB)
       │
       ├─► 4. SingleFlight Group: deduplicate concurrent incoming requests for the same service
       │
       ├─► 5. Dependency Graph Resolution (Build multi-pod DAG from annotations / CheckpointRecords)
       │      │
       │      ▼
       ├─► 6. Execute Restoration Stages (Topological Order)
       │      │
       │      ├─► For each stage:
       │      │   ├─► RestoreEngine.RestorePod() (concurrently reconstitute pods from CheckpointRecords)
       │      │   └─► Poll EndpointSlices until workloads are Ready (250ms interval)
       │      │
       │      └─► Apply Warmup Cooldown (default 10 min: protects pod from immediate re-eviction)
       │
       ├─► 7. Replay Buffered HTTP Request to newly restored pod's ClusterIP / DNS
       │
       ▼
[Return Response to Client]
```

---

### 2.2 Step-by-Step Execution Details

#### Step 1: Interception & Target Parsing (`pkg/activator/activator.go`)
* The Activator runs an HTTP server on port `8083`.
* Incoming requests are inspected for the target service using:
  1. `X-Target-Service` header (e.g., `ecommerce/backend-api:3000` or `backend-api:3000`).
  2. `target_service` URL query parameter.
  3. `Host` header (e.g., `backend-api.ecommerce.svc.cluster.local`).

#### Step 2: Endpoint Readiness Probe (`pkg/activator/readiness.go`)
* Checks whether the target service already has ready backend pods:
  1. Queries Kubernetes `discovery.k8s.io/v1` `EndpointSlices` with label `kubernetes.io/service-name=<target>`.
  2. Fallback: checks `corev1.Endpoints` addresses.
  3. Fallback: checks for running pods matching label `app=<target>` with `PodReady=True`.
* **Fast-Path**: If ready endpoints exist, `proxyDirect()` immediately streams the request to the upstream pod.

#### Step 3: In-Flight Request Buffering (`pkg/activator/buffer.go`)
* If the service is scaled to zero (no ready endpoints), `BufferHTTPRequest()` reads the request headers, URI, method, and body (up to 10 MB limit) into memory.
* The original client connection remains held open while restoration proceeds.

#### Step 4: Single-Flight Request Collapsing (`pkg/activator/buffer.go`)
* If 100 clients simultaneously hit a dormant service, waking the service 100 times would spawn 100 duplicate pods.
* `SingleFlightGroup.DoWithTimeout()` groups concurrent calls under the key `namespace/service`.
* The **first request** executes the restoration sequence; subsequent concurrent requests **block and wait** on the same execution channel.

#### Step 5: Multi-Pod Dependency DAG Resolution (`pkg/dependency/dag.go`)
Real microservices rarely wake up in isolation (e.g., `backend-api` may depend on `cache` or `database`).
* The activator reads dependency declarations from annotations:
  * `reclaim.io/depends-on: "auth-service,database"`
  * `reclaim.io/startup-order: "1"`
* It inspects live cluster pods and all `CheckpointRecord` custom resources in the namespace to build a directed graph (`BuildGraph`).
* `ResolveRestorationStages()` runs a topological sort to partition unready workloads into **sequential stages**:
  * Workloads within the same stage can be restored **concurrently**.
  * Stages are executed strictly in order (prerequisites first).

#### Step 6: Pod Reconstitution via `RestoreEngine` (`pkg/action/restore.go`)
For each workload in the stage, `restoreSingleWorkload()` finds the matching `CheckpointRecord` in status `Ready` and calls `RestoreEngine.RestorePod()`:

1. **Phase Update**: Transitions `CheckpointRecord.Status.Phase` from `Ready` to `Restoring`.
2. **Graceful Reclaim vs. CRIU Path**:
   * *Graceful Deployment Path*: If the checkpoint path is `graceful://` (workload was an HTTP service managed by a Deployment), the engine invokes `client.AppsV1().Deployments().UpdateScale()` to scale `spec.replicas` from 0 back up to 1.
   * *CRIU Process Archive Path*: Unmarshals `record.Spec.PodSpecSnapshot` to recover the exact original pod spec, container commands, volume mounts, and original resource requests/limits.
3. **Pod Manifest Re-creation**:
   * Clears `spec.NodeName = ""` so the custom adaptive scheduler can assign the pod to the node with the best actual physical headroom.
   * Sets the restored pod name: `<sourcePodName>-restored-<unixTimestamp>`.
   * Stamps tracking metadata:
     * Labels: `app.kubernetes.io/restored-from: <checkpoint-record-name>`, `reclaim.io/source-pod: <source-pod-name>`.
     * Annotations: `reclaim.io/checkpoint-path`, `reclaim.io/checkpoint-sha256`, `reclaim.io/restored-at`, and `reclaim.io/checkpoint-record-name`.
4. **Kubernetes API Submission**:
   * Calls `client.CoreV1().Pods(namespace).Create()` to spawn the pod.
   * Sets `CheckpointRecord.Status.Phase = Restored` and updates `RestoredPodName` and `RestoredAt`.
5. **Readiness Verification**:
   * `readiness.WaitUntilServiceReady()` polls the service EndpointSlice every 250ms until the new pod's IP is published and passes its readiness probe.

#### Step 7: Warmup Cooldown Registration
* Once restored, `cooldownRecorder.RecordCooldown(namespace, service, 10*time.Minute)` registers a 10-minute cooldown in memory.
* The scheduler's reclamation loop respects this cooldown and ignores this pod even if its initial CPU/network utilization starts near zero during warmup.

#### Step 8: Replay Buffered Request
* `BufferedRequest.ToHTTPRequest()` reconstructs the original HTTP request targeting the service's internal URL:
  `http://<service-name>.<namespace>.svc.cluster.local:<port>`
* The activator forwards the request via its HTTP client and streams the response headers and body back to the waiting client.
* To the outside client, the request succeeded seamlessly (with only an initial latency delay corresponding to pod startup).

---

### 2.3 Self-Healing & Safety: The Restore Watchdog (`pkg/action/restore_watchdog.go`)

If a restored pod is submitted to Kubernetes, but all cluster nodes are temporarily starved of compute capacity, the restored pod would remain stuck in `Pending` (`Unschedulable`).

To prevent permanent deadlock:
1. `RestoreWatchdog` runs in the background, polling every 30 seconds.
2. It monitors pods carrying the `reclaim.io/checkpoint-record-name` annotation.
3. If a restored pod remains `Pending` and unschedulable beyond `UnschedulablePendingTimeout` (default: **2 minutes**):
   * **Deletes the stuck pod** to release scheduling queues and reservations.
   * **Rolls back the `CheckpointRecord` status from `Restoring` back to `Ready`**.
   * Logs a warning so the restoration can be cleanly re-attempted once physical capacity becomes available.

---

### 2.4 Summary Comparison of Restoration Paths

| Feature | Scale-from-Zero Demand Activator | Declarative Pending Demand | REST Webhook Endpoint |
| :--- | :--- | :--- | :--- |
| **Trigger Source** | Inbound HTTP traffic to port `:8083` | A `Pending` pod created in K8s | `POST /api/v1/checkpoints/{ns}/{id}/restore` |
| **Request Buffering** | Yes (in-memory, up to 10 MB) | N/A (declarative pod) | No (synchronous REST response) |
| **Concurrency Control** | `SingleFlightGroup` deduplication | Kubernetes Informer sync | Caller-managed |
| **Dependency Resolution** | Multi-pod DAG topological stages | Annotations (`reclaim.io/depends-on`) | Direct single-pod restore |
| **Readiness Verification** | Polls `EndpointSlices` until live | Handled by Kubernetes scheduler | Returns newly created Pod spec |
| **Cooldown Protection** | Yes (10-minute warmup cooldown) | Yes (reclaim cooldown map) | Manual |
| **Failure Recovery** | `RestoreWatchdog` rollback (2m) | `RestoreWatchdog` rollback (2m) | `RestoreWatchdog` rollback (2m) |
