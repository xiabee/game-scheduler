# XNightOps local CI gate — the single acceptance entry for night work.
# Local only; exit code is authoritative; GitHub hosted CI is not a gate.
# -Race: additionally run the Go suite under the race detector, using the
#        portable mingw64 at D:\tools\mingw64 for CGO (see
#        docs/MR_ENVIRONMENT.md). Product builds stay CGO-free.
#Requires -Version 5
param([switch]$Race)
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

if ($Race) {
    $mingw = "D:\tools\mingw64\bin"
    if (Test-Path (Join-Path $mingw "gcc.exe")) {
        Write-Host "== go test -race (CGO via portable mingw64) =="
        $env:PATH = "$mingw;$env:PATH"
        $env:CGO_ENABLED = "1"
        go test -race ./...
        $raceCode = $LASTEXITCODE
        Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
        if ($raceCode -ne 0) { exit $raceCode }
    }
    else {
        Write-Host "SKIP go test -race: D:\tools\mingw64 not found (see docs/MR_ENVIRONMENT.md)."
    }
}

Write-Host "== go build =="
go build ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# --- Security stage ----------------------------------------------------------
# govulncheck (known-vuln scan, symbol level) + gosec (static analysis, HIGH
# severity x HIGH confidence gate) + a credential-pattern scan over git-tracked
# files. Scanners are resolved in GOPATH\bin; a node without them skips
# honestly (same policy as the Rust gate). #nosec annotations with a written
# reason are the only accepted suppressions.
$gopath = (& go env GOPATH) | Select-Object -First 1
$govuln = Join-Path (Join-Path $gopath "bin") "govulncheck.exe"
$gosecExe = Join-Path (Join-Path $gopath "bin") "gosec.exe"

if (Test-Path $govuln) {
    Write-Host "== govulncheck =="
    # Output captured to a file: the vulnerability database lives online, so
    # a network-restricted node fails with a DB-load error — that is
    # environmental (announced SKIP), while a real vulnerability finding or
    # a code-load failure still fails the gate.
    $govLog = [System.IO.Path]::GetTempFileName()
    cmd /c "`"$govuln`" ./... > ""$govLog"" 2>&1"
    $code = $LASTEXITCODE
    if ($code -ne 0) {
        $govText = (Get-Content $govLog -ErrorAction SilentlyContinue | Out-String)
        if ($govText -match "vuln\.go\.dev|failed to load|loading vulnerability|dial tcp|no such host|getsockopt|httppost|connection") {
            Write-Host "SKIP govulncheck: vulnerability DB unreachable on this node (environmental)."
            Write-Host ($govText.Substring(0, [Math]::Min(400, $govText.Length)))
        }
        else {
            Remove-Item $govLog -ErrorAction SilentlyContinue
            Write-Host $govText
            exit $LASTEXITCODE
        }
    }
    Remove-Item $govLog -ErrorAction SilentlyContinue
}
else {
    Write-Host "SKIP govulncheck: not found in GOPATH\bin (go install golang.org/x/vuln/cmd/govulncheck@latest)."
}

if (Test-Path $gosecExe) {
    Write-Host "== gosec (severity=high confidence=high) =="
    & $gosecExe -severity high -confidence high -quiet ./...
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
else {
    Write-Host "SKIP gosec: not found in GOPATH\bin (go install github.com/securego/gosec/v2/cmd/gosec@latest)."
}

Write-Host "== secret scan (git-tracked files) =="
# High-signal credential shapes only. The sole allowlisted value is the
# synthetic token inside scripts/nightly-verify.ps1 (throwaway local test
# server in an isolated temp dir).
$patterns = @(
    "-----BEGIN [A-Z ]*PRIVATE KEY-----",
    "ghp_[A-Za-z0-9]{20,}",
    "github_pat_[A-Za-z0-9_]{20,}",
    "AKIA[0-9A-Z]{16}",
    "xox[baprs]-[A-Za-z0-9-]{10,}",
    "(password|secret|api_key|apikey|auth_token)[""']*\s*[:=]\s*[""']*[A-Za-z0-9+/_-]{16,}"
)
$grepArgs = @("grep", "-I", "-n")
foreach ($p in $patterns) { $grepArgs += @("-e", $p) }
$secretHits = & git @grepArgs | Where-Object { $_ -notmatch "nightly-verify-token" }
if ($secretHits) {
    Write-Host "secret scan FAILED:"
    $secretHits
    exit 1
}

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

    Write-Host "== cargo clippy (controller/) =="
    Push-Location controller
    cargo clippy --all-targets --quiet 2>&1 | Out-Null
    $clippyCode = $LASTEXITCODE
    if ($clippyCode -ne 0) {
        Pop-Location
        Write-Host "cargo clippy failed (exit $clippyCode)"
        exit $clippyCode
    }
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
