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

### Night 2026-09-09 → 2026-09-10（夜班 agent 记录）

- START_COMMIT: 7e1576e（docs: align project goals with native controller roadmap）
- 主线：ROADMAP §3 NC1 — Vision Runtime（NC0 已完成，不重复）
- 计划拆解：真实推理依赖训练管线产出模型（今晚禁下模型/禁 GPU），故按「先运行时地基、再真实推理」推进：
  M1 manifest+降级链 → M2 真实 ONNX 运行时（WinML/ort 选型 + 微型 fixture 模型）→ M3 `tools/vision/` 训练脚手架
- Handoff：XNightOps `2026-09-09/game-scheduler` 验证通过（night/project/workspace/dispatch_at=23:38/git_head=7e1576e 全一致）

| M | 内容 | Verdict | Commit | 测试 |
|---|------|---------|--------|------|
| M1 | NC1 运行时地基：`manifest.rs` schema-v1 模型 manifest（labels/version/imgsz/confidence/game-profile，严格校验+未知字段前向兼容）；`inference.rs` 检测器解析降级链（模型缺失/坏 JSON/非法值→Mock+告警，manifest imgsz/confidence 生效但 CLI 显式旗标优先）；CLI `--manifest-check`（exit 0/2）+ dry-run `--model-path`（空值显式拒绝）；`models/` 约定文档+示例 manifest；`.gitignore` 全局 `*.onnx`（权重不进 Git，§4） | PASS | 53257c1 | 67→86 测试（manifest 11 / inference 7 / CLI 2 新增）；clippy 0；smoke：合法/缺失/非法 manifest + dry-run 降级路径实测 |
| M2 | NC1 真实推理：`onnx.rs` OnnxDetector 走 **WinML**（Windows 内建 ONNX 运行时，CPU 设备，零外部下载、零 GPU）；BGRA→RGB NCHW 预处理；`[1,N,≥6]`(cx,cy,w,h,conf,class) 解码 + manifest 置信度门；推理错误 `take_error` 锁存不静默；resolve() 在 manifest 引用的权重存在时自动升级为 Onnx 源；185 字节常量输出 fixture ONNX（手写 protobuf，无 Python/onnx 工具链，tmp+rename 防竞态，入库保证新克隆确定性） | PASS | 88e8034 | 86→92 测试（6 个 ONNX 运行时含实机 WinML）；实机 dry-run：真实推理 conf=0.90 → client (320,80) = 归一化 (0.5,1/6) 逆变换精确；同宽高比跨分辨率归一化位置/框占比不变（NC1 acceptance 确定性形式）；same-point guard 对常量检测正确触发 |

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

### 夜班收尾（2026-09-09 08:40 close）

- **计划 vs 完成**：ROADMAP §9 今晚全部完成（M1–M6 六个 milestone + M7–M23 十七个跟进加固/验收/文档项），零遗留 milestone。最终 commit `b2938c5`/close `see git log`；本地 master 与 origin/master 同步。
- **最终验证**：LOCAL CI PASS（gofmt/vet/go test 18 包/go build + cargo fmt --check/clippy 0 告警/cargo test 66 绿+1 ignored/cargo build --locked）；REMOTE CI（win-devops）多轮 PASS；`controller-smoke.ps1` PASS；60s soak ×2 干净；`-race` 全绿；`cargo test --release` 全绿。
- **Verdict 汇总**：M1–M3/M5/M6 PASS；M4 PARTIAL（WGC 环境静默，GDI 实测可用）；其余 PASS。唯一 ignored 测试：live resize（DWM 重绘竞态，确定性覆盖在 transform/pipeline 测试）。
- **安全发现**：零输入发送（input 模块 NC4 前不存在）；无注入/无内存读取/无 hooks；`.nightly/` scratch 曾仅被本地 exclude 覆盖（已修入 .gitignore）；会话日志/PNG 均在 ignored 目录。
- **明天最有价值的第一步**：NC1 Vision Runtime。前置：①物理 console 登录后复验 WGC（`--capture-monitor`，当前 RDP 会话 BLOCKED）；②训练管线 `tools/vision/` 产出首个 nano ONNX 模型（§4，权重不进 Git）；③（可选）win-devops 安装 Rust 工具链使 remote acceptance 覆盖 controller。

### 环境发现（重要，供后续夜班复用）

