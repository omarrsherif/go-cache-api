# Starts MySQL and Redis with Docker Compose and waits until both are healthy.
$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

docker compose up -d mysql redis
if ($LASTEXITCODE -ne 0) { exit 1 }

foreach ($svc in @("mysql", "redis")) {
    $id = (docker compose ps -q $svc).Trim()
    $deadline = (Get-Date).AddSeconds(120)
    do {
        $status = (docker inspect --format "{{.State.Health.Status}}" $id).Trim()
        if ($status -eq "healthy") { break }
        if ((Get-Date) -gt $deadline) { Write-Error "$svc did not become healthy in time (status: $status)"; exit 1 }
        Start-Sleep -Seconds 2
    } while ($true)
    Write-Host "$svc is healthy"
}
