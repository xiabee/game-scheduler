# 🎮 game-scheduler · 多游戏资源收集与路线调度器

> 🌏 **English docs: [README_EN.md](README_EN.md)** · 中文文档如下。

[![CI](https://github.com/xiabee/game-scheduler/actions/workflows/ci.yml/badge.svg)](https://github.com/xiabee/game-scheduler/actions/workflows/ci.yml)
[![Security](https://github.com/xiabee/game-scheduler/actions/workflows/security.yml/badge.svg)](https://github.com/xiabee/game-scheduler/actions/workflows/security.yml)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go)](go.mod)

一个**多游戏「资源收集与路线」调度器**:把你已经安装好的开源自动化工具，作为普通本地进程统一编排、定时与监控。

> ### 🔗 项目地址 → **<https://github.com/xiabee/game-scheduler>**

> ⚠️ **范围与安全边界（务必阅读）**
> 本项目**只是一个调度器**。它**不**作弊、**不**注入代码、**不**读写游戏内存、**不**抓包改包、**不**实现任何反检测能力。
> 每个被支持的工具都只当作一个**不透明的本地子进程**来启动（`os/exec`）——本项目只决定 **跑什么、什么时候跑**，并记录结果。
> 工具本身（BetterGI、March7thAssistant/Fhoe-Rail、ok-wuthering-waves、M9A）**不随本项目分发**，需你自行安装、配置、并对其使用合规性负责。

---

## ✨ 功能一览

- **四游戏适配器**:原神 / 崩铁 / 鸣潮 / 重返未来1999，各自把任务翻译成对应工具的命令行。
- **SQLite 存储**:游戏、任务、路线、计划、执行日志。
- **手动触发 + 定时任务**(标准 5 段 cron，或 `@daily`、`@every 6h` 等)。
- **串行执行队列**:这些工具共用鼠标/键盘/前台窗口,默认 `max_concurrent=1` 串行,绝不互相抢屏。
- **完整执行日志**:stdout / stderr / 退出码 / 起止时间;失败时另记错误、**截图路径**、重试次数。
- **崩溃自愈**:进程意外退出后,残留的 `running/pending` 执行在启动时被重置为 `failed`。
- **进程树终止**:取消/超时会用 `taskkill /T` 杀掉整棵子进程树,避免工具残留控制游戏。
- **控制看板**(类 Grafana):实时推送、增删改查、执行历史、失败截图缩略图——见下文。
- **REST API + CLI**(`ctl`),**可选令牌鉴权**。
- **持续集成 + 定期安全扫描**:Linux/Windows 双平台测试、`-race`、`govulncheck`、Dependabot。

---

## 🧩 需要安装的自动化工具（必装，否则跑不起来）

调度器本身**不含**任何工具或游戏。请自行安装各工具,再把游戏的 `tool_path` / `working_dir` / `extra_config` 指向它。崩铁两个工具需要 `python`。

| 游戏 | 工具 | 🔗 下载地址 | 调度器需要配置 |
|------|------|-------------|----------------|
| 🌸 **原神** | BetterGI | **<https://github.com/babalae/better-genshin-impact/releases>** | `tool_path` → `BetterGI.exe`;脚本用 [**bettergi-scripts-list**](https://github.com/babalae/bettergi-scripts-list)(在 BetterGI 内订阅) |
| 🚂 **崩铁** | March7thAssistant | **<https://github.com/moesnow/March7thAssistant/releases>** | `extra_config.march7th_dir` → 项目目录(需 Python 3.12+) |
| 🚂 **崩铁** | Fhoe-Rail | **<https://github.com/linruowuyin/Fhoe-Rail>** | `extra_config.fhoe_dir` → 项目目录(也有打包版 `Fhoe-Rail.exe`,可用 `exe`/`raw_args` 指过去) |
| 🌊 **鸣潮** | ok-wuthering-waves | **<https://github.com/ok-oldking/ok-wuthering-waves/releases>** | 用 setup 安装后,`tool_path` → `ok-ww.exe` |
| 🕰️ **1999** | M9A | **<https://github.com/MAA1999/M9A/releases>** | 下 **PiCLI** 版,`tool_path` → `MaaPiCli.exe`、`working_dir` → M9A 目录;[CLI 用法](https://github.com/MAA1999/M9A/blob/main/docs/zh_cn/manual/MaaPiCli.md) |

> 装完后用 **`ctl tasks preflight <任务id>`**(或 `GET /api/tasks/{id}/preflight`)验证:它会拼出实际命令、检查可执行文件是否存在并给出 `ready` 标志,**不会真的启动游戏**。

> 🔍 **不知道工具装在哪?** 用**扫描**功能自动定位:看板头部「扫描」按钮、`ctl discover [-paths "F:/Games;D:/Tools"]`、或 `POST /api/discover`。它会在磁盘里查找上述可执行文件/项目目录(只读、不执行任何东西),找到后在看板里可**一键填入新游戏**。留空路径则扫描所有磁盘;指定路径更快。

---

## 🏗️ 架构

```
cmd/server          REST API + cron 调度器(常驻进程)
cmd/ctl             REST API 的命令行客户端
internal/config     服务端配置(JSON 文件 + 环境变量)
internal/store      SQLite 持久化(纯 Go 的 modernc.org/sqlite)
internal/runner     启动外部工具,采集 stdout/stderr/退出码
internal/task       编排:适配器 → 命令 → runner,串行队列、重试、截图
internal/scheduler  robfig/cron 引擎,把计划绑到任务
internal/events     轻量事件总线(给看板做实时推送)
internal/game       Adapter 接口 + 注册表
internal/game/{genshin,hsr,wuwa,r1999}   各工具的命令构建器
internal/api        net/http 的 JSON REST + 看板 + SSE 实时流
```

数据流:**计划(cron)** 或**手动触发** → 运行某个**任务**;任务所属**游戏**选定一个**适配器**;适配器把任务翻译成命令行;`runner` 执行,并把
`command / stdout / stderr / exit_code / start_time / end_time` 以及失败时的 `error_msg / screenshot_path / retry_count` 记成一条**执行记录**。

---

## 🚀 构建与运行

```powershell
go build -o bin/server.exe ./cmd/server
go build -o bin/ctl.exe    ./cmd/ctl
```

需要 Go 1.26.4+。无需 cgo(SQLite 驱动是纯 Go)。

```powershell
# 默认用 ./data 存放 db/日志/截图,监听 127.0.0.1:8080
./bin/server.exe

# 或指定配置文件 / 覆盖监听地址
./bin/server.exe -config config.json -addr 127.0.0.1:8080
```

配置优先级:默认值 → `config.json`(见 `config.example.json`) → 环境变量
(`GS_ADDR`、`GS_DATA_DIR`、`GS_DB_PATH`、`GS_SCREENSHOT_CMD`、`GS_MAX_CONCURRENT`、`GS_AUTH_TOKEN`、`GS_MONITOR_ENABLED`、`GS_CPU_THRESHOLD`、`GS_MEM_THRESHOLD`、`GS_MONITOR_INTERVAL_SEC`、`GS_OVERLOAD_POLICY`、`GS_NOTIFY_CMD`) → `-addr` 参数。

### 并发(重要）

`max_concurrent`(默认 **1**)限制同时运行的执行数。这些工具都要操作**共用的鼠标/键盘和前台窗口**,同时跑两个会互相抢屏。默认情况下第二次触发会**排队**(记为 `pending`),等当前这次跑完才开始。只有当你的执行确实跑在**相互独立的机器/虚拟机**上时才需要调大。

### 失败截图

`screenshot_cmd` 是可选的、尽力而为的观测钩子,任务失败时执行,`{{.Path}}` 会被替换为目标 PNG 路径。它**不碰游戏**,只是抓个屏方便排查。Windows 全屏示例:

```json
{
  "screenshot_cmd": "powershell -NoProfile -Command \"Add-Type -AssemblyName System.Windows.Forms,System.Drawing; $b=[System.Windows.Forms.SystemInformation]::VirtualScreen; $bmp=New-Object System.Drawing.Bitmap($b.Width,$b.Height); $g=[System.Drawing.Graphics]::FromImage($bmp); $g.CopyFromScreen($b.Location,[System.Drawing.Point]::Empty,$b.Size); $bmp.Save('{{.Path}}')\""
}
```

不设置时,路径仍会被记录(布局可预期),但不会真的生成图片。命令通过 `cmd /S /C` 执行,因此含引号的命令(如上面的 PowerShell 一行)也能正确运行。

---

## 🔐 鉴权

默认 API **无鉴权**——仅在绑定到 localhost(默认)时才安全。要对外暴露,请用 `auth_token`(配置)或 `GS_AUTH_TOKEN` 设置令牌。设置后,所有 `/api/*` 与 `/screenshots/*` 都需令牌;看板页面(`/`)与 `/healthz` 保持开放。

- **API / CLI**:发送 `Authorization: Bearer <令牌>`(CLI:`ctl -token <令牌> …`,或 `GS_TOKEN`)。
- **浏览器**:看板首次遇到 401 会弹框输入令牌并存入 `localStorage`;🔑 按钮可随时设置/修改。实时流通过 `?token=` 鉴权(浏览器的 EventSource 无法发自定义头)。

> 令牌只是**单一共享密钥**,适合可信局域网。对外网/多用户,建议在前面再挂一个做 TLS + 真实认证的反向代理。

---

## 📊 控制看板

浏览器打开服务器地址 —— **<http://127.0.0.1:8080/>** —— 即是一个类 Grafana 的控制看板。它是一个内嵌单页(无需任何前端构建),通过 **Server-Sent Events**(`GET /api/stream`)**实时更新**:任何变化(开始/结束一次运行、开关计划、增删游戏/任务)服务器都会推一份新快照。若 SSE 被代理拦截,会自动退化为轮询。加 `?live=0` 可得静态快照。

![控制看板](docs/dashboard.png)

它展示:

- **顶部统计**:游戏/任务/计划总数、正在运行数、近 24h 失败数(非零标红)。
- **每游戏一张卡片**,带彩色健康灯:`ok`(绿,上次成功)、`error`(红,上次失败并显示错误)、`running`(蓝色脉冲)、`warn`(已取消)、`idle`(灰,无记录)。卡片含最近运行+相对时间、下次计划、任务/计划数、成功/失败计数、禁用标记。
- **每任务「运行」按钮**:`POST /api/tasks/{id}/run`,触发后看板刷新显示排队/运行。
- **每计划开关**:就地启停 `PUT /api/plans/{id}`,调度器重载、下次运行时间实时更新。
- **执行详情弹窗**:点最近记录的任意一行(或卡片上的状态徽章),查看完整命令、错误、**stdout/stderr**、退出码、时间,以及失败**截图缩略图**(由 `/screenshots/` 提供)。若仍在运行,弹窗内有**取消**按钮(`POST /api/executions/{id}/cancel`,会杀整棵进程树)。
- **完整增删改**:头部 **+ 游戏 / + 任务** 按钮,以及每张卡上的 **+ 添加 / ✎ 编辑 / ✕ 删除**,无需命令行即可管理游戏/任务/计划(适配器与任务类型下拉来自 `GET /api/meta`)。
- **执行历史**:**历史** 按钮打开可筛选的历史视图(按状态、限制条数),点行进详情弹窗。
- **资源监控面板**:顶部用**环形仪表**实时显示本机 **CPU / 内存 / 磁盘** 使用率,各带**历史曲线**(随 SSE 持续刷新);超过阈值时变红并弹出**过载横幅**(见下节)。

---

## 🖥️ 资源监控与过载保护

服务器内置一个轻量监控,按 `monitor_interval_sec`(默认 3 秒)采样本机 **CPU / 内存 / 磁盘**,在看板顶部用**环形仪表 + 历史曲线(sparkline)**实时展示(磁盘取数据目录所在分区,仅作展示、不参与过载判定)。这些工具会吃满 CPU/内存,机器过载时自动化容易出错、卡死甚至连环失败——本功能用来**防止资源过载**。

- **阈值**:`cpu_threshold` / `mem_threshold`(默认各 90%)。连续 2 次采样超过阈值才判定**过载**(去抖,避免瞬时尖峰误报);掉回阈值以下立即解除。
- **过载策略** `overload_policy`:
  - `alert`(默认):只在看板**红色横幅提醒**(`⚠ 资源过载:…`),不干预任务。
  - `pause`:在此基础上,**过载期间跳过新的定时任务**(调度器记日志并在看板标注「已暂停定时任务」),手动触发不受影响;资源回落后自动恢复。
- 纯只读观测 + 调度闸门,**不碰游戏或工具**;只看 CPU/内存(`gopsutil`),不读进程内存。
- 相关配置:`monitor_enabled`、`cpu_threshold`、`mem_threshold`、`monitor_interval_sec`、`overload_policy`(对应环境变量 `GS_MONITOR_ENABLED`、`GS_CPU_THRESHOLD`、`GS_MEM_THRESHOLD`、`GS_MONITOR_INTERVAL_SEC`、`GS_OVERLOAD_POLICY`)。实时数据也在 `GET /api/dashboard` 的 `resource` 字段中。

---

## 🔔 通知提醒(notify_cmd)

看板上的红色横幅只有盯着屏幕才看得到。配置 `notify_cmd`(或 `GS_NOTIFY_CMD`)后,在**任务失败**和**资源过载**时会执行一条你指定的命令,把提醒推送出去(Windows 通知、企业微信/钉钉机器人、Bark、ServerChan、webhook 等都行)。

模板字段(均已**净化 shell 特殊字符**,防止动态文本破坏命令或注入):`{{.Event}}`(如 `task_failed` / `overload`)、`{{.Title}}`、`{{.Message}}`。

示例 —— Windows 弹原生通知(需 PowerShell 模块 `BurntToast`):
```json
{ "notify_cmd": "powershell -NoProfile -Command \"New-BurntToastNotification -Text '{{.Title}}','{{.Message}}'\"" }
```
示例 —— 推送到 webhook / Bark:
```json
{ "notify_cmd": "curl -s -X POST https://example.com/notify -d \"event={{.Event}}&title={{.Title}}&msg={{.Message}}\"" }
```
不设置时不发任何通知。命令尽力而为执行,失败只记日志,**不会影响任务本身**。

---

## 🖥️ 命令行(`ctl`)

> 全局参数(`-server`、`-token`、`-data`、`-game` …)必须放在**资源/动作之前**,例如 `ctl -server http://... -data '{...}' games add`。服务器开启鉴权时传 `-token`(或 `GS_TOKEN`)。

```
ctl [-server URL] [-token T] <资源> <动作> [id]

games   list | get <id> | add | update <id> | delete <id>
tasks   list [-game id] | get <id> | add | update <id> | delete <id> | run <id> | preflight <id>
routes  list [-game id] | add | delete <id>
plans   list | get <id> | add | update <id> | delete <id>
execs   list [-task id] [-status s] [-limit n] | get <id> | cancel <id>
health
```

`add`/`update` 从 `-data '<json>'` 或 `-data -`(stdin)读取请求体。`-server` 默认 `$GS_SERVER` 或 `http://127.0.0.1:8080`。

---

## 🌐 REST API

| 方法与路径 | 用途 |
|---|---|
| `GET /healthz` | 存活探针 + 已注册适配器 |
| `GET/POST /api/games`、`GET/PUT/DELETE /api/games/{id}` | 游戏增删改查 |
| `GET/POST /api/tasks`、`GET/PUT/DELETE /api/tasks/{id}` | 任务增删改查(`?game_id=`) |
| `POST /api/tasks/{id}/run` | **手动触发**(返回 pending 执行) |
| `GET /api/tasks/{id}/preflight` | 拼命令并检查工具是否存在,**不运行** |
| `GET/POST /api/routes`、`DELETE /api/routes/{id}` | 路线(`?game_id=`) |
| `GET/POST /api/plans`、`GET/PUT/DELETE /api/plans/{id}` | 定时计划(cron 会校验) |
| `GET /api/executions`、`GET /api/executions/{id}` | 执行日志(`?task_id=&status=&limit=`) |
| `POST /api/executions/{id}/cancel` | 取消运行中的执行 |
| `GET /api/dashboard` | 看板聚合快照(各游戏健康、计划、最近执行) |
| `GET /api/stream` | SSE 实时推送看板更新 |
| `GET /api/meta` | 适配器键 + 任务类型(给表单用) |
| `POST /api/discover` | 扫描磁盘查找工具可执行文件(body 可选 `{"paths":[...],"max_depth":N}`) |
| `GET /screenshots/{name}` | 取失败截图(受鉴权保护) |

---

## ⚙️ 各适配器的任务配置

每个任务有一个 `type`(由适配器解释)和一个 `params`(JSON 字符串)。所有适配器都提供两个万能逃生口:

- `"exe": "<路径>"` —— 覆盖本任务的可执行文件。
- `"raw_args": ["...", "..."]` —— 直接给出**完整命令行参数**,绕过适配器默认值。**当你的工具版本参数和文档默认值不一致时就用它**(各工具 CLI 参数会随版本变)。

### 🌸 genshin(BetterGI) —— `tool_path = BetterGI.exe`
| type | params | 默认参数 |
|---|---|---|
| `onedragon` | `{"group":"<名>"}`(可选) | `--startOneDragon [--group <名>]` |
| `config_group` | `{"group":"<名>"}` | `--startGroup <名>` |
| `script` | `{"script":"<名或路径>"}` | `--script <名>` |
| `raw` | `{"raw_args":[...]}` | 原样 |

### 🚂 hsr(March7thAssistant / Fhoe-Rail) —— Python 项目
`extra_config`:
```json
{ "python_path":"python",
  "march7th_dir":"C:/.../March7thAssistant", "march7th_entry":"main.py",
  "fhoe_dir":"C:/.../Fhoe-Rail", "fhoe_entry":"main.py" }
```
| type | params | 运行 |
|---|---|---|
| `march7th_daily` | — | `python <march7th_dir>/main.py` |
| `fhoe_route` | `{"route":"<文件或名>"}`(可选) | `python <fhoe_dir>/main.py [--route ...]` |
| `raw` | `{"raw_args":[...]}` | `python <raw_args...>` |

### 🌊 wuwa(ok-wuthering-waves) —— `tool_path = ok-ww.exe`
| type | params | 默认参数 |
|---|---|---|
| `task` | `{"task_index":N,"exit":true}` | `-t N [-e]` |
| `farm`(预留 RouteFarmTask) | `{"task_index":N,"route":"<名>","exit":true}` | `-t N [-r <route>] [-e]` |
| `raw` | `{"raw_args":[...]}` | 原样 |

### 🕰️ r1999(M9A MaaPiCli) —— `tool_path = MaaPiCli.exe`、`working_dir = M9A 项目目录`
| type | params | 默认参数 |
|---|---|---|
| `run` | `{"config":"<名>"}`(可选) | `[-c <名>]` |
| `config` | `{"config":"<名>"}` | `-c <名>` |
| `raw` | `{"raw_args":[...]}` | 原样 |

---

## 📝 完整示例

```powershell
$S = "http://127.0.0.1:8080"

# 1. 注册游戏（tool_path 指向你装好的 BetterGI）
ctl -server $S -data '{"id":"genshin","name":"原神","adapter":"genshin","tool_path":"D:/BetterGI/BetterGI.exe","enabled":true}' games add

# 2. 定义任务（跑一条龙配置组）
ctl -server $S -data '{"game_id":"genshin","name":"每日采集","type":"onedragon","params":"{\"group\":\"采集\"}","max_retries":1,"retry_delay_sec":30,"timeout_sec":3600,"enabled":true}' tasks add

# 3. 立刻运行
ctl -server $S tasks run 1

# 4. 每天 06:00 定时（5 段 cron，也支持 @daily / @every 6h）
ctl -server $S -data '{"name":"原神晨间","task_id":1,"cron_expr":"0 6 * * *","enabled":true}' plans add

# 5. 查看日志
ctl -server $S execs list -task 1
```

---

## ⏰ cron 格式

标准 5 段 cron(`分 时 日 月 周`),外加 robfig/cron 描述符(`@hourly`、`@daily`、`@weekly`、`@every 6h` …)。时间用服务器本地时区。创建/更新时会校验表达式。

---

## 🧪 开发与持续集成

```powershell
go test ./...        # 单元 + 集成测试(store / runner / 适配器 / task / api)
go test -race ./...  # 竞态检测(Linux/macOS,或装了 C 工具链的 Windows)
gofmt -l .           # 应当无输出
go vet ./...
```

- **CI**([.github/workflows/ci.yml](.github/workflows/ci.yml)):gofmt + vet,在 **Linux 与 Windows** 双平台构建+测试(让 `*_windows.go` 在真 Windows 上被检查),并在 Linux 上跑 `-race`。
- **安全**([.github/workflows/security.yml](.github/workflows/security.yml)):每次推送/PR + **每周定时**跑 [`govulncheck`](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck) 扫描依赖与标准库漏洞。
- **Dependabot**([.github/dependabot.yml](.github/dependabot.yml)):每周自动为 Go 依赖与 GitHub Actions 提更新 PR。

---

## 🔒 安全说明

- 本项目**不实现任何**注入/内存读写/抓包/反检测能力;外部工具一律按子进程启动。
- 任务的 `raw_args` / `exe` 可执行任意命令——这是本工具的**核心用途**(运行你配置的外部工具)。因此:**开启鉴权后**才暴露 API,**令牌不要泄露**;能调用 API 的人即可在本机运行命令。
- `/screenshots/` 做了路径穿越防护(只接受纯文件名);令牌比较用常量时间;SQL 全部参数化。
- 漏洞由 `govulncheck`(CI + 定时)与 Dependabot 持续监控。

---

## ⚠️ 已知限制(MVP)

- 各工具 CLI 参数随版本变化;适配器默认值是常见写法的最佳猜测——拿不准就用 `raw_args` 写死。
- API 鉴权是单一共享令牌;多用户/公网请用反向代理做真正的认证 + TLS。
- SQLite 为简化使用单写连接(WAL 模式)。
