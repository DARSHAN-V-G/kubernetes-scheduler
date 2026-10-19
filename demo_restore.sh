#!/usr/bin/env bash
# ==============================================================================
# Adaptive Kubernetes Scheduler - Workload Restoration Demo
# Demonstrates:
#   1. Stateless Network Services (Demand Activator, Scale-to-Zero, Buffer & Replay)
#   2. Batch Workloads (CRIU Reconstitution from CheckpointRecord via Demand Trigger)
# ==============================================================================

set -eo pipefail

NAMESPACE="ecommerce"
ACTIVATOR_PORT=8085
SCHEDULER_INTERNAL_PORT=8083

# Colors for terminal presentation
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

log_step() {
    echo -e "\n${BLUE}${BOLD}==>${NC} ${BOLD}$1${NC}"
}

log_info() {
    echo -e "    ${CYAN}ℹ${NC} $1"
}

log_success() {
    echo -e "    ${GREEN}✓${NC} $1"
}

log_warn() {
    echo -e "    ${YELLOW}⚠${NC} $1"
}

log_error() {
    echo -e "    ${RED}✖${NC} $1"
}

# ------------------------------------------------------------------------------
# Helper: Ensure Activator Port-Forward is Active
# ------------------------------------------------------------------------------
ensure_activator_forward() {
    log_step "Ensuring Activator Port-Forward (localhost:${ACTIVATOR_PORT} -> scheduler:${SCHEDULER_INTERNAL_PORT})"

    if nc -z 127.0.0.1 "${ACTIVATOR_PORT}" 2>/dev/null; then
        log_success "Port ${ACTIVATOR_PORT} is already open and accepting traffic."
        return 0
    fi

    log_info "Discovering adaptive-scheduler pod..."
    SCHEDULER_POD=$(kubectl get pod -n kube-system -l app=adaptive-scheduler -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
    if [ -z "$SCHEDULER_POD" ]; then
        log_error "adaptive-scheduler pod not found in kube-system!"
        exit 1
    fi

    log_info "Starting background port-forward for $SCHEDULER_POD (${ACTIVATOR_PORT}:${SCHEDULER_INTERNAL_PORT})..."
    kubectl port-forward -n kube-system "$SCHEDULER_POD" "${ACTIVATOR_PORT}:${SCHEDULER_INTERNAL_PORT}" > /tmp/activator_pf.log 2>&1 &
    PF_PID=$!
    sleep 2

    if kill -0 "$PF_PID" 2>/dev/null; then
        log_success "Port-forward established (PID $PF_PID)."
    else
        log_error "Port-forward failed to launch. Check /tmp/activator_pf.log:"
        cat /tmp/activator_pf.log
        exit 1
    fi
}

# ------------------------------------------------------------------------------
# DEMO 1: Stateless Network Service (backend-api)
# ------------------------------------------------------------------------------
demo_stateless() {
    echo -e "\n${BOLD}${CYAN}====================================================================${NC}"
    echo -e "${BOLD}${CYAN} DEMO 1: Stateless Service Restoration (backend-api)${NC}"
    echo -e "${BOLD}${CYAN} (Scale-to-Zero, Request Buffering, Auto-Start, & Replay)${NC}"
    echo -e "${BOLD}${CYAN}====================================================================${NC}"

    ensure_activator_forward

    log_step "1. Confirming backend-api is running and healthy"
    if ! kubectl get deployment backend-api -n "$NAMESPACE" &>/dev/null; then
        log_error "Deployment backend-api not found in $NAMESPACE!"
        exit 1
    fi

    # Ensure at least 1 replica is running to capture initial snapshot
    REPLICAS=$(kubectl get deployment backend-api -n "$NAMESPACE" -o jsonpath='{.spec.replicas}')
    if [ "$REPLICAS" -eq 0 ]; then
        log_info "Scaling backend-api to 1 to establish baseline..."
        kubectl scale deployment backend-api -n "$NAMESPACE" --replicas=1
        kubectl rollout status deployment/backend-api -n "$NAMESPACE" --timeout=60s
    fi

    POD=$(kubectl get pod -n "$NAMESPACE" -l app=backend-api -o jsonpath='{.items[0].metadata.name}')
    log_success "Active backend pod discovered: $POD"

    log_step "2. Creating CheckpointRecord for backend-api"
    SPEC=$(kubectl get pod "$POD" -n "$NAMESPACE" -o json | jq -c '.spec')
    LABELS=$(kubectl get pod "$POD" -n "$NAMESPACE" -o json | jq -c '.metadata.labels')
    POD_UID=$(kubectl get pod "$POD" -n "$NAMESPACE" -o jsonpath='{.metadata.uid}')

    kubectl delete checkpointrecord graceful-backend-api-demo -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1

    kubectl apply -f - <<EOF >/dev/null
apiVersion: reclaim.io/v1alpha1
kind: CheckpointRecord
metadata:
  name: graceful-backend-api-demo
  namespace: $NAMESPACE
spec:
  sourcePodName: $POD
  sourcePodUid: "$POD_UID"
  nodeName: adaptive-cluster-control-plane
  ownerKind: Deployment
  ownerName: backend-api
  containerName: backend-api
  imageUri: backend-api:latest
  checkpointPath: graceful://$NAMESPACE/$POD
  capturedAt: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  podSpecSnapshot: '$SPEC'
  podLabelsSnapshot: $LABELS
EOF

    kubectl patch checkpointrecord graceful-backend-api-demo \
      -n "$NAMESPACE" \
      --subresource=status \
      --type=merge \
      -p '{"status":{"phase":"Ready","message":"Ready for graceful request-triggered redeployment"}}' >/dev/null

    log_success "CheckpointRecord 'graceful-backend-api-demo' is in phase: Ready"

    log_step "3. Simulating Checkpoint & Hibernation: Scaling backend-api to 0 replicas"
    kubectl scale deployment/backend-api -n "$NAMESPACE" --replicas=0
    sleep 3

    log_info "Verifying zero pods and zero endpoints:"
    kubectl get pods -n "$NAMESPACE" -l app=backend-api
    kubectl get endpointslice -n "$NAMESPACE" -l kubernetes.io/service-name=backend-api

    log_step "4. Triggering Auto-Start via HTTP Demand Request to Activator"
    log_info "Executing: curl http://127.0.0.1:${ACTIVATOR_PORT}/api/health -H 'X-Target-Service: ecommerce/backend-api:3000'"
    log_info "Activator will: 1) buffer request -> 2) scale deployment 0->1 -> 3) wait for readiness -> 4) replay request"

    RESPONSE=$(curl -s -i --retry 3 --retry-all-errors --max-time 45 \
      "http://127.0.0.1:${ACTIVATOR_PORT}/api/health" \
      -H 'X-Target-Service: ecommerce/backend-api:3000')

    echo -e "${YELLOW}--- HTTP Response Received ---${NC}"
    echo "$RESPONSE" | head -n 12
    echo -e "${YELLOW}-----------------------------${NC}"

    if echo "$RESPONSE" | grep -q "200 OK"; then
        log_success "HTTP 200 OK received! Request was held, service woke up, and payload was returned!"
    else
        log_warn "Received non-200 response, checking current status..."
    fi

    log_step "5. Verifying Cluster & CheckpointRecord State"
    echo -e "${BOLD}Current backend-api pods:${NC}"
    kubectl get pods -n "$NAMESPACE" -l app=backend-api

    echo -e "\n${BOLD}Current CheckpointRecord Status:${NC}"
    kubectl get checkpointrecord graceful-backend-api-demo -n "$NAMESPACE" \
      -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,RESTORED_POD:.status.restoredPodName,RESTORED_AT:.status.restoredAt

    log_success "Stateless service restoration demo complete!"
}

# ------------------------------------------------------------------------------
# DEMO 2: Batch Workload (test-idle-worker)
# ------------------------------------------------------------------------------
demo_batch() {
    echo -e "\n${BOLD}${CYAN}====================================================================${NC}"
    echo -e "${BOLD}${CYAN} DEMO 2: Batch Workload Restoration (test-idle-worker)${NC}"
    echo -e "${BOLD}${CYAN} (Checkpoint Reconstitution via Demand-Triggered Restore Loop)${NC}"
    echo -e "${BOLD}${CYAN}====================================================================${NC}"

    POD_NAME="test-idle-worker"

    log_step "1. Ensuring base batch workload exists in $NAMESPACE"
    if ! kubectl get pod "$POD_NAME" -n "$NAMESPACE" &>/dev/null; then
        log_info "Deploying sample batch worker '$POD_NAME'..."
        kubectl apply -f - <<EOF >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: $POD_NAME
  namespace: $NAMESPACE
  labels:
    app: $POD_NAME
  annotations:
    reclaim.io/checkpointable: "true"
spec:
  containers:
  - name: worker
    image: alpine:3.20
    command: ["/bin/sh", "-c", "i=0; while true; do echo \\$i; i=\\$((i+1)); sleep 1; done"]
    resources:
      requests:
        cpu: "100m"
        memory: "64Mi"
EOF
        kubectl wait --for=condition=Ready pod/"$POD_NAME" -n "$NAMESPACE" --timeout=60s
    fi
    log_success "Batch pod '$POD_NAME' is Running."

    log_step "2. Creating synthetic CheckpointRecord for $POD_NAME"
    SPEC=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o json | jq -c '.spec')
    LABELS=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o json | jq -c '.metadata.labels')
    POD_UID=$(kubectl get pod "$POD_NAME" -n "$NAMESPACE" -o jsonpath='{.metadata.uid}')
    CKPT_NAME="ckpt-${POD_NAME}-demo"

    kubectl delete checkpointrecord "$CKPT_NAME" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1

    kubectl apply -f - <<EOF >/dev/null
