<#
.SYNOPSIS
    Builds, loads, and deploys the 5-container benchmark application to both
    'test-application-default' and 'test-application-adaptive' namespaces.
#>

[CmdletBinding()]
param(
    [string]$ClusterName = "adaptive-cluster",
    [switch]$SkipBuild = $false
)

$ErrorActionPreference = "Stop"

Write-Host "=================================================================" -ForegroundColor Cyan
Write-Host " 5-Container Benchmark Testbed Workload Deployment" -ForegroundColor Cyan
Write-Host " Cluster: $ClusterName" -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Cyan

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path

# 1. Build and Load Docker Images
if (-not $SkipBuild) {
    Write-Host "`n[1/3] Building lightweight microservice images..." -ForegroundColor Yellow

    Write-Host "  -> Building benchmark-gateway:latest..." -ForegroundColor Gray
    docker build -t benchmark-gateway:latest "$scriptRoot/src/gateway"

    Write-Host "  -> Building benchmark-core-api:latest..." -ForegroundColor Gray
    docker build -t benchmark-core-api:latest "$scriptRoot/src/core-api"

    Write-Host "  -> Building benchmark-batch-worker:latest..." -ForegroundColor Gray
    docker build -t benchmark-batch-worker:latest "$scriptRoot/src/batch-worker"

    Write-Host "`n[2/3] Loading custom images into Kind cluster '$ClusterName'..." -ForegroundColor Yellow
    kind load docker-image benchmark-gateway:latest --name $ClusterName
    kind load docker-image benchmark-core-api:latest --name $ClusterName
    kind load docker-image benchmark-batch-worker:latest --name $ClusterName

    Write-Host "  -> Pre-pulling redis and postgres inside Kind control plane..." -ForegroundColor Gray
    docker exec "$ClusterName-control-plane" crictl pull redis:7.2-alpine 2>$null | Out-Null
    docker exec "$ClusterName-control-plane" crictl pull postgres:16-alpine 2>$null | Out-Null
    Write-Host "  [OK] All images ready." -ForegroundColor Green
} else {
    Write-Host "`n[1/3] Skipping image build as -SkipBuild was specified." -ForegroundColor Gray
}

# 2. Deploy Default Scheduler Workload (test-application-default)
Write-Host "`n[3/3] Deploying manifests to Kubernetes..." -ForegroundColor Yellow

Write-Host "  -> Deploying test-application-default (Standard kube-scheduler)..." -ForegroundColor Cyan
kubectl apply -f "$scriptRoot/k8s/default"

Write-Host "  -> Deploying test-application-adaptive (Adaptive Scheduler)..." -ForegroundColor Cyan
kubectl apply -f "$scriptRoot/k8s/adaptive"

Write-Host "`nWaiting for rollouts in both namespaces..." -ForegroundColor Gray
kubectl rollout status deployment/datastore -n test-application-default --timeout=60s
kubectl rollout status deployment/cache -n test-application-default --timeout=60s
kubectl rollout status deployment/core-api -n test-application-default --timeout=60s
kubectl rollout status deployment/gateway -n test-application-default --timeout=60s

kubectl rollout status deployment/datastore -n test-application-adaptive --timeout=60s
kubectl rollout status deployment/cache -n test-application-adaptive --timeout=60s
kubectl rollout status deployment/core-api -n test-application-adaptive --timeout=60s
kubectl rollout status deployment/gateway -n test-application-adaptive --timeout=60s

Write-Host "`n=================================================================" -ForegroundColor Green
Write-Host " Both workloads deployed successfully side-by-side!" -ForegroundColor Green
Write-Host "=================================================================" -ForegroundColor Green

Write-Host "`nWorkload Summary:" -ForegroundColor Yellow
Write-Host "`n--- Namespace: test-application-default (Standard Scheduler) ---" -ForegroundColor White
kubectl get pods -n test-application-default -o wide

Write-Host "`n--- Namespace: test-application-adaptive (Adaptive Scheduler) ---" -ForegroundColor White
kubectl get pods -n test-application-adaptive -o wide
