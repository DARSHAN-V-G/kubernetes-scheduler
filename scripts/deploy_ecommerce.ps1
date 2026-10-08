# ==============================================================================
# Deploy Cloud E-Commerce Platform on Kind with Adaptive Scheduler & Activator
# ==============================================================================

param(
    [string]$ClusterName = "adaptive-cluster",
    [string]$Namespace = "ecommerce",
    [int]$NginxPort = 8080,
    [int]$SimulatorPort = 8082,
    [int]$ActivatorPort = 8085
)

$ErrorActionPreference = "Continue"

function Write-Step {
    param([string]$Msg)
    Write-Host "`n==> $Msg" -ForegroundColor Cyan
}

function Write-Success {
    param([string]$Msg)
    Write-Host "    [OK] $Msg" -ForegroundColor Green
}

function Write-Info {
    param([string]$Msg)
    Write-Host "    [INFO] $Msg" -ForegroundColor Gray
}

function Write-Warn {
    param([string]$Msg)
    Write-Host "    [WARN] $Msg" -ForegroundColor Yellow
}

function Write-Failure {
    param([string]$Msg)
    Write-Host "    [ERROR] $Msg" -ForegroundColor Red
}

$rootDir = Split-Path -Parent $PSScriptRoot

Write-Host "====================================================================" -ForegroundColor Blue
Write-Host " Cloud E-Commerce & Adaptive Scheduler Deployment Orchestrator" -ForegroundColor Cyan
Write-Host "====================================================================" -ForegroundColor Blue

# 1. Check Prerequisites
Write-Step "STEP 1: Verifying Local Tools"
foreach ($tool in @("docker", "kubectl", "kind", "go")) {
    if (Get-Command $tool -ErrorAction SilentlyContinue) {
        Write-Success "Found $tool"
    } else {
        Write-Failure "Required tool '$tool' not found in PATH."
        exit 1
    }
}

# 2. Kind Cluster Provisioning
Write-Step "STEP 2: Ensuring Kind Cluster '$ClusterName' is Active"
$existingClusters = kind get clusters 2>$null
if ($existingClusters -contains $ClusterName) {
    Write-Info "Kind cluster '$ClusterName' already exists."
    # Ensure current context
    kubectl config use-context "kind-$ClusterName" | Out-Null
} else {
    Write-Info "Creating Kind cluster '$ClusterName' with ContainerCheckpoint feature gate..."
    $configPath = Join-Path $rootDir "kind-config.yaml"
    if (Test-Path $configPath) {
        kind create cluster --name $ClusterName --config $configPath
    } else {
        kind create cluster --name $ClusterName
    }
    Write-Success "Kind cluster '$ClusterName' created."
}

Write-Info "Waiting for node to be Ready..."
kubectl wait --for=condition=Ready node --all --timeout=120s | Out-Null
$nodeName = kubectl get nodes -o jsonpath='{.items[0].metadata.name}'
Write-Success "Node '$nodeName' is Ready."

# Verify CRIU inside the node
Write-Info "Verifying CRIU in node '$nodeName'..."
docker exec $nodeName criu check 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Info "Installing CRIU in node container..."
    docker exec $nodeName apt-get update -qq
    docker exec $nodeName apt-get install -y -qq criu
}
docker exec $nodeName mkdir -p /var/lib/kubelet/checkpoints
docker exec $nodeName chmod 777 /var/lib/kubelet/checkpoints
Write-Success "CRIU is verified and checkpoints volume mounted."

# 3. Build Docker Images
Write-Step "STEP 3: Building & Loading Docker Images"

# Ensure adaptive-scheduler image exists
$images = docker images --format "{{.Repository}}:{{.Tag}}"
if ($images -notcontains "adaptive-scheduler:latest") {
    Write-Info "Building adaptive-scheduler:latest..."
    docker build -t adaptive-scheduler:latest (Join-Path $rootDir "adaptive-k8s-scheduler")
}
Write-Success "adaptive-scheduler:latest ready."

