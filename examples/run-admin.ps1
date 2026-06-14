# run-admin.ps1 — launch the scheduler server as Administrator.
#
# Tools like BetterGI must run elevated to simulate input into the game (which
# itself runs elevated); a child process can only inherit elevation, so the
# scheduler has to be started as Administrator. Running without it is the usual
# cause of BetterGI exit code 553.
#
# Usage (from the repo root, after `go build -o bin/server.exe ./cmd/server`):
#   powershell -ExecutionPolicy Bypass -File examples\run-admin.ps1
#   powershell -ExecutionPolicy Bypass -File examples\run-admin.ps1 -Addr 127.0.0.1:8080
#
# A UAC prompt will appear — accept it. No tokens, accounts or paths are
# embedded; the server still binds to localhost and stores data under .\data.

param(
    [string]$Addr = "127.0.0.1:8080"
)

$ErrorActionPreference = "Stop"

# Resolve repo root = parent of this script's folder.
$repo = Split-Path -Parent $PSScriptRoot
$exe  = Join-Path $repo "bin\server.exe"

if (-not (Test-Path $exe)) {
    Write-Error "server.exe not found at $exe — build it first: go build -o bin/server.exe ./cmd/server"
    exit 1
}

Write-Host "Launching elevated server: $exe -addr $Addr (working dir: $repo)" -ForegroundColor Cyan
Write-Host "Accept the UAC prompt when it appears." -ForegroundColor Yellow

# -Verb RunAs triggers UAC. -WorkingDirectory keeps .\data alongside the repo.
# (RunAs cannot be combined with output redirection, so logs go to the server's
# own hidden console.)
Start-Process -FilePath $exe -ArgumentList "-addr", $Addr -WorkingDirectory $repo -Verb RunAs -WindowStyle Hidden

Start-Sleep -Seconds 3
try {
    $h = (Invoke-WebRequest -UseBasicParsing "http://$Addr/healthz" -TimeoutSec 4).Content
    Write-Host "Server is up: $h" -ForegroundColor Green
    Write-Host "Open the dashboard: http://$Addr/"
} catch {
    Write-Warning "Could not reach the server yet — if you just accepted UAC it may need another second. Check http://$Addr/ in a browser."
}
