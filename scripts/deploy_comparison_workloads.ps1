<#
.SYNOPSIS
    Deploys SmartFleet under both Default K8s Scheduler and Adaptive Scheduler side-by-side.

.DESCRIPTION
    1. Namespace 'test-application-default': Managed by standard kube-scheduler (no reclamation).
    2. Namespace 'test-application': Managed by adaptive-scheduler (with multi-signal reclamation & activator).
    3. Verifies PVC binding and waits for rollout readiness in both namespaces.
#>

[CmdletBinding()]
param(
    [string]$ClusterName = "adaptive-cluster"
)

$ErrorActionPreference = "Stop"

Write-Host "=================================================================" -ForegroundColor Cyan
Write-Host " Deploying SmartFleet: Default Scheduler vs Adaptive Scheduler" -ForegroundColor Cyan
Write-Host " Cluster: $ClusterName" -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Cyan

# Verify cluster connectivity
$context = kubectl config current-context 2>$null
Write-Host "`n[1/4] Current Kubernetes Context: $context" -ForegroundColor Yellow

$controlPlaneNode = "$ClusterName-control-plane"

# Function to annotate PVCs for Kind local-path provisioner
function Ensure-PVCNodeAnnotation([string]$Namespace) {
    Start-Sleep -Seconds 2
    $pvcs = kubectl get pvc -n $Namespace -o jsonpath='{.items[*].metadata.name}' 2>$null
    if ($pvcs) {
        foreach ($pvc in $pvcs.Split(' ', [System.StringSplitOptions]::RemoveEmptyEntries)) {
            kubectl annotate pvc $pvc -n $Namespace "volume.kubernetes.io/selected-node=$controlPlaneNode" --overwrite 2>$null | Out-Null
        }
    }
}

# -----------------------------------------------------------------------------
# Step 1: Deploy Adaptive Scheduler Workload (test-application)
# -----------------------------------------------------------------------------
Write-Host "`n[2/4] Deploying/Verifying Adaptive Scheduler stack in 'test-application'..." -ForegroundColor Cyan
$adaptiveK8sDir = "..\test-application\Smart_Fleet_management\k8s"
kubectl apply -k $adaptiveK8sDir
Ensure-PVCNodeAnnotation "test-application"

Write-Host "Waiting for Adaptive Scheduler workloads to become ready..." -ForegroundColor Gray
kubectl rollout status statefulset/mongodb -n test-application --timeout=120s
kubectl rollout status statefulset/neo4j -n test-application --timeout=180s
kubectl rollout status deployment/backend -n test-application --timeout=120s
kubectl rollout status deployment/frontend -n test-application --timeout=120s
Write-Host "Namespace 'test-application' is ready!" -ForegroundColor Green

# -----------------------------------------------------------------------------
# Step 2: Deploy Default Scheduler Workload (test-application-default)
# -----------------------------------------------------------------------------
Write-Host "`n[3/4] Deploying Default Scheduler stack in 'test-application-default'..." -ForegroundColor Cyan
$defaultK8sDir = "..\test-application\Smart_Fleet_management\k8s\default"
kubectl apply -k $defaultK8sDir
Ensure-PVCNodeAnnotation "test-application-default"

Write-Host "Waiting for Default Scheduler workloads to become ready..." -ForegroundColor Gray
kubectl rollout status statefulset/mongodb -n test-application-default --timeout=120s
kubectl rollout status statefulset/neo4j -n test-application-default --timeout=180s
kubectl rollout status deployment/backend -n test-application-default --timeout=120s
kubectl rollout status deployment/frontend -n test-application-default --timeout=120s
Write-Host "Namespace 'test-application-default' is ready!" -ForegroundColor Green

# -----------------------------------------------------------------------------
# Step 3: Print Workload Comparison Summary
# -----------------------------------------------------------------------------
Write-Host "`n[4/4] Workloads Deployment Summary:" -ForegroundColor Yellow
Write-Host "`n--- Namespace: test-application-default (Standard K8s Scheduler) ---" -ForegroundColor White
kubectl get pods -n test-application-default -o custom-columns=NAME:.metadata.name,SCHEDULER:.spec.schedulerName,STATUS:.status.phase,IP:.status.podIP

Write-Host "`n--- Namespace: test-application (Adaptive Scheduler) ---" -ForegroundColor White
kubectl get pods -n test-application -o custom-columns=NAME:.metadata.name,SCHEDULER:.spec.schedulerName,STATUS:.status.phase,IP:.status.podIP

Write-Host "`n=================================================================" -ForegroundColor Green
Write-Host " Both workloads deployed successfully side-by-side!" -ForegroundColor Green
Write-Host "=================================================================" -ForegroundColor Green