# Cloud E-Commerce images
$services = @(
    @{ Name = "backend-api"; Dir = "cloud-ecommerce/backend-api"; Tag = "backend-api:latest" },
    @{ Name = "frontend"; Dir = "cloud-ecommerce/frontend"; Tag = "frontend:latest" },
    @{ Name = "nginx-custom"; Dir = "cloud-ecommerce/nginx"; Tag = "nginx-custom:latest" },
    @{ Name = "worker"; Dir = "cloud-ecommerce/worker"; Tag = "worker:latest" },
    @{ Name = "analytics-service"; Dir = "cloud-ecommerce/analytics-service"; Tag = "analytics-service:latest" }
)

foreach ($svc in $services) {
    $svcPath = Join-Path $rootDir $svc.Dir
    Write-Info "Building $($svc.Tag) from $($svc.Dir)..."
    docker build -t $svc.Tag $svcPath
    if ($LASTEXITCODE -ne 0) {
        Write-Failure "Failed to build $($svc.Tag)"
        exit 1
    }
    Write-Success "Built $($svc.Tag)"
    
    Write-Info "Loading $($svc.Tag) into Kind cluster '$ClusterName'..."
    kind load docker-image $svc.Tag --name $ClusterName
    Write-Success "Loaded $($svc.Tag)"
}

Write-Info "Loading adaptive-scheduler:latest into Kind..."
kind load docker-image adaptive-scheduler:latest --name $ClusterName
Write-Success "All images successfully loaded into cluster."

