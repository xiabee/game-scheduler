# ROADMAP · game-scheduler

> 最后更新:2026-09-08。本文档以 **master 实际代码** 为准整理;已完成能力一律标记 ✅,不再作为未来任务出现。

---

## Current Focus

**Native Vision Controller(自研视觉控制器)**

用户实机体验表明:BetterGI / MAA 系 / ok-ww 等第三方控制器存在分辨率限制、UI Scale 与游戏设置要求、配置方式互不相同、前置安装复杂、CLI 随版本漂移、实机调试成本高等问题。**继续深入适配第三方控制器不再是核心主线。**

新主线:自研一套统一的轻量控制器 —— 纯窗口捕获 + 普通计算机视觉(Template Matching / YOLO / 可选 OCR)+ 状态机 + 普通 Windows 输入模拟。

现有外部适配器**保留为 legacy adapter / fallback / 迁移期兼容**,不删除(见 [§6 旧外部控制器的定位](#6-旧外部控制器的定位))。

## Current Focus / Current Phase

```text
Current Focus:
Native Vision Controller

Current Phase:
NC1 — Vision Runtime (ONNX) 🚧 运行时已落地,首个真实模型待训练
```

NC0 — Native Controller Foundation ✅ 已完成(2026-09-08/09 夜班,`controller/` Rust crate:GameWindow、四坐标系 Transform、SafetyGovernor、捕获后端、dry-run 闭环;详见 §9)。**不要倒退重复实现 NC0。**

## 状态标记

`✅ Done` · `🚧 In Progress` · `⬜ Planned` · `⏸ Deferred`

---

## 1. 已完成能力盘点 ✅

以下能力**已在 master 实现**,有代码与测试背书,历史阶段摘要保留、不再进入未来路线:

### H1 调度核心 ✅
- Go 调度核心、SQLite 持久化(modernc 纯 Go 驱动、WAL、migration、孤儿恢复)
- Task / Plan / Execution 全生命周期;robfig/cron 定时计划(next_run_at 竞争安全)
- 串行任务队列(MaxConcurrent)、timeout / cancel / 进程树 kill(taskkill /T + PPID 兜底清扫 + KILL_ON_JOB_CLOSE Job Object)
- graceful shutdown(defers 全路径执行、SSE 快速释放、失败路径不孤儿化)
- preflight(exe / working_dir / `*_dir` / python entry 检查)
- 执行日志 retention(30 天 + 保底 1000 条)、失败截图、通知钩子(notify_cmd / screenshot_cmd,15s 上限)

### H2 路线与资源 ✅
- 路线资产中心 v1:扫描入库 / 搜索 / 标签 / route → task(taskfactory)/ 执行统计(success/fail 计数)
- 资源监控与过载保护(CPU/内存/磁盘,overload policy:alert/pause)
- B站攻略搜索(WBI 签名)+ 本地路线匹配,一键导入为路线

### H3 规划器(Planner)v1 ✅
- 角色 / 目标 / 材料需求 CRUD;体力预算推荐(farming recommendations)
- 推荐生命周期:open → task_created / planned → completed / dismissed(终态守卫,不可复活)
- 推荐绑定已有路线(attach-route,同游戏校验)、recommendation → task / plan
- planner import/export(单事务原子、dry_run、非覆盖式 upsert、全量校验报错)

### H4 界面与运维 ✅
- Web dashboard(单文件嵌入式,无构建步骤):游戏/任务/计划/执行管理、培养计划弹窗(角色/目标/需求/材料/推荐全 CRUD + 状态管理 + 备份导入导出)、SSE 实时流(hub 共享快照)
- CLI(`ctl`):全资源 CRUD + run/preflight/discover/guides/planner 全动作
- 安全:token 鉴权(恒时比较)、game id 白名单、XSS 转义体系、CSP/nosniff/X-Frame-Options、无 token 跨源写拒绝、1 MiB body 上限(超限 413)
- 外部工具适配:BetterGI / March7thAssistant / Fhoe-Rail / ok-ww / M9A(命令构建器 + working_dir / 管理员权限 / 553 诊断)
- Windows 可靠性:管理员提权检测与警告、run-admin.ps1、打包脚本(build.ps1)、本地 CI 入口(ci-local.ps1)、windows_smoke.ps1 全链路冒烟

### 已有但仅为骨架 ⬜→归入 NC0
- `internal/vision`:纯接口骨架(Frame / Region / Detection / TextResult / Detector / Matcher / OCR / CommandFrameSource),无模型、无推理。**保留**为 Go 侧"截图辅助录入 / 诊断"扩展点;Native Controller 的视觉栈在 Rust 侧独立实现(见 §8)。

---

## 2. 目标架构

```text
                game-scheduler
                     Go
                      │
         ┌────────────┴────────────┐
         │                         │
 Existing External           Native Controller
    Adapters                    Rust
   (fallback)                     │
                                  │
                ┌─────────────────┼────────────────┐
                │                 │                │
             Capture          Perception        Input
           WGC / DXGI        Vision Stack      SendInput
                │                 │                │
                │        ┌────────┼────────┐       │
                │        │        │        │       │
                │    Template    YOLO     OCR      │
                │        │        │        │       │
                └────────┴────────┴────────┴───────┘
                                  │
                             World/UI State
                                  │
                                  ▼
                            State Machine
                                  │
                                  ▼
                         Safety Governor
                                  │
                                  ▼
                               Action
```

职责边界:

- **Go(现有,不重写)**:Scheduler、API、SQLite、Planner、Route、Task、Execution、Web、CLI、orchestration。
- **Rust(`controller/` 新 workspace,单独进程)**:Capture、Perception、Input、State Machine、Safety Governor。
- 两者通过简单进程协议对接(stdin/stdout JSON lines 或 localhost IPC,NC6 定义)。
- **不因为引入 controller 重写 Go 后端**;不把 Rust 逻辑塞进 Go API。

---

## 3. 主线阶段:Native Vision Controller

### NC0 — Native Controller Foundation ✅(2026-09-08/09 夜班完成)

- **Status**:✅ Done。M1–M6 + resize 稳定性验收全部落地(commit `60f0719..3e71fc6`);`cargo test` 58 绿、clippy 0 告警;`--dry-run` 全链路实测通过。已知环境项:本机 RDP 会话下 WGC 静默(见 docs/NIGHTLY_PROGRESS.md 环境发现),捕获默认走 PrintWindow(GDI) 回退链;WGC 待非 RDP 环境复验。
- **Objective**:打通"窗口 → 捕获 → 坐标变换 → mock 感知 → 调试输出"的最小闭环。不训练模型、不接真实输入自动化。
- **Scope**:
  - 新建 `controller/` Rust workspace:
    ```text
    controller/
      Cargo.toml
      src/
        main.rs
        window/     # GameWindow
        capture/    # CaptureBackend trait + WGC 实现
        frame/      # FrameInfo / Transform / LetterboxInfo
        vision/     # Detector / Matcher / OCR traits + Mock 实现
        input/      # 仅 trait 与 stub(NC4 才实现)
        state/      # 状态机类型占位
        safety/     # SafetyGovernor
        protocol/   # (预留)与 Go 的 JSON lines 协议
    ```
  - **GameWindow**:按进程名/窗口标题找 HWND;client rect;DPI 感知;窗口位置;ClientToScreen;foreground 检查;resize/变化检测。
  - **Capture**:`CaptureBackend` trait;首选 **Windows Graphics Capture(按 HWND)**;预留 DXGI Desktop Duplication 作为 fallback;帧在内存/GPU surface 流转,**不落 PNG 才能处理下一帧**;FPS 可限;ROI 可选;debug 截图导出。
  - **Frame / Coordinate Transform(第一天就做,架构要求而非后期补丁)**:`FrameInfo` / `Transform` / `LetterboxInfo`;支持 client / normalized / model / desktop 四种坐标系互转;链路:
    `原始窗口 → resize/letterbox → model input → detection → inverse letterbox → client coordinate → desktop coordinate`。1080p / 1440p / 4K / 21:9 共用同一套坐标;**任何 Skill 禁止硬编码 `Click(1733, 944)` 这类绝对像素**。
  - **Vision abstraction**:仅定义 `Detector` / `Matcher` / `OCR` trait,实现 `MockDetector`;YOLO / OCR 只留 placeholder。
  - **Debug / Dry Run(本阶段核心交付)**:`--dry-run` 模式只做 捕获→感知→输出检测结果(含 bbox)→显示计划动作,**不发送任何真实输入**;提供 debug overlay / debug frame 输出。
  - **SafetyGovernor(第一阶段必须存在,不许以后补)**:目标 HWND 不匹配→stop;foreground 不正确→stop;窗口尺寸变化→pause/recalibrate;置信度过低→no action;最大 actions/sec;最大同坐标连续点击;最大 retry;最大 state loop;emergency stop;max session duration。
- **Deliverables**:`controller/` 可编译运行;`--dry-run` 对一个真实窗口输出带 bbox 的 debug frame + 计划动作日志;坐标变换单元测试。
- **Acceptance Criteria**:对同一窗口,把窗口从 1080p 拖到另一尺寸,dry-run 的 normalized 坐标保持稳定;SafetyGovernor 的每条规则都有触发路径;`cargo test` 全绿。
- **Tests**:坐标变换(含 letterbox/逆变换/越界)、GameWindow 枚举与 DPI、MockDetector、SafetyGovernor 每条规则的单元测试;WGC 捕获做手工 smoke(无头 CI 无窗口)。
- **Dependencies**:Windows 10 1903+(WGC);Rust toolchain;无 Python/CUDA 依赖。
- **Out of Scope**:YOLO 训练与模型下载、OCR、真实输入发送、开放世界导航、与 Go 的协议对接(NC6)。

### NC1 — Vision Runtime 🚧(运行时已落地,首个真实模型待训练)

- **Status**:🚧 In Progress(2026-09-09/10 夜班:运行时侧全部落地;剩余为训练产出首个真实模型)。NC0 稳定后启动。
- **Objective**:接入真实 YOLO 推理。**训练与运行解耦**:训练用 Python/Ultralytics,部署用 ONNX;controller 运行时不依赖 Python / PyTorch / CUDA。
- **Scope**:`InferenceBackend` trait;Windows 优先 WinML 或 ONNX Runtime;`models/` 目录 + model manifest(labels / version / input size / confidence / game-profile);配置项 `model_path` / `confidence` / `imgsz` / `device(provider)`;**大型权重不进 Git**。
- **已落地(2026-09-09/10)**:WinML 推理后端(CPU 设备,运行时零下载、零 GPU 依赖);schema-v1 模型 manifest(`controller/models/`,严格校验);`--model-path` / `--manifest-check` CLI;模型缺失/非法→Mock 诚实降级;输出布局自动识别(rows-major `[1,N,≥6]` + YOLOv8 channels-first `[1,4+nc,N]`)+ class-aware NMS;`tools/vision/` 训练脚手架(PLAN gate 防意外下载/训练);跨机验证:win-devops 全量 Rust 门禁 PASS(含 WinML 实测,节点 WinML 兼容线 = ir3/opset9)。
- **剩余**:用 `tools/vision/` 训练并导出首个真实 nano 模型(白天工作:采集/标注/训练);`device(provider)` 配置化(当前固定 CPU,低资源约束下非必需)。
- **Deliverables**:可加载一个导出的 ONNX nano 级模型并输出 Detection;manifest 校验。
- **Acceptance Criteria**:同一 ONNX 模型在 1080p 与 1440p 截图上,经 letterbox 变换后检出一致(坐标按 client 系换算正确)。(已以确定性形式覆盖:fixture 模型跨同宽高比分辨率归一化位置/框占比不变,有测试;真实模型就绪后以实机对拍复验。)
- **Tests**:letterbox 前后数值对拍、manifest 解析、推理超时与降级(模型缺失→Mock)。(降级已落地;推理超时线程化 deferred——WinML 同步调用不可取消,当前以连续失败熔断代替。)
- **Dependencies**:NC0 ✅;训练管线(§4)✅ 脚手架已落地,首个模型待训练。
- **Out of Scope**:OCR;训练代码本身(在 `tools/vision/`,Python 仓内工具,不属于 controller runtime)。

### NC2 — Perception Stack(分层感知) 🚧(L0/L1 地基已落地)

- **Status**:🚧 In Progress(2026-09-09/10 夜班:骨架+离线验证设施落地;真实 UI 页面的就绪判定待真实数据)。
- **Objective**:不把所有识别都交给 YOLO;按成本分层,cheap-first。
- **Scope**:L0 像素/状态检测(取色、进度条、加载判定)→ L1 Template Matching(高频)→ L2 YOLO(button / icon / interaction prompt / dialog / minimap element / resource icon / enemy/UI object,按需低频触发)→ L3 OCR(可选:数字、材料数量、角色名、副本名、弹窗文字)。优先级恒为 `cheap detector → template → YOLO → OCR`。
- **已落地(2026-09-09/10)**:`perception.rs` 分层骨架(L0 PixelProbe 区域颜色+容差+比例门,统一 Evidence,逐层开关);L1 复用 NCC matcher 并转 Detection 共享通路;`--probes` JSON 配置(校验+示例+守护测试);离线设施:`--record`/`--replay` 帧录制回放 + L1 真实帧跟踪测试;CPU 预算确定性守护测试(100 次 L0+L1 评估 debug 构建 <60ms 均值);帧级推理缓存(§5「YOLO 不每帧跑」)。
- **Deliverables**:分层感知管线与统一 Evidence 输出;各层可独立开关。(L0/L1 ✅;L2 经 OnnxDetector 独立可用,归入 Evidence 契约的整合待真实模型)
- **Acceptance Criteria**:一个 UI 页面的"是否就绪"判定仅用 L0+L1 即可完成且 CPU 低于预算(§5)。(结构性部分已测;待真实 UI 页面数据复验)
- **Tests**:每层独立单测 + 离线帧回放(录制帧 → 断言层输出)。(离线回放设施 ✅)
- **Dependencies**:NC1 🚧(运行时已落地)。
- **Out of Scope**:OCR 训练;语义理解。

### NC3 — State Machine / Skill Engine 🚧(引擎地基已落地)

- **Status**:🚧 In Progress(2026-09-09/10 夜班:数据驱动定义+纯转移评估器+dry-run 集成落地;真实业务 Skill 待 NC5)。
- **Objective**:Skill 是状态机,不是 `if sees button: click`。
- **Scope**:`Skill / State / Evidence / Action / Expectation / Timeout / Retry / Fallback` 类型与执行引擎;每个状态必须定义:进入条件、感知证据、动作、期望结果、超时、重试、fallback、终止条件。示例:`Home → OpenMenu → MenuDetected → OpenDaily → DailyDetected → Claim → ConfirmSuccess → Done`。
- **已落地(2026-09-09/10)**:`skill.rs` 数据驱动 SkillDefinition(严格 JSON 校验:状态名唯一/转移与 fallback 可解析/终态语义);Expectation 两形态(L0 探针触发 / 标签检测+置信度门);SkillRunner 纯转移评估器(Waiting/Transitioned/FellBack/Done/Failed,超时预算+有界重试);dry-run `--skill` 每周期以真实管线证据驱动并记录转移与计划动作(计划只记录——NC4 前无输入);`skill_pipeline` 集成测试 + 示例守护测试。
- **Deliverables**:Skill 定义格式(数据驱动,JSON/TOML)+ 引擎 + 执行轨迹日志(与 dry-run overlay 联动)。(定义+引擎+轨迹日志 ✅;overlay 联动待打磨)
- **Acceptance Criteria**:示例 Skill 在 dry-run 回放中每一步的 evidence/expectation 均可追溯;超时/重试/fallback 路径有测试。(超时/重试/fallback 测试 ✅;回放追溯待真实 Skill)
- **Tests**:状态机引擎单测(用 MockDetector 编排固定帧序列)。(✅ 纯转移评估器 + 管线集成两层)
- **Dependencies**:NC0–NC2。(NC1 运行时 🚧、NC2 地基 🚧 均已可支撑)
- **Out of Scope**:真实输入(NC4);具体业务 Skill 内容(NC5)。

### NC4 — Input Controller 🚧(输入层已接线,实机 selftest 待操作者执行)

- **Status**:🚧 In Progress(2026-09-10/11 夜班:`input.rs` SendInput 级封装落地;默认零输入契约不变)。夜班已实现并单测,gated 实机验证待人工。
- **Objective**:普通 Windows 输入 API 的干净封装,坐标全部来自检测/锚点/归一化变换。
- **Scope**:`InputController` trait(`MouseMove / Click / Drag / Scroll / KeyDown / KeyUp / KeyPress`,统一 `PlannedInput`);`SendInputController` 真实后端(绝对坐标走虚拟屏 0..65535 归一化,按键走 scancode);`NoInput` 默认后端(结构性零输入);`GovernedInput` 安全包装——每个动作先过 HWND 身份 + foreground + governor 时钟/急停前置,指针动作另过置信度/频率/同点规则,管线已授权动作走 `execute_authorized` 防止重复计数。CLI:`--allow-input`(显式 opt-in,无交互桌面直接拒绝)、`--input-selftest`(向自有探针窗口发一次真实点击+按键,操作者手动运行)。
- **Acceptance Criteria**:注入式输入不可用时,该能力**标记 unsupported**(已实现:无交互桌面→Unsupported/拒绝);**明确不做**(见 §7 红线):driver injection、DLL injection、memory manipulation、anti-detection、input bypass(未触碰)。
- **Tests**:焦点/HWND 校验与 governor 否决路径单测(recording mock,零真实输入);探针窗口输入计数器测试(PostMessage 注入,不碰真实键鼠);真实输入按 ROADMAP 做人工 smoke(`--input-selftest`)。
- **Dependencies**:NC0、NC3;SafetyGovernor 已稳定。✅
- **Out of Scope**:任何绕过类技术(红线,永不);无人值守真实输入(夜班纪律);拖拽/滚动的管线级执行(当前管线只执行 click,其余 trait 能力就绪待 NC5 消费)。

### NC5 — 第一个真实 Skill ⬜

- **Status**:⬜ Planned
- **Objective**:端到端验证整套栈。
- **Scope**:选**短流程、UI-heavy、可验证、可恢复**的 Skill:每日奖励领取 / 固定 UI 菜单操作 / 固定副本结算。**不做**:自动锄大地、长距离跑图、自动战斗 AI、自动剧情、全地图导航(那是 NC8)。
- **Acceptance Criteria**:**至少在 1920×1080 与 2560×1440 两个分辨率下,不改代码完成同一个 UI Skill**;有条件再加测 3440×1440。失败路径(fallback/超时)有 dry-run 演练。
- **Dependencies**:NC0–NC4。
- **Out of Scope**:开放世界;多游戏泛化(先一个游戏打透)。

### NC6 — Scheduler Integration ⬜(协议 schema v1 已冻结)

- **Status**:⬜ Planned。线格式已定稿(2026-09-10/11 评审):stdin/stdout JSON lines,信封 `v/seq/ts/type/payload`,schema 由 `controller/src/protocol.rs` serde 类型+测试固化,决策与文档见 docs/controller-protocol-draft.md(D1 事件粒度/D2 截图路径引用/D3 一次一进程已定,D4 executor 配置留 NC6)。Go 侧消费仍属 NC6 实施。
- **Objective**:Native Controller 接回 Go 调度器。
- **Scope**:简单进程协议(stdin/stdout JSON lines 或 localhost IPC);Go Task 支持 `executor = external | native`;native task 形如 `{"executor":"native","skill":"daily_reward","game_id":"genshin"}`;Go 侧:启动/停止 controller、接收 events、写入 Execution、cancel / timeout / screenshot / log / stats 复用现有 runner 基建。
- **Acceptance Criteria**:一个 native task 从 API 触发到 Execution 落库全链路可走通;cancel/timeout 行为与 external 任务一致;协议有 schema 与版本字段。
- **Tests**:协议编解码、生命周期(canon: start→events→exit)、cancel 竞争;Go 侧集成测试用 fake controller 进程。
- **Dependencies**:NC5。
- **Out of Scope**:把 controller 逻辑塞进 Go API(红线)。

### NC7 — Planner Integration ⬜

- **Status**:⬜ Planned
- **Objective**:打通 `Planner → Recommendation → Skill/Route → Native Controller`,执行结果回流 Execution。
- **Scope**:recommendation 增加可选 skill 绑定;执行结果(feedback)反哺 planner 统计。
- **Acceptance Criteria**:"今天需要刷材料 A"的推荐可以一键生成 native 任务并执行,结果在 dashboard 可见。
- **Dependencies**:NC6。
- **Out of Scope**:自动生成 Skill(人工/半自动制作 Skill)。

### NC8 — 更复杂视觉自动化 ⏸

- **Status**:⏸ Deferred(NC0–NC7 全部稳定后;**不要提前实现**)
- **Scope(届时再细化)**:短距离移动、相机控制、minimap perception、waypoint navigation、战斗状态识别、path recovery、卡死恢复;开放世界导航是后期目标。
- **Out of Scope**:一切违反 §7 安全红线的能力;开放世界导航在 NC8 内部也排最后。

### NC9 — Route & Skill Learning(视频学习路线) ⬜

- **Status**:⬜ Planned。**学习与模拟侧可先行**(纯离线工作,夜班安全);**实际游戏测试显式 deferred**——待真实游戏控制测试环境可用后再进入(NC5 流程)。
- **Motivation**:当前没有可安全进行真实游戏控制测试的环境。先从视频内容离线学习"路线与操作"(B 站采集路线、关卡教程、跑图全流程等),把时间投入转化为可执行的 Skill/Route 资产,环境就绪后直接进入实测。
- **Objective**:自动/半自动地把攻略视频转成结构化 Route / SkillDefinition 草案,并能在捕获窗口或录制帧上以 **dry-run 形式模拟输出**(只记录计划动作,不发送任何真实输入),全程资源轻量。
- **Scope**:
  - 视频获取:用户投喂本地视频文件,或经既有 B 站攻略搜索/导入通路(H2,官方 Web 接口)。**不自动下载大体积视频,不绕过任何访问控制**。
  - 离线学习管线(`tools/` 侧轻量工具):低频抽帧(默认 ≤1fps,CPU 优先)→ 关键帧/场景切换检测 → L2/L3 感知按需(YOLO nano / OCR,复用 NC1/NC2 栈)→ 结合标题/简介/字幕文本 → 产出结构化草案:关键锚点(normalized 坐标)、动作序列、状态转移(对齐 NC3 SkillDefinition schema)。
  - 模拟输出:学习产物经 NC3 SkillRunner + dry-run 管线回放(录制帧或捕获窗口),逐状态记录"计划动作";不依赖 NC4,不发送任何真实输入。
  - 资源控制(硬约束):抽帧低频;批处理可中断、可恢复;无 GPU 依赖;不下载大型模型;进程空闲时 0 CPU;学习产物(草案 JSON)进 Git,视频与大帧缓存不进 Git(§4 原则)。
- **Acceptance Criteria**:
  - 从一条真实攻略视频(或用户提供的录屏)产出至少一个能通过严格校验、被 SkillRunner 加载的 SkillDefinition 草案;
  - 该草案在 dry-run 回放中完整走查并输出计划动作轨迹(与 NC3 轨迹日志/TSV 联动);
  - 学习管线处理 10 分钟视频的资源占用符合 §5 预算(无 GPU、内存 <300MB、可中断恢复);
  - 实际游戏测试条目保持 deferred 标注,仅在真实环境可用后单独排期。
- **Tests**:草案 schema 校验;关键帧检测单测(合成帧序列);字幕/文本解析单测;端到端用小体积录制视频夹具(入库,保证 CI 确定性)。
- **Dependencies**:H2(B 站搜索/导入)✅;NC1 运行时 🚧(可用);NC2 感知 🚧(L0/L1 可用,L2/L3 按需);NC3 SkillDefinition ✅ 地基。
- **Out of Scope**:真实输入发送(归 NC4,红线内另行评审);绕过视频平台访问控制(红线,永不);大型模型下载/GPU 训练;任何账号风控相关的自动化。

---

## 4. 数据集与模型生命周期

```text
datasets/
  README.md          # 数据规范、采集流程、许可与隐私(不含真实账号信息)
  manifests/         # 每个数据集的清单(json/yaml:来源、分辨率、标注版本)
tools/
  vision/
    prepare_dataset.py
    train.py
    export_onnx.py
```

- **大图片数据与模型权重不进 Git**(gitignore + manifest 引用外部存储)。
- 数据集覆盖矩阵:**1080p / 1440p / 4K;16:9 / 21:9;windowed / borderless;不同 brightness;不同 UI scale;(如需)不同语言**。
- 数据增强:scale / crop / brightness / contrast / mild blur。
- 目标是 **resolution independent** 的模型与坐标系统,而不是为每个分辨率写一份脚本。
- 模型 manifest 必含:labels、version、input size、confidence 默认值、game/profile。

## 5. 性能预算(工程目标,非硬 SLA,可按 benchmark 调整)

| 项 | 预算 |
|---|---|
| Capture | 约 5–15 FPS 可调;idle 时尽量 0 采集 |
| YOLO | 按需触发(事件驱动/低频轮询),不每帧跑 |
| Template Matching | 可较高频 |
| Idle CPU | 尽可能 < 1% |
| 常规运行 CPU | < 5–10% |
| 内存 | < 300 MB(不含大型模型 runtime 特殊情况) |
| GPU | 使用可配置;推理默认低频 |

Controller 的设计目标**不是 60 FPS YOLO**,是"够用的感知 + 低的空闲开销"。

## 6. 旧外部控制器的定位

BetterGI / March7thAssistant / Fhoe-Rail / ok-ww / M9A 的现有适配器:

- **短期保留**,定位 `Legacy / External Adapter` + `Fallback`;**不删除现有代码**。
- 当 Native Controller 对某项 Skill 达到稳定标准后,**该项优先 native**,否则 fallback external。
- 最终由用户选择,规划配置项:

```json
{ "execution_mode": "auto | native | external" }
```

- `auto`:native skill 可用 → native,否则 → external(默认)。
- 迁移期文档:两种模式的差异、各自的前置要求分开写,不混写。

## 7. 安全边界(红线,对所有阶段生效)

**允许**:
- 普通窗口捕获(WGC/DXGI)、Screenshot、Computer Vision、Template Matching、YOLO、OCR
- 普通 Windows input API(SendInput 级)
- 状态机、本地 UI 自动化

**禁止**(发现即回退,永不实现):
- DLL injection / process injection
- memory read/write(游戏内存读写)
- packet interception / packet modification(抓包改包)
- driver-level bypass、anti-cheat bypass
- anti-detection、hidden automation
- account risk-control bypass(绕过账号风控)

与 SECURITY.md 的威胁模型一致:外部工具不被沙箱化、不注入、不读内存、不抓包;本红线同样约束 Native Controller。

## 8. 与现有 Go 代码的关系

- `internal/vision`(Go 接口骨架)**保留**,作为 Go 侧截图辅助录入/诊断的扩展点;不迁移、不删除。
- Native Controller 视觉栈在 `controller/`(Rust)独立实现;两者在 NC6 通过进程协议对接,不共享进程、不 cgo 桥接。
- `execution_mode` 配置进入 Go 侧 config/任务模型的时间点:NC6。

## 9. Nightly Development 记录

### 2026-09-08/09 夜班:NC0 全部完成 ✅

- **M1** ✅:`controller/` Rust crate 骨架 + GameWindow(HWND 枚举 / 精确 client rect / per-monitor-v2 DPI / ClientToScreen / foreground / 变化检测)— `60f0719`。
- **M2** ✅:Frame(BGRA8、padded stride)+ Transform 四坐标系与 letterbox 逆变换,1080p↔1440p 对拍、21:9 padding 精确断言 — `1e287da`。
- **M3** ✅:SafetyGovernor 十条规则全测试(纯决策引擎、时间显式注入)— `2bd04e8`。
- **M4** ✅(环境项 PARTIAL):CaptureBackend + WGC 实现 + PrintWindow(GDI) 后端 + 合成帧 + FPS 限速;WGC 本节点静默已诊断并记录,GDI 实测 PASS — `f50b812`。
- **M5** ✅:MockDetector + overlay + `--dry-run` CLI 闭环(auto 后端回退、debug PNG、紧急停止路径)+ 集成测试 — `27aef86`。
- **M6** ✅:NCC TemplateMatcher 骨架 — `8622dfa`。
- **验收加项** ✅:实机 resize 稳定性测试(ROADMAP NC0 acceptance 自动化,WM_PRINT 根因修复)— `3e71fc6`;clippy 门禁 — 后续 commit;dry-run 瞬态错误退避重试 — 后续 commit。

**NC0 完成定义达成**:`窗口 → 捕获 → resize → mock detection → 坐标反算 → debug overlay` 全链路实测跑通;`cargo test` 全绿;SafetyGovernor 每条规则有触发路径测试。今晚未做(按计划):YOLO/模型/OCR/真实输入/开放世界/Go 协议对接。

### 2026-09-09/10 夜班:NC1 运行时落地 + NC2/NC3 地基 🚧

详见 docs/NIGHTLY_PROGRESS.md(M1–M31)。要点:

- **NC1 运行时**:schema-v1 模型 manifest(严格校验);WinML ONNX 推理(CPU,零下载零 GPU);rows-major `[1,N,≥6]` 与 YOLOv8 channels-first `[1,4+nc,N]` 解码自动识别 + class-aware NMS;模型缺失→Mock 诚实降级;`--manifest-check`/`--model-path`/`--probes`/`--skill`/`--record`/`--replay` CLI;`tools/vision/` 训练脚手架(PLAN gate);185B/193B 手写 ONNX fixture 走通真实 load→session→bind→evaluate 全链。
- **NC2/NC3 地基**:L0 探针进主管线(原始 client 帧);统一 Evidence;SkillDefinition+SkillRunner 纯转移评估器(超时/重试/回退/终态);`--skill` 由真实证据驱动、计划只记录;会话 TSV `skill_state` 列。
- **可靠性**:推理错误上报+连续失败熔断;推理缓存(静态场景 77 周期仅 4 次推理);DPI/枚举竞态修复;服务会话环境门控;全表面 120s soak 零泄漏;CPU 预算守护测试。
- **跨机验证**:win-devops 首次全量跑 Rust 门禁(129 测试)PASS——节点已装 Rust;节点 WinML 兼容线 ir3/opset9、服务会话无交互桌面(环境发现见 NIGHTLY_PROGRESS)。
- **未做(按计划)**:首个真实 nano 模型(白天采集/标注/训练);WGC 非 RDP 复验;输入发送(NC4,红线内未触碰);下一阶段 NC2 真实 UI 数据补全。

### 下一夜班起点:NC2 真实数据补全 / NC4 输入设计评审

前置:①用 `tools/vision/` 完成首个真实 nano 模型的采集/标注/训练/导出(白天);②NC4 输入控制器的安全设计评审(SendInput 封装 + governor 硬前置, Dummy window 验证);③NC6 协议草案(docs/controller-protocol-draft.md)评审定稿。

夜班安全备选:**NC9 视频学习路线**(纯离线学习 + dry-run 模拟输出,不依赖真实游戏环境;见 §3 NC9)。

## 10. 变更记录

- **2026-09-10(夜)**:新增 **NC9 Route & Skill Learning(视频学习路线)**(§3)——无真实游戏测试环境期间,从 B 站攻略/教程/跑图视频离线学习操作路线,转结构化 Skill/Route 草案并 dry-run 模拟输出;实际游戏测试 deferred 待环境。
- **2026-09-09**:NC0 标记完成(§3/§9);下一夜班起点更新为 NC1;README(中/英)新增「当前开发方向」章节并明确 Controller 安全边界;NIGHTOPS.yaml 夜间优先级对齐本路线图。
- **2026-09-08**:全面重写。历史 Go 调度核心/路线/Planner/界面能力标记 ✅(§1);主线改为 **Native Vision Controller**(§2–§3,NC0–NC8);新增数据集生命周期(§4)、性能预算(§5)、旧外部控制器降级为 fallback(§6)、安全红线(§7);明确今晚 NC0 起点(§9)。