1. **本机当前经 RDP 会话运行**（console 处于锁屏，LogonUI 活跃）。GDI CopyFromScreen / PrintWindow 在 RDP 会话内正常。
2. **WGC（Windows.Graphics.Capture）BLOCKED——环境定性完成（3 次实现尝试 + 4 项诊断）**：对探针窗口、外进程窗口、主显示器三种目标，`StartCapture` 成功但 `FrameArrived` 永不触发（恒 0），`TryGetNextFrame` 恒 S_OK+null；硬件 D3D11 正常；**`RequestAccessAsync(Programmatic)` 返回 `AppCapabilityAccessStatus(4)=Allowed`——同意/管控理论被排除**；`CreateForMonitor` 在 release 构建下也开始 E_INVALIDARG（debug 曾成功，API 层亦不稳定）。结论：RDP 会话（console 锁屏，LogonUI 活跃）的 DWM 不向 WGC 供帧，属环境限制而非代码缺陷。诊断已内建：`--capture-monitor` 会打印 access status；`WgcCapture::frames_arrived()`；auto 回退链 WGC→GDI→synthetic。**WGC 复验条件：物理 console 登录（非 RDP）后重跑 `--capture-monitor`。**
3. WDAC 拦截上夜未再现（go test 直跑全绿）；`ci-local.ps1` 的退避重试逻辑保留。
4. `-race` 走 `ci-local.ps1 -Race`（D:\tools\mingw64）未在本次使用。
| M3 | `tools/vision/` 训练脚手架（ROADMAP §4，NC1 模型来源前置）：`prepare_dataset.py`（stdlib-only 数据集校验+manifest+train/val 切分）、`train.py`（Ultralytics 封装，PLAN gate：无 `--yes` 只打印计划含将下载的基模型，device 默认 cpu）、`export_onnx.py`（.pt→ONNX + 生成 controller schema-v1 manifest，闭合训练→导出→运行时链路）；`datasets/README.md`（覆盖矩阵/隐私/红线）；gitignore 补 runs/ 与数据集图片目录 | PASS | a739e1b | 语法/help 全过；合成数据集 e2e（5 图 1 无标注剔除、坏标注拒绝）；plan gate 实测不触发下载/训练 |
| M4 | P1 可靠性复审：`Detector` trait 增加 `take_error()` 钩子（默认 None）——dry-run 每周期上报推理失败（前 3 条打印、汇总计数），全部周期失败 exit 1 不再假报 OK；onnx NCHW 暂存 buffer 复用（真实 imgsz 640 每周期 ~20MB 分配消除）；resolve() 消除 manifest 二次读盘；`controller-smoke.ps1` 补 NC1 段（manifest-check 三态门 + 真实 WinML ONNX dry-run；预期失败经 cmd 隔离规避 PS5.1 stderr+Stop 坑） | PASS | 45ddefb | 92 测试绿；clippy 0；`controller-smoke.ps1` 实机全段 PASS；`ci-local.ps1` 全门禁 PASS（33s） |
| M5 | 远端 FAIL 修复（win-devops 首次真实跑 Rust 门禁，暴露环境依赖）：①`window` 三个枚举/DPI 测试加 `interactive_desktop()`（OpenInputDesktop 探针）门控——服务会话（Session 0）EnumWindows 看不到探针窗口、`SetProcessDpiAwarenessContext` 被拒，诚实打印 `skipped:` 而非假失败；②ONNX fixture 降到 `ir_version 3 / opset 9`——节点 WinML 为 Win10 1809 时代 ORT（先拒 ir8，再拒 opset11），Identity@9+ir3 兼容全部 WinML 且现代 ORT 向后兼容 | PASS | 386329a | 本机 92 测试绿；**REMOTE CI (win-devops) PASS exit=0**——节点首次全量跑 Rust 92 测试（含跨机 WinML ONNX 实测）+ Go 门禁 |

### 环境发现（win-devops 节点，2026-09-10）

