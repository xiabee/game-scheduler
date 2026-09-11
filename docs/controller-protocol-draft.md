# Go ↔ Native Controller 进程协议(NC6 协议,v1 定稿)

> 状态:**v1 定稿(2026-09-10/11 夜班评审)**。信封与载荷 schema 已由
> `controller/src/protocol.rs` 的 serde 类型 + 测试固化(代码即规格,
> 防文档漂移);NC6 实施时如需不兼容改动,必须 bump `v` 并同步更新
> Rust/Go 两侧类型与本文件。Go 侧消费(启动/EVENT→Execution 映射)仍属
> NC6 实施,本文件只冻结线格式。

## 目标

Go 调度器把 native controller 当作一个**普通本地进程**编排(与外部
adapter 同等地位):启动、喂参数、收事件、超时/取消、落 Execution。
不做 IPC 共享内存、不做 cgo 桥接、不重写 Go 后端(§2/§8)。

## 传输

- **stdin/stdout JSON lines**(每行一个 JSON 对象,UTF-8,`\n` 分隔)。
- stderr 仅作人类可读日志(不得承载协议)。
- 选择理由:与现有 runner 的进程模型、`taskkill /T` 进程树清理、
  stdout 捕获基建完全兼容;无端口占用、无防火墙问题。

## 信封

每一行(双向)都是一个信封:

```json
{
  "v": 1,
  "seq": 42,
  "ts": "2026-09-10T08:00:00.500+08:00",
  "type": "EVENT",
  "payload": {
    "cycle": 7,
    "state": "home/menu_open",
    "probes_fired": ["daily_banner"],
    "detections": [{ "label": "button", "cx": 866.0, "cy": 472.0, "w": 120.0, "h": 40.0, "conf": 0.91 }],
    "planned_actions": ["click(button)"]
  }
}
```

- `v`:协议版本,整数;**不匹配即 fail-fast**(收到更高版本的 HELLO,
  Go 侧明确报"协议版本不支持",不得猜测性解析)。
- `seq`:发送方单调递增序号(u64)。
- `ts`:RFC 3339 时间戳字符串。
- `type`:`HELLO | READY | EVENT | LOG | RESULT | PING | PONG`。
- 线格式与 Rust 类型定义一一对应:`controller/src/protocol.rs`
  (`Envelope` / `MessageKind` / 各 Payload 结构,serde 严格解析);
  Go 侧在 NC6 实施时生成对等结构。

## 生命周期(Go → controller 只有两类请求;controller 主动输出事件)

```text
Go                                  controller
│  spawn: args(--skill x.json        │
│   --dry-run|--run 等,经 manifest)  │
│ ─────────────────────────────────▶ │
│                          HELLO(v)  │
│ ◀───────────────────────────────── │
│              READY(session 元数据) │
│ ◀───────────────────────────────── │
│         EVENT(state/evidence 变化) │  ← 仅状态变化时(见决策 D1)
│ ◀───────────────────────────────── │
│                            RESULT  │  ← 终态:done|failed|stopped
│ ◀───────────────────────────────── │
│  (Go: kill 进程树 = cancel)        │
```

- **cancel** = Go 侧现有取消路径(kill 进程树 + PPID 兜底),协议层无
  cancel 消息——进程死亡即取消,RESULT 可能缺失,Execution 记为
  `cancelled`(与 external 任务语义一致)。
- **timeout** 同理:超时由 Go runner 强制,controller 不自知。
- **emergency stop**:controller 内部 governor 已经实现;触发时输出
  `RESULT outcome=stopped reason=...` 并退出(非零 exit code 仅用于
  进程自身故障,业务终止用 RESULT 表达)。

## 关键 payload(schema 已冻结于 protocol.rs)

- `HELLO`: `{ protocol_version, controller_version }`
- `READY`: `{ session_id, skill?, manifest?: { name, version, imgsz[2], labels[] }, backend }`
- `EVENT`: `{ cycle, state, probes_fired[], detections[{label,cx,cy,w,h,conf}],
   planned_actions[] }` ——语义状态变化快照;全量逐周期诊断数据仍走
  `--session-log` TSV,不进协议流(见决策 D1)
- `LOG`: `{ level: info|warn|error, message }`(人类可读日志的结构化旁路,低频)
- `RESULT`: `{ outcome: done|failed|stopped, state, cycles, inference_count,
   cache_hits, error? }`

## 评审决策(2026-09-10/11,原"开放问题"定稿)

- **D1 EVENT 粒度 = 仅语义变化。** EVENT 只在 skill 状态变化(转移/
  回退,含落入终态的那一次——终态由该条 EVENT 宣告,恰好一条)时发出;
  引擎在终态后每周期重复返回的 Done/Failed 不再发 EVENT(2026-09-11
  修复:落终态的转移此前不进闩锁,Done 分支会重复发一次)。15fps 级
  逐周期检测数据不进协议流,全量诊断职责由既有 `--session-log` TSV
  承担。理由:Execution/SSE 事件流不应被感知循环刷屏;两个通道分工
  清晰(协议=语义,TSV=诊断)。
- **D2 截图/调试帧回传 = 文件路径引用。** controller 把 debug 帧写入
  `--debug-dir`,消息里只传路径。理由:stdout 行受内存/管道约束,
  base64 内联既膨胀 33% 又绕开了 runner 已有的截图/日志文件基建。
- **D3 进程模型 = 一次一进程。** 每个 native 任务一个 controller 进程,
  不做长驻复用。理由:与现有 timeout/cancel/进程树清理语义零冲突;
  WinML session 建立开销为百毫秒级,长驻复用引入的状态隔离复杂度
  不值得。
- **D4 `executor` 进入点 = 任务 Params 字段(2026-09-11 定稿)。**
  任务 Params JSON 增加 `"executor":"native"`(缺省/其他值=既有
  external 路径,零迁移成本);配套契约与双保险如下——
  - config:`native_controller_path`(空=native 执行器不可用,任务
    fail-fast 且报错明确)、`native_allow_input`(真实输入的配置级
    总闸,默认 false);
  - 任务 params:`skill`/`probes`/`window`(默认 @probe)/`backend`/
    `model`/`dry_run`(默认 true)/`allow_input`(默认 false)/
    `duration_sec`;
  - 真实输入需要 params.allow_input ∧ !dry_run ∧ config.native_allow_input
    三者同时成立,控制器侧 SafetyGovernor 仍是逐动作最终裁决;
  - RESULT→Execution 映射:done→success,failed/stopped→failed
    (governor stop 属业务终态),cancelled→cancelled,timeout→failed;
    仅进程级故障(SessionError)参与 MaxRetries 重试。
  - Go 侧落地:`internal/native`(协议镜像+会话执行器)、
    `internal/task/native.go`(params 契约+分发)、`cmd/fake-controller`
    (无游戏测试缝)。

## 复用现有 runner 基建

launch/preflight、stdout/stderr 捕获、退出码、执行日志 retention、失败
截图(`--debug-dir`)、超时/取消、stats——全部按 external 任务同一套;
Go 侧新增的只有:native 任务类型 + controller 参数拼装 + EVENT→Execution
事件的映射。
