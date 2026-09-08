# XNightOps local CI gate — the single acceptance entry for night work.
# Local only; exit code is authoritative; GitHub hosted CI is not a gate.
#Requires -Version 5
$ErrorActionPreference = "Stop"
Set-Location -LiteralPath (Join-Path $PSScriptRoot "..")

# Organizational WDAC intermittently blocks freshly compiled test binaries
# for minutes at a time ("An Application Control policy has blocked this
# file"). Retry with backoff; label exhausted retries as environmental so
# the night flow re-runs later instead of misreading a code regression.
function Invoke-WithWdacRetry {
    param([scriptblock]$Step, [string]$Name)
    for ($attempt = 1; $attempt -le 8; $attempt++) {
        & $Step
        $code = $LASTEXITCODE
        if ($code -eq 0) { return 0 }
        & $Step 2>&1 | Tee-Object -Variable stepOut | Out-Null
        $joined = ($stepOut | Out-String)
        if ($joined -match "Application Control policy|Device Guard") {
            Write-Host "$Name : WDAC block hit (attempt $attempt/8), waiting 15s..."
            Start-Sleep -Seconds 15
            continue
        }
        return $code
    }
    Write-Host "$Name : failed 8x with the WDAC-block signature - ENVIRONMENTAL, not a code failure; re-run later."
    return 2
}

Write-Host "== gofmt check =="
$unformatted = gofmt -l ./cmd ./internal
if ($unformatted) {
    Write-Host "gofmt needed:"
    $unformatted
    exit 1
}

Write-Host "== go vet =="
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== go test =="
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "== go build =="
go build ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# --- Rust controller gate (NC0) -------------------------------------------
# Runs when cargo exists. Nodes without the Rust toolchain (win-devops)
# skip honestly instead of failing: remote acceptance covers the Go side
# until Rust is installed there. The skip is always announced.
$cargo = Get-Command cargo -ErrorAction SilentlyContinue
if ($cargo) {
    Write-Host "== cargo fmt --check (controller/) =="
    Push-Location controller
    cargo fmt --check
    if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
    Pop-Location

    Write-Host "== cargo test (controller/) =="
    Push-Location controller
    cargo test --quiet
    $testCode = $LASTEXITCODE
    Pop-Location
    if ($testCode -ne 0) {
        Write-Host "cargo test failed (exit $testCode)"
        exit $testCode
    }

    Write-Host "== cargo build (controller/) =="
    Push-Location controller
    cargo build --quiet
    if ($LASTEXITCODE -ne 0) { Pop-Location; exit $LASTEXITCODE }
    Pop-Location
}
else {
    Write-Host "SKIP rust gate: cargo not found on this node (Go-only acceptance)."
}

Write-Host "LOCAL CI PASS"
exit 0
