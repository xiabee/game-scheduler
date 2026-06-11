# planner_quickstart.ps1 — character planner walkthrough (Windows PowerShell)
#
# Prereqs:
#   1. Server running locally:  .\server.exe   (defaults to http://127.0.0.1:8080)
#   2. ctl.exe on PATH or in the current directory.
#
# This script only talks to the local API with demo data. It contains no real
# accounts, tokens, cookies or machine-specific paths. If your server has an
# auth token configured, add:  -token $env:GS_TOKEN  to every ctl call.
#
# It does NOT assume an empty database: every id is parsed from the API
# response, and an existing demo-genshin game is reused.
#
# JSON bodies are passed via temp files with ctl's `-data @file` form — inline
# JSON quoting differs between Windows PowerShell 5.1 and PowerShell 7, files
# behave identically on both.
#
# Flow: game -> character -> goal -> material -> requirement -> recommend -> list.

$S   = "http://127.0.0.1:8080"
$ctl = ".\ctl.exe"   # adjust if ctl is elsewhere

function Step($msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }

# Runs ctl; returns parsed JSON or $null on failure. If -Body is given, it is
# serialized to a temp file and passed as -data @file (PS 5.1/7 safe).
function Invoke-Ctl {
    param([string[]]$CtlArgs, $Body = $null)
    $tmp = $null
    try {
        $argv = @()
        if ($null -ne $Body) {
            $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("gsqs_" + [guid]::NewGuid().ToString("n") + ".json")
            $Body | ConvertTo-Json -Depth 10 | Set-Content -Path $tmp -Encoding UTF8
            $argv += @("-data", "@$tmp")
        }
        $argv += $CtlArgs
        $raw = & $ctl -server $S @argv 2>$null
        if ($LASTEXITCODE -ne 0 -or -not $raw) { return $null }
        $text = $raw -join "`n"
        Write-Host $text
        try { return $text | ConvertFrom-Json } catch { return $null }
    } finally {
        if ($tmp -and (Test-Path $tmp)) { Remove-Item $tmp -Force }
    }
}

Step "0. health check"
& $ctl -server $S health
if ($LASTEXITCODE -ne 0) { Write-Error "server not reachable at $S"; exit 1 }

Step "1. demo game (reuse if it already exists)"
$game = Invoke-Ctl -CtlArgs @("games", "get", "demo-genshin")
if ($null -eq $game) {
    $game = Invoke-Ctl -CtlArgs @("games", "add") -Body @{
        id = "demo-genshin"; name = "Demo Genshin"; adapter = "genshin"
        tool_path = "C:/Tools/BetterGI/BetterGI.exe"; enabled = $true
    }
    if ($null -eq $game) { Write-Error "failed to create demo game"; exit 1 }
} else {
    Write-Host "demo-genshin already exists — reusing it. (Delete with: ctl games delete demo-genshin)"
}

Step "2. add a character (id parsed from the response)"
$ch = Invoke-Ctl -CtlArgs @("characters", "add") -Body @{
    game_id = "demo-genshin"; name = "DemoChar"; role_type = "dps"; element = "pyro"; tags = @("demo")
}
if ($null -eq $ch -or -not $ch.id) { Write-Error "failed to create character"; exit 1 }
$charId = $ch.id
Write-Host "-> character_id = $charId"

Step "3. add a goal for character $charId"
$goal = Invoke-Ctl -CtlArgs @("goals", "add") -Body @{
    character_id = $charId; name = "Ascend to 90"; target_level = "90"; priority = 5
}
if ($null -eq $goal -or -not $goal.id) { Write-Error "failed to create goal"; exit 1 }
$goalId = $goal.id
Write-Host "-> goal_id = $goalId"

Step "4. add a material"
$mat = Invoke-Ctl -CtlArgs @("materials", "add") -Body @{
    game_id = "demo-genshin"; name = "Demo Flower"; category = "collect"
    source_hint = "open-world pickup"; route_type_hint = "collect"
}
if ($null -eq $mat -or -not $mat.id) { Write-Error "failed to create material"; exit 1 }
$matId = $mat.id
Write-Host "-> material_id = $matId"

Step "5. add a material requirement (goal $goalId needs 10x material $matId, owns 2)"
$req = Invoke-Ctl -CtlArgs @("requirements", "add") -Body @{
    goal_id = $goalId; material_id = $matId; required_count = 10; owned_count = 2; priority = 8
}
if ($null -eq $req) { Write-Error "failed to create requirement"; exit 1 }

Step "6. generate recommendations for goal $goalId"
Invoke-Ctl -CtlArgs @("planner", "recommend") -Body @{ goal_id = $goalId; max_tasks = 5 } | Out-Null

Step "7. list recommendations (filterable)"
& $ctl -server $S -goal $goalId planner recommendations
& $ctl -server $S -game demo-genshin -status open planner recommendations

Step "8. filtered listings"
& $ctl -server $S -game demo-genshin -status open goals list
& $ctl -server $S -game demo-genshin -category collect materials list

Step "9. planner export (backup) and seed import"
& $ctl -server $S -game demo-genshin planner export
Write-Host ""
Write-Host "Save a backup:    ctl -server $S -game demo-genshin planner export > backup.json"
Write-Host "                  (wrap as {""upsert"":true,""data"":<backup>} to import it elsewhere)"
Write-Host "Try the seed:     ctl -server $S -data @examples/planner_seed_demo.json planner import"
Write-Host "                  (requires the demo-genshin game; run dry first by editing dry_run to true)"

# --- From recommendation to automation (commands shown, not executed) ---------
# These need a real route file (e.g. from a bettergi-scripts-list clone), so
# this demo only prints them.
Step "10. next steps with real data (not executed)"
Write-Host "  ctl -server $S -data @route.json routes add        # route.json: {game_id, name, file_path -> real route file, route_type}"
Write-Host "  ctl -server $S -data @recommend.json planner recommend   # re-run; now matches the route"
Write-Host "  ctl -server $S planner create-task <recommendation-id>   # 400 if the rec has no route"
Write-Host "  ctl -server $S planner create-plan <recommendation-id>"
Write-Host "  ctl -server $S planner complete <recommendation-id>"
Write-Host ""
Write-Host "Cleanup when done:  ctl -server $S games delete demo-genshin   (cascades the demo game's planner data)"