apiVersion: reclaim.io/v1alpha1
kind: CheckpointRecord
metadata:
  name: $CKPT_NAME
  namespace: $NAMESPACE
spec:
  sourcePodName: $POD_NAME
  sourcePodUid: "$POD_UID"
  nodeName: adaptive-cluster-control-plane
  containerName: worker
  imageUri: alpine:3.20
  checkpointPath: /var/lib/kubelet/checkpoints/checkpoint-${POD_NAME}_worker.tar
  capturedAt: "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  podSpecSnapshot: '$SPEC'
  podLabelsSnapshot: $LABELS
EOF

    kubectl patch checkpointrecord "$CKPT_NAME" \
      -n "$NAMESPACE" \
      --subresource=status \
      --type=merge \
      -p '{"status":{"phase":"Ready","message":"Ready for demand-triggered restoration"}}' >/dev/null

    log_success "CheckpointRecord '$CKPT_NAME' created in phase: Ready"

    log_step "3. Simulating Full Reclamation (evicting $POD_NAME)"
    kubectl delete pod "$POD_NAME" -n "$NAMESPACE" --now >/dev/null 2>&1 || true
    sleep 2
    log_success "Original batch pod '$POD_NAME' has been evicted."

    log_step "4. Triggering Demand Restoration for batch workload"
    log_info "Creating pending demand pod annotated with reclaim.io/restore-source-pod: $POD_NAME"
    log_info "Scheduler's demand loop evaluates every 10s and reconstitutes the workload pod."

    kubectl delete pod "demand-trigger-${POD_NAME}" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1

    kubectl apply -f - <<EOF >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: demand-trigger-${POD_NAME}
  namespace: $NAMESPACE
  annotations:
    reclaim.io/restore-source-pod: "$POD_NAME"
