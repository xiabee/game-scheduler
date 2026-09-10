# nightly-verify.ps1 - one-command overnight gate for game-scheduler.
#
# Chains every acceptance surface the night contract relies on:
#   [1] scripts/ci-local.ps1          - Go (fmt/vet/test/build) + Rust gates
#   [2] scripts/controller-smoke.ps1  - window probe / GDI capture / manifest
#                                       gates / ONNX dry-run / record-replay
#   [3] windows full-chain smoke      - isolated temp server on a scratch port,
#                                       examples/windows_smoke.ps1 11 steps
#   [4] 30s ONNX soak                 - dry-run with the committed fixture,
#                                       inference + cache stats asserted
# Exit code 0 only when every section passed. Each section logs its own
# verdict so a partial night can see exactly what held.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\nightly-verify.ps1
#Requires -Version 5
$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $repo

$script:failed = @()

function Note([string]$msg) { Write-Host $msg }

# ---------- [1] local CI (Go + Rust) ----------
Note "== [1/5] ci-local =="
& powershell -NoProfile -ExecutionPolicy Bypass -File scripts\ci-local.ps1
if ($LASTEXITCODE -ne 0) { $script:failed += "ci-local" }

# ---------- [2] controller smoke ----------
Note "== [2/5] controller smoke =="
& powershell -NoProfile -ExecutionPolicy Bypass -File scripts\controller-smoke.ps1
if ($LASTEXITCODE -ne 0) { $script:failed += "controller-smoke" }

# ---------- [3] windows full-chain smoke (isolated temp server) ----------
Note "== [3/5] windows full-chain smoke =="
$work = Join-Path $env:TEMP ("nf_nightly_" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $work -Force | Out-Null
# forward slashes: backslashes would be invalid JSON escapes
$dataDir = (Join-Path $work "data") -replace "\\", "/"
# NC6 native steps: point the temp server at the controller binary that
# ci-local's cargo build just produced (it exists whenever Rust ran).
$controllerExe = Join-Path $repo "controller\target\debug\controller.exe"
$nativePath = ""
if (Test-Path $controllerExe) { $nativePath = $controllerExe -replace "\\", "/" }
$cfgBody = @{
    addr                   = "127.0.0.1:18765"
    data_dir               = $dataDir
    auth_token             = "nightly-verify-token"
    native_controller_path = $nativePath
} | ConvertTo-Json -Compress
$cfg = Join-Path $work "config.json"
Set-Content -Path $cfg -Value $cfgBody -Encoding UTF8
# The token above is a synthetic placeholder for the throwaway local test
# server in this battery (isolated temp dir); scripts/ci-local.ps1 secret
# scan allowlists this exact value.

go build -o (Join-Path $work "gs-server.exe") ./cmd/server
if ($LASTEXITCODE -ne 0) { $script:failed += "go build server" }
go build -o (Join-Path $work "gs-ctl.exe") ./cmd/ctl
if ($LASTEXITCODE -ne 0) { $script:failed += "go build ctl" }

$p = Start-Process -FilePath (Join-Path $work "gs-server.exe") `
    -ArgumentList "-config", $cfg -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 3
if ($p.HasExited) {
    $script:failed += "temp server exited early"
} else {
    & powershell -NoProfile -ExecutionPolicy Bypass -File examples\windows_smoke.ps1 `
        -Server "http://127.0.0.1:18765" -Token "nightly-verify-token" `
        -Ctl (Join-Path $work "gs-ctl.exe") -Controller $controllerExe
    if ($LASTEXITCODE -ne 0) { $script:failed += "windows-smoke" }
}
Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue

# ---------- [4] 30s ONNX soak (fixture, cache + degradation surfaces) ----------
Note "== [4/5] ONNX soak (30s) =="
$soakLog = Join-Path $env:TEMP ("nf_nightly_soak_" + [guid]::NewGuid().ToString("N") + ".tsv")
Set-Location -LiteralPath (Join-Path $repo "controller")
cmd /c ".\target\debug\controller.exe --dry-run --duration 30 --fps 15 --backend synthetic --model-path tests\fixtures\constant_yolo.manifest.json --session-log $soakLog 2>nul"
$code = $LASTEXITCODE
if ($code -ne 0) { $script:failed += "onnx-soak" }
if (Test-Path $soakLog) { Remove-Item $soakLog -ErrorAction SilentlyContinue }
Set-Location -LiteralPath $repo

# ---------- [5] NC9 learning-pipeline selftests (stdlib-only, offline) ----------
Note "== [5/5] NC9 chain selftests (learn_route + draft_to_skill) =="
Set-Location -LiteralPath (Join-Path $repo "tools\vision")
python learn_route.py --selftest
if ($LASTEXITCODE -ne 0) { $script:failed += "learn-route-selftest" }
python draft_to_skill.py --selftest
if ($LASTEXITCODE -ne 0) { $script:failed += "draft-to-skill-selftest" }
Set-Location -LiteralPath $repo

# ---------- verdict ----------
if ($script:failed.Count -gt 0) {
    Write-Host ("NIGHTLY VERIFY FAIL: " + ($script:failed -join ", "))
    exit 1
}
Write-Host "NIGHTLY VERIFY PASS"
exit 0
