# 4-Day Industrial Comparative Benchmark Suite
## Adaptive Kubernetes Scheduler vs. Default Kubernetes Scheduler

This benchmark testbed evaluates and proves the memory efficiency and physical headroom reclamation of the **Adaptive Kubernetes Scheduler** against the **Default Kubernetes Scheduler** on an **8 GB RAM system**.

---

## 1. Quickstart (How to Run)

### Step 1: Deploy Workloads to Kind
Builds the 5 microservice images, loads them into your Kind cluster, and applies both namespaces (`test-application-default` and `test-application-adaptive`):

```powershell
./benchmark-workloads/deploy_testbed_workloads.ps1
```

### Step 2: Start the Benchmark Suite (Dashboard + Simulator)
Launch the master orchestrator in **Time-Warp Mode** (for quick 30-minute validation) or **Real-Time Mode** (for sustained 72–96 hour longevity runs):

```powershell
# Option A: Fast 30-minute Time-Warp Demo (48x acceleration, 24-hour cycle compressed)
./benchmark-workloads/run_benchmark_suite.ps1 -Mode timewarp -TimeWarpFactor 48 -Duration 30m

# Option B: Full 4-Day Undisturbed Production Longevity Benchmark (1x wall-clock)
./benchmark-workloads/run_benchmark_suite.ps1 -Mode realtime -Duration 96h
```

### Step 3: View Live Per-Second Dashboard
Open your browser to:
```
http://localhost:8090
```
* **Graph 1**: Node Physical Memory Available at each second (Cluster Free Headroom).
* **Graph 2**: Working Set RAM (Default [Static ~450MB] vs. Adaptive [Dynamic Reclaimed ~110MB]).
* **Graph 3**: Workload CPU Rate (Millicores).
* **Live KPI Cards**: Instantaneous RAM reclaimed, cumulative $\text{GB}\cdot\text{Hours}$ saved, and container states.

### Step 4: Generate Publication-Quality Report & Plots
After running for 30 minutes, 1 day, or 4 days, run:

```powershell
python ./benchmark-workloads/telemetry/generate_report.py
```
This automatically produces:
1. `SCHEDULER_COMPARATIVE_PERFORMANCE_REPORT.md` (Executive summary, percentiles, mathematical integral).
2. `benchmark_memory_timeline.png` (High-resolution dual-subplot timeline).
3. `benchmark_cdf_memory.png` (Cumulative Distribution Function curve).

---

## 2. Directory Structure

```
benchmark-workloads/
├── src/
│   ├── gateway/                 # Nginx 1.25 Alpine reverse proxy with buffer timeout
│   ├── core-api/                # Go 1.22 REST API (transactions, catalog, Redis/Postgres)
│   └── batch-worker/            # Go 1.22 worker with 100MB compute burst & rapid idle decay
├── k8s/
│   ├── default/                 # Standard K8s scheduler deployment manifests
│   └── adaptive/                # Adaptive scheduler manifests (reclaim.io/* & DAG annotations)
├── traffic-simulator/           # Industrial Stochastic Traffic Simulator
│   ├── main.go                  # Diurnal Poisson process & dual-mirroring HTTP dispatcher
│   ├── Dockerfile
│   └── simulator-job.yaml       # In-cluster Kubernetes Job manifest
├── telemetry/                   # High-Frequency Analysis & Visualization Suite
│   ├── collector.py             # 1-second PromQL poller with SQLite WAL logging
│   ├── dashboard.py             # SSE streaming server on :8090
│   ├── static/index.html        # Modern glassmorphism live dashboard with Chart.js
│   └── generate_report.py       # Post-run statistical cruncher & publication plot generator
├── deploy_testbed_workloads.ps1 # Builds and deploys the dual workloads
└── run_benchmark_suite.ps1      # Master one-click benchmark orchestrator
```
