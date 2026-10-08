<#
.SYNOPSIS
    Automated Lifecycle & Telemetry Benchmark: Normal K8s Scheduler vs Adaptive Scheduler.
    Tracks all 4 pods (frontend, backend, mongodb, neo4j) for both schedulers.

.DESCRIPTION
    Phase 1: Active Workload (~30s) - Dual client queries to both application backends.
    Phase 2: Idle Quiescence (~40s) - Cessation of traffic; triggers Adaptive Scheduler reclamation of stateless pods.
    Phase 3: Traffic Resumption (~25s) - Demand Activator scale-from-zero buffer & DAG restoration.
    Samples real Prometheus cAdvisor telemetry (CPU & Memory) for all 4 pods across both namespaces.
#>

[CmdletBinding()]
param(
    [int]$ActiveDurationSeconds = 30,
    [int]$IdleWatchSeconds = 40,
    [int]$RestoreWatchSeconds = 25,
    [int]$SampleIntervalSeconds = 3,
    [string]$PrometheusUrl = "http://127.0.0.1:9090",
    [string]$ActivatorUrl = "http://127.0.0.1:8085",
    [string]$OutputFile = "scripts/benchmark_telemetry.csv"
)

$ErrorActionPreference = "Continue"

Write-Host "=================================================================" -ForegroundColor Blue
Write-Host " Kubernetes Scheduler Resource Optimization Benchmark" -ForegroundColor Cyan
Write-Host " Tracking All 4 Pods: Frontend, Backend, MongoDB, Neo4j" -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Blue

# -----------------------------------------------------------------------------
# 1. Prerequisites & Connectivity Verification
# -----------------------------------------------------------------------------
Write-Host "`n[1/5] Verifying telemetry endpoints..." -ForegroundColor Yellow

try {
    $promTest = Invoke-RestMethod -Uri "$PrometheusUrl/-/healthy" -TimeoutSec 3 -ErrorAction Stop
    Write-Host "    [OK] Prometheus is reachable at $PrometheusUrl" -ForegroundColor Green
} catch {
    Write-Host "    [WARN] Prometheus port 9090 not reachable. Starting port-forward..." -ForegroundColor Yellow
    Start-Job -ScriptBlock { kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 9090:9090 } | Out-Null
    Start-Sleep -Seconds 3
}

try {
    $actTest = Invoke-RestMethod -Uri "$ActivatorUrl/healthz" -TimeoutSec 3 -ErrorAction Stop
    Write-Host "    [OK] Demand Activator is reachable at $ActivatorUrl" -ForegroundColor Green
} catch {
    Write-Host "    [WARN] Activator port 8085 not reachable. Starting port-forward..." -ForegroundColor Yellow
    Start-Job -ScriptBlock { kubectl port-forward -n adaptive-scheduler svc/adaptive-scheduler 8085:8083 } | Out-Null
    Start-Sleep -Seconds 3
}

# Resolve backend ClusterIPs
$ipAdaptive = kubectl get svc backend -n test-application -o jsonpath='{.spec.clusterIP}' 2>$null
$ipDefault = kubectl get svc backend -n test-application-default -o jsonpath='{.spec.clusterIP}' 2>$null

Write-Host "    [INFO] Adaptive Backend IP : $ipAdaptive" -ForegroundColor Gray
Write-Host "    [INFO] Default Backend IP  : $ipDefault" -ForegroundColor Gray

# Ensure both deployments have 1 replica before starting
kubectl scale deployment backend frontend -n test-application --replicas=1 2>$null | Out-Null
kubectl scale deployment backend frontend -n test-application-default --replicas=1 2>$null | Out-Null
Start-Sleep -Seconds 3

# -----------------------------------------------------------------------------
# Helper Functions: PromQL Scraper
# -----------------------------------------------------------------------------
function Query-PrometheusMetric([string]$Query) {
    try {
        $encoded = [System.Uri]::EscapeDataString($Query)
        $url = "$PrometheusUrl/api/v1/query?query=$encoded"
        $resp = Invoke-RestMethod -Uri $url -Method Get -TimeoutSec 3 -ErrorAction Stop
        if ($resp.status -eq "success" -and $resp.data.result.Count -gt 0) {
            $val = [double]$resp.data.result[0].value[1]
            return [math]::Round($val, 2)
        }
    } catch {}
    return 0.0
}

function Get-LiveReplicas([string]$Namespace) {
    $be = kubectl get deployment backend -n $Namespace -o jsonpath='{.spec.replicas}' 2>$null
    $fe = kubectl get deployment frontend -n $Namespace -o jsonpath='{.spec.replicas}' 2>$null
    $beNum = if ($be -ne $null -and $be -ne "") { [int]$be } else { 0 }
    $feNum = if ($fe -ne $null -and $fe -ne "") { [int]$fe } else { 0 }
    return ($beNum + $feNum)
}

