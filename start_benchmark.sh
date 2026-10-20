#!/usr/bin/env bash
# ==============================================================================
# Master Benchmark Orchestrator: Adaptive vs. Default Kubernetes Scheduler
#
# This script deploys and runs the complete benchmark testbed end-to-end:
#   1. Starts & verifies Kind cluster with ContainerCheckpoint & CRIU support
#   2. Builds & deploys the Adaptive Scheduler & co-located Prometheus (:9090)
#   3. Builds & deploys the 5-container test workload to:
#      - 'test-application-default' (standard kube-scheduler, static allocation)
#      - 'test-application-adaptive' (adaptive-scheduler, autonomous reclamation)
#   4. Establishes background port-forwards for Gateways, Activator, and Prometheus
#   5. Launches the Real-Time Telemetry Dashboard server on http://localhost:8090
#   6. Runs the Stochastic Industrial Traffic Simulator (Diurnal Poisson cycle)
#   7. On exit / interrupt: Generates publication-ready report & comparison plots
# ==============================================================================

set -eo pipefail
MAIN_PID="$$"

# ------------------------------------------------------------------------------
# Configuration & CLI Argument Defaults
# ------------------------------------------------------------------------------
CLUSTER_NAME="adaptive-cluster"
MODE="timewarp"          # "timewarp" (fast demo) or "realtime" (3-4 days)
PROFILE="fast"           # "fast" (1h validation: 2m burst / 8m idle) or "48h" (stochastic diurnal)
TIMEWARP_FACTOR=48       # 48x = 24-hour cycle compressed into 30 minutes
DURATION="30m"           # Default duration (30m for timewarp, 96h for realtime)
SKIP_BUILD=false

# Color formatting
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
MAGENTA='\033[0;35m'
BOLD='\033[1m'
NC='\033[0m'

log_header() {
    echo -e "\n${BLUE}${BOLD}====================================================================${NC}"
    echo -e "${BOLD}${CYAN} $1${NC}"
    echo -e "${BLUE}${BOLD}====================================================================${NC}"
}
log_step() { echo -e "\n${BLUE}${BOLD}==>${NC} ${BOLD}$1${NC}"; }
log_info() { echo -e "    ${CYAN}ℹ${NC} $1"; }
log_success() { echo -e "    ${GREEN}✓${NC} $1"; }
log_warn() { echo -e "    ${YELLOW}⚠${NC} $1"; }
log_error() { echo -e "    ${RED}✖${NC} $1"; }

# Parse arguments
while [[ "$#" -gt 0 ]]; do
    case $1 in
        -p|--profile) PROFILE="$2"; shift ;;
        -m|--mode) MODE="$2"; shift ;;
        -d|--duration) DURATION="$2"; shift ;;
        -w|--timewarp-factor) TIMEWARP_FACTOR="$2"; shift ;;
        --skip-build) SKIP_BUILD=true ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo "Options:"
            echo "  -p, --profile <fast|48h>        Workload profile: 'fast' (1h validation) or '48h' (default: fast)"
            echo "  -m, --mode <timewarp|realtime>  Execution mode (default: timewarp)"
            echo "  -d, --duration <time>           Duration (e.g. 1h, 48h, 30m; default: 30m)"
            echo "  -w, --timewarp-factor <int>     Time acceleration factor (default: 48)"
            echo "  --skip-build                    Skip Docker image builds"
            exit 0
            ;;
        *) echo "Unknown parameter: $1"; exit 1 ;;
    esac
    shift
done

if [ "$PROFILE" = "fast" ]; then
    MODE="realtime"
fi

if [ "$PROFILE" = "48h" ] && [ "$DURATION" = "30m" ]; then
    DURATION="48h"
    MODE="realtime"
fi

if [ "$MODE" = "realtime" ] && [ "$DURATION" = "30m" ]; then
    DURATION="96h"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# ------------------------------------------------------------------------------
# Activate Virtual Environment (if compatible)
# ------------------------------------------------------------------------------
VENV_ACTIVATED=false
IS_LINUX=false
if [[ "$(uname -s)" == "Linux"* ]]; then
    IS_LINUX=true