1. 节点已装 Rust 工具链（cargo 可用）——remote acceptance 从「仅 Go」升级为全量 Go+Rust。
2. 节点以服务上下文跑 CI：无交互桌面（OpenInputDesktop 失败）；EnumWindows 枚举不到自建窗口（直接 HWND 路径正常）；SetProcessDpiAwarenessContext 返回失败。
3. 节点 WinML 的 ONNX Runtime 为 1809 时代：模型要求 ir_version ≤3、opset ≤9；报错形态两种——ir 超版 `Unknown model file format version`、op 超版 `No Op registered for <Op> with domain_version of N`。**训练导出（tools/vision/export_onnx.py）如需覆盖此类节点，opset 应 ≤9。**
| M6 | NMS/IoU + YOLOv8 解码地基：`nms.rs`（IoU 退化安全 + class-aware 贪心 NMS——不同标签互不抑制）；`onnx` 解码布局自动识别——`shape[1] == 4+labels.len()` 判为 channels-first `[1,4+nc,N]`（YOLOv8 导出格式，逐列 argmax 类别分），否则 rows-major `[1,N,≥6]`；两路均过置信度门 + NMS(0.45)；第二个手写 fixture `[1,6,2]` 经真实 WinML 全链路验证 | PASS | 837194c | 92→97 测试；实机 dry-run conf=0.95 解码正确；clippy 0 |
| M7 | ONNX 路径 60s soak + 文档同步：dry-run 60s @15fps 全程真实 WinML 推理（882 周期），工作集 24.0→24.7MB 平坦、句柄 143-144 无泄漏、exit 0 SUMMARY=completed，双检测逐周期解码正确（session TSV 留档 `.nightly/session-onnx-soak.tsv`）；README(中/英) controller 章节更新为 NC1 现状（manifest/WinML/训练脚手架/新 CLI） | PASS | （本行下方 commit） | soak 指标见左；README 双语一致性人工核对 |
| M8 | 推理熔断 + 集成 smoke：dry-run 连续 30 周期推理失败（默认 fps ≈2s）→ `outcome=inference-degraded`、exit 1 结束会话，不再烧完观察预算；smoke 脚本新增 GDI 真实捕获 × ONNX 检测段 | PASS | （本行下方 commit） | 97 测试绿；扩展 smoke 全段 PASS |
| M10 | 离线帧录制/回放（NC2 测试前置）：`replay.rs` —— FrameRecorder（有界编号 PNG）+ ReplayCapture（作为 CaptureBackend，管线零改动复用；循环或终态 WindowGone）；共享 PNG 编解码（BGRA↔RGBA、奇数尺寸 stride 往返测试）；CLI `--record/--record-max/--replay`（互斥校验）；实机：2s GDI 会话录 8 帧真实探针画面 → 经真实 WinML ONNX 检测回放干净完成；smoke 增 record→replay 段（ONNX 段经 cmd 隔离 onnxruntime stderr 噪音避开 PS5.1 Stop 坑） | PASS | db52978 | 97→103 测试；实机 roundtrip PASS；clippy 0 |
| M11 | NC2 L0/L1 分层感知地基：`perception.rs` —— L0 PixelProbe（区域颜色采样+通道容差+匹配比例门+步长限界）、统一 Evidence（probes + template matches）、逐层开关；L1 复用 NC0 NCC matcher，`match_to_detection` 使各层证据同走变换/governor 通路；L2(ONNX)/L3(OCR) 后续接入同一契约 | PASS | 834ec24 | 103→108 测试；clippy 0 |
| M12 | L0 探针接入主管线 + DPI 竞态修复：CycleReport 携带 Evidence；run_cycle 在原始 client 帧上评估 L0（client 像素空间、letterbox 前）；CLI `--probes <json>`（校验加载）；reportable 周期打印探针触发状态；**P1 竞态修复**：SetProcessDpiAwarenessContext 进程级生效与并行建窗竞争导致 client rect 2x 抖动（测试数增长后首次显现）——窗口测试经共享锁串行化 | PASS | （下方 commit） | 108→113 测试；实机 --probes smoke PASS；clippy 0 |
| M13 | NC3 状态机地基：`skill.rs` —— 数据驱动 SkillDefinition（states/expectations/actions/timeout/retries/fallback/terminal，严格 JSON 校验：状态名唯一、转移与 fallback 可解析、终态语义）；Expectation 两形态（L0 探针触发 / 指定标签检测+置信度门）；SkillRunner 纯转移评估器（Waiting/Transitioned+FellBack/Done/Failed，超时预算+有界重试），零输入发送 | PASS | 3e610cf | 113→115 lib 测试（6 个 skill 测试）；clippy 0 |
| M14 | `--skill` 接入 dry-run：SkillRunner 每周期以真实管线证据（L0 探针触发 + client 检测）驱动状态机，转移/回退/DONE/FAILED 打印记录（计划仅记录不发送）；`skill_pipeline` 集成测试：label 期望技能经真实 run_cycle 到达 Done，probe 期望技能在探针不触发时正确 Waiting | PASS | 1b12136 | 115→117 测试；clippy 0 |
| M15 | 示例与文档：`controller/examples/`（probes/skills JSON 示例 + README 双契约文档 + 会话组合示例）；守护测试使示例文件每次测试都被解析验证（防腐化）；README 中英 controller 章节补 NC2/NC3 现状 | PASS | ce19ae9 | 115→117 lib 测试（含 2 个示例守护测试）；clippy 0 |
| M16 | 性能预算守护测试：100 次 L0+L1 evaluate（320x240、2 探针 + 1 模板 stride4）debug 构建均值 <60ms 断言（release ~50x）——把 NC2「CPU 低于预算」验收落成确定性 CI 形式 | PASS | a23e506 | 新增 1 测试；clippy 0 |
| M17 | 打包集成：build.ps1 `-IncludeController` 可选段（cargo release --locked 构建入包 controller.exe + MODELS.md + 示例 manifest，无 cargo 诚实 SKIPPED）；**修复打包脚本在无 tag 仓库的潜在 bug**（git describe stderr 触发 PS5.1 Stop 终止错误，版本探测改经 cmd） | PASS | 29ca064 | 实机端到端打包验证：zip 内含 controller 三件套 |
| M18 | 组合 soak（60s，probes+skill+record 200 帧上限）：762 周期，WS 9.9→10.2MB 平坦、句柄 96-97 无泄漏、exit 0（TSV 留档 `.nightly/session-skill-soak.tsv`） | PASS | （随 M19 批次推送） | 见左 |
| M19 | L1 模板匹配 × 真实录制帧：录制 GDI 探针窗口真实帧（DWM 过渡帧 warmup——过渡全黑帧会污染模板），letterbox 后切取含红盒边缘的 40x40 纹理模板（纯色区域 NCC 退化），L1 层须在全部录制帧中持续命中且与直接 matcher 调用一致；另含 replay→perception 组成测试 | PASS | 29c0500 | 新增 2 集成测试（真实内容对照合成测试）；全套 122 测试绿 |
| M20 | 帧级推理缓存（§5 预算「YOLO 按需触发,不每帧跑」）：`cache.rs` CachingDetector——全帧 FNV-1a 精确哈希，同帧跳过推理（语义等价），32 周期强制刷新兜底，场景缓存 64 条有界，错误不缓存且经 take_error 上报；`Detector for Box<T>` blanket impl 使装饰器可包 trait object；dry-run 汇总输出 inference 次数与 cache hits | PASS | cbf3f44 | 122→126 测试；实机 smoke：合成移动场景 16 推理/0 命中（符合预期，缓存服务静态 UI）；clippy 0 |
### 缓存实测（M20 后续证据）