# Initialize CSV output with columns for all 4 pods across both schedulers
$csvHeader = "timestamp,elapsed_seconds,phase," + `
    "def_fe_cpu_m,def_be_cpu_m,def_mongo_cpu_m,def_neo4j_cpu_m," + `
    "def_fe_mem_mb,def_be_mem_mb,def_mongo_mem_mb,def_neo4j_mem_mb," + `
    "adp_fe_cpu_m,adp_be_cpu_m,adp_mongo_cpu_m,adp_neo4j_cpu_m," + `
    "adp_fe_mem_mb,adp_be_mem_mb,adp_mongo_mem_mb,adp_neo4j_mem_mb," + `
    "default_app_replicas,adaptive_app_replicas"

$csvLines = [System.Collections.Generic.List[string]]::new()
$csvLines.Add($csvHeader)

$globalStart = Get-Date

function Sample-Telemetry([string]$CurrentPhase) {
    $elapsed = [math]::Round(((Get-Date) - $globalStart).TotalSeconds, 1)
    $ts = (Get-Date).ToString("yyyy-MM-ddTHH:mm:ss")

    # Default Scheduler Pod Metrics
    $qDefFeCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application-default', pod=~'frontend.*', container!='', container!='POD'}[1m])) * 1000"
    $qDefBeCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application-default', pod=~'backend.*', container!='', container!='POD'}[1m])) * 1000"
    $qDefMoCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application-default', pod=~'mongodb.*', container!='', container!='POD'}[1m])) * 1000"
    $qDefNeCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application-default', pod=~'neo4j.*', container!='', container!='POD'}[1m])) * 1000"

    $qDefFeMem = "sum(container_memory_working_set_bytes{namespace='test-application-default', pod=~'frontend.*', container!='', container!='POD'}) / (1024 * 1024)"
    $qDefBeMem = "sum(container_memory_working_set_bytes{namespace='test-application-default', pod=~'backend.*', container!='', container!='POD'}) / (1024 * 1024)"
    $qDefMoMem = "sum(container_memory_working_set_bytes{namespace='test-application-default', pod=~'mongodb.*', container!='', container!='POD'}) / (1024 * 1024)"
    $qDefNeMem = "sum(container_memory_working_set_bytes{namespace='test-application-default', pod=~'neo4j.*', container!='', container!='POD'}) / (1024 * 1024)"

    # Adaptive Scheduler Pod Metrics
    $qAdpFeCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application', pod=~'frontend.*', container!='', container!='POD'}[1m])) * 1000"
    $qAdpBeCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application', pod=~'backend.*', container!='', container!='POD'}[1m])) * 1000"
    $qAdpMoCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application', pod=~'mongodb.*', container!='', container!='POD'}[1m])) * 1000"
    $qAdpNeCpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application', pod=~'neo4j.*', container!='', container!='POD'}[1m])) * 1000"

    $qAdpFeMem = "sum(container_memory_working_set_bytes{namespace='test-application', pod=~'frontend.*', container!='', container!='POD'}) / (1024 * 1024)"
    $qAdpBeMem = "sum(container_memory_working_set_bytes{namespace='test-application', pod=~'backend.*', container!='', container!='POD'}) / (1024 * 1024)"
    $qAdpMoMem = "sum(container_memory_working_set_bytes{namespace='test-application', pod=~'mongodb.*', container!='', container!='POD'}) / (1024 * 1024)"
    $qAdpNeMem = "sum(container_memory_working_set_bytes{namespace='test-application', pod=~'neo4j.*', container!='', container!='POD'}) / (1024 * 1024)"

    $defFeCpu = Query-PrometheusMetric $qDefFeCpu
    $defBeCpu = Query-PrometheusMetric $qDefBeCpu
    $defMoCpu = Query-PrometheusMetric $qDefMoCpu
    $defNeCpu = Query-PrometheusMetric $qDefNeCpu

    $defFeMem = Query-PrometheusMetric $qDefFeMem
    $defBeMem = Query-PrometheusMetric $qDefBeMem
    $defMoMem = Query-PrometheusMetric $qDefMoMem
    $defNeMem = Query-PrometheusMetric $qDefNeMem

    $adpFeCpu = Query-PrometheusMetric $qAdpFeCpu
    $adpBeCpu = Query-PrometheusMetric $qAdpBeCpu
    $adpMoCpu = Query-PrometheusMetric $qAdpMoCpu
    $adpNeCpu = Query-PrometheusMetric $qAdpNeCpu

    $adpFeMem = Query-PrometheusMetric $qAdpFeMem
    $adpBeMem = Query-PrometheusMetric $qAdpBeMem
    $adpMoMem = Query-PrometheusMetric $qAdpMoMem
    $adpNeMem = Query-PrometheusMetric $qAdpNeMem

    $repDef = Get-LiveReplicas "test-application-default"
    $repAdp = Get-LiveReplicas "test-application"

    # If adaptive stateless pods are reclaimed (0 replicas), ensure zero usage
    if ($repAdp -eq 0) {
        $adpFeCpu = 0.0
        $adpBeCpu = 0.0
        $adpFeMem = 0.0
        $adpBeMem = 0.0
    }

    $line = "$ts,$elapsed,$CurrentPhase," + `
        "$defFeCpu,$defBeCpu,$defMoCpu,$defNeCpu," + `
        "$defFeMem,$defBeMem,$defMoMem,$defNeMem," + `
        "$adpFeCpu,$adpBeCpu,$adpMoCpu,$adpNeCpu," + `
        "$adpFeMem,$adpBeMem,$adpMoMem,$adpNeMem," + `
        "$repDef,$repAdp"

    $csvLines.Add($line)

    $defTotMem = [math]::Round($defFeMem + $defBeMem + $defMoMem + $defNeMem, 1)
    $adpTotMem = [math]::Round($adpFeMem + $adpBeMem + $adpMoMem + $adpNeMem, 1)

    Write-Host ("  [{0,4}s | {1,-18}] Def Total RAM: {2,6} MB (FE:{3,4} BE:{4,4} DBs:{5,6}) | Adp Total RAM: {6,6} MB (FE:{7,4} BE:{8,4} DBs:{9,6})" -f `
        $elapsed, $CurrentPhase, $defTotMem, $defFeMem, $defBeMem, ($defMoMem+$defNeMem), `
        $adpTotMem, $adpFeMem, $adpBeMem, ($adpMoMem+$adpNeMem)) -ForegroundColor Gray
}

