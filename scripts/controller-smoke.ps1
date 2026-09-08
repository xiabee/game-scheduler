# One-command smoke for the Rust controller (NC0).
# Runs the window probe, the GDI capture+detection probe, and a short
# dry-run; exit code is authoritative for nightly close-out checks.
#Requires -Version 5
$ErrorActionPreference = "Stop"
Set-Location -LiteralPath (Join-Path $PSScriptRoot "..\controller")

cargo build --quiet
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== self-probe =="
.\target\debug\controller.exe --self-probe
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== capture-gdi =="
.\target\debug\controller.exe --capture-gdi
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== dry-run (gdi, 3s) =="
.\target\debug\controller.exe --dry-run --backend gdi --fps 10 --duration 3
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "CONTROLLER SMOKE PASS"
exit 0
