# Go ↔ Native Controller 进程协议(NC6 设计草案,DRAFT)

> 状态:**草案**。NC6 实施前可修订;本文档只固定已经确定的部分,
> 开放问题见文末。实现不属于 NC6 之前的工作(ROADMAP 顺序)。

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
  "v": 1,                  // 协议版本,整数;不匹配即 Fail-fast
  "seq": 42,               // 发送方单调序号
  "ts": "2026-09-10T08:00:00.500+08:00",
  "type": "EVENT",         // HELLO | READY | EVENT | LOG | RESULT | PING | PONG
  "payload": { }
}
```

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
│         EVENT(state/evidence 变化) │  ← 每 cycle 或状态转移
│ ◀───────────────────────────────── │
│                            RESULT  │  ← 终态:done|failed|stopped|timeout
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

## 关键 payload(节选,完整 schema 在 NC6 落地时用 JSON Schema 固化)

- `READY`: `{ session_id, skill, manifest { name, version, imgsz, labels }, backend }`
- `EVENT`: `{ cycle, state, evidence { probes_fired[], detections[{label,cx,cy,w,h,conf}] },
   planned_actions[] }` ——与 `--session-log` TSV 同源信息,机器可读版
- `RESULT`: `{ outcome: done|failed|stopped, state, cycles, inference_count,
   cache_hits, error? }`

## 复用现有 runner 基建

launch/preflight、stdout/stderr 捕获、退出码、执行日志 retention、失败
截图(`--debug-dir`)、超时/取消、stats——全部按 external 任务同一套;
Go 侧新增的只有:native 任务类型 + controller 参数拼装 + EVENT→Execution
事件的映射。

## 开放问题(NC6 决定)

1. EVENT 粒度与限流(每 cycle vs 仅状态变化;SSE 前置缓冲)。
2. 截图/调试帧回传:stdout 内联(base64,受 1MiB 约束)vs debug 目录
   文件路径引用(倾向后者)。
3. controller 长驻复用(一个会话跑多个 skill)还是一次一进程(倾向后者,
   与现有超时/取消语义零冲突)。
4. `executor = native | external | auto` 的配置进入点与优先级回退。
