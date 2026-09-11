# windows_smoke.ps1 - end-to-end acceptance for game-scheduler on Windows.
#
# Walks the full chain with a HARMLESS fake tool (a copied cmd.exe) — no game,
# no BetterGI/March7th/Fhoe/ok-ww/M9A, no mouse/keyboard input:
#
#   health -> discover -> create game -> create route -> create task
#     -> preflight -> fake execution -> execution log -> route stats -> cleanup
#
# Usage (server must already be running; see README quickstart):
#   powershell -ExecutionPolicy Bypass -File examples\windows_smoke.ps1 `
#       -Server http://127.0.0.1:8080 [-Token <GS_AUTH_TOKEN>] [-Ctl .\ctl.exe]
#
# Exit code 0 = every step passed. Any failure stops and reports the step.

param(
    [string]$Server = $(if ($env:GS_SERVER) { $env:GS_SERVER } else { "http://127.0.0.1:8080" }),
    [string]$Token = $(if ($env:GS_TOKEN) { $env:GS_TOKEN } else { "" }),
    [string]$Ctl = "ctl",
    # Native (NC6) section: path to the Rust controller executable. When
    # empty or missing, the native steps are announced and skipped
    # honestly (a Go-only node still gets full coverage of its surface).
    [string]$Controller = $(if ($env:GS_SMOKE_CONTROLLER) { $env:GS_SMOKE_CONTROLLER } else { "" })
)

$ErrorActionPreference = "Stop"
$script:Step = 0
$script:Failures = @()

function Invoke-Ctl {
    # ctl's flag parser stops at the first positional (resource) argument, so
    # global flags must be passed BEFORE "resource action". Flags collects them.
    # JsonBody is piped to ctl via `-data -` (stdin): PowerShell 5.1 mangles
    # embedded quotes when passing JSON as a native command-line argument.
    param(
        [string[]]$ResourceArgs = @(),
        [string[]]$Flags = @(),
        [string]$JsonBody = $null
    )
    $argv = @($Ctl, "-server", $Server)
    if ($Token -ne "") { $argv += @("-token", $Token) }
    if ($null -ne $JsonBody) { $Flags += @("-data", "-") }
    $argv += $Flags + $ResourceArgs
    $errFile = Join-Path $env:TEMP ("gs-ctl-err-" + [guid]::NewGuid().ToString("N") + ".txt")
    $rest = @($argv[1..($argv.Count - 1)])
    try {
        if ($null -ne $JsonBody) {
            $out = $JsonBody | & $argv[0] @rest 2>$errFile
        } else {
            $out = & $argv[0] @rest 2>$errFile
        }
    } finally {
        $errText = ""
        if (Test-Path $errFile) {
            $errText = (Get-Content $errFile -Raw -ErrorAction SilentlyContinue)
            Remove-Item $errFile -ErrorAction SilentlyContinue
        }
    }
    if ($LASTEXITCODE -ne 0) {
        $detail = (($errText | Out-String).Trim() -split "`r?`n" | Select-Object -Last 1)
        throw "ctl $($ResourceArgs -join ' ') failed (exit $LASTEXITCODE): $detail"
    }
    $text = ($out | Out-String).Trim()
    if ($text) {
        # ctl prints the plain status line (e.g. "204 No Content") for empty
        # responses; only attempt a JSON parse when the payload looks like JSON.
        if ($text.StartsWith("{") -or $text.StartsWith("[")) {
            try { return $text | ConvertFrom-Json } catch { return $text }
        }
        return $text
    }
    return $null
}

function Step {
    param([string]$Name, [scriptblock]$Body)
    $script:Step++
    Write-Host ("[{0:D2}] {1} ... " -f $script:Step, $Name) -NoNewline
    try {
        & $Body
        Write-Host "OK" -ForegroundColor Green
    } catch {
        Write-Host "FAIL" -ForegroundColor Red
        Write-Host "      $($_.Exception.Message)" -ForegroundColor Red
        $script:Failures += "$Name : $($_.Exception.Message)"
    }
}

# ---- 0. sanity ----
Step "healthz reachable" {
    $null = Invoke-Ctl -ResourceArgs @("health")
}