# 4. Apply CRDs, RBAC, Namespace
Write-Step "STEP 4: Applying CRDs, RBAC, and Namespace Configurations"
kubectl create namespace $Namespace --dry-run=client -o yaml | kubectl apply -f - | Out-Null
kubectl apply -f (Join-Path $rootDir "adaptive-k8s-scheduler/deployments/crds/reclaim.io_checkpointrecords.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "adaptive-k8s-scheduler/deployments/crds/reclaim.io_reclaimpolicies.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "adaptive-k8s-scheduler/deployments/rbac.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "adaptive-k8s-scheduler/deployments/prometheus-configmap.yaml") | Out-Null
Write-Success "CRDs, RBAC, and Cluster configurations applied."

# 5. Deploy Adaptive Scheduler & Prometheus in kube-system
Write-Step "STEP 5: Deploying Adaptive Scheduler & Demand Activator in kube-system"
kubectl apply -f (Join-Path $rootDir "adaptive-k8s-scheduler/deployments/scheduler-deployment.yaml") | Out-Null
Write-Info "Waiting for adaptive-scheduler deployment to become available..."
kubectl rollout status deployment/adaptive-scheduler -n kube-system --timeout=90s | Out-Null
Write-Success "Adaptive Scheduler, Metrics Scraper & Demand Activator are active."

# 6. Deploy E-Commerce Secrets, ConfigMaps, and Persistence Tier
Write-Step "STEP 6: Deploying Data Persistence & Cache Tier (PostgreSQL & Redis)"
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/secrets/ecommerce-secrets.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/configmaps/postgres-init-configmap.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/statefulsets/postgres.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/deployments/redis.yaml") | Out-Null

Write-Info "Waiting for PostgreSQL StatefulSet..."
kubectl rollout status statefulset/postgres -n $Namespace --timeout=120s | Out-Null
Write-Success "PostgreSQL is Ready and database initialized."

Write-Info "Waiting for Redis Deployment..."
kubectl rollout status deployment/redis -n $Namespace --timeout=60s | Out-Null
Write-Success "Redis Cache & Queue is Ready."

# 7. Deploy Application Workloads
Write-Step "STEP 7: Deploying Core Microservices & Ingress Proxy"
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/deployments/backend-api.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/deployments/worker.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/deployments/analytics-service.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/deployments/frontend.yaml") | Out-Null
kubectl apply -f (Join-Path $rootDir "cloud-ecommerce/kubernetes/deployments/nginx.yaml") | Out-Null

Write-Info "Waiting for backend-api..."
kubectl rollout status deployment/backend-api -n $Namespace --timeout=90s | Out-Null
Write-Info "Waiting for worker..."
kubectl rollout status deployment/worker -n $Namespace --timeout=60s | Out-Null
Write-Info "Waiting for analytics-service..."
kubectl rollout status deployment/analytics-service -n $Namespace --timeout=60s | Out-Null
Write-Info "Waiting for frontend..."
kubectl rollout status deployment/frontend -n $Namespace --timeout=60s | Out-Null
Write-Info "Waiting for nginx..."
kubectl rollout status deployment/nginx -n $Namespace --timeout=60s | Out-Null
Write-Success "All Cloud E-Commerce microservices are deployed and healthy!"

# 8. Start Background Port-Forwards & Simulator
Write-Step "STEP 8: Setting Up Port-Forwards & Starting Simulator UI"

# Kill any existing kubectl port-forward processes for these ports
Get-Process -Name kubectl -ErrorAction SilentlyContinue | Where-Object {
    $_.MainWindowTitle -match "port-forward"
} | Stop-Process -Force -ErrorAction SilentlyContinue

# Forward Nginx
$nginxPod = kubectl get pods -n $Namespace -l app=nginx -o jsonpath='{.items[0].metadata.name}'
if ($nginxPod) {
    Write-Info "Forwarding Nginx ($nginxPod) to localhost:$NginxPort..."
    Start-Process kubectl -ArgumentList "port-forward -n $Namespace $nginxPod ${NginxPort}:8080" -WindowStyle Hidden
}

# Forward Activator from scheduler pod in kube-system
$schedPod = kubectl get pods -n kube-system -l app=adaptive-scheduler -o jsonpath='{.items[0].metadata.name}'
if ($schedPod) {
    Write-Info "Forwarding Activator ($schedPod) to localhost:$ActivatorPort..."
    Start-Process kubectl -ArgumentList "port-forward -n kube-system $schedPod ${ActivatorPort}:8083" -WindowStyle Hidden
}

# Start Simulator Backend if not running
$simHealthy = $false
try {
    $h = Invoke-RestMethod -Uri "http://localhost:${SimulatorPort}/api/health" -TimeoutSec 1 -ErrorAction SilentlyContinue
    if ($h.status -eq "healthy") { $simHealthy = $true }
} catch {}

if (-not $simHealthy) {
    Write-Info "Starting Simulator Backend on port $SimulatorPort..."
    $simDir = Join-Path $rootDir "simulator"
    Start-Process go -ArgumentList "run ./backend" -WorkingDirectory $simDir -WindowStyle Hidden
    for ($i = 0; $i -lt 10; $i++) {
        Start-Sleep -Seconds 1
        try {
            $h = Invoke-RestMethod -Uri "http://localhost:${SimulatorPort}/api/health" -TimeoutSec 1 -ErrorAction SilentlyContinue
            if ($h.status -eq "healthy") { $simHealthy = $true; break }
        } catch {}
    }
}

if ($simHealthy) {
    Write-Success "Simulator Backend running at http://localhost:$SimulatorPort"
}

Write-Host "`n====================================================================" -ForegroundColor Blue
Write-Host " CLOUD E-COMMERCE DEPLOYMENT COMPLETE" -ForegroundColor Green
Write-Host "====================================================================" -ForegroundColor Blue
Write-Host "  Storefront (Nginx):      http://localhost:$NginxPort" -ForegroundColor Green
Write-Host "  API Health Endpoint:     http://localhost:$NginxPort/api/health" -ForegroundColor Green
Write-Host "  Simulator UI:            http://localhost:$SimulatorPort" -ForegroundColor Green
Write-Host "  Activator Port:          http://localhost:$ActivatorPort" -ForegroundColor Green
Write-Host "`nCurrent Pod Status in ${Namespace}:" -ForegroundColor Cyan
kubectl get pods -n $Namespace -o wide
Write-Host ""