fi

for venv_path in "$SCRIPT_DIR/.venv_linux" "$SCRIPT_DIR/.venv" "$SCRIPT_DIR/venv"; do
    if [ -f "$venv_path/bin/activate" ]; then
        # Native Linux / WSL venv
        # shellcheck disable=SC1090
        source "$venv_path/bin/activate"
        VENV_ACTIVATED=true
        break
    elif [ "$IS_LINUX" = false ] && [ -f "$venv_path/Scripts/activate" ]; then
        # Windows Git Bash / MSYS only (never inside Linux/WSL)
        # shellcheck disable=SC1090
        source <(tr -d '\r' < "$venv_path/Scripts/activate") 2>/dev/null || true
        VENV_ACTIVATED=true
        break
    fi
done

get_python() {
    if [ "$IS_LINUX" = true ]; then
        # In Linux/WSL, always use native Linux python binary
        if [ -n "$VIRTUAL_ENV" ] && [ -x "$VIRTUAL_ENV/bin/python3" ]; then
            echo "$VIRTUAL_ENV/bin/python3"
        elif [ -n "$VIRTUAL_ENV" ] && [ -x "$VIRTUAL_ENV/bin/python" ]; then
            echo "$VIRTUAL_ENV/bin/python"
        elif command -v python3 &>/dev/null; then
            echo "python3"
        elif command -v python &>/dev/null; then
            echo "python"
        else
            echo ""
        fi
    else
        # Windows / Git Bash
        if [ -n "$VIRTUAL_ENV" ] && [ -x "$VIRTUAL_ENV/Scripts/python.exe" ]; then
            echo "$VIRTUAL_ENV/Scripts/python.exe"
        elif command -v python3 &>/dev/null; then
            echo "python3"
        elif command -v python &>/dev/null; then
            echo "python"
        else
            echo ""
        fi
    fi
}

# Background process tracking
declare -a BG_PIDS=()
BENCHMARK_RUN_ID=""

