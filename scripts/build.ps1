# build.ps1 - build and package game-scheduler for Windows (and Linux).
#
# Produces dist/game-scheduler_<version>_windows_amd64.zip containing:
#   bin/server.exe, bin/ctl.exe, config.example.json, README.md, README_EN.md,
#   LICENSE, SECURITY.md, examples/, VERSION
# plus SHA256SUMS.txt over the archives. With -IncludeLinux also builds a
# linux-amd64 tar.gz.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\build.ps1 [-Version v1.2.3] [-IncludeLinux]
#
# The release workflow (.github/workflows/release.yml) packages the same way on
# tag push; this script exists for local verification of the same output.

param(
    [string]$Version = "",
    [string]$OutDir = "dist",
    [switch]$IncludeLinux,
    [switch]$IncludeController
)

$ErrorActionPreference = "Stop"

if ($Version -eq "") {
    # Prefer the last tag; fall back to a short commit hash. Run through
    # cmd so a failing git (untagged repo) cannot trip the PS 5.1
    # stderr + Stop preference quirk.
    $tag = cmd /c "git describe --tags --abbrev=0 2>nul"
    if ($LASTEXITCODE -ne 0 -or -not $tag) { $tag = "v0.0.0-dev" }
    $sha = (git rev-parse --short HEAD).Trim()
    $dirty = git status --porcelain
    $Version = "$tag-g$sha$(if ($dirty) { "-dirty" })"
}
Write-Host "Version: $Version"

$ldflags = "-s -w -X github.com/xiabee/game-scheduler/internal/version.Version=$Version"
$pkg = Join-Path $OutDir "pkg"
$targets = @("windows")
if ($IncludeLinux) { $targets += "linux" }

if (Test-Path $pkg) { Remove-Item -Recurse -Force $pkg }

foreach ($goos in $targets) {
    $bin = Join-Path $pkg "game-scheduler-$goos-amd64\bin"
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    $srvName = if ($goos -eq "windows") { "server.exe" } else { "server" }
    $ctlName = if ($goos -eq "windows") { "ctl.exe" } else { "ctl" }
    Write-Host "Building $goos/amd64 ..."
    $env:GOOS = $goos; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
    try {
        go build -trimpath -ldflags $ldflags -o (Join-Path $bin $srvName) ./cmd/server
        if ($LASTEXITCODE -ne 0) { throw "go build server ($goos) failed" }
        go build -trimpath -ldflags $ldflags -o (Join-Path $bin $ctlName) ./cmd/ctl
        if ($LASTEXITCODE -ne 0) { throw "go build ctl ($goos) failed" }
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    }

    $root = Join-Path $pkg "game-scheduler-$goos-amd64"
    foreach ($f in @("config.example.json", "README.md", "README_EN.md", "LICENSE", "SECURITY.md")) {
        Copy-Item $f (Join-Path $root $f)
    }
    Copy-Item examples (Join-Path $root "examples") -Recurse

    # NC1+: optionally ship the native controller (Rust, Windows only).
    # Honest skip when cargo is missing or the switch is off - the Go
    # scheduler is fully functional without it (external adapters remain).
    if ($goos -eq "windows" -and $IncludeController) {
        $ctrl = Join-Path $root "controller"
        New-Item -ItemType Directory -Path $ctrl -Force | Out-Null
        $hasCargo = $false
        try { cargo --version | Out-Null; $hasCargo = ($LASTEXITCODE -eq 0) } catch { $hasCargo = $false }
        if ($hasCargo) {
            Push-Location controller
            try {
                cargo build --release --locked
                if ($LASTEXITCODE -ne 0) { throw "cargo build (controller) failed" }
            } finally {
                Pop-Location
            }
            Copy-Item controller/target/release/controller.exe (Join-Path $ctrl "controller.exe")
            Copy-Item controller\models\README.md (Join-Path $ctrl "MODELS.md")
            Copy-Item controller\models\example.manifest.json (Join-Path $ctrl "example.manifest.json")
            Write-Host "controller: packaged (native-controller.exe + example manifest)"
        } else {
            Write-Host "controller: SKIPPED (cargo not available on this machine)"
        }
    }

    Set-Content -Path (Join-Path $root "VERSION") -Value $Version -Encoding UTF8
}

# Archives. Names match the release workflow: <name>-<version>.(zip|tar.gz)
Write-Host "Archiving ..."
$archives = @()
foreach ($goos in $targets) {
    $name = "game-scheduler-$goos-amd64"
    $a = Join-Path $OutDir "$name-$Version.zip"
    if ($goos -eq "windows") {
        Compress-Archive -Path (Join-Path $pkg $name) -DestinationPath $a -Force
    } else {
        $a = Join-Path $OutDir "$name-$Version.tar.gz"
        tar -czf $a -C $pkg $name
        if ($LASTEXITCODE -ne 0) { throw "tar failed" }
    }
    $archives += $a
}
Remove-Item -Recurse -Force $pkg

# Checksums over the final artifacts.
Write-Host "Hashing ..."
$hashLines = foreach ($a in $archives) {
    (Get-FileHash $a -Algorithm SHA256).Hash + "  " + (Split-Path $a -Leaf)
}
$hashLines | Set-Content (Join-Path $OutDir "SHA256SUMS.txt") -Encoding ASCII

Write-Host ""
Write-Host "Done. Artifacts in $OutDir\:" -ForegroundColor Green
Get-ChildItem $OutDir -File | ForEach-Object { Write-Host ("  " + $_.Name) }
