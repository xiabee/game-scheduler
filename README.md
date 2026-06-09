# game-scheduler

A multi-game **resource-collection & route scheduler** that orchestrates existing
open-source automation tools as ordinary local processes.

> ⚠️ **Scope / safety boundary.** This project is *only* an orchestrator. It does
> **not** cheat, inject code, read or write game memory, intercept or forge
> packets, or implement any anti-detection behaviour. Every supported tool is
> launched as an opaque child process via `os/exec`; the scheduler just decides
> *what to run, when*, and records the result. The tools themselves (BetterGI,
> March7thAssistant/Fhoe-Rail, ok-wuthering-waves, M9A) are not bundled — you
> install and configure them yourself, and point the scheduler at their paths.

## Supported games & tools

| Game | Adapter key | External tool | Notes |
|------|-------------|---------------|-------|
| 原神 Genshin Impact | `genshin` | BetterGI (+ bettergi-scripts-list) | map tracing, auto-pickup, gathering, mining, 锄地 |
| 崩坏：星穹铁道 Honkai: Star Rail | `hsr` | March7thAssistant / Fhoe-Rail | 锄大地, pre-recorded routes, combat, loot |
| 鸣潮 Wuthering Waves | `wuwa` | ok-wuthering-waves (`ok-ww.exe`) | one-click dailies, background auto-battle, echo farming, reserved `farm` (RouteFarmTask) |
| 重返未来：1999 Reverse: 1999 | `r1999` | M9A (`MaaPiCli.exe`) | 收荒原, 每日心相, 常规作战, 活动刷取 |

## Install the automation tools (required to actually run anything)

The scheduler is only an orchestrator — it ships none of the tools or games.
Install each tool yourself, then point a game's `tool_path` / `working_dir` /
`extra_config` at it. `python` is needed only for the HSR tools.

| Game | Tool | Source | What the scheduler needs |
|------|------|--------|--------------------------|
| 原神 | BetterGI | https://github.com/babalae/better-genshin-impact/releases | `tool_path` → `BetterGI.exe`. Scripts via [bettergi-scripts-list](https://github.com/babalae/bettergi-scripts-list) (subscribed inside BetterGI). |
| 崩铁 | March7thAssistant | https://github.com/moesnow/March7thAssistant/releases | `extra_config.march7th_dir` → project dir (`python` 3.12+). |
| 崩铁 | Fhoe-Rail | https://github.com/linruowuyin/Fhoe-Rail | `extra_config.fhoe_dir` → project dir. (Packaged build also exposes `Fhoe-Rail.exe`; point `exe`/`raw_args` at it if you prefer.) |
| 鸣潮 | ok-wuthering-waves | https://github.com/ok-oldking/ok-wuthering-waves/releases | install via the setup `.exe`, then `tool_path` → `ok-ww.exe`. |
| 1999 | M9A | https://github.com/MAA1999/M9A/releases | download the **PiCLI** build; `tool_path` → `MaaPiCli.exe`, `working_dir` → M9A dir. CLI usage: [MaaPiCli.md](https://github.com/MAA1999/M9A/blob/main/docs/zh_cn/manual/MaaPiCli.md). |

After installing, verify each game with `ctl tasks preflight <id>` (or
`GET /api/tasks/{id}/preflight`) — it reports the resolved command, whether the
executable exists, and a `ready` flag, without launching the game.

## Architecture

```
cmd/server          REST API + cron scheduler (long-running process)
cmd/ctl             CLI client for the REST API
internal/config     server settings (JSON file + env overrides)
internal/store      SQLite persistence (pure-Go modernc.org/sqlite)
internal/runner     spawns an external tool, captures stdout/stderr/exit code
internal/task       orchestration: adapter -> command -> runner, retries, screenshots
internal/scheduler  robfig/cron engine binding plans to tasks
internal/game       Adapter interface + registry
internal/game/{genshin,hsr,wuwa,r1999}   per-tool command builders
internal/api        net/http JSON REST handlers
```

Data flow: a **Plan** (cron) or a **manual trigger** runs a **Task**; the task's
**Game** selects an **Adapter**; the adapter turns the task into a command line;
`runner` executes it and an **Execution** row records
`command / stdout / stderr / exit_code / start_time / end_time` plus
`error_msg / screenshot_path / retry_count` on failure.

