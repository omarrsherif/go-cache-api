# Seeds products into a running API. Usage: scripts\seed.ps1 [-Products 1000] [-Url http://127.0.0.1:8080]
param(
    [int]$Products = 1000,
    [string]$Url = "http://127.0.0.1:8080"
)
$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)
go run ./cmd/loadtest -seed-only -products $Products -url $Url
exit $LASTEXITCODE