PYTHON_BIN=$(get_python)
if [ -n "$PYTHON_BIN" ]; then
    BENCHMARK_RUN_ID=$("$PYTHON_BIN" -c "
import sys
sys.path.append('$SCRIPT_DIR/benchmark-workloads/telemetry')
try:
    from collector import get_or_create_active_run_id
    print(get_or_create_active_run_id(mode='$MODE', timewarp_factor=$TIMEWARP_FACTOR, duration='$DURATION'))
except Exception:
    print('')
" 2>/dev/null || echo "")
    if [ -n "$BENCHMARK_RUN_ID" ]; then
        export BENCHMARK_RUN_ID
        log_info "Initialized Benchmark Run ID: $BENCHMARK_RUN_ID"
    fi
fi

cleanup() {
    local sig="${1:-EXIT}"

    # Disarm traps immediately
    trap - EXIT INT TERM

    # Ensure child processes or subshells never execute cleanup
    if [ -n "$BASHPID" ] && [ "$BASHPID" -ne "$MAIN_PID" ]; then
        exit 0
    fi

    # Atomic cross-process lock: guarantees cleanup logic runs exactly ONCE
    local lock_dir="${SCRIPT_DIR}/.benchmark_cleanup.lock"
    if ! mkdir "$lock_dir" 2>/dev/null; then
        return 0 2>/dev/null || exit 0
    fi

    if [ "${CLEANUP_EXECUTED:-false}" = true ]; then
        rm -rf "$lock_dir" 2>/dev/null || true
        return 0 2>/dev/null || exit 0
    fi
    CLEANUP_EXECUTED=true

    log_header "CLEANUP, FINAL REPORT & WORKLOAD TEARDOWN"
    log_info "Terminating testbed port-forwards and traffic simulator..."
    for pid in "${BG_PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
        fi
    done

    # Terminate traffic simulator load generators & port-forwards (Dashboard server remains RUNNING)
    pkill -f "traffic-simulator" 2>/dev/null || true
    pkill -f "kubectl.*port-forward" 2>/dev/null || true

    # Generate final performance report for this specific run ID
    DB_FILE="$SCRIPT_DIR/benchmark-workloads/telemetry/benchmark_telemetry.sqlite3"
    if [ -f "$DB_FILE" ]; then
        log_step "Generating post-run empirical performance report (Run ID: ${BENCHMARK_RUN_ID:-latest})..."
        PYTHON_EXEC=$(get_python)
        if [ -n "$PYTHON_EXEC" ] && [ -f "$SCRIPT_DIR/benchmark-workloads/telemetry/generate_report.py" ]; then
            RUN_ARG=""
            if [ -n "$BENCHMARK_RUN_ID" ]; then
                RUN_ARG="--run-id $BENCHMARK_RUN_ID"
            fi
            "$PYTHON_EXEC" "$SCRIPT_DIR/benchmark-workloads/telemetry/generate_report.py" \
                --db "$DB_FILE" \
                --outdir "$SCRIPT_DIR/benchmark-workloads/telemetry" $RUN_ARG || log_warn "Report generation encountered an error."
        fi
    fi

    # Mark run completed in database
    if [ -n "$PYTHON_EXEC" ] && [ -n "$BENCHMARK_RUN_ID" ]; then
        "$PYTHON_EXEC" -c "
import sys
sys.path.append('$SCRIPT_DIR/benchmark-workloads/telemetry')
try:
    from collector import finish_benchmark_run
    finish_benchmark_run($BENCHMARK_RUN_ID)
except Exception:
    pass
" 2>/dev/null || true
    fi

    # Stop workload containers by scaling deployments and statefulsets to 0 (preserves cluster, namespaces & configs)
    log_step "Stopping benchmark workload containers (scaling to 0 replicas)..."
    kubectl scale deployment --all -n test-application-default --replicas=0 2>/dev/null || true
    kubectl scale deployment --all -n test-application-adaptive --replicas=0 2>/dev/null || true
    kubectl scale statefulset --all -n test-application-default --replicas=0 2>/dev/null || true
    kubectl scale statefulset --all -n test-application-adaptive --replicas=0 2>/dev/null || true

    # Stop adaptive-scheduler deployment (as requested)
    log_step "Stopping adaptive-scheduler controller (scaling to 0 replicas)..."
    kubectl scale deployment/adaptive-scheduler -n adaptive-scheduler --replicas=0 2>/dev/null || true

    # Free benchmark workload network ports (Port 8090 remains OPEN for dashboard!)
    for port in 8080 8081 8082 8085 9090; do
        if command -v fuser &>/dev/null; then
            fuser -k "${port}/tcp" >/dev/null 2>&1 || true
        fi
    done

    rm -rf "$lock_dir" 2>/dev/null || true
    log_success "All test workload containers, traffic simulator, and scheduler stopped."
    echo -e "\n${BOLD}${GREEN}====================================================================${NC}"
    echo -e " ${BOLD}${GREEN}✔ Dashboard Server Kept Running on http://localhost:8090${NC}"
    echo -e " ${CYAN}Historical runs, comparative charts, and CRIU logs remain accessible.${NC}"
    echo -e "${BOLD}${GREEN}====================================================================${NC}\n"

    if [ "$sig" = "INT" ] || [ "$sig" = "TERM" ]; then
        exit 0
    fi
}
trap 'cleanup INT' INT
trap 'cleanup TERM' TERM
trap 'cleanup EXIT' EXIT

# ------------------------------------------------------------------------------
# STEP 1: Verify Prerequisites
# ------------------------------------------------------------------------------
log_header "STEP 1: Verifying Host Prerequisites"

check_cmd() {
    if ! command -v "$1" &>/dev/null; then
        log_error "Missing required tool: '$1' in PATH."
        exit 1
    fi
    log_success "Found $1 ($(command -v "$1"))"
}

check_cmd docker
check_cmd kind
check_cmd kubectl

# Go check (optional: if missing, simulator runs via Docker container)
get_go() {
    if command -v go &>/dev/null; then
        echo "go"
    elif [ -x "/usr/local/go/bin/go" ]; then
        echo "/usr/local/go/bin/go"
    elif [ -x "$HOME/go/bin/go" ]; then
        echo "$HOME/go/bin/go"
    elif [ -x "/mnt/c/Program Files/Go/bin/go.exe" ]; then
        echo "/mnt/c/Program Files/Go/bin/go.exe"
    else
        echo ""
    fi
}

GO_BIN=$(get_go)
if [ -n "$GO_BIN" ]; then
    log_success "Found Go ($GO_BIN)"
else
    log_info "Go compiler not found in local PATH. Traffic simulator will run via Docker container."
fi

PYTHON_CMD=$(get_python)
if [ -z "$PYTHON_CMD" ]; then
    log_error "Python 3 is required for the telemetry visualizer. Please install python3."
    exit 1
fi

if [ "$VENV_ACTIVATED" = true ]; then
    log_success "Activated Virtual Environment Python ($PYTHON_CMD)"
else
    log_info "Using System Python ($PYTHON_CMD)"
fi

# Ensure numpy and matplotlib exist for report charts
if ! "$PYTHON_CMD" -c "import numpy" &>/dev/null; then
    log_info "numpy not detected in $PYTHON_CMD. Installing numpy & matplotlib..."
    "$PYTHON_CMD" -m pip install --quiet numpy matplotlib 2>/dev/null || \
    "$PYTHON_CMD" -m pip install --user --quiet numpy matplotlib 2>/dev/null || \
    log_warn "Could not pip install numpy/matplotlib. Pure-Python fallback will be used."
fi

if ! docker info &>/dev/null; then
    log_error "Docker daemon is not running. Please start Docker / Docker Desktop."
    exit 1
fi
log_success "Docker daemon is active."

# ------------------------------------------------------------------------------
# STEP 2: Kind Cluster with 2 Dedicated Worker Nodes
# ------------------------------------------------------------------------------
log_header "STEP 2: Checking/Starting 3-Node Kind Cluster ($CLUSTER_NAME)"

EXISTING_CLUSTER=$(kind get clusters 2>/dev/null | grep "^${CLUSTER_NAME}$" || true)
RECREATE_CLUSTER=false

if [ -n "$EXISTING_CLUSTER" ]; then
    kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null 2>&1 || true
    NODE_COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l || echo "0")
    if [ "$NODE_COUNT" -lt 3 ]; then
        log_warn "Existing cluster '$CLUSTER_NAME' has $NODE_COUNT node(s), but 3 nodes (1 control-plane + 2 workers) are required for 2-line physical isolation."
        log_info "Recreating cluster '$CLUSTER_NAME' with 2 dedicated worker nodes..."
        kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
        RECREATE_CLUSTER=true
    else
        log_info "Cluster '$CLUSTER_NAME' has $NODE_COUNT nodes ready. Switching context..."
    fi
