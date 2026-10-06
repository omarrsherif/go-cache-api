# Formatting check, vet, and the full test suite under the race detector.
$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

$unformatted = gofmt -l .
if ($unformatted) { Write-Error "gofmt: these files need formatting:`n$unformatted"; exit 1 }

go vet ./...
if ($LASTEXITCODE -ne 0) { exit 1 }

go test -race ./...
if ($LASTEXITCODE -ne 0) { exit 1 }

# The concurrency-sensitive packages get extra repetitions.
go test -race -count=5 ./internal/service/ ./internal/workerpool/
if ($LASTEXITCODE -ne 0) { exit 1 }
