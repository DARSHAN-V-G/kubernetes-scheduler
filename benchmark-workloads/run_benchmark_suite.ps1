<#
.SYNOPSIS
    Master Runner for the 4-Day Kubernetes Scheduler Benchmark Suite.
    Starts port-forwards, launches the real-time visualizer dashboard (:8090),
    starts the stochastic traffic simulator, and opens the live graph in your browser.
#>

[CmdletBinding()]
param(
    [ValidateSet("realtime", "timewarp")]
    [string]$Mode = "timewarp",

    [double]$TimeWarpFactor = 48.0,

    [string]$Duration = "30m",

    [string]$ClusterName = "adaptive-cluster"
)

$ErrorActionPreference = "Continue"

Write-Host "=================================================================" -ForegroundColor Blue
Write-Host " Kubernetes Scheduler Benchmark Suite: Master Orchestrator" -ForegroundColor Cyan
Write-Host " Mode: $Mode (Acceleration: ${TimeWarpFactor}x, Duration: $Duration)" -ForegroundColor Cyan
Write-Host " Target Cluster: $ClusterName" -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Blue

$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path

# 1. Verify kubectl connectivity
$ctx = kubectl config current-context 2>$null
if (-not $ctx) {
    Write-Host "[ERROR] Could not connect to Kubernetes cluster. Ensure Docker and Kind are running." -ForegroundColor Red
    exit 1
}
Write-Host "`n[1/5] Connected to Kubernetes Context: $ctx" -ForegroundColor Green

# 2. Configure Port-Forwards
Write-Host "`n[2/5] Setting up port-forwards for Telemetry and Gateways..." -ForegroundColor Yellow

# Kill any existing dangling port-forwards on these ports
Get-Process -Name kubectl -ErrorAction SilentlyContinue | Where-Object { $_.CommandLine -match "port-forward" } | Stop-Process -Force -ErrorAction SilentlyContinue

Write-Host "  -> Port-forwarding Prometheus (:9090)..." -ForegroundColor Gray
Start-Process -FilePath "kubectl" -ArgumentList "port-forward -n adaptive-scheduler svc/adaptive-scheduler 9090:9090" -WindowStyle Hidden

Write-Host "  -> Port-forwarding Demand Activator (:8085)..." -ForegroundColor Gray
Start-Process -FilePath "kubectl" -ArgumentList "port-forward -n adaptive-scheduler svc/adaptive-scheduler 8085:8083" -WindowStyle Hidden

Write-Host "  -> Port-forwarding Default Gateway (:8080)..." -ForegroundColor Gray
Start-Process -FilePath "kubectl" -ArgumentList "port-forward -n test-application-default svc/gateway 8080:80" -WindowStyle Hidden

Write-Host "  -> Port-forwarding Adaptive Gateway (:8081)..." -ForegroundColor Gray
Start-Process -FilePath "kubectl" -ArgumentList "port-forward -n test-application-adaptive svc/gateway 8081:80" -WindowStyle Hidden

Start-Sleep -Seconds 3

# 3. Launch Real-Time Dashboard Server on :8090
Write-Host "`n[3/5] Starting Real-Time Telemetry Dashboard Server on http://localhost:8090..." -ForegroundColor Yellow
$telemetryDir = "$scriptRoot/telemetry"
$dashProc = Start-Process -FilePath "python" -ArgumentList "$telemetryDir/dashboard.py" -WorkingDirectory $telemetryDir -PassThru -WindowStyle Minimized

Write-Host "  [OK] Dashboard server running at: http://localhost:8090" -ForegroundColor Green
Start-Process "http://localhost:8090"

# 4. Launch Stochastic Traffic Simulator
Write-Host "`n[4/5] Starting Industrial Stochastic Traffic Simulator..." -ForegroundColor Yellow
Write-Host "  Mode: $Mode | Diurnal Poisson Flow | Dual Mirrored Dispatch" -ForegroundColor Gray

$simDir = "$scriptRoot/traffic-simulator"
$simDuration = if ($Mode -eq "timewarp") { $Duration } else { "96h" }

$simArgs = "-target-default=http://127.0.0.1:8080 -target-adaptive=http://127.0.0.1:8081 -mode=$Mode -timewarp-factor=$TimeWarpFactor -duration=$simDuration"

Write-Host "`n[5/5] Running Traffic Simulator (Press Ctrl+C to stop)..." -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Blue

& go run "$simDir/main.go" -target-default="http://127.0.0.1:8080" -target-adaptive="http://127.0.0.1:8081" -mode="$Mode" -timewarp-factor=$TimeWarpFactor -duration="$simDuration"

Write-Host "`nBenchmark session completed or interrupted." -ForegroundColor Yellow
Write-Host "To generate the formal performance report and graphs, run:" -ForegroundColor Cyan
Write-Host "  python $telemetryDir/generate_report.py" -ForegroundColor White