else
    RECREATE_CLUSTER=true
fi

if [ "$RECREATE_CLUSTER" = true ]; then
    log_step "Creating Kind cluster '$CLUSTER_NAME' with 1 Control-Plane & 2 Worker Nodes..."
    kind create cluster --name "$CLUSTER_NAME" --config "$SCRIPT_DIR/kind-config.yaml"
    kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null
    log_success "3-Node cluster '$CLUSTER_NAME' created."
fi

kubectl wait --for=condition=Ready node --all --timeout=120s >/dev/null
CONTROL_PLANE=$(kubectl get nodes -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "$CLUSTER_NAME-control-plane")
DEFAULT_WORKER=$(kubectl get nodes -l benchmark.role=default -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "$CLUSTER_NAME-worker")
ADAPTIVE_WORKER=$(kubectl get nodes -l benchmark.role=adaptive -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "$CLUSTER_NAME-worker2")

log_success "Cluster nodes are Ready:"
log_info "  Control Plane   : $CONTROL_PLANE"
log_info "  Default Worker  : $DEFAULT_WORKER (Dedicated to Default Scheduler)"
log_info "  Adaptive Worker : $ADAPTIVE_WORKER (Dedicated to Adaptive Scheduler)"

# Verify CRIU inside the adaptive worker container
log_step "Verifying CRIU checkpointing on '$ADAPTIVE_WORKER'..."
for node_target in "$ADAPTIVE_WORKER" "$CONTROL_PLANE"; do
    if docker ps --format '{{.Names}}' | grep -q "^${node_target}$"; then
        if ! docker exec "$node_target" which criu &>/dev/null; then
            log_info "Installing CRIU in container '$node_target'..."
            docker exec "$node_target" apt-get update -qq && docker exec "$node_target" apt-get install -y -qq criu >/dev/null 2>&1 || true
        fi
        docker exec "$node_target" mkdir -p /var/lib/kubelet/checkpoints
        docker exec "$node_target" chmod 777 /var/lib/kubelet/checkpoints
    fi