spec:
  schedulerName: dummy-waiting-scheduler
  containers:
  - name: trigger
    image: registry.k8s.io/pause:3.9
EOF
    log_info "Demand trigger pod submitted. Waiting for scheduler to reconstitute workload..."

    # Wait up to 30 seconds for restoration
    RESTORED_POD=""
    for i in {1..15}; do
        RESTORED_POD=$(kubectl get pods -n "$NAMESPACE" -l "reclaim.io/source-pod=$POD_NAME" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
        if [ -n "$RESTORED_POD" ]; then
            break
        fi
        sleep 2
    done

    if [ -n "$RESTORED_POD" ]; then
        log_success "Scheduler detected demand and restored workload as: $RESTORED_POD"
        kubectl wait --for=condition=Ready pod/"$RESTORED_POD" -n "$NAMESPACE" --timeout=45s || true
    else
        log_warn "Waiting for pod creation..."
    fi

    # Clean up the dummy trigger pod
    kubectl delete pod "demand-trigger-${POD_NAME}" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1

    log_step "5. Verifying Restored Batch Pod & CheckpointRecord Phase"
    echo -e "${BOLD}Restored Pods matching label reclaim.io/source-pod=${POD_NAME}:${NC}"
    kubectl get pods -n "$NAMESPACE" -l "reclaim.io/source-pod=$POD_NAME" -o wide

    echo -e "\n${BOLD}CheckpointRecord Final Status:${NC}"
    kubectl get checkpointrecord "$CKPT_NAME" -n "$NAMESPACE" \
      -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,RESTORED_POD:.status.restoredPodName,RESTORED_AT:.status.restoredAt

    log_success "Batch workload restoration demo complete!"
}

