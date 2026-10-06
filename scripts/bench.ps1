# Runs the benchmark suite and writes docs\benchmarks\latest.md.
#
#   scripts\bench.ps1                  full run in Docker (about 5 minutes)
#   scripts\bench.ps1 -Quick           half-length scenarios
#   scripts\bench.ps1 -Scenarios A,B   only some scenarios (A-F)
#   scripts\bench.ps1 -Native          API and load generator on the host, MySQL/Redis in Docker
#   scripts\bench.ps1 -Port 8090       host port for the API if 8080 is taken
#
# Default mode runs everything as containers on one Compose network, so no
# request crosses the Docker Desktop host proxy. -Native runs bin\server.exe
# and bin\loadtest.exe on Windows instead, which adds a proxy hop to every
# MySQL and Redis call.
param(
    [switch]$Quick,
    [switch]$Native,
    [int]$Products = 1000,
    [string]$Scenarios = "all",
    [int]$Port = 8080,
    [int]$Concurrency = 50,
    [switch]$BatchCached,
    [string]$Out = "docs/benchmarks"
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$common = @("-products", "$Products", "-scenarios", $Scenarios, "-concurrency", "$Concurrency")
if ($Quick) { $common += "-quick" }
if ($BatchCached) { $common += "-batch-cached" }

if (-not $Native) {
    $env:API_PORT = "$Port"
    New-Item -ItemType Directory -Force $Out | Out-Null
    docker compose -f docker-compose.yml -f docker-compose.bench.yml run --rm --build loadtest @common
    $code = $LASTEXITCODE
    docker compose -f docker-compose.yml -f docker-compose.bench.yml stop api | Out-Null
    if ($code -ne 0) { exit $code }
    Write-Host "`nreport: docs\benchmarks\latest.md"
    exit 0
}

& "$PSScriptRoot\up.ps1"
if ($LASTEXITCODE -ne 0) { exit 1 }

go build -o bin\server.exe .\cmd\server
if ($LASTEXITCODE -ne 0) { exit 1 }
go build -o bin\loadtest.exe .\cmd\loadtest
if ($LASTEXITCODE -ne 0) { exit 1 }

# Benchmark-friendly server settings: long TTL so entries do not expire
# mid-run, a larger DB pool for the batch matrix, no per-request logging.
$env:SERVER_PORT = "$Port"
$env:CACHE_TTL = "10m"
$env:LOG_LEVEL = "warn"
$env:DB_MAX_OPEN_CONNS = "64"
$env:DB_MAX_IDLE_CONNS = "64"

$server = Start-Process -FilePath "$root\bin\server.exe" -PassThru -NoNewWindow `
    -RedirectStandardOutput "$root\bin\server.log" -RedirectStandardError "$root\bin\server.err.log"
try {
    $base = "http://127.0.0.1:$Port"
    $deadline = (Get-Date).AddSeconds(60)
    $ready = $false
    while (-not $ready) {
        try {
            $r = Invoke-WebRequest -UseBasicParsing -Uri "$base/readyz" -TimeoutSec 2
            if ($r.StatusCode -eq 200) { $ready = $true }
        } catch {
            if ($server.HasExited) { Write-Error "server exited early; see bin\server.err.log"; exit 1 }
            if ((Get-Date) -gt $deadline) { Write-Error "server not ready on $base"; exit 1 }
            Start-Sleep -Milliseconds 500
        }
    }
    Write-Host "server ready on $base"

    $args = @("-url", $base, "-out", $Out, "-topology", "load generator and API on the host; MySQL and Redis in Docker Desktop") + $common
    & "$root\bin\loadtest.exe" @args
    $code = $LASTEXITCODE
} finally {
    if (-not $server.HasExited) { Stop-Process -Id $server.Id -Force }
}
if ($code -ne 0) { exit $code }
Write-Host "`nreport: $Out\latest.md"