done
log_success "CRIU is configured on '$ADAPTIVE_WORKER'."

# ------------------------------------------------------------------------------
# STEP 3: Deploy Adaptive Scheduler Stack
# ------------------------------------------------------------------------------
log_header "STEP 3: Deploying Adaptive Scheduler & Prometheus"

if [ "$SKIP_BUILD" = false ]; then
    log_step "Building adaptive-scheduler Docker image..."
    docker build -t adaptive-scheduler:latest "$SCRIPT_DIR/adaptive-k8s-scheduler"
    log_info "Loading adaptive-scheduler:latest into Kind..."
    kind load docker-image adaptive-scheduler:latest --name "$CLUSTER_NAME"
    log_success "Scheduler image loaded into Kind."
fi

kubectl create namespace adaptive-scheduler --dry-run=client -o yaml | kubectl apply -f - >/dev/null

log_step "Applying Scheduler CRDs, RBAC, and Prometheus deployment..."
kubectl apply -f "$SCRIPT_DIR/adaptive-k8s-scheduler/deployments/crds/reclaim.io_reclaimpolicies.yaml" >/dev/null
kubectl apply -f "$SCRIPT_DIR/adaptive-k8s-scheduler/deployments/crds/reclaim.io_checkpointrecords.yaml" >/dev/null
kubectl wait --for=condition=Established --timeout=30s crd/reclaimpolicies.reclaim.io crd/checkpointrecords.reclaim.io >/dev/null

kubectl apply -f "$SCRIPT_DIR/adaptive-k8s-scheduler/deployments/rbac.yaml" >/dev/null
kubectl apply -f "$SCRIPT_DIR/adaptive-k8s-scheduler/deployments/prometheus-configmap.yaml" >/dev/null
kubectl apply -f "$SCRIPT_DIR/adaptive-k8s-scheduler/deployments/scheduler-deployment.yaml" >/dev/null
kubectl scale deployment/adaptive-scheduler -n adaptive-scheduler --replicas=1 >/dev/null 2>&1 || true

log_info "Waiting for adaptive-scheduler deployment readiness..."
kubectl rollout status deployment/adaptive-scheduler -n adaptive-scheduler --timeout=120s
log_success "Adaptive Scheduler & co-located Prometheus are READY."

# ------------------------------------------------------------------------------
# STEP 4: Build & Deploy 5-Container Benchmark Workloads
# ------------------------------------------------------------------------------
log_header "STEP 4: Deploying 5-Container Benchmark Applications"

if [ "$SKIP_BUILD" = false ]; then
    log_step "Building microservices (Gateway, Core API, Batch Worker, Simulator)..."
    docker build -t benchmark-gateway:latest "$SCRIPT_DIR/benchmark-workloads/src/gateway"
    docker build -t benchmark-core-api:latest "$SCRIPT_DIR/benchmark-workloads/src/core-api"
    docker build -t benchmark-batch-worker:latest "$SCRIPT_DIR/benchmark-workloads/src/batch-worker"
    docker build -t benchmark-traffic-simulator:latest "$SCRIPT_DIR/benchmark-workloads/traffic-simulator"

    log_info "Loading custom workload images into Kind cluster '$CLUSTER_NAME'..."
    kind load docker-image benchmark-gateway:latest --name "$CLUSTER_NAME"
    kind load docker-image benchmark-core-api:latest --name "$CLUSTER_NAME"
    kind load docker-image benchmark-batch-worker:latest --name "$CLUSTER_NAME"
    kind load docker-image benchmark-traffic-simulator:latest --name "$CLUSTER_NAME" 2>/dev/null || true

    log_info "Pre-pulling public Redis and Postgres images inside Kind worker nodes..."
    for node in "$DEFAULT_WORKER" "$ADAPTIVE_WORKER" "$CONTROL_PLANE"; do
        docker exec "$node" crictl pull redis:7.2-alpine >/dev/null 2>&1 || true
        docker exec "$node" crictl pull postgres:16-alpine >/dev/null 2>&1 || true
    done
    log_success "All workload images ready."
