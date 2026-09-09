# One-command smoke for the Rust controller (NC0 dry-run + NC1 manifest/ONNX).
# Runs the window probe, the GDI capture+detection probe, manifest checks,
# an ONNX-backed dry-run against the committed fixture, and a plain dry-run;
# exit code is authoritative for nightly close-out checks.
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

Write-Host "== manifest-check (valid / missing / bad schema) =="
.\target\debug\controller.exe --manifest-check models\example.manifest.json
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
# expected-failure probes: run through cmd so the stderr of a FAILING
# command does not trip $ErrorActionPreference=Stop (PS 5.1 quirk)
cmd /c ".\target\debug\controller.exe --manifest-check models\definitely_missing.json 2>nul"
$code = $LASTEXITCODE
if ($code -ne 2) { Write-Host "manifest-check: missing file must exit 2 (got $code)"; exit 1 }
Set-Content -Path "$env:TEMP\nc_bad_schema.json" -Value '{""schema_version"": 2}'
cmd /c ".\target\debug\controller.exe --manifest-check %TEMP%\nc_bad_schema.json 2>nul"
$code = $LASTEXITCODE
if ($code -ne 2) { Write-Host "manifest-check: bad schema must exit 2 (got $code)"; exit 1 }
Remove-Item "$env:TEMP\nc_bad_schema.json" -ErrorAction SilentlyContinue
Write-Host "manifest-check: gates OK"

Write-Host "== dry-run with REAL ONNX fixture (WinML, 2s) =="
.\target\debug\controller.exe --dry-run --backend synthetic --fps 10 --duration 2 --model-path tests\fixtures\constant_yolo.manifest.json
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== dry-run ONNX x real GDI capture (2s) =="
.\target\debug\controller.exe --dry-run --backend gdi --fps 5 --duration 2 --model-path tests\fixtures\constant_yolo.manifest.json
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== dry-run (gdi, 3s) =="
.\target\debug\controller.exe --dry-run --backend gdi --fps 10 --duration 3
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "CONTROLLER SMOKE PASS"
exit 0
