# XNightOps local CI gate — the single acceptance entry for night work.
# Local only; exit code is authoritative; GitHub hosted CI is not a gate.
#Requires -Version 5
$ErrorActionPreference = "Stop"
Set-Location -LiteralPath (Join-Path $PSScriptRoot "..")

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
Write-Host "LOCAL CI PASS"
exit 0