# ---- smoke assets: a fake BetterGI.exe (copy of cmd.exe) + a fake route ----
$work = Join-Path $env:TEMP ("gs-smoke-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
New-Item -ItemType Directory -Path $work | Out-Null
$fakeExe = Join-Path $work "BetterGI.exe"
Copy-Item "$env:SystemRoot\System32\cmd.exe" $fakeExe
$routeFile = Join-Path $work "route-smoke.json"
Set-Content -Path $routeFile -Value '{"waypoints":[]}' -Encoding UTF8
$gameId = "smoke-genshin"

try {
    # ---- 1. discover finds the fake tool in an explicit path ----
    Step "discover finds fake BetterGI.exe" {
        $res = Invoke-Ctl -ResourceArgs @("discover") -Flags @("-paths", $work)
        $hit = @($res.candidates | Where-Object { $_.adapter -eq "genshin" -and $_.kind -eq "exe" })
        if ($hit.Count -lt 1) { throw "discover returned no genshin exe candidate in $work" }
    }

    # ---- 2. create game ----
    Step "create game" {
        $body = @{ id = $gameId; name = "SMOKE Genshin"; adapter = "genshin"; tool_path = $fakeExe; enabled = $true } | ConvertTo-Json -Compress
        $null = Invoke-Ctl -ResourceArgs @("games", "add") -JsonBody $body
    }

    # ---- 3. create route ----
    Step "create route" {
        $body = @{ game_id = $gameId; adapter = "genshin"; route_type = "collect"; name = "smoke-route";
                   file_path = $routeFile; tags = @("smoke") } | ConvertTo-Json -Compress
        $script:route = Invoke-Ctl -ResourceArgs @("routes", "add") -JsonBody $body
        if (-not $script:route.id) { throw "route add returned no id" }
    }

    # ---- 4. create task from route ----
    Step "route -> create-task" {
        $script:task = Invoke-Ctl -ResourceArgs @("routes", "create-task", "$($script:route.id)")
        if (-not $script:task.id) { throw "create-task returned no task id" }
        if (-not $script:task.route_id) { throw "created task lost route binding" }
    }

    # ---- 5. preflight: environment must be ready ----
    Step "preflight reports Ready" {
        $pf = Invoke-Ctl -ResourceArgs @("tasks", "preflight", "$($script:task.id)")
        if ($pf.validation_error) { throw "validation_error: $($pf.validation_error)" }
        if ($pf.build_error) { throw "build_error: $($pf.build_error)" }
        if ($pf.missing.Count -gt 0) { throw "missing: $($pf.missing -join '; ')" }
        if (-not $pf.ready) { throw "preflight not ready" }
    }

    # ---- 6. harmless execution: swap args to a no-op cmd /c echo ----
    Step "update task to harmless raw command" {
        $body = @{ game_id = $gameId; name = $script:task.name; type = "raw";
                   route_id = $script:task.route_id;
                   params = @{ exe = $fakeExe; raw_args = @("/c", "echo", "SMOKE_RUN_OK") } | ConvertTo-Json -Compress;
                   enabled = $true } | ConvertTo-Json -Compress -Depth 4
        $null = Invoke-Ctl -ResourceArgs @("tasks", "update", "$($script:task.id)") -JsonBody $body
    }

    Step "run task (fake execution)" {
        $script:exec = Invoke-Ctl -ResourceArgs @("tasks", "run", "$($script:task.id)")
        if (-not $script:exec.id) { throw "run returned no execution id" }
    }

    Step "execution reaches success" {
        $deadline = (Get-Date).AddSeconds(60)
        do {
            Start-Sleep -Milliseconds 500
            $e = Invoke-Ctl -ResourceArgs @("execs", "get", "$($script:exec.id)")
            if ((Get-Date) -gt $deadline) { throw "execution did not finish in 60s (status=$($e.status))" }
        } while ($e.status -in @("pending", "running"))
        if ($e.status -ne "success") { throw "status=$($e.status) error=$($e.error_msg)" }
        if ($e.exit_code -ne 0) { throw "exit_code=$($e.exit_code)" }
        if (($e.stdout | Out-String) -notmatch "SMOKE_RUN_OK") { throw "stdout does not contain SMOKE_RUN_OK: $($e.stdout)" }
    }

    # ---- 7. route stats recorded ----
    Step "route success_count incremented" {
        $rt = Invoke-Ctl -ResourceArgs @("routes", "get", "$($script:route.id)")
        if ($rt.success_count -lt 1) { throw "success_count=$($rt.success_count)" }
        if (-not $rt.last_run_at) { throw "last_run_at not recorded" }
    }

    # ---- 8. deleting a running task kills its process and cascades cleanly ----
    Step "delete running task (process killed, no orphans)" {
        $body = @{ game_id = $gameId; name = $script:task.name; type = "raw";
                   route_id = $script:task.route_id;
                   params = @{ exe = $fakeExe; raw_args = @("/c", "ping", "-n", "8", "127.0.0.1") } | ConvertTo-Json -Compress;
                   enabled = $true } | ConvertTo-Json -Compress -Depth 4
        Invoke-Ctl -ResourceArgs @("tasks", "update", "$($script:task.id)") -JsonBody $body | Out-Null
        $exec = Invoke-Ctl -ResourceArgs @("tasks", "run", "$($script:task.id)")
        Start-Sleep -Seconds 2
        $e = Invoke-Ctl -ResourceArgs @("execs", "get", "$($exec.id)")
        if ($e.status -ne "running") { throw "expected running, got $($e.status)" }
        $null = Invoke-Ctl -ResourceArgs @("tasks", "delete", "$($script:task.id)")
        # the execution rows cascade away with the task - nothing may be left
        $left = Invoke-Ctl -ResourceArgs @("execs") -Flags @("-task", "$($script:task.id)")
        if ($null -ne $left -and @($left).Count -gt 0) { throw "execution rows survived the cascade" }
    }

    # ---- 9. native (NC6) full chain: real controller, zero input ----------
    $nativeReady = ($Controller -ne "") -and (Test-Path $Controller)
    if (-not $nativeReady) {
        Write-Host "SKIP native steps: controller executable not provided (set -Controller)."
    }
    else {
        $probeFile = Join-Path $work "probes-smoke.json"
        Set-Content -Path $probeFile -Value '[{"name":"smoke_probe","x":10,"y":10,"w":8,"h":8,"expected":[200,40,16],"tolerance":24,"min_fraction":0.4,"step":2}]' -Encoding UTF8
        Step "native: create task (executor=native, dry-run synthetic)" {
            $params = @{ executor = "native"; probes = $probeFile; window = "@probe";
                         backend = "synthetic"; duration_sec = 2 } | ConvertTo-Json -Compress
            $body = @{ game_id = $gameId; name = "smoke-native"; type = "native";
                       params = $params; enabled = $true } | ConvertTo-Json -Compress -Depth 4
            $script:nativeTask = Invoke-Ctl -ResourceArgs @("tasks", "add") -JsonBody $body
            if (-not $script:nativeTask.id) { throw "native task add returned no id" }
        }
        Step "native: preflight ready (controller + probes file)" {
            $pf = Invoke-Ctl -ResourceArgs @("tasks", "preflight", "$($script:nativeTask.id)")
            if ($pf.validation_error) { throw "validation_error: $($pf.validation_error)" }
            if ($pf.missing.Count -gt 0) { throw "missing: $($pf.missing -join '; ')" }
            if (-not $pf.ready) { throw "preflight not ready" }
        }
        Step "native: run real controller session (RESULT done -> success)" {
            $params2 = @{ executor = "native"; probes = $probeFile; window = "@probe";
                          backend = "synthetic"; duration_sec = 2 } | ConvertTo-Json -Compress
            $body = @{ game_id = $gameId; name = $script:nativeTask.name; type = "native";
                       params = $params2; enabled = $true } | ConvertTo-Json -Compress -Depth 4
            $null = Invoke-Ctl -ResourceArgs @("tasks", "update", "$($script:nativeTask.id)") -JsonBody $body
            $exec = Invoke-Ctl -ResourceArgs @("tasks", "run", "$($script:nativeTask.id)")
            $deadline = (Get-Date).AddSeconds(60)
            do {
                Start-Sleep -Milliseconds 500
                $e = Invoke-Ctl -ResourceArgs @("execs", "get", "$($exec.id)")
                if ((Get-Date) -gt $deadline) { throw "native execution did not finish in 60s (status=$($e.status))" }
            } while ($e.status -in @("pending", "running"))
            if ($e.status -ne "success") { throw "status=$($e.status) error=$($e.error_msg)" }
            if ($e.exit_code -ne 0) { throw "exit_code=$($e.exit_code)" }
        }
        Step "native: cancel mid-run (process tree killed, status cancelled)" {
            $params3 = @{ executor = "native"; probes = $probeFile; window = "@probe";
                          backend = "synthetic"; duration_sec = 60 } | ConvertTo-Json -Compress
            $body = @{ game_id = $gameId; name = $script:nativeTask.name; type = "native";
                       params = $params3; enabled = $true } | ConvertTo-Json -Compress -Depth 4
            $null = Invoke-Ctl -ResourceArgs @("tasks", "update", "$($script:nativeTask.id)") -JsonBody $body
            $exec = Invoke-Ctl -ResourceArgs @("tasks", "run", "$($script:nativeTask.id)")
            Start-Sleep -Seconds 3
            $e = Invoke-Ctl -ResourceArgs @("execs", "get", "$($exec.id)")
            if ($e.status -ne "running") { throw "expected running mid-session, got $($e.status)" }
            $null = Invoke-Ctl -ResourceArgs @("execs", "cancel", "$($exec.id)")
            Start-Sleep -Seconds 3
            $e = Invoke-Ctl -ResourceArgs @("execs", "get", "$($exec.id)")
            if ($e.status -ne "cancelled") { throw "status=$($e.status), want cancelled" }
        }

        # ---- 9b. auto executor (NC6): resolve and announce the branch ----
        Step "native: auto executor resolves to the controller (preflight + run)" {
            # adapter-owned type with a usable script fallback; native
            # prerequisites hold, so the controller branch must win
            $paramsAuto = @{ executor = "auto"; probes = $probeFile; window = "@probe";
                             backend = "synthetic"; duration_sec = 2; script = "auto-fallback" } | ConvertTo-Json -Compress
            $body = @{ game_id = $gameId; name = $script:nativeTask.name; type = "script";
                       params = $paramsAuto; enabled = $true } | ConvertTo-Json -Compress -Depth 4
            $null = Invoke-Ctl -ResourceArgs @("tasks", "update", "$($script:nativeTask.id)") -JsonBody $body
            $pf = Invoke-Ctl -ResourceArgs @("tasks", "preflight", "$($script:nativeTask.id)")
            if ($pf.resolution -ne "auto→native") { throw "resolution=$($pf.resolution), want auto→native" }
            if (-not $pf.ready) { throw "preflight not ready: $($pf.validation_error) $($pf.missing -join '; ')" }
            $exec = Invoke-Ctl -ResourceArgs @("tasks", "run", "$($script:nativeTask.id)")
            $deadline = (Get-Date).AddSeconds(60)
            do {
                Start-Sleep -Milliseconds 500
                $e = Invoke-Ctl -ResourceArgs @("execs", "get", "$($exec.id)")
                if ((Get-Date) -gt $deadline) { throw "auto execution did not finish in 60s (status=$($e.status))" }
            } while ($e.status -in @("pending", "running"))
            if ($e.status -ne "success") { throw "status=$($e.status) error=$($e.error_msg)" }
            if ($e.stdout -notmatch "executor=auto resolved=native") { throw "trail missing auto resolution: $($e.stdout)" }
        }
        Step "native: auto executor degrades to the adapter when an asset is missing" {
            $paramsDegrade = @{ executor = "auto"; probes = $probeFile; skill = "Z:/definitely/missing/skill.json";
                                window = "@probe"; backend = "synthetic"; duration_sec = 2; script = "auto-fallback" } | ConvertTo-Json -Compress
            $body = @{ game_id = $gameId; name = $script:nativeTask.name; type = "script";
                       params = $paramsDegrade; enabled = $true } | ConvertTo-Json -Compress -Depth 4
            $null = Invoke-Ctl -ResourceArgs @("tasks", "update", "$($script:nativeTask.id)") -JsonBody $body
            $pf = Invoke-Ctl -ResourceArgs @("tasks", "preflight", "$($script:nativeTask.id)")
            if ($pf.resolution -notmatch "auto→external") { throw "resolution=$($pf.resolution), want auto→external" }
            if ($pf.resolution -notmatch "skill file not found") { throw "resolution lacks the reason: $($pf.resolution)" }
            if (-not $pf.ready) { throw "external fallback branch must be ready: $($pf.validation_error) $($pf.missing -join '; ')" }
        }
    }
} finally {
    # ---- 8. cleanup (delete game cascades tasks/routes/executions) ----
    try { $null = Invoke-Ctl -ResourceArgs @("games", "delete", $gameId) } catch { }
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}

if ($script:Failures.Count -gt 0) {
    Write-Host ""
    Write-Host "SMOKE FAILED ($($script:Failures.Count) step(s)):" -ForegroundColor Red
    $script:Failures | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
    exit 1
}
if ($nativeReady) {
    Write-Host "SMOKE PASS - all steps OK (discover/game/route/task/preflight/execution/log/stats/running-delete/native)." -ForegroundColor Green
}
else {
    Write-Host "SMOKE PASS - all steps OK except native (controller not provided)." -ForegroundColor Green
}
exit 0
