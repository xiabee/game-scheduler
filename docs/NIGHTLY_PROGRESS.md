# Nightly Progress

## Project Goal

多游戏资源收集与路线调度器。

## Current State

近期落地：终端 rec 防护、非覆盖式 upsert、origin guard、ctl delete/depth。
**2026-09-08 夜班起主线切换至 Native Vision Controller（ROADMAP §9），`controller/` Rust crate 已落地 NC0 全部六个 milestone。**

## Candidate Backlog

- NC1 Vision Runtime（ONNX 推理，训练/运行解耦）
- WGC 在 RDP/管控环境下的验证（见下方环境发现）
- win-devops 节点安装 Rust 工具链，使 remote acceptance 覆盖 Rust 门禁

## Night Runs

### Night 2026-09-08 → 2026-09-09（夜班 agent 记录）

- START_COMMIT: 2828d9a（docs: pivot roadmap to native vision controller）
- 主线：ROADMAP §9 NC0 — Native Controller Foundation，M1–M6 全部完成
- 验收：`cargo test` 51 绿（46 单元 + 4 集成 + doc）；`ci-local.ps1`（含新增 Rust 门禁）PASS；本机实机冒烟全过

| M | 内容 | Verdict | Commit | 测试 |
|---|------|---------|--------|------|
| M1 | `controller/` Rust crate + GameWindow（标题/进程名枚举、精确 client rect、per-monitor-v2 DPI、ClientToScreen、foreground、变化检测） | PASS | 60f0719 | 9 单元 + `--self-probe` 实机（200% DPI 下 512×384 精确、变化检测 OK） |
| M2 | Frame（BGRA8、padded stride）+ Transform（client/normalized/model/desktop 四坐标系、letterbox 及逆变换、rect→desktop 链） | PASS | 1e287da | 8 单元含 **1080p↔1440p 分辨率无关性对拍**、21:9 padding 精确值、随机点往返 ≤0.5px |
| M3 | SafetyGovernor 十条规则（emergency/session/HWND 身份/foreground→Stop；尺寸/DPI/位移→Pause；置信度/限速/同点连击→Skip；retry/state-loop 计数） | PASS | 2bd04e8 | 12 单元，规则全覆盖、时间显式注入无 sleep |
| M4 | CaptureBackend trait + WGC 实现 + GDI PrintWindow 后端 + SyntheticCapture + FpsLimiter | **PARTIAL**（WGC 本节点静默；GDI 实测 PASS） | f50b812 | 38 绿 + `--capture-gdi` 实机 PASS |
| M5 | dry-run 闭环：capture→letterbox→MockDetector→逆变换→governor 授权→overlay→debug PNG；CLI `--dry-run`（auto 后端回退） | PASS | 27aef86 | 50 绿（含 4 集成）+ 实机：探针窗口 16 周期/紧急停止路径、真实 ZCode 窗口 3840×2064×7 周期 |
| M6 | NCC TemplateMatcher 骨架（灰度、stride、亮度偏移不变） | PASS | 8622dfa | 5 单元（唯一纹理定位、亮度鲁棒、退化输入） |

- CI：`scripts/ci-local.ps1` 新增 Rust 门禁（cargo fmt/test/build；**无 cargo 的节点诚实 SKIP 并公告**，win-devops 安装 Rust 前 remote acceptance 仅覆盖 Go 侧）
- REMOTE CI：见下方 Night Runs 追加记录
- 安全：全程零输入发送（input 模块仍为空 stub，NC4 前不存在）；无注入/无内存读取/无 hooks；WGC 与 PrintWindow 均为 OS 提供的捕获 API；无新增危险依赖（windows/png）
- Remaining：NC1 起步、WGC 环境验证、CI 节点 Rust 工具链
- Next：NC1 Vision Runtime 前置——先解决 WGC 验证环境；或按 roadmap 做 `--dry-run` overlay 实机调优

### 环境发现（重要，供后续夜班复用）

1. **本机当前经 RDP 会话运行**（console 处于锁屏，LogonUI 活跃）。GDI CopyFromScreen / PrintWindow 在 RDP 会话内正常。
2. **WGC（Windows.Graphics.Capture）在本会话整体静默**：对探针窗口、外进程窗口（ZCode 3840×2064）、主显示器 `for_primary_monitor` 三种目标，`StartCapture` 成功但 `FrameArrived` 永不触发（`frame_arrived_events=0`），`TryGetNextFrame` 恒 S_OK+null。硬件 D3D11 正常（D3D_DRIVER_TYPE_HARDWARE 创建成功）。疑似 RDP 会话限制或组织管控（该机已有 WDAC 管控记录）。诊断路径已内建：`controller --capture-monitor` / `--capture-foreign <title>` / `WgcCapture::frames_arrived()`；自动回退链 WGC→GDI→synthetic 已在 `--dry-run --backend auto` 生效并如实告警。
3. WDAC 拦截上夜未再现（go test 直跑全绿）；`ci-local.ps1` 的退避重试逻辑保留。
4. `-race` 走 `ci-local.ps1 -Race`（D:\tools\mingw64）未在本次使用。