fi

log_step "Deploying Default Scheduler workload ('test-application-default')..."
kubectl apply -f "$SCRIPT_DIR/benchmark-workloads/k8s/default" >/dev/null
kubectl scale deployment --all -n test-application-default --replicas=1 >/dev/null 2>&1 || true

log_step "Deploying Adaptive Scheduler workload ('test-application-adaptive')..."
kubectl apply -f "$SCRIPT_DIR/benchmark-workloads/k8s/adaptive" >/dev/null
kubectl scale deployment --all -n test-application-adaptive --replicas=1 >/dev/null 2>&1 || true

log_info "Waiting for workload rollouts in both namespaces..."
kubectl rollout status deployment/datastore -n test-application-default --timeout=60s >/dev/null
kubectl rollout status deployment/core-api -n test-application-default --timeout=60s >/dev/null
kubectl rollout status deployment/gateway -n test-application-default --timeout=60s >/dev/null

kubectl rollout status deployment/datastore -n test-application-adaptive --timeout=60s >/dev/null
kubectl rollout status deployment/core-api -n test-application-adaptive --timeout=60s >/dev/null
kubectl rollout status deployment/gateway -n test-application-adaptive --timeout=60s >/dev/null
log_success "All pods deployed and ready in both namespaces."

# ------------------------------------------------------------------------------
# STEP 5: Establish Port-Forwards
# ------------------------------------------------------------------------------
log_header "STEP 5: Configuring Telemetry & Gateway Port-Forwards"

# Kill existing dangling port-forwards (Port 8090 dashboard remains untouched!)
kill_port() {
    local port=$1
    if command -v fuser &>/dev/null; then
        fuser -k "${port}/tcp" 2>/dev/null || true
    elif command -v lsof &>/dev/null; then
        local pid
        pid=$(lsof -ti :"$port" 2>/dev/null || true)
        if [ -n "$pid" ]; then kill -9 $pid 2>/dev/null || true; fi
    fi
}
kill_port 9090
kill_port 8085
kill_port 8080
kill_port 8081
kill_port 8082

log_info "Starting port-forward: Prometheus (:9090)..."
kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 9090:9090 >/dev/null 2>&1 &
BG_PIDS+=($!)

log_info "Starting port-forward: Demand Activator (:8085 -> :8083)..."
kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 8085:8083 >/dev/null 2>&1 &
BG_PIDS+=($!)

log_info "Starting port-forward: Scheduler Evaluation API (:8082 -> :8081)..."
kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 8082:8081 >/dev/null 2>&1 &
BG_PIDS+=($!)

log_info "Starting port-forward: Default Gateway (:8080 -> :80)..."
kubectl port-forward -n test-application-default svc/gateway 8080:80 >/dev/null 2>&1 &
BG_PIDS+=($!)

log_info "Starting port-forward: Adaptive Gateway (:8081 -> :80)..."
kubectl port-forward -n test-application-adaptive svc/gateway 8081:80 >/dev/null 2>&1 &
BG_PIDS+=($!)

sleep 3
log_success "Port-forwards established."

# ------------------------------------------------------------------------------
# STEP 6: Launch Real-Time Telemetry Dashboard (:8090)
# ------------------------------------------------------------------------------
log_header "STEP 6: Launching Real-Time Telemetry Visualizer"

