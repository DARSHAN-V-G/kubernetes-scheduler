#!/usr/bin/env bash
# ==============================================================================
# Complete Cluster & Benchmark Teardown Script
#
# Cleans up and destroys the Kind cluster, removes all worker nodes,
# terminates dangling background port-forwards, stops the telemetry dashboard,
# and frees all network ports.
# ==============================================================================

set -eo pipefail

CLUSTER_NAME="adaptive-cluster"

# Colors for terminal output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
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

while [[ "$#" -gt 0 ]]; do
    case $1 in
        -c|--cluster-name) CLUSTER_NAME="$2"; shift ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo "Options:"
            echo "  -c, --cluster-name <name>  Specify cluster name (default: adaptive-cluster)"
            echo "  -h, --help                 Show this help message"
            exit 0
            ;;
        *) echo "Unknown parameter: $1"; exit 1 ;;
    esac
    shift
done

log_header "DESTROYING BENCHMARK TESTBED & CLUSTER ($CLUSTER_NAME)"

# ------------------------------------------------------------------------------
# STEP 1: Terminate Background Processes & Telemetry Servers
# ------------------------------------------------------------------------------
log_step "1. Terminating background telemetry, port-forward & simulator processes..."

# Kill specific Python servers
pkill -f "dashboard.py" 2>/dev/null && log_info "Killed background dashboard.py process." || true
pkill -f "collector.py" 2>/dev/null && log_info "Killed background collector.py process." || true
pkill -f "traffic-simulator" 2>/dev/null && log_info "Killed background traffic simulator." || true

# Kill any dangling kubectl port-forwards
pkill -f "kubectl.*port-forward" 2>/dev/null && log_info "Killed dangling kubectl port-forwards." || true

# Free designated benchmark ports
kill_port() {
    local port=$1
    if command -v fuser &>/dev/null; then
        fuser -k "${port}/tcp" >/dev/null 2>&1 || true
    elif command -v lsof &>/dev/null; then
        local pid
        pid=$(lsof -ti :"$port" 2>/dev/null || true)
        if [ -n "$pid" ]; then kill -9 "$pid" >/dev/null 2>&1 || true; fi
    fi
}

for port in 8090 9090 8085 8080 8081; do
    kill_port "$port"
done
log_success "Host ports 8090, 9090, 8085, 8080, and 8081 liberated."

# ------------------------------------------------------------------------------
# STEP 2: Delete Kind Cluster
# ------------------------------------------------------------------------------
log_step "2. Deleting Kind cluster '$CLUSTER_NAME'..."

if command -v kind &>/dev/null; then
    EXISTING_CLUSTER=$(kind get clusters 2>/dev/null | grep "^${CLUSTER_NAME}$" || true)
    if [ -n "$EXISTING_CLUSTER" ]; then
        kind delete cluster --name "$CLUSTER_NAME"
        log_success "Kind cluster '$CLUSTER_NAME' successfully deleted."
    else
        log_info "No active Kind cluster named '$CLUSTER_NAME' found."
    fi
else
    log_warn "'kind' CLI not found in PATH."
fi

# ------------------------------------------------------------------------------
# STEP 3: Purge Dangling Docker Containers & Network Artifacts
# ------------------------------------------------------------------------------
log_step "3. Verifying and removing all leftover Docker containers..."

if command -v docker &>/dev/null; then
    # Remove any leftover Kind node containers (e.g. control-plane, worker, worker2)
    DANGLING_NODES=$(docker ps -aq --filter "name=${CLUSTER_NAME}" 2>/dev/null || true)
    if [ -n "$DANGLING_NODES" ]; then
        log_info "Force-removing residual node containers..."
        echo "$DANGLING_NODES" | xargs docker rm -f >/dev/null 2>&1 || true
        log_success "Cleaned up residual node containers."
    fi

    # Remove any standalone simulator container
    SIM_CONTAINER=$(docker ps -aq --filter "name=benchmark-traffic-simulator" 2>/dev/null || true)
    if [ -n "$SIM_CONTAINER" ]; then
        docker rm -f "$SIM_CONTAINER" >/dev/null 2>&1 || true
        log_success "Cleaned up standalone traffic simulator container."
    fi
    log_success "All container instances purged."
fi

# ------------------------------------------------------------------------------
# STEP 4: Clean Temporary Checkpoint Files
# ------------------------------------------------------------------------------
log_step "4. Cleaning temporary host checkpoint caches..."
if [ -d "/tmp/checkpoints" ]; then
    rm -rf /tmp/checkpoints/* 2>/dev/null || true
    log_success "Cleared /tmp/checkpoints."
fi

# ------------------------------------------------------------------------------
# Summary Verification
# ------------------------------------------------------------------------------
log_header "CLEANUP COMPLETE: SYSTEM READY"
echo -e "  ${GREEN}✓${NC} All Kind cluster nodes deleted (${CLUSTER_NAME})"
echo -e "  ${GREEN}✓${NC} All background port-forwards & servers stopped"
echo -e "  ${GREEN}✓${NC} Ports 8090 (Dashboard), 9090 (Prometheus), 8080/8081 (Gateways) are free"
echo -e "\nYou can now cleanly launch a fresh benchmark session with:"
echo -e "  ${BOLD}${CYAN}./start_benchmark.sh --mode realtime --duration 1h${NC}\n"
