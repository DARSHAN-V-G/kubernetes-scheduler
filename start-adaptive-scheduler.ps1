<#
.SYNOPSIS
    Automated setup script for Adaptive Scheduler cluster, CRIU, deployments, port-forwards, and simulator backend.

.DESCRIPTION
    1. Checks/creates the Kind cluster with ContainerCheckpoint feature-gates.
    2. Builds and loads the latest scheduler image into Kind.
    3. Creates namespace 'adaptive-scheduler' (and 'test-application' if needed).
    4. Installs CRIU inside the control-plane container and verifies it.
    5. Applies CRDs, RBAC, Prometheus ConfigMap, and Scheduler Deployment.
    6. Waits for scheduler pod readiness.
    7. Sets up port-forwarding in background jobs for Prometheus (9090), Activator (8085->8083), and Scheduler HTTP API (8081).
    8. Launches the simulator backend server on http://localhost:8082.
#>

[CmdletBinding()]
param(
    [string]$ClusterName = "adaptive-cluster",
    [string]$ImageName = "adaptive-scheduler:latest",
    [string]$KindConfigFile = "kind-config.yaml",
    [string]$TargetNamespace = "test-application",
    [switch]$SkipBuildImage
)

$ErrorActionPreference = "Stop"
$RootPath = $PSScriptRoot

Write-Host "=================================================================" -ForegroundColor Cyan
Write-Host "  Adaptive Kubernetes Scheduler Setup & Launch Script" -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Cyan

# -----------------------------------------------------------------------------
# Step 1: Check or Create Kind Cluster
# -----------------------------------------------------------------------------
Write-Host "`n[1/6] Checking Kind cluster '$ClusterName'..." -ForegroundColor Yellow
$existingClusters = @()
try {
    $existingClusters = (& kind get clusters 2>&1) | Where-Object { $_ -is [string] -and $_ -notmatch "No kind clusters found" }
} catch {
    $existingClusters = @()
}
if ($existingClusters -contains $ClusterName) {
    Write-Host "Cluster '$ClusterName' already exists. Switching context..." -ForegroundColor Green
    kubectl config use-context "kind-$ClusterName"
} else {
    Write-Host "Cluster '$ClusterName' not found. Creating from '$KindConfigFile'..." -ForegroundColor Cyan
    $configFullPath = Join-Path $RootPath $KindConfigFile
    if (-not (Test-Path $configFullPath)) {
        throw "Kind config file not found at $configFullPath"
    }
    kind create cluster --name $ClusterName --config $configFullPath
    kubectl config use-context "kind-$ClusterName"
    Write-Host "Cluster '$ClusterName' created successfully." -ForegroundColor Green
}

# -----------------------------------------------------------------------------
# Step 2: Build and Load Scheduler Docker Image
# -----------------------------------------------------------------------------
if (-not $SkipBuildImage) {
    Write-Host "`n[2/6] Building and loading image '$ImageName' into Kind..." -ForegroundColor Yellow
    $dockerfileDir = Join-Path $RootPath "adaptive-k8s-scheduler"
    if (Test-Path $dockerfileDir) {
        Write-Host "Building Docker image '$ImageName' from '$dockerfileDir'..." -ForegroundColor Gray
        docker build -t $ImageName -f (Join-Path $dockerfileDir "Dockerfile") $dockerfileDir
        if ($LASTEXITCODE -ne 0) { throw "Docker build failed" }
    } else {
        Write-Host "Warning: directory '$dockerfileDir' not found, skipping build..." -ForegroundColor Yellow
    }

    Write-Host "Loading image '$ImageName' into cluster '$ClusterName'..." -ForegroundColor Gray
    kind load docker-image $ImageName --name $ClusterName
    if ($LASTEXITCODE -ne 0) { throw "kind load docker-image failed" }
    Write-Host "Image loaded successfully into Kind." -ForegroundColor Green
} else {
    Write-Host "`n[2/6] Skipping image build as requested." -ForegroundColor Gray
}

# -----------------------------------------------------------------------------
# Step 3: Create Namespaces
# -----------------------------------------------------------------------------
Write-Host "`n[3/6] Setting up namespaces..." -ForegroundColor Yellow
$namespaces = @("adaptive-scheduler", $TargetNamespace)
foreach ($ns in $namespaces) {
    $exists = kubectl get namespace $ns --ignore-not-found -o name
    if (-not $exists) {
        kubectl create namespace $ns
        Write-Host "Created namespace '$ns'." -ForegroundColor Green
    } else {
        Write-Host "Namespace '$ns' already exists." -ForegroundColor Gray
    }
}

# -----------------------------------------------------------------------------
# Step 4: Install CRIU inside Kind Node & Apply Manifests
# -----------------------------------------------------------------------------
Write-Host "`n[4/6] Installing CRIU inside Kind control-plane and applying deployments..." -ForegroundColor Yellow
$controlPlaneNode = "$ClusterName-control-plane"
Write-Host "Checking CRIU on node '$controlPlaneNode'..." -ForegroundColor Gray
$hasCriu = $false
try {
    $out = (& docker exec $controlPlaneNode which criu 2>&1)
    if ($LASTEXITCODE -eq 0 -and $out -like "*criu*") {
        $hasCriu = $true
    }
} catch {
    $hasCriu = $false
}

if (-not $hasCriu) {
    Write-Host "Installing CRIU in container '$controlPlaneNode'..." -ForegroundColor Cyan
    docker exec $controlPlaneNode apt-get update -y
    docker exec $controlPlaneNode apt-get install -y criu
    docker exec $controlPlaneNode criu check
    Write-Host "CRIU installed and verified." -ForegroundColor Green
} else {
    $ver = (& docker exec $controlPlaneNode criu --version 2>&1) | Select-Object -First 1
    Write-Host "CRIU is already installed ($ver)." -ForegroundColor Green
}