## Build

```powershell
go build -o bin/server.exe ./cmd/server
go build -o bin/ctl.exe    ./cmd/ctl
```

Requires Go 1.26+. No cgo (SQLite driver is pure Go).

## Run the server

```powershell
# uses ./data for the SQLite db, logs and screenshots; listens on 127.0.0.1:8080
./bin/server.exe

# or with a config file / overrides
./bin/server.exe -config config.json -addr 127.0.0.1:8080
```

Configuration precedence: defaults → `config.json` (see `config.example.json`) →
environment (`GS_ADDR`, `GS_DATA_DIR`, `GS_DB_PATH`, `GS_SCREENSHOT_CMD`,
`GS_MAX_CONCURRENT`) → `-addr` flag.

### Concurrency (important)

`max_concurrent` (default **1**) bounds how many executions run at once. The
supported tools all drive the shared mouse/keyboard and foreground window, so
running two at the same time would make them fight over the screen. With the
default, a second trigger is **queued** (recorded as `pending`) and starts only
when the current run finishes. Raise this only if your executions target
genuinely independent machines/VMs.

### Failure screenshots

`screenshot_cmd` is an optional, best-effort observability hook run when a task
fails. `{{.Path}}` is substituted with the destination PNG. It never touches the
game — it just captures the screen so an operator can see what happened. Example
(Windows full-screen capture):

```json
{
  "screenshot_cmd": "powershell -NoProfile -Command \"Add-Type -AssemblyName System.Windows.Forms,System.Drawing; $b=[System.Windows.Forms.SystemInformation]::VirtualScreen; $bmp=New-Object System.Drawing.Bitmap($b.Width,$b.Height); $g=[System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($b.Location,[System.Drawing.Point]::Empty,$b.Size); $bmp.Save('{{.Path}}')\""
}
```

If unset, the path is still recorded (so the layout is predictable) but no image
is written.

## CLI (`ctl`)

> Global flags (`-server`, `-data`, `-game`, …) must come **before** the
> resource/action, e.g. `ctl -server http://... -data '{...}' games add`.

```
ctl [-server URL] <resource> <action> [id]

games   list | get <id> | add | update <id> | delete <id>
tasks   list [-game id] | get <id> | add | update <id> | delete <id> | run <id> | preflight <id>
routes  list [-game id] | add | delete <id>
plans   list | get <id> | add | update <id> | delete <id>
execs   list [-task id] [-status s] [-limit n] | get <id> | cancel <id>
health
```

`add`/`update` read a JSON body from `-data '<json>'` or `-data -` (stdin).
`-server` defaults to `$GS_SERVER` or `http://127.0.0.1:8080`.

## REST API

| Method & path | Purpose |
|---|---|
| `GET /healthz` | liveness + registered adapters |
| `GET/POST /api/games`, `GET/PUT/DELETE /api/games/{id}` | games CRUD |
| `GET/POST /api/tasks`, `GET/PUT/DELETE /api/tasks/{id}` | tasks CRUD (`?game_id=`) |
| `POST /api/tasks/{id}/run` | **manual trigger** (returns the pending execution) |
| `GET /api/tasks/{id}/preflight` | build the command & check the tool exists, **without running** |
| `GET/POST /api/routes`, `DELETE /api/routes/{id}` | routes (`?game_id=`) |
| `GET/POST /api/plans`, `GET/PUT/DELETE /api/plans/{id}` | scheduled plans (cron validated) |
| `GET /api/executions`, `GET /api/executions/{id}` | execution logs (`?task_id=&status=&limit=`) |
| `POST /api/executions/{id}/cancel` | cancel a running execution |

## Task configuration by adapter

Each task has a `type` (interpreted by the adapter) and a `params` JSON string.
Two universal escape hatches exist on every adapter:

- `"exe": "<path>"` — override the executable for this task.
- `"raw_args": ["...", "..."]` — supply the exact CLI args, bypassing the
  adapter's defaults. **Use this whenever your tool's flags differ from the
  documented defaults** (CLI flags vary across tool versions).

