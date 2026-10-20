# Benchmark Operations & Cluster Commands Cheatsheet

This cheatsheet provides the exact commands for running the empirical benchmark, inspecting pod RAM utilization, and manually triggering container checkpoints.

---

## 1. Starting the Benchmark (`start_benchmark.sh`)

### A. Run in Background (Recommended for Long Runs / 48h)
Runs the 48-hour synthetic diurnal profile decoupled from your terminal:
```bash
nohup ./start_benchmark.sh --profile 48h --duration 48h --mode realtime --skip-build > benchmark_48h.log 2>&1 &
```

### B. Run in Foreground (Interactive / Quick Tests)
Runs a shorter benchmark (e.g., 30 minutes) interactively:
```bash
./start_benchmark.sh --profile 30m --duration 30m --mode realtime --skip-build
```

### C. Useful Flags for `start_benchmark.sh`
- `--profile <name>`: Workload traffic curve (`48h`, `24h`, `12h`, `6h`, `1h`, `30m`).
- `--duration <time>`: Test run duration (e.g., `48h`, `60m`, `300s`).
- `--mode <mode>`: `realtime` (1s = 1s) or `accelerated` (compressed time).
- `--skip-build`: Skips rebuilding container images and uses existing local cache.

### D. Monitoring Benchmark Logs
```bash
# Follow logs in real-time
tail -f benchmark_48h.log

# View last 50 lines
tail -n 50 benchmark_48h.log
```

### E. Stopping the Benchmark Safely
When running in the background, stop it cleanly:
```bash
pkill -f "start_benchmark.sh"
```
*(Stopping the script triggers its cleanup routine, which records the final SQLite evaluation data, generates the performance report, and scales workloads to 0 replicas).*

---

## 2. Checking Current RAM Usage of Pods & Containers

### A. Direct Container Runtime RAM Usage (`crictl stats`)
This queries `containerd` directly inside the Kind worker nodes to show live CPU %, RAM consumption, and disk stats:

**Inspect Worker Node 2 (where `batch-worker` typically runs):**
```bash
docker exec adaptive-cluster-worker2 crictl stats
```

**Inspect Worker Node 1:**
```bash
docker exec adaptive-cluster-worker crictl stats
```

**Inspect All Kind Nodes at Once:**
```bash
for node in adaptive-cluster-worker adaptive-cluster-worker2 adaptive-cluster-control-plane; do
  echo "=== Node: $node ==="
  docker exec $node crictl stats
  echo ""
done
```

### B. Kubernetes Native Resource Metrics (`kubectl top`)
```bash
# Pod RAM and CPU in test-application-adaptive namespace
docker exec adaptive-cluster-control-plane kubectl top pods -n test-application-adaptive

# Pod RAM across all namespaces
docker exec adaptive-cluster-control-plane kubectl top pods -A

# Node-level RAM and CPU usage
docker exec adaptive-cluster-control-plane kubectl top nodes
```

### C. Live Pod RAM & Status via Telemetry Dashboard
Open your browser to:
- **Telemetry & Autonomous Reclamation Dashboard**: `http://localhost:8090`
- Displays per-pod live memory, CPU, composite scores, and cycle decisions updated every 2 seconds.

---

## 3. Checkpointing the `batch-worker` Pod

### A. If Pod is Scaled Down (Replica count is 0)
If the benchmark was stopped and workloads were scaled down, scale `batch-worker` up first:
```bash
docker exec adaptive-cluster-control-plane kubectl scale deployment batch-worker -n test-application-adaptive --replicas=1
```

Verify it is running:
```bash
docker exec adaptive-cluster-control-plane kubectl get pods -n test-application-adaptive -l app=batch-worker -o wide
```

### B. Checkpoint via `crictl` (Direct & Fastest)
Run this command from your host terminal to automatically find the container ID on `adaptive-cluster-worker2` and export the CRIU checkpoint:
```bash
docker exec adaptive-cluster-worker2 bash -c '
  CID=$(crictl ps --name batch-worker -q | head -n 1)
  if [ -z "$CID" ]; then
    echo "batch-worker container not found on adaptive-cluster-worker2"
    exit 1
  fi
  TARGET="/var/lib/kubelet/checkpoints/checkpoint-batch-worker-$(date +%s).tar"
  echo "Exporting checkpoint for container $CID to $TARGET..."
  crictl checkpoint --export "$TARGET" "$CID"
'
```

### C. Checkpoint via Native Kubernetes Kubelet API
Uses the Kubelet API (`:10250/checkpoint/...`) authenticated via apiserver client certificates:
```bash
docker exec adaptive-cluster-control-plane bash -c '
  POD_INFO=$(kubectl get pod -n test-application-adaptive -l app=batch-worker -o jsonpath="{.items[0].metadata.name} {.items[0].spec.nodeName}")
  POD_NAME=$(echo $POD_INFO | awk "{print \$1}")
  NODE_NAME=$(echo $POD_INFO | awk "{print \$2}")

  if [ -z "$POD_NAME" ]; then
    echo "No running batch-worker pod found in test-application-adaptive"
    exit 1
  fi

  echo "Invoking Kubelet Checkpoint API on node $NODE_NAME for pod $POD_NAME..."
  curl -sk -X POST \
    --cert /etc/kubernetes/pki/apiserver-kubelet-client.crt \
    --key /etc/kubernetes/pki/apiserver-kubelet-client.key \
    "https://${NODE_NAME}:10250/checkpoint/test-application-adaptive/${POD_NAME}/batch-worker"
'
```

### D. Verify Generated Checkpoint Tarballs
List all generated CRIU archives stored in the node checkpoints directory:
```bash
docker exec adaptive-cluster-worker2 ls -lh /var/lib/kubelet/checkpoints/
```

---

## 4. Workload Lifecycle Management Quick Reference

| Action | Command |
| :--- | :--- |
| **Start all workloads** | `docker exec adaptive-cluster-control-plane kubectl scale deployment --all -n test-application-adaptive --replicas=1` |
| **Stop all workloads** | `docker exec adaptive-cluster-control-plane kubectl scale deployment --all -n test-application-adaptive --replicas=0` |
| **Check deployment status** | `docker exec adaptive-cluster-control-plane kubectl get deployments -n test-application-adaptive` |
| **Check pod placement** | `docker exec adaptive-cluster-control-plane kubectl get pods -n test-application-adaptive -o wide` |
| **View Kubelet checkpoints** | `docker exec adaptive-cluster-worker2 ls -lh /var/lib/kubelet/checkpoints/` |
| **Clean old checkpoints** | `docker exec adaptive-cluster-worker2 rm -f /var/lib/kubelet/checkpoints/*.tar` |
