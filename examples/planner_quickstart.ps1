# planner_quickstart.ps1 — character planner v1 acceptance walkthrough (Windows)
#
# Prereqs:
#   1. Server running locally:  .\server.exe   (defaults to http://127.0.0.1:8080)
#   2. ctl.exe on PATH or in the current directory.
#
# This script only talks to the local API with demo data. It contains no real
# accounts, tokens, cookies or machine-specific paths. If your server has an
# auth token configured, add:  -token $env:GS_TOKEN  to every ctl call.
#
# Flow: game -> character -> goal -> material -> requirement -> recommend -> list.

$ErrorActionPreference = "Stop"
$S = "http://127.0.0.1:8080"
$ctl = ".\ctl.exe"   # adjust if ctl is elsewhere

function Step($msg) { Write-Host "`n=== $msg ===" -ForegroundColor Cyan }

Step "0. health check"
& $ctl -server $S health

Step "1. register a demo game (tool_path is a placeholder; preflight will simply report not-ready)"
& $ctl -server $S -data '{"id":"demo-genshin","name":"Demo Genshin","adapter":"genshin","tool_path":"C:/Tools/BetterGI/BetterGI.exe","enabled":true}' games add

Step "2. add a character"
& $ctl -server $S -data '{"game_id":"demo-genshin","name":"DemoChar","role_type":"dps","element":"pyro","tags":["demo"]}' characters add
# note the returned "id" — the demo assumes ids start at 1 on a fresh data dir

Step "3. add a goal for character 1"
& $ctl -server $S -data '{"character_id":1,"name":"Ascend to 90","target_level":90,"priority":5}' goals add

Step "4. add a material"
& $ctl -server $S -data '{"game_id":"demo-genshin","name":"Demo Flower","category":"collect","source_hint":"Mondstadt field","route_type_hint":"collect"}' materials add

Step "5. add a material requirement (goal 1 needs 10x material 1, owns 2)"
& $ctl -server $S -data '{"goal_id":1,"material_id":1,"required_count":10,"owned_count":2,"priority":8}' requirements add

Step "6. generate recommendations for goal 1"
& $ctl -server $S -data '{"goal_id":1,"max_tasks":5}' planner recommend

Step "7. list recommendations (filterable)"
& $ctl -server $S -goal 1 planner recommendations
& $ctl -server $S -game demo-genshin -status open planner recommendations

Step "8. filtered listings added in this release"
& $ctl -server $S -game demo-genshin -status open goals list
& $ctl -server $S -game demo-genshin -category collect materials list

# --- From recommendation to automation (commands shown, not executed) ---------
# The steps below need a real route file (e.g. from a bettergi-scripts-list
# clone), so this demo only prints them.
Write-Host @"

Next steps with real data (not executed by this demo):
  # attach a real route file, then let the planner derive a runnable task/plan:
  ctl -server $S -data '{\"game_id\":\"demo-genshin\",\"name\":\"My Route\",\"file_path\":\"<path-to-route.json>\",\"route_type\":\"collect\"}' routes add
  ctl -server $S -data '{\"goal_id\":1,\"max_tasks\":5}' planner recommend   # now matches the route
  ctl -server $S planner create-task <recommendation-id>                 # 400 if the rec has no route
  ctl -server $S -data '{\"cron_expr\":\"0 9 * * *\"}' planner create-plan <recommendation-id>
  ctl -server $S planner complete <recommendation-id>

Cleanup when done:
  ctl -server $S games delete demo-genshin   # cascades characters/goals/materials of the demo game
"@
