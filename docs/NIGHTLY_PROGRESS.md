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
| M7 | NC0 实机 resize 验收自动化（探针窗口画归一化锚定图案；WM_PRINT 根因修复——PrintWindow(PW_CLIENTONLY) 走 WM_PRINT 路径取内容，缺处理器读到陈旧表面） | PASS | 3e71fc6 | 5/5 稳定运行（56 测试） |
| M8 | clippy 门禁：controller 0 告警，ci-local 加 clippy 阶段 | PASS | （本表下方 commit） | clippy --all-targets 0 warning |
| M9 | dry-run 瞬态错误有界退避重试（RetryTracker，100ms×2^n 封顶 2s，5 次）；WindowGone 保持终态；GDI 后端区分可重试 GetDIBits 失败；`now` 写法清理 | PASS | （本表下方 commit） | 58 测试全绿 + gdi 冒烟 |
| M10 | ROADMAP NC0 状态 ✅ + §9 夜班记录 + 下一夜班起点 NC1；README 增 controller/NC0 章节 | PASS | 8b93a24 | 文档一致性核对 |
| M10b | `--list-windows`（find_all 重构）+ dry-run 参数校验（空 window/非法 backend/fps/model/confidence 一律 exit 2 明确报错） | PASS | （见 git log） | 实机：空 window 拒绝、bogus backend 拒绝、list-windows 列出真实窗口 |
| M11 | README_EN 镜像 controller 章节 | PASS | f2d954c | — |
| M12 | `--session-log` TSV 会话日志（每周期一行 + SUMMARY；字段净化；写失败仅告警一次不中断观察）；实测探针窗口检测点精确稳定在 normalized (0.7,0.7) | PASS | 4934c96 前后 | 60 测试全绿 + 实机 TSV 校验；发现：PrintWindow 强制渲染 ~280ms/次(640×480)，FpsLimiter 只节流不保证吞吐 |
| M16 | WGC BLOCKED 定性：`RequestAccessAsync(Programmatic)`=Allowed(4) 排除管控理论；RDP 会话 DWM 不供帧成立；复验条件=物理 console 登录后跑 `--capture-monitor` | PASS | fb265b7 | 诊断内建、60 测试绿 |
| M17 | run_cycle 返回帧，debug 导出不再二次捕获（原先每导出周期多一次全量 PrintWindow） | PASS | 74ae4b6 | 60 测试绿 + PNG 导出复验 |
| M18 | `.nightly/` 从本地 exclude 移入入库 .gitignore（其他克隆/win-devops 不再见到 scratch 噪音）；`--locked` 构建通过；`cargo test --release` 全绿；soak #2 带内存采样：45s 连续捕获工作集 +0.5MB 无泄漏 | PASS | 057e15e | 见左 |
| M19 | 【复审发现】`window_process` 的 OpenProcess 句柄从不关闭——观察循环每周期泄漏一个句柄；已 CloseHandle + 句柄计数回归测试（50 次查找增量 ≤25 容差） | PASS | 76fdb1e | 61 测试绿 |
| M20 | 【复审发现】WgcCapture 无 Drop——重标定丢弃后端时会话可能残留；补 session/pool Close（结构性验证；本机 WGC 静默无法实测帧） | PASS | 83d1a9b | 61 测试绿 + clippy 0 |
| M21 | capture.rs 逐行复审：FrameArrived 注册补 stage 标签；GetMonitorInfoW 失败改用 GetLastError（替代硬编码错误码） | PASS | 5a4b478 | clippy 0 / 61 绿 |
| M22 | build_backend 改返回 Result：重标定期后端构造失败降级 synthetic 并告警（原先 process::exit 会跳过 SUMMARY 硬退）；verdict_notes 封顶 50 条防长会话无界增长；三级回退链保持完整 | PASS | be465c3 | 61 测试绿 + auto 链实机复验（WGC 静默告警→GDI 正常） |
| M23 | CLI 校验矩阵单测（默认值/空 window/backend 白名单/fps-model-confidence 边界/--resize-after 双槽位缺省显式报错）+ export_png 写读往返（BGRA→RGBA） | PASS | b2938c5 | 67 测试全绿 |

- CI：`scripts/ci-local.ps1` Rust 门禁 = cargo fmt --check / **clippy** / test / build；**无 cargo 的节点诚实 SKIP 并公告**（win-devops 安装 Rust 前 remote acceptance 仅覆盖 Go 侧）
| M14 | dry-run resize 重标定修复（check_geometry Pause 后循环曾永远对着旧快照报错；现自动更新 calibrated + 重建后端；`--resize-after` 驱动实测） | PASS | （见 git log） | 实机：resize 后 recalibrated、归一化位置跨重标定保持 0.703/0.703；same-point guard 实测触发 |
| M15 | `ci-local.ps1 -Race` 补实现（MR 文档描述的能力实际缺失；便携 mingw64 提供 CGO）+ soak 218 周期/60s 干净退出 + README CI 门禁说明 | PASS | 11fbdf0 | `-Race` 全绿无 DATA RACE；soak SUMMARY=completed |

- REMOTE CI：本地全绿后 `xnightops ci run game-scheduler --node win-devops` 共 14 轮 **PASS**（exit 0；节点无 Rust，Go 侧验收）
- 已 push：2828d9a..057e15e 全部在远端（分两批推送，无 force）
- 安全：全程零输入发送（input 模块仍为空 stub，NC4 前不存在）；无注入/无内存读取/无 hooks；WGC 与 PrintWindow 均为 OS 提供的捕获 API；无新增危险依赖（windows/png）
- Remaining：NC1 起步（需 ONNX 模型/训练脚手架前置）、WGC 非 RDP 环境复验、win-devops 装 Rust 工具链
- Next：NC1 Vision Runtime；若模型未就绪，次选 `tools/vision/` 训练脚手架或 controller 打包集成

### 环境发现（重要，供后续夜班复用）

1. **本机当前经 RDP 会话运行**（console 处于锁屏，LogonUI 活跃）。GDI CopyFromScreen / PrintWindow 在 RDP 会话内正常。
2. **WGC（Windows.Graphics.Capture）BLOCKED——环境定性完成（3 次实现尝试 + 4 项诊断）**：对探针窗口、外进程窗口、主显示器三种目标，`StartCapture` 成功但 `FrameArrived` 永不触发（恒 0），`TryGetNextFrame` 恒 S_OK+null；硬件 D3D11 正常；**`RequestAccessAsync(Programmatic)` 返回 `AppCapabilityAccessStatus(4)=Allowed`——同意/管控理论被排除**；`CreateForMonitor` 在 release 构建下也开始 E_INVALIDARG（debug 曾成功，API 层亦不稳定）。结论：RDP 会话（console 锁屏，LogonUI 活跃）的 DWM 不向 WGC 供帧，属环境限制而非代码缺陷。诊断已内建：`--capture-monitor` 会打印 access status；`WgcCapture::frames_arrived()`；auto 回退链 WGC→GDI→synthetic。**WGC 复验条件：物理 console 登录（非 RDP）后重跑 `--capture-monitor`。**
3. WDAC 拦截上夜未再现（go test 直跑全绿）；`ci-local.ps1` 的退避重试逻辑保留。
4. `-race` 走 `ci-local.ps1 -Race`（D:\tools\mingw64）未在本次使用。