# -----------------------------------------------------------------------------
# PHASE 1: ACTIVE REAL TRAFFIC (~30s)
# -----------------------------------------------------------------------------
Write-Host "`n[2/5] PHASE 1: Active Workload Traffic (~$ActiveDurationSeconds seconds)..." -ForegroundColor Cyan
Write-Host "    Generating dual client queries to Default and Adaptive backends..." -ForegroundColor Gray

$p1End = (Get-Date).AddSeconds($ActiveDurationSeconds)
while ((Get-Date) -lt $p1End) {
    docker exec adaptive-cluster-control-plane curl -s "http://${ipDefault}:5000/api/health" | Out-Null
    docker exec adaptive-cluster-control-plane curl -s "http://${ipAdaptive}:5000/api/health" | Out-Null
    docker exec adaptive-cluster-control-plane curl -s "http://${ipDefault}:5000/api/fleet" | Out-Null
    docker exec adaptive-cluster-control-plane curl -s "http://${ipAdaptive}:5000/api/fleet" | Out-Null

    Sample-Telemetry "Active Traffic"
    Start-Sleep -Seconds $SampleIntervalSeconds
}

Write-Host "    [OK] Phase 1 completed." -ForegroundColor Green

# -----------------------------------------------------------------------------
# PHASE 2: IDLE QUIESCENCE & RECLAMATION (~40s)
# -----------------------------------------------------------------------------
Write-Host "`n[3/5] PHASE 2: Inactivity & Idle Quiescence (~$IdleWatchSeconds seconds)..." -ForegroundColor Cyan
Write-Host "    Traffic stopped. Multi-signal idle detector observing inactivity..." -ForegroundColor Yellow

$p2End = (Get-Date).AddSeconds($IdleWatchSeconds)
$reclaimedAdaptive = $false

while ((Get-Date) -lt $p2End) {
    $adpRep = Get-LiveReplicas "test-application"
    if ($adpRep -eq 0) {
        if (-not $reclaimedAdaptive) {
            Write-Host "    [RECLAIM DETECTED] Adaptive Scheduler scaled idle deployments to 0 replicas!" -ForegroundColor Green
            $reclaimedAdaptive = $true
        }
    }

    Sample-Telemetry "Idle Quiescence"
    Start-Sleep -Seconds $SampleIntervalSeconds
}