# ------------------------------------------------------------------------------
# DEMO STATUS: Live Cluster Overview
# ------------------------------------------------------------------------------
show_status() {
    log_step "Live Cluster Workload & Checkpoint Status in $NAMESPACE"

    echo -e "\n${BOLD}--- CheckpointRecords in $NAMESPACE ---${NC}"
    kubectl get checkpointrecord -n "$NAMESPACE" \
      -o custom-columns=NAME:.metadata.name,PHASE:.status.phase,SOURCE_POD:.spec.sourcePodName,RESTORED_AS:.status.restoredPodName,AGE:.metadata.creationTimestamp \
      | tail -n 15

    echo -e "\n${BOLD}--- Live Pods in $NAMESPACE ---${NC}"
    kubectl get pods -n "$NAMESPACE" -o wide

    echo -e "\n${BOLD}--- Endpoints in $NAMESPACE ---${NC}"
    kubectl get endpointslice -n "$NAMESPACE"
}

# ------------------------------------------------------------------------------
# Main Menu / CLI Dispatcher
# ------------------------------------------------------------------------------
case "${1:-}" in
    stateless)
        demo_stateless
        ;;
    batch)
        demo_batch
        ;;
    all)
        demo_stateless
        echo ""
        demo_batch
        ;;
    status)
        show_status
        ;;
    *)
        echo -e "${BOLD}${BLUE}====================================================================${NC}"
        echo -e "${BOLD}${BLUE} Adaptive Kubernetes Scheduler - Workload Restoration Demo${NC}"
        echo -e "${BOLD}${BLUE}====================================================================${NC}"
        echo -e "Usage: $0 [stateless | batch | all | status]"
        echo ""
        echo -e "Select a demo option:"
        echo -e "  ${BOLD}1)${NC} ${CYAN}Stateless Network Service${NC} (backend-api: scale-0 -> auto-start on curl -> 200 OK)"
        echo -e "  ${BOLD}2)${NC} ${CYAN}Batch Workload${NC}           (test-idle-worker: reclaim -> demand trigger -> restored)"
        echo -e "  ${BOLD}3)${NC} ${CYAN}Run Both Demos in Sequence${NC}"
        echo -e "  ${BOLD}4)${NC} ${CYAN}Check Live Status${NC}          (CheckpointRecords, pods, endpoint slices)"
        echo -e "  ${BOLD}q)${NC} Quit"
        echo ""
        read -rp "Enter choice [1-4, q]: " choice
        case "$choice" in
            1) demo_stateless ;;
            2) demo_batch ;;
            3) demo_stateless; echo ""; demo_batch ;;
            4) show_status ;;
            *) echo "Exiting." ;;
        esac
        ;;
esac