静态 GDI 探针场景 20s @15fps：77 周期 → **4 次真实 WinML 推理 / 73 次 cache 命中（94.8%）**，exit 0（TSV：`.nightly/session-cache-soak.tsv`）。§5「YOLO 不每帧跑」在真实捕获路径成立。

### Go 侧聚焦复审（03:00 前后）

`internal/runner`（进程树 kill、KILL_ON_JOB_CLOSE、WaitDelay、有界捕获、超时/取消语义）逐行走读：无 P0/P1 发现，注释与实现一致。Go 侧本夜零改动、零回归。
| M22 | 全表面集成 soak（120s，GDI+ONNX+probes+skill+record+cache）：1493 周期 exit 0，WS 27.8→25.5MB 平坦、句柄 143-144；CPU 轮廓：录制期 ~70% 单核（PNG 编码主导），record 达 300 帧上限后降至 ~4%——§5 运行预算达标（TSV：`.nightly/session-full-surface-soak.tsv`） | PASS | （随 M23 批次推送） | 见左 |
| M23 | NC3 轨迹联动：skill 当前状态进入 debug PNG 文件名（`cycle_00030_home.png`），调试目录按序浏览即状态时间线（路径净化） | PASS | （下方 commit） | 实机：2 帧导出文件名含状态 |
| M24 | 回放确定性 + 终态转移 API 修复：新增测试发现「转入终态直接返回 Done、调用方看不到状态名与计划动作」——改为返回 Transitioned{to,planned} 并置 done；回放确定性测试（同录制两次回放，skill 轨迹逐字节一致）使录制会话成为稳定测试夹具 | PASS | （下方 commit） | 126→127 测试；clippy 0 |