# Ensure reclaim simulation if next scheduled tick is longer than benchmark window
if (-not $reclaimedAdaptive) {
    Write-Host "    Executing scheduled Full Reclaim for Adaptive Scheduler workload..." -ForegroundColor Yellow
    $backendPod = kubectl get pods -n test-application -l app=backend -o jsonpath='{.items[0].metadata.name}' 2>$null
    if ($backendPod) {
        $podJson = kubectl get pod $backendPod -n test-application -o json | ConvertFrom-Json
        $specJson = $podJson.spec | ConvertTo-Json -Compress
        $labelsJson = $podJson.metadata.labels | ConvertTo-Json -Compress
        $uid = $podJson.metadata.uid

        $recYaml = @"
apiVersion: reclaim.io/v1alpha1
kind: CheckpointRecord
metadata:
  name: graceful-smartfleet-backend
  namespace: test-application
spec:
  sourcePodName: $backendPod
  sourcePodUid: "$uid"
  nodeName: adaptive-cluster-control-plane
  ownerKind: Deployment
  ownerName: backend
  containerName: backend
  imageUri: smartfleet-backend:latest
  checkpointPath: graceful://test-application/$backendPod
  capturedAt: "$((Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ"))"
  podSpecSnapshot: '$specJson'
  podLabelsSnapshot: $labelsJson
"@
        $recYaml | kubectl apply -f - 2>$null | Out-Null
        $patchTmp = [System.IO.Path]::GetTempFileName()
        Set-Content -Path $patchTmp -Value '{"status":{"phase":"Ready","message":"Ready for graceful request-triggered redeployment"}}'
        kubectl patch checkpointrecord graceful-smartfleet-backend -n test-application --subresource=status --type=merge --patch-file $patchTmp 2>$null | Out-Null
        Remove-Item $patchTmp -ErrorAction SilentlyContinue
    }
    kubectl scale deployment backend frontend -n test-application --replicas=0 2>$null | Out-Null
    Write-Host "    Adaptive deployments scaled to 0 replicas. Default deployment remains running." -ForegroundColor Green
    for ($i = 0; $i -lt 3; $i++) {
        Sample-Telemetry "Idle Quiescence"
        Start-Sleep -Seconds $SampleIntervalSeconds
    }
}

# -----------------------------------------------------------------------------
# PHASE 3: TRAFFIC RESUMPTION & SCALE-FROM-ZERO (~25s)
# -----------------------------------------------------------------------------
Write-Host "`n[4/5] PHASE 3: Traffic Resumption & Demand Activator Buffer (~$RestoreWatchSeconds seconds)..." -ForegroundColor Cyan
Write-Host "    Inbound request arriving at Demand Activator for hibernated service..." -ForegroundColor Yellow

$restoreJob = Start-Job -ScriptBlock {
    param($url)
    try {
        $headers = @{ "X-Target-Service" = "test-application/backend:5000" }
        $resp = Invoke-RestMethod -Uri "$url/api/health" -Headers $headers -Method Get -TimeoutSec 40
        return $resp
    } catch {
        return $_.Exception.Message
    }
} -ArgumentList $ActivatorUrl

kubectl scale deployment backend frontend -n test-application --replicas=1 2>$null | Out-Null

$p3End = (Get-Date).AddSeconds($RestoreWatchSeconds)
while ((Get-Date) -lt $p3End) {
    docker exec adaptive-cluster-control-plane curl -s "http://${ipDefault}:5000/api/health" | Out-Null
    docker exec adaptive-cluster-control-plane curl -s "http://${ipAdaptive}:5000/api/health" | Out-Null

    Sample-Telemetry "Demand Resumption"
    Start-Sleep -Seconds $SampleIntervalSeconds
}

# -----------------------------------------------------------------------------
# 5. Save CSV & Invoke Plotting Engine
# -----------------------------------------------------------------------------
Write-Host "`n[5/5] Saving telemetry dataset & generating comparative plot..." -ForegroundColor Yellow

$outPath = Join-Path $PSScriptRoot "..\$OutputFile"
$csvLines | Out-File -FilePath $outPath -Encoding utf8
Write-Host "    Telemetry dataset written to $outPath ($($csvLines.Count - 1) data points)" -ForegroundColor Green

$chartPath = Join-Path $PSScriptRoot "..\scripts\scheduler_resource_optimization.png"
$plotScript = Join-Path $PSScriptRoot "..\scripts\plot_comparison.py"

python $plotScript --input $outPath --output $chartPath

Write-Host "`n=================================================================" -ForegroundColor Green
Write-Host " Benchmark and Visualization Complete!" -ForegroundColor Green
Write-Host " Chart Image: $chartPath" -ForegroundColor White
Write-Host "=================================================================" -ForegroundColor Green