if curl -s "http://localhost:8090/api/runs" &>/dev/null; then
    log_success "Real-Time Telemetry Visualizer is already active at: ${BOLD}${GREEN}http://localhost:8090${NC}"
else
    log_info "Starting standalone telemetry visualizer on http://localhost:8090 (persists after benchmark completes)..."
    (cd "$SCRIPT_DIR/benchmark-workloads/telemetry" && nohup "$PYTHON_CMD" dashboard.py > /tmp/dashboard_server.log 2>&1 &)
    sleep 2
    if curl -s "http://localhost:8090" &>/dev/null; then
        log_success "Real-Time Visualizer active at: ${BOLD}${GREEN}http://localhost:8090${NC}"
    else
        log_warn "Dashboard server initializing, check http://localhost:8090 shortly."
    fi
fi

# ------------------------------------------------------------------------------
# STEP 7: Run Stochastic Traffic Simulator
# ------------------------------------------------------------------------------
log_header "STEP 7: Running Stochastic Traffic Simulator"
log_info "Mode: $MODE (TimeWarp Factor: ${TIMEWARP_FACTOR}x, Duration: $DURATION)"
log_info "Traffic Flow: Diurnal Poisson | Dual-Mirrored Dispatch (Default vs Adaptive)"
log_info "Press Ctrl+C at any time to stop testing and generate the final report."
echo -e "${YELLOW}Dashboard URL: http://localhost:8090${NC}\n"

SIM_BIN="$SCRIPT_DIR/benchmark-workloads/traffic-simulator/traffic-simulator"
if [ ! -x "$SIM_BIN" ] && command -v docker &>/dev/null; then
    log_info "Extracting compiled traffic simulator binary from Docker image..."
    docker create --name temp-sim benchmark-traffic-simulator:latest >/dev/null 2>&1 || true
    docker cp temp-sim:/app/traffic-simulator "$SIM_BIN" >/dev/null 2>&1 || true
    docker rm temp-sim >/dev/null 2>&1 || true
    chmod +x "$SIM_BIN" 2>/dev/null || true
fi

if [ -x "$SIM_BIN" ]; then
    log_info "Running native traffic simulator binary in WSL (direct local port-forward access)..."
    (trap - EXIT INT TERM; cd "$SCRIPT_DIR/benchmark-workloads/traffic-simulator" && \
        exec "$SIM_BIN" \
            -profile="$PROFILE" \
            -target-default="http://127.0.0.1:8080" \
            -target-adaptive="http://127.0.0.1:8081" \
            -mode="$MODE" \
            -timewarp-factor="$TIMEWARP_FACTOR" \
            -duration="$DURATION" \
            -requests-file="$SCRIPT_DIR/benchmark-workloads/telemetry/recent_requests.json")
elif [ -n "$GO_BIN" ]; then
    (trap - EXIT INT TERM; cd "$SCRIPT_DIR/benchmark-workloads/traffic-simulator" && \
        exec "$GO_BIN" run main.go \
            -profile="$PROFILE" \
            -target-default="http://127.0.0.1:8080" \
            -target-adaptive="http://127.0.0.1:8081" \
            -mode="$MODE" \
            -timewarp-factor="$TIMEWARP_FACTOR" \
            -duration="$DURATION" \
            -requests-file="$SCRIPT_DIR/benchmark-workloads/telemetry/recent_requests.json")
else
    log_info "Running traffic simulator via Docker container..."
    docker run --rm -i --network host \
        -v "$SCRIPT_DIR/benchmark-workloads/traffic-simulator:/data" \
        -v "$SCRIPT_DIR/benchmark-workloads/telemetry:/telemetry" \
        benchmark-traffic-simulator:latest \
        -profile="$PROFILE" \
        -target-default="http://127.0.0.1:8080" \
        -target-adaptive="http://127.0.0.1:8081" \
        -mode="$MODE" \
        -timewarp-factor="$TIMEWARP_FACTOR" \
        -duration="$DURATION" \
        -metrics-file="/data/benchmark_traffic_telemetry.csv" \
        -requests-file="/telemetry/recent_requests.json"
fi

log_success "Traffic simulation cycle finished."
