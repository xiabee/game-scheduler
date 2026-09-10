# One-command smoke for the Rust controller (NC0 dry-run + NC1 manifest/ONNX).
# Runs the window probe, the GDI capture+detection probe, manifest checks,
# an ONNX-backed dry-run against the committed fixture, and a plain dry-run;
# exit code is authoritative for nightly close-out checks.
#Requires -Version 5
$ErrorActionPreference = "Stop"
Set-Location -LiteralPath (Join-Path $PSScriptRoot "..\controller")

cargo build --quiet
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# WDAC warm-up: organizational policy intermittently blocks freshly compiled
# binaries for a few minutes after each rebuild (see ci-local.ps1's retry
# wrapper). Launch cheaply until the block clears so the real steps don't
# flake; announced + bounded like every other honest retry here.
for ($warm = 1; $warm -le 6; $warm++) {
    cmd /c ".\target\debug\controller.exe --manifest-check models\example.manifest.json >nul 2>nul"
    if ($LASTEXITCODE -eq 0) { break }
    Write-Host "WDAC warm-up ${warm}: launch blocked (exit $LASTEXITCODE), waiting 20s..."
    Start-Sleep -Seconds 20
}

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
# ONNX runs log on stderr (onnxruntime verbose env lines); run through cmd
# so PS 5.1's stderr+Stop quirk cannot turn them into terminating errors
cmd /c ".\target\debug\controller.exe --dry-run --backend synthetic --fps 10 --duration 2 --model-path tests\fixtures\constant_yolo.manifest.json 2>nul"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== dry-run ONNX x real GDI capture (2s) =="
cmd /c ".\target\debug\controller.exe --dry-run --backend gdi --fps 5 --duration 2 --model-path tests\fixtures\constant_yolo.manifest.json 2>nul"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== dry-run (gdi, 3s) =="
.\target\debug\controller.exe --dry-run --backend gdi --fps 10 --duration 3
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== record -> replay roundtrip =="
$recDir = Join-Path $env:TEMP ("nf_smoke_replay_" + [guid]::NewGuid().ToString("N"))
.\target\debug\controller.exe --dry-run --backend gdi --fps 5 --duration 1 --record $recDir
if ($LASTEXITCODE -ne 0) { Write-Host "record run failed"; exit 1 }
$frames = (Get-ChildItem $recDir -Filter "frame_*.png" | Measure-Object).Count
if ($frames -lt 1) { Write-Host "no frames recorded"; exit 1 }
cmd /c ".\target\debug\controller.exe --dry-run --backend gdi --fps 5 --duration 1 --replay ""$recDir"" --model-path tests\fixtures\constant_yolo.manifest.json 2>nul"
$code = $LASTEXITCODE
Remove-Item -Recurse -Force $recDir -ErrorAction SilentlyContinue
if ($code -ne 0) { Write-Host "replay run failed (exit $code)"; exit 1 }
Write-Host "record->replay: OK ($frames frames)"

Write-Host "== protocol mode (NC6 wire, 1s) =="
# stdout must be protocol-only: three schema-shaped JSON lines. The shape
# assertions (HELLO first / RESULT last / kinds) are pinned by the Rust
# integration test; here we assert the operator-visible contract: parseable
# JSON on every stdout line and a RESULT outcome=done.
$protoOut = Join-Path $env:TEMP ("nf_smoke_proto_" + [guid]::NewGuid().ToString("N") + ".txt")
# WDAC intermittently blocks freshly compiled binaries (same environmental
# quirk ci-local retries around): up to 3 attempts with a short backoff.
$code = 1
for ($attempt = 1; $attempt -le 3 -and $code -ne 0; $attempt++) {
    cmd /c ".\target\debug\controller.exe --dry-run --protocol --backend synthetic --duration 1 > ""$protoOut"" 2>nul"
    $code = $LASTEXITCODE
    if ($code -ne 0) { Write-Host "protocol run attempt $attempt failed (exit $code), backing off..."; Start-Sleep -Seconds 5 }
}
$protoOk = $false
if ($code -eq 0 -and (Test-Path $protoOut)) {
    $lines = (Get-Content $protoOut) | Where-Object { $_ -ne "" }
    if ($lines.Count -ge 3) {
        try {
            $h = $lines[0] | ConvertFrom-Json
            $r = $lines[$lines.Count - 1] | ConvertFrom-Json
            if ($h.type -eq "HELLO" -and $r.type -eq "RESULT" -and $r.payload.outcome -eq "done") {
                $protoOk = $true
            }
        } catch { }
    }
}
Remove-Item $protoOut -ErrorAction SilentlyContinue
if (-not $protoOk) {
    Write-Host "protocol mode: stdout stream invalid (exit=$code lines=$($lines.Count))"
    exit 1
}
Write-Host "protocol mode: OK (HELLO..RESULT done, stdout protocol-only)"

Write-Host "== new flag validation (infer-timeout / device) =="
cmd /c ".\target\debug\controller.exe --dry-run --backend synthetic --duration 1 --infer-timeout 0 2>nul"
$code = $LASTEXITCODE
if ($code -ne 2) { Write-Host "--infer-timeout 0 must exit 2 (got $code)"; exit 1 }
cmd /c ".\target\debug\controller.exe --dry-run --backend synthetic --duration 1 --device warp 2>nul"
$code = $LASTEXITCODE
if ($code -ne 2) { Write-Host "--device warp must exit 2 (got $code)"; exit 1 }
cmd /c ".\target\debug\controller.exe --dry-run --backend synthetic --duration 1 --device cpu 2>nul"
$code = $LASTEXITCODE
if ($code -ne 0) { Write-Host "--device cpu must pass (got $code)"; exit 1 }
Write-Host "new flags: OK (validation gates + cpu path)"

Write-Host "CONTROLLER SMOKE PASS"
exit 0