### genshin (BetterGI) — `game.tool_path = BetterGI.exe`
| type | params | default args |
|---|---|---|
| `onedragon` | `{"group":"<name>"}` (optional) | `--startOneDragon [--group <name>]` |
| `config_group` | `{"group":"<name>"}` | `--startGroup <name>` |
| `script` | `{"script":"<name|path>"}` | `--script <name>` |
| `raw` | `{"raw_args":[...]}` | verbatim |

### hsr (March7thAssistant / Fhoe-Rail) — Python projects
`game.extra_config`:
```json
{ "python_path":"python",
  "march7th_dir":"C:/.../March7thAssistant", "march7th_entry":"main.py",
  "fhoe_dir":"C:/.../Fhoe-Rail", "fhoe_entry":"main.py" }
```
| type | params | runs |
|---|---|---|
| `march7th_daily` | — | `python <march7th_dir>/main.py` |
| `fhoe_route` | `{"route":"<file|name>"}` (optional) | `python <fhoe_dir>/main.py [--route ...]` |
| `raw` | `{"raw_args":[...]}` | `python <raw_args...>` |

### wuwa (ok-wuthering-waves) — `game.tool_path = ok-ww.exe`
| type | params | default args |
|---|---|---|
| `task` | `{"task_index":N,"exit":true}` | `-t N [-e]` |
| `farm` *(reserved RouteFarmTask)* | `{"task_index":N,"route":"<name>","exit":true}` | `-t N [-r <route>] [-e]` |
| `raw` | `{"raw_args":[...]}` | verbatim |

### r1999 (M9A MaaPiCli) — `game.tool_path = MaaPiCli.exe`, `game.working_dir = M9A project dir`
| type | params | default args |
|---|---|---|
| `run` | `{"config":"<name>"}` (optional) | `[-c <name>]` |
| `config` | `{"config":"<name>"}` | `-c <name>` |
| `raw` | `{"raw_args":[...]}` | verbatim |

## Example: end-to-end

```powershell
$S = "http://127.0.0.1:8080"

# 1. register a game (point tool_path at your installed BetterGI)
ctl -server $S -data '{"id":"genshin","name":"Genshin","adapter":"genshin","tool_path":"D:/BetterGI/BetterGI.exe","enabled":true}' games add

# 2. define a task (run a 一条龙 config group)
ctl -server $S -data '{"game_id":"genshin","name":"daily-collect","type":"onedragon","params":"{\"group\":\"采集\"}","max_retries":1,"retry_delay_sec":30,"timeout_sec":3600,"enabled":true}' tasks add

# 3. run it now
ctl -server $S tasks run 1

# 4. schedule it daily at 06:00 (5-field cron, also accepts @daily / @every 6h)
ctl -server $S -data '{"name":"genshin-morning","task_id":1,"cron_expr":"0 6 * * *","enabled":true}' plans add

# 5. inspect logs
ctl -server $S execs list -task 1
```

## Cron format

Standard 5-field cron (`min hour dom mon dow`) plus robfig/cron descriptors
(`@hourly`, `@daily`, `@weekly`, `@every 6h`, …). Times use the server's local
timezone. Expressions are validated on create/update.

## Execution lifecycle & safety

- **Serialized.** Runs go through a bounded queue (`max_concurrent`, default 1)
  so input-automation tools never collide on the shared screen.
- **No overlap.** Scheduled fires skip if the same task is still
  pending/running, so a long task on a short cron does not stack up. Manual
  triggers are always queued (explicit operator intent).
- **Cancellable.** `POST /api/executions/{id}/cancel` works whether the run is
  queued or in flight, and kills the **whole child process tree** (taskkill /T
  on Windows) so helper processes don't keep controlling the game.
- **Crash-safe.** On startup, executions left in `pending`/`running` by a
  previous crash are reconciled to `failed` ("interrupted…").

## Notes & limitations (MVP)

- Tool CLI flags differ across versions; adapter defaults are documented best
  guesses — prefer `raw_args` when in doubt.
- No auth on the REST API — bind to localhost (default) or front it yourself.
- SQLite uses a single writer connection (WAL mode) for simplicity.