$deploymentsDir = Join-Path $RootPath "adaptive-k8s-scheduler\deployments"
Write-Host "Applying CRD definitions..." -ForegroundColor Gray
kubectl apply -f (Join-Path $deploymentsDir "crds\reclaim.io_reclaimpolicies.yaml")
kubectl apply -f (Join-Path $deploymentsDir "crds\reclaim.io_checkpointrecords.yaml")
Write-Host "Waiting for CRDs to be established..." -ForegroundColor Gray
kubectl wait --for=condition=established --timeout=30s crd/reclaimpolicies.reclaim.io crd/checkpointrecords.reclaim.io 2>$null

Write-Host "Applying RBAC..." -ForegroundColor Gray
kubectl apply -f (Join-Path $deploymentsDir "rbac.yaml")

Write-Host "Applying Prometheus ConfigMap..." -ForegroundColor Gray
kubectl apply -f (Join-Path $deploymentsDir "prometheus-configmap.yaml")

Write-Host "Applying Scheduler & Prometheus Deployment..." -ForegroundColor Gray
kubectl apply -f (Join-Path $deploymentsDir "scheduler-deployment.yaml")

Write-Host "Waiting for adaptive-scheduler deployment to become available..." -ForegroundColor Gray
kubectl rollout status deployment/adaptive-scheduler -n adaptive-scheduler --timeout=90s

# -----------------------------------------------------------------------------
# Step 5: Port Forwarding for Simulator & Telemetry
# -----------------------------------------------------------------------------
Write-Host "`n[5/6] Ensuring port-forwards are running..." -ForegroundColor Yellow

# Helper to check if a local port is listening
function Test-PortListening([int]$Port) {
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    return [bool]$conn
}

# 1. Prometheus (9090)
if (-not (Test-PortListening 9090)) {
    Write-Host "Starting background port-forward for Prometheus (9090:9090)..." -ForegroundColor Cyan
    Start-Job -Name "pf-prometheus" -ScriptBlock {
        kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 9090:9090
    } | Out-Null
} else {
    Write-Host "Port 9090 is already active." -ForegroundColor Gray
}

# 2. Activator (8085 -> 8083)
if (-not (Test-PortListening 8085)) {
    Write-Host "Starting background port-forward for Activator (8085:8083)..." -ForegroundColor Cyan
    Start-Job -Name "pf-activator" -ScriptBlock {
        kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 8085:8083
    } | Out-Null
} else {
    Write-Host "Port 8085 is already active." -ForegroundColor Gray
}

# 3. Scheduler HTTP API (8081)
if (-not (Test-PortListening 8081)) {
    Write-Host "Starting background port-forward for Scheduler API (8081:8081)..." -ForegroundColor Cyan
    Start-Job -Name "pf-scheduler-api" -ScriptBlock {
        kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 8081:8081
    } | Out-Null
} else {
    Write-Host "Port 8081 is already active." -ForegroundColor Gray
}

Start-Sleep -Seconds 2

# -----------------------------------------------------------------------------
# Step 6: Launch Simulator Backend Server
# -----------------------------------------------------------------------------
Write-Host "`n[6/6] Launching Simulator Dashboard Backend..." -ForegroundColor Yellow
$backendDir = Join-Path $RootPath "simulator\backend"
$backendExe = Join-Path $backendDir "backend.exe"

# Kill existing simulator backend instance if running
$oldProcs = Get-Process -Name "backend" -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "*simulator*" }
if ($oldProcs) {
    foreach ($proc in $oldProcs) {
        Write-Host "Stopping existing simulator backend process (PID $($proc.Id))..." -ForegroundColor Gray
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
    }
    Start-Sleep -Seconds 1
}

# Compile simulator backend to ensure latest scheduler logic is included
Write-Host "Compiling simulator backend..." -ForegroundColor Cyan
Push-Location (Join-Path $RootPath "simulator")
try {
    go build -o backend/backend.exe ./backend
} finally {
    Pop-Location
}

Write-Host "Starting simulator backend (TargetNamespace='$TargetNamespace')..." -ForegroundColor Green
$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $backendExe
$psi.WorkingDirectory = $backendDir
$psi.EnvironmentVariables["TARGET_NAMESPACE"] = $TargetNamespace
$psi.EnvironmentVariables["PROMETHEUS_URL"] = "http://127.0.0.1:9090"
$psi.UseShellExecute = $false
$psi.CreateNoWindow = $false

[System.Diagnostics.Process]::Start($psi) | Out-Null

Write-Host "`n=================================================================" -ForegroundColor Green
Write-Host "  Setup Complete! All components are active:" -ForegroundColor Green
Write-Host "  - Kind Cluster:        $ClusterName" -ForegroundColor White
Write-Host "  - Scheduler Namespace: adaptive-scheduler" -ForegroundColor White
Write-Host "  - Target Namespace:    $TargetNamespace" -ForegroundColor White
Write-Host "  - Prometheus URL:      http://localhost:9090" -ForegroundColor White
Write-Host "  - Activator URL:       http://localhost:8085" -ForegroundColor White
Write-Host "  - Scheduler API:       http://localhost:8081" -ForegroundColor White
Write-Host "  - Simulator Dashboard: http://localhost:8082" -ForegroundColor Yellow
Write-Host "=================================================================" -ForegroundColor Green
