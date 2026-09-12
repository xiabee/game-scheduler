# Nightly Progress

## Project Goal

多游戏资源收集与路线调度器。

## Current State

近期落地：终端 rec 防护、非覆盖式 upsert、origin guard、ctl delete/depth。
**2026-09-08 夜班起主线切换至 Native Vision Controller（ROADMAP §9）**：
NC0 基础 ✅ → NC1 推理运行时 ✅（运行时侧收官，仅首个真实模型待白天训练）→
NC4 输入层 ✅（SendInput+governor 硬前置，默认零输入，selftest 留操作者）→
NC6 调度集成 ✅ 功能面收官（协议 schema v1 冻结、Go 会话执行器、native 任务分发与取消、dashboard 表单、**auto 执行器**——native 可用即走、否则回退外部命令，2026-09-11/12 夜；余 EVENT→SSE 打磨，暂缓理由在案）→
NC7 Planner 集成 🚧 **首片落地**（recommendation 可绑 NC3 skill,create-task 产出 auto 任务,bind-after-create 回灌;余反馈统计与无路线纯 skill 推荐）→
NC9 视频学习管线 🚧（帧→draft→skill→回放 DONE 最小闭环已通,真实素材待白天）。
环境发现：隐藏控制台启动时 GDI 捕获黑帧（已可观测化告警）；D1 终态 EVENT 恰好一次已修复并 120s soak 验证（2026-09-11/12 夜）。

## Candidate Backlog

- 白天：首个真实 nano 模型训练（tools/vision 全链路已就绪），NC1 收官对拍
- NC2 真实 UI 数据补全验收（L2 归入 Evidence 契约待真实模型）
- NC7 第二片：执行结果反馈 planner 统计（需产品语义定义:一次成功 run ≠ 材料入账,不宜自动改 owned_count）;无路线纯 skill 推荐推荐形态
- NC6 收尾单件：EVENT→SSE 事件流打磨（D1 事件稀疏,trail+TSV 已可观测,暂缓理由在案）
- NC5 前置：真实 skill 流程设计（待 NC1 真实模型/真实 UI 数据）
- WGC 在实体控制台的复验（长期 BLOCKED，RDP 环境）

## Night Runs

### Night 2026-09-12 → 2026-09-13（夜班 agent 记录）

- Handoff：XNightOps `2026-09-12/game-scheduler` 验证通过（night/project/workspace=本仓/git_head=a29670b 全一致；dispatch_at=23:35，23:38 到岗即开工；prompt/manifest/context/close 四件套齐全）。
- START_COMMIT `a29670b`。

| M | 内容 | Verdict | Commit | 验收 |
|---|------|---------|--------|------|
| M1 | **NC7 二片：执行反馈统计（保守语义）**——store 层 `RecommendationFeedback` 只读 rollup（推荐关联任务的总执行/成功/失败/取消/进行中 + 最近一次执行 + `estimated_runs` 对照;悬空 task_id 诚实报 `task_missing`——正常路径不可能产生该态[ON DELETE SET NULL],分支为 DB 外部编辑/迁移残留而设,测试用租用连接临时关 FK 制造,**注意 store 是 MaxOpenConns(1),租用连接必须先归还**——首轮测试死锁 5 分钟的教训）;API `GET /api/planner/recommendations/{id}/feedback`;ctl `planner feedback <id>`;README 中英 + ROADMAP NC7 状态同步。**语义有意保守**:一次成功 run ≠ 材料入账——不自动改 `owned_count`、不动推荐生命周期,入库/完成始终人工决定(测试锁定只读性:feedback 前后推荐行逐字段不变) | PASS | 092ee0c | store 4 测试 + API 集成测试全绿;LOCAL CI PASS |
| M1b | dashboard 推荐行「反馈」按钮 + 弹窗:点击时拉取 rollup 渲染(无任务/悬空/有任务三态文案),不进列表轮询(避免 N+1 请求);内嵌 JS node --check 通过;README 看板提示同步;实机 smoke:临时库起服 → 页面含 openFeedback ×2 → 未知推荐 404 → 建 fixture 走通 | PASS | 7c8c6d3 | LOCAL CI PASS(22 包 + cargo 门禁);JS 语法守卫过;实机 smoke PASS |
| M2 | **NC7 deferred 收口：无路线纯 skill 推荐 → 纯 native 任务**——`ensureRecommendationTask` 对「有 skill 无路线」的推荐产出 `type=native`/`executor=native` 任务(无外部回退分支,故**必须** native:auto 对 type=native 严格报错的既有契约不变且有测试锁定);无 skill 无路线仍 400(既有测试不变);create-task 经 rec.TaskID 幂等;api 测试覆盖全流程(绑定→创建→链接落库→幂等→反馈 rollup 互通);实机 smoke:recommend 引擎产出 manual 推荐 → attach-skill(用 `controller/examples/skills/daily_claim.example.json`) → create-task 201 native → feedback 零值正确;dashboard 绑定弹窗提示 + README 中英 ctl 用法块(补齐漂移的 attach-route/attach-skill/feedback 条目) + ROADMAP NC7 deferred 项清零 | PASS | 9262193 | api 定向测试全绿;LOCAL CI PASS;实机 smoke PASS |
| M3 | **NIGHTLY VERIFY 一键电池全绿**(本夜全表面证据):ci-local(22 包+cargo 门禁) + controller smoke(协议段/旗标段) + 17 步全链 windows_smoke[native 段 12-17:创建/preflight/真实会话 RESULT done/中途 cancel/auto 双分支全 OK] + 30s ONNX soak(448 周期/389 次推理,RESULT done) + NC9 selftests(learn_route+draft_to_skill PASS) + dashboard JS 守卫 OK | PASS | (无代码变更,证据轮) | NIGHTLY VERIFY PASS,日志 .nightly/nightly-verify-tonight.log |
| M3b | ROADMAP NC7 状态 → **✅ Done**(验收映射写明:推荐一键生成 native 任务=api 测试+实机 smoke;结果 dashboard 可见=反馈弹窗+执行视图;执行链路=windows_smoke [12]-[17]);§10 变更记录同步 | PASS | 2de51a6 | 文档一致性核对 |
| M4 | **create-task 并发竞态守卫**(自审发现的真 bug 面):`CreateTaskForRecommendation` 原为跨事务 check-then-act——两个并发 create-task(看板双击)会各插一个任务并回灌链接,产生重复任务且其一成孤儿(无执行/反馈通路);修复=事务内 `UPDATE ... WHERE id=? AND task_id IS NULL` 条件守卫,败者回滚插入并报新哨兵 `ErrRecommendationTaskExists`,事务内先做存在性检查区分「rec 不存在=ErrNotFound」与「输掉竞态」;`ensureRecommendationTask` 捕获哨兵回读链接返回胜者任务——双重 POST 幂等;store 测试锁定(胜者/败者回滚[tasks 计数=1]/链接保持/未知 rec) | PASS | 7d7c004 | store+api 定向测试全绿;LOCAL CI PASS;远端 PASS(2m50s) |
| M5 | **NC9 链式夹具 e2e 收口 ROADMAP 测试项**:新增 `tools/vision/pipeline_e2e.py`——临时目录生成**确定性** 4 场景合成录像(16 帧 320×240,暗底 + 红/蓝/绿块弹入,无随机无时间戳,生成脚本即入库夹具定义、仓库不放二进制帧)→ learn_route(断言恰 4 段)→ draft_to_skill(断言 3 探针 + step_00..03→done 走查序)→ controller `--dry-run --replay` 断言末条 RESULT outcome=done。本地全链 PASS;nightly-verify [5] 段接入(controller.exe 存在才跑,缺失诚实 SKIP);tools/vision/README 记录。ROADMAP NC9「端到端用小体积录制视频夹具」测试项以此落地(真实攻略视频仍按 ROADMAP 留给实机素材) | PASS | fe86826 | 全电池复跑 PASS(含新 [5] 链式段);远端 PASS(1m46s) |
| M6 | **Go 侧调度链 2h 回归 soak**(fe86826 重建 server/ctl):raw 任务 @every 10s 持续点火 7200s,收尾断言 total/success/failed/无滞留——今晚 Go 面(api/store/planner)改动的调度链级回归证据;桌面无关(拷贝 cmd.exe 作假任务),资源形态与 M14/M15 同族 | (运行中) | — | 预计 04:15 收尾,结果入晨报前账本 |

- **NC6「EVENT→SSE 打磨」暂缓复核(本夜)**:读 stream.go/events bus 后确认维持暂缓——D1 门控下一次运行仅 2-5 条语义事件;中途 trail 不落库,按事件广播快照无新信息;有效实现需跨包事件管道(task.Service→api)+独立 SSE 帧类型+看板实时视图,当前无真实技能流量支撑该成本。暂缓理由由「事件稀疏」更新为「事件稀疏 + 管道成本 vs 无真实流量」。
- **外键语义确认**:executions/plans 对 tasks 删除均为 ON DELETE CASCADE——删除任务连带清理历史与计划,系刻意模式(smoke [11] 依赖),非缺陷。

- **REMOTE CI 根因终于抓到并当场修复（昨晚 M31b 三连 FAIL 同族,晨间运维清单该条可销）**：
  - 症状：本地同码全绿,win-devops 两连 FAIL（exit=1,91s/86s,确定性;快照上传正常 22s/166MB）。
  - 突破：昨夜「节点日志不可达」不再成立——SSH publickey 现已放行,直接读到 `D:\CI\jobs\<id>\logs\ci.log`：**Go 全家(test 18 包/vet/govulncheck/gosec/secret scan)在节点上全绿**,挂在 `cargo clippy`: `error: failed to download adler2 v2.0.1`。
  - 根因链：节点 04:30 的 `cleanup-ci-node.ps1`（09-10 由本机运维部署）按 **mtime>21 天**清理 `D:\CI\cache`,当日删 14525 文件/315MB——缓存系 09-10 从他机拷贝播种,**文件携带旧 mtime 被整批误杀**;`registry\src` 解压树残缺(348 包目录中 adler2-2.0.1 的 Cargo.toml 缺失),`.crate` 存档尚在,但节点无 crates.io 外网,cargo 在线模式报 download 失败、离线模式报 read 失败。
  - 修复（当场,纯缓存重建零数据损失）：删除 `D:\CI\cache\cargo\registry\src` 整树 → cargo 从本地 `.crate` 存档自动重解压;作业 source 目录内 `cargo clippy --all-targets --offline` 复验 **EXIT=0**。
  - 防复发建议（归 XNightOps/运维侧,本仓不动）：cleanup 应排除 `cache\cargo\registry`(或对播种拷贝归一化 mtime/改用 mtime 之外的活性判据);另 `cargo clippy` 可加 `--offline` 降级重试作第二道保险。
- 资源纪律：GOMAXPROCS=2 / -p 2;单跑 cargo,无并行大构建;无 GPU;无真实游戏输入;临时库 smoke 后即清理。

### Night 2026-09-11 → 2026-09-12（夜班 agent 记录）

- START_COMMIT: 6e596a7（docs(progress): night close 2026-09-11）
- Handoff：XNightOps `2026-09-11/game-scheduler` 验证通过（night/project/workspace/dispatch_at=23:35/git_head=6e596a7 全一致；prompt_hash a797ca59a0ab72a8）
- 基线发现：**master 基线 LOCAL CI 红**——`protocol` 集成测试 `terminal_skill_event_emits_once` 确定性数出 2 条 done EVENT（P0，M1 修复）

| M | 内容 | Verdict | Commit | 测试 |
|---|------|---------|--------|------|
| M1 | D1 终态 EVENT 恰好一次（P0 修复）：闩锁此前只盖引擎重复 Done/Failed 分支，但**落入终态的转移以 Transitioned 形态上报**（skill.rs 契约）并发自己的 EVENT、不进闩锁——下一周期 Done 再发一条；通过与否取决于转移后是否还有剩余周期（时序脆弱，昨夜侥幸全绿）。修复：main.rs 落终态（runner.is_done()）即置闩锁（Transitioned/FellBack 两臂）；Done 分支只剩「起始态即终态」场景，改发 runner.current() 真实状态名（原硬编码 "done"）；skill.rs fallback 落终态也置 done（与转移路径对称，原遗漏，+单测）；集成测试强化 transitions==1 并给 s0 加终态 fallback（2s 超时）使「探针未触发」负载瞬态也恰好落一次（M24 异常族去脆弱）；协议草案 D1 措辞更新 | PASS | e0240cf | `--test protocol` 3 连绿；全量 ci-local PASS（Go+Rust 全门禁含安全阶段）；skill 单测 9 绿；**REMOTE CI（win-devops）PASS exit=0 3m4s**——昨夜 05:xx 3 连 FAIL 未复现，节点已恢复（晨间运维项降级：历史 FAIL 根因未追，但当前节点健康且同码全绿，阻塞解除） |
| M2 | auto 执行模式（NC6 最后功能项）：params `executor:"auto"` 触发时实时决议——controller 已配置+可执行+声明 skill/probes/model 全存在 → native，否则回退外部 adapter 命令；**只降级不升级**（auto 永不启用真实输入，双闸不变）。决议可观测：Preflight 新增 `resolution` 字段（`auto→native` / `auto→external (原因)`），native 分支执行轨迹首行 `executor=auto resolved=native`，external 分支记日志。**顺带修真实 footgun**：dashboard 图形表单 collectParams 从 schema 字段重建 params、从不写 executor 选择器——表单创建的 native 任务静默落入外部路径、点火即败（M19 只修了类型下拉覆盖）；现 API create/update 对 type=native 且无选择器的任务默认注入 `executor:"native"`（显式值优先），native 表单加 executor 字段（native\|auto，留空=native）。外部任务 params 不受影响；params 无法解析时保持旧契约=无选择器走外部 | PASS | aec8ceb | +4 task 测试（auto 择 native/fallback-无配置/fallback-资产缺失/Preflight 决议矩阵）+ API 选择器默认测试（含 update 路径与非 native 不注入）；全量 ci-local PASS；dashboard JS 语法守卫过 |
| M3 | NC7 首片——recommendation skill 绑定：`POST /api/planner/recommendations/{id}/attach-skill`（open 守卫同 attach-route；**绑定时校验文件存在**——typo 路径否则每轮静默降级 external）+ `farming_recommendations.skill` 列（additive 迁移）+ create-task 注入 `executor=auto`+`skill`（保留 adapter-owned Type 与 taskfactory 参数=external 回退分支所需；推荐生命周期保持 route 中心）。ctl `planner attach-skill` 镜像；看板推荐行 skill 徽标+路径+「绑定 Skill」弹窗。NC7 依赖 NC6 auto ✅ 同夜达成。Deferred（书面）：执行结果反哺 planner 统计；无路线纯 skill 推荐 | PASS | bbf5fc6 | TestPlannerAttachSkill（绑定时校验矩阵/route+skill 注入/adapter 回退参数保留/closed 守卫）；store CRUD 往返经 API e2e 覆盖；全量 ci-local PASS；JS 守卫过 |
| M4 | auto 严格规则 + 实机锁定：设计 smoke 步骤时发现 M2 语义缺口——type=native+executor=auto 时回退分支根本不存在（无 adapter 认 native 类型），native 可用期 preflight 假 ready、controller 一旦缺位即落 guaranteed-fail 分支。修复：autoPreflight 显式 ValidationError、execute 落 failed 同文案（用 executor=native 才是 native-only 正解）+回归测试；windows_smoke 新增 [16][17] 两步（真实 controller：auto→native 决议+实跑+轨迹断言；资产缺失→auto→external 决议+原因+回退分支 ready）；quickstart 补 auto 章节；nightly-verify 头注释步数漂移清理 | PASS | a45209f | TestAutoRequiresAdapterOwnedTypeForFallback；**NIGHTLY VERIFY PASS 全电池**（ci-local+controller smoke+17 步全链 smoke 含新 auto 两步+30s ONNX soak+NC9 selftests+JS 守卫） |
| M5 | D1 协议长会话 soak + 并发卫生：M1 修复的集成测试只有 6s 会话,而 EVENT 刷屏正是长会话 soal 抓出的——120s GDI+@probe+probes+skill(终态 fallback 版)协议会话：**432 周期协议流恰好 4 行**(HELLO/READY/EVENT×1 done@cycle1 落终态转移/RESULT done),M1 修复在持续运转下成立；缓存 418/432、same-point guard allowed=5、零黑帧告警、SUMMARY completed exit 0（TSV 留档 `.nightly/m5-soak/`）；定向 `-race`(internal/task+native) 干净。顺带巡检 planner import/export——数据集只覆盖角色/目标/材料/需求,推荐(含新 skill 字段)不在其中,无字段漂移 | PASS | (无代码变更,验证轮) | 432 周期协议断言全过；`go test -race` task+native ok |
| M6 | auto 回退自解释：native 分支有 `resolved=native` 轨迹行,但计划任务静默降级到外部时原因只在服务器日志——无人值守操作者只看到 adapter 命令不知为何 native 没跑。external 分支现在把 `executor=auto resolved=external (原因)` 前缀写进执行 stdout(与 native 分支对齐,+测试断言);README 双语决议可观测性措辞同步 | PASS | 57bfa76 | TestAutoExecutorFallsBackToExternal 增 stdout 断言；全量 ci-local PASS |
| M7 | dashboard 编辑保 params（footgun 家族第三例修复）：collectParams 从 schema 字段从零重建 params——图形表单编辑 native/auto 任务会**静默丢 executor/skill/probes**（M19 修类型下拉覆盖、M2 服务端默认注入修创建,编辑路径仍丢）。修复:编辑时以任务原 params 为底(`form.__baseParams`),表单字段只覆盖自己的键;高级 JSON 手改仍整体生效;类型切换残留键惰性(adapter 只读认识的键),可在高级视图删除。语义四用例 node 行为级验证(编辑保参/新建不变/高级优先/切型惰性) | PASS | af23b0d | node 行为级 4/4 PASS；JS parse 守卫过（看板无 JS 测试架,行为模拟+人工走查为证,已如实注明） |
| M8 | 未近审面走读（负结果）：Go `vision`(命令行截图源,模板渲染+临时文件清理)/`notify`(15s 超时+WaitDelay+shell 元字符消毒)/`shellcmd`(cmd /S /C CmdLine 直通,注释与实现一致)/`discover`(只读遍历,深度/超时/上限/跳过表全齐)/`guide`(WBI 签名 md5 属协议要求已 adjudicated、限读 64KB/1MB/4MB、BVID 字符集白名单防注入、-412 友好报错);Rust `session.rs`(lazy append+warn-once+字段消毒+列数兼容钉死)/`replay.rs`(有界录制+清晰错误分类)。**零 P0/P1**；全仓零 TODO/FIXME/unimplemented；README 测试计数声明无漂移(cargo 163 ≥ "150+") | PASS | (无代码变更,审计轮) | 现有测试全绿佐证 |
| M9 | 绑定回灌：**先建任务后绑 skill 时绑定曾静默无效**（ensureRecommendationTask 幂等返回既有任务,skill 只落在推荐行上）。修复:attach-skill 成功后把绑定回灌进关联任务 params——任务已有显式 executor 则尊重（native 照用,external 保持）,无选择器补 `auto`（既有外部命令保留为回退分支）;任务行已删/params 不可解析容忍跳过;store 失败以 API 错误浮现（半应用状态不可伪装成功,重绑即重试）。顺带钉死:create-task 幂等返回既有任务但恒 201（客户端可见契约,不改,测试注明）。README 双语补 bind-after-create 语义 | PASS | fd0a9d4 | TestPlannerAttachSkill 扩展（回灌断言:skill 更新+executor=auto+script 保留;幂等 201+同 ID）；全量 ci-local PASS |
| M10 | 状态文档刷新 + release 保险：NIGHTLY_PROGRESS Current State/Candidate Backlog 刷新至今晚后状态（NC6 功能面 ✅、NC7 首片 🚧、D1 soak 结论、远端 3 连 FAIL 降级为已恢复）;晨间运维清单移除（节点已恢复,历史 FAIL 根因不再阻塞）;`cargo test --release` 全量 163 绿（skill 闩锁/时序新代码 opt 下无回归） | PASS | (本轮无代码变更) | release 163 绿 exit 0 |
| M11 | D1 失败路径锁定：done 路径有真实二进制锁,failed 路径（超时耗尽无 fallback）共用同一闩锁但无测试驱动。新集成测试:期望永不出现的 label 的 skill 对 synthetic 后端必须恰好发 1 条 `failed` EVENT 且无其他状态变化。**环境注记**：01:4x 本机桌面转锁（OpenInputDesktop FAIL）,窗口类测试按约定诚实跳过——M1 三连跑/M5 soak/nightly-verify 均在锁屏前真实跑毕,有效性不受影响;今晚后续实机捕获验证不可用（与黑帧同族环境约束） | PASS | 7b56f78 | ci-local PASS（跳过模式）;实跑验证待交互桌面（与既有约定一致） |
| M12 | main.rs 全文走读（2012 行,编排核心,此前仅增量审）（负结果）：CLI 分发/参数校验矩阵（有专属测试钉死）/后端三级回退（诚实 WARNING）/探测窗口生命周期/干跑主循环（错误分类→RetryTracker 有界退避→WindowGone 终态→推理熔断 30 周期→黑帧看门狗→重标定重建后端→verdict_notes 有界 50）/ProtocolEmitter（seq 单调、stdout 纯协议、RFC3339 有已知日期测试）。**零 P0/P1**。examples/README 契约文档补「fallback 落终态亦完成 + D1 恰好一次」语义（M1 修复的契约面收尾） | PASS | 1296fc7 | cargo test 全量 164 绿（含示例守护） |
| M13 | 审计扩展（负结果）：`internal/planner` 推荐引擎（确定性评分 route_type>source>名称、体力预算 `len(fresh)>0` 保护首条、ReplaceOpenRecommendations 原子替换;每 gap 一次 200 条路线查询属本地 SQLite 可接受规模）、`internal/game/hsr` 适配器（python 入口/绝对 entry 不与 dir 拼接/preflight 同口径）、`internal/game/cmdutil`（BaseSpec 默认工作目录=可执行文件所在目录的注释钉死 BetterGI exit 553 真实脚枪;Timeout 0=无超时语义）。**零 P0/P1** | PASS | (无代码变更,审计轮) | M32 直测佐证 |
| M14 | **Go 侧持续点火 soak（首个调度/执行/存储链路 soak）**：此前全部 soak 均 controller 侧,Go 的 cron→Enqueue→runner→落库循环只有单测/集成级证据。300s 隔离临时 server + raw 任务（复制 cmd.exe /c exit 0,桌面无关,锁屏会话可跑）+ 计划 `@every 3s`：**恰好 100 次点火（=300/3 理论值,零丢槽零重复）,100 success,0 pending/running 卡死,0 failed**,exit 0。脚本留档 `.nightly/m14/soak.ps1`（一次性验证,不入库——是否固化为 nightly-verify 段留操作者决定） | PASS | (无代码变更,验证轮) | 100/100 精确点火;计划/执行/存储全链路持续运转证据 |
| M15 | **Go 侧长程 soak**（3.5h 版,@every 10s,01:56→05:17 主动停表保留 DB）：**1176 次点火全部 success,0 pending/running 卡死,0 failed/cancelled**;SQLite `pragma integrity_check`=ok（WAL 长持续写后）;server WS 17.4→23.3MB 后趋平（无失控增长）。走读中确认 executions API 列表硬上限 500 行（`limit<=0||>500→500`）——脚本终检按 API 计数会误报,故以 SQLite 直查为权威口径（已改为停表后直查;API 分页上限属既有契约,未改）。脚手架清理完毕 | PASS | (无代码变更,验证轮) | DB 直查 1176/1176 + integrity ok;全程 ~12100s 等效 10.3s/次,与 @every 10s 吻合 |

### Night 2026-09-10 → 2026-09-11（夜班 agent 记录）

- START_COMMIT: 6ad2efb（docs(progress): night close 2026-09-10 04:20）
- Handoff：XNightOps `2026-09-10/game-scheduler` 验证通过（night/project/workspace/dispatch_at=23:38/git_head=6ad2efb 全一致；prompt_hash c6edabc6c244e30b）
- 主线：昨夜 deferred 项 + ROADMAP §9 建议起点（NC4 输入层 / NC6 协议评审 / 安全扫描接入）；真实模型训练仍属白天工作（夜间禁下载/GPU）
- 用户插播：NC9 视频学习路线需求已入 ROADMAP（§3/§9/§10，commit 7a9bb14）

| M | 内容 | Verdict | Commit | 测试 |
|---|------|---------|--------|------|
| M1 | 安全阶段接入本地 CI（overnight-prompt 强制项）：安装并接线 govulncheck v1.8.0 + gosec（HIGH×HIGH 门禁）+ git 跟踪文件 secret 扫描（工具缺失诚实 SKIP）；x/sys 0.42→0.44（GO-2026-5024，无可调用路径，顺手修复）；runner/kill_windows 两处 G115 以 Windows PID=DWORD 语义书面注销（#nosec）；MEDIUM 13 项审查记录（G202=clamp 后整数 LIMIT 拼接/G204=runner 本职/G304=用户指定路径/G301=单用户本地/G401·G501=B 站 WBI 协议要求 md5——均属设计内，记入 backlog 观察） | PASS | b78f8b7 | LOCAL CI PASS（安全阶段实跑：govulncheck 0 可调用漏洞、gosec HIGH 门 0、secret 扫描 0 命中）；后续 726fe6d 修复扫描器查找路径（GOPATH/bin 而非 GOPATH 根） |
| M2 | NC4 输入层：`input.rs` 从 6 行 stub 变为完整实现——`InputController` trait（统一 `PlannedInput`：MouseMove/Click/Drag/Scroll/KeyDown/Up/Press）；`SendInputController`（绝对坐标虚拟屏 0..65535 归一化——纯函数可测、按键 scancode 路由+VK 兜底、部分插入即失败）；`NoInput` 默认后端（结构性零输入）；`GovernedInput` 硬前置（HWND IsWindow + foreground + governor 时钟/急停，指针动作另过置信度/频率/同点；`execute_authorized` 防管线重复计数）；探针窗口 CLICK/KEY 计数器 + `pump_pending_messages` + `bring_to_foreground`；CLI `--allow-input`（显式 opt-in、无交互桌面 exit 2、自动强制 foreground 要求）、`--input-selftest`（3 秒倒计时→自有探针窗口真实点击+按键闭环，操作者手动运行）；README 双语 + ROADMAP NC4 状态 🚧 同步。**夜班纪律：本机有交互桌面且用户在场，真实输入 selftest 不无人值守执行** | PASS | b28d5f2 | +9 测试（输入 8 + 窗口计数器 1，全部零真实输入：recording mock 验证 governor 全 verdict 路径、PostMessage 注入验证 wndproc 计数）；controller 143 全绿、clippy 0；实机默认 dry-run 复验 `input_sent=0` + "no input was sent" 契约不变；LOCAL CI PASS |
| M3 | NC6 协议 schema v1 冻结（评审定稿）：`protocol.rs` 从 2 行占位变为完整线格式——信封 `v/seq/ts/type/payload`，七种消息（HELLO/READY/EVENT/LOG/RESULT/PING/PONG）严格 serde 解析；版本不匹配 fail-fast、kind 用显式映射（未知 kind → 精确 UnknownKind 而非笼统 JSON 错误）、kind/payload 不匹配拒绝、PING/PONG 要求空载荷、v1 内未知字段前向兼容；草案 4 个开放问题定稿 3 个（D1 事件粒度=仅语义变化，全量诊断留 TSV；D2 截图=文件路径引用；D3=一次一进程；D4 executor 配置留 NC6）；文档示例由守护测试钉死防漂移；ROADMAP NC6 状态注记同步 | PASS | 84c7769 | +7 协议测试（doc 示例钉死/七 kind 往返/版本 fail-fast/未知 kind/payload 不匹配/空载荷校验/error 字段省略）；controller 146 全绿、clippy 0 |
| M4 | NC9 学习管线骨架（用户插播需求落地第一步）：`pnglite.py` stdlib-only PNG 读写（8-bit RGB/RGBA、filter 0-4 全解、无 PIL/numpy）；`learn_route.py` 帧目录→降采样（≤64px 网格）→相邻帧差分→场景切分（全局均值 or **峰值块**双阈值——局部 UI 弹窗占画面 4% 时全局均值会稀释，峰值块恰好抓菜单/对话框展开这类 NC9 核心事件）→每段「进入时变化区域」归一化锚点（0..1，分辨率无关硬约束同样约束学习产物）→ `route-draft.json`（schema v1，NC9→NC3 桥）；`--selftest` 合成端到端守护接入 nightly-verify [5/5]；真实数据验证：200 帧真实 GDI 录制帧全部解析、静态场景正确不切分。锚点语义=「进入该段时变化了什么」（前段末帧 vs 本段代表帧），即路线步进语义。真实游戏测试按 NC9 保持 deferred，本管线全部工作在帧目录上 | PASS | 2c560c4 | selftest PASS（两段切分@8、锚点≈(0.7,0.7)、静态不分裂、PNG 往返字节相等）；NIGHTLY VERIFY PASS 全 5 段 |
| M5 | NC9→NC3 桥：`draft_to_skill.py` 把 route-draft 转成 controller 可直接消费的 `probes.json`+`skill.json`（NC3 SkillDefinition 契约）：归一化锚点按录制帧尺寸烙定为 client 像素、主色采样（`learn_route` 新增 anchor dominant_rgb，段落代表帧区域 RGB 均值）、**RGB→BGRA 通道翻转**（探针 expected 为 BGRA 序——端到端联调抓出的真实契约问题）；线性状态机 step_00→…→done（baseline 段空期望）。**NC9 最小闭环首次全通**：合成两场景帧序列→learn→convert→`controller --dry-run --replay --probes --skill` 轨迹走查至 `skill: DONE`（计划动作携带归一化锚点，零真实输入）；selftest 双双接入 nightly-verify [5/5] | PASS | f27c6fe | draft_to_skill selftest PASS（bake-out/颜色翻转/走查序）；实机回放 DONE 轨迹；NIGHTLY VERIFY PASS |
| M6 | 推理超时线程化（昨夜 deferred #3）：`timeout.rs` TimeoutDetector——内部检测器移交**独占 worker 线程**（每请求独立应答通道），`detect()` 带预算等待；超时→空结果+take_error 上报（喂既有连续失败熔断），会话不再被挂死的 WinML 调用冻结；inner 错误经响应通道转发保持 take_error 语义跨线程。ONNX 侧 `SendOnnxDetector`（unsafe Send 双重论证：WinML agile 文档 + 独占所有权，编译期 Send 断言测试）；CLI `--infer-timeout`（默认 5s，1..=300000 校验）；管线包装序 = 缓存在外（命中跳过通道）、超时在内 | PASS | ef18e90 | +3 超时测试（快速通过/慢推理超时后恢复/挂死连续超时不冻结且逐次上报，recording mock 零真实推理依赖）；实机 WinML 经 worker 25 推理全过；controller 154 绿、clippy 0；LOCAL CI PASS |
| M7 | NC6 Go 侧地基：`internal/native` 协议镜像（protocol.rs 严格对齐——版本 fail-fast、七 kind、必填字段+枚举校验、v1 内未知字段前向兼容）+ 流式会话执行器 `RunSession`（stdout 逐行扫描、EVENT 顺序分发、RESULT 恰好一次、协议违规即杀进程树 fail-fast、cancel/timeout 与 RESULT 缺失的裁决矩阵）；`runner` 导出 KillProcessTree/AssignJob 复用缝；fake controller 双实现（test 二进制重执行 + `cmd/fake-controller` 真实子进程），真实 IPC 覆盖 7 场景 | PASS | 69e7841 | +10 Go 测试（happy 生命周期/版本 fail-fast/未知 kind/cancel/timeout/无 RESULT/RESULT 与 exit 矛盾/协议解析契约表）；go vet 0；LOCAL CI PASS |
| M8 | D4 定稿 + native 调度集成：草案开放问题 #4 关闭——`executor` 入任务 Params（`{"executor":"native",...}`，零 schema 迁移）；params 契约（skill/probes/window 默认@probe/backend/model/dry_run 默认 true/allow_input/duration_sec）；config 双闸 `native_controller_path`+`native_allow_input`（**真实输入需 params∧!dry_run∧config 三闸同时**）；`internal/task/native.go` 分发（execute/Preflight 双分支，native 免 adapter 校验）+ RESULT→Execution 映射（done→success，failed/stopped→failed 业务终态，仅进程级故障参与重试）+ 会话 TSV 入 `<DataDir>/native/`；API `type:"native"` 旁路 adapter TaskTypes；`cmd/fake-controller` 集成测试走真实 execute() 全路径 | PASS | 4bb5e1a | +6 Go 测试（happy path 经真实子进程/store 断言 exit 0+会话日志路径/无配置 fail-fast/preflight 缺文件/默认值与旗标矩阵/双闸矩阵）；LOCAL CI PASS |
| M9 | NC6 Rust 侧线协议落地 + 全链路验收：①dry-run 人读输出 stdout→stderr（57 处，协议模式 stdout 仅协议——满足草案「stderr 承载人读日志」契约）；②`--protocol` 旗标：HELLO（会话首行）/READY（detector 解析后）/RESULT（终态恰好一行：completed→done、stopped/input-blocked→stopped、inference-degraded→failed），RFC3339 UTC std-only 实现（Hinnant civil 算法，clippy 抓出并修复 mm 变量遮蔽真 bug）；③probes/skill/manifest 读取 **BOM 容错**（PS5.1 UTF8 BOM 同类问题第二次出现——config.Load 修复后的 controller 侧对称修复，BOM 文件实测通过）；④windows_smoke 增加 native 三步（任务创建/preflight/真实 controller 会话→success）；⑤nightly-verify 临时 server 配置注入 native_controller_path 并传 -Controller。**NC6 验收核心达成：API 创建→preflight→调度→协议会话（真实 Rust controller）→Execution success 全链路自动化走通** | PASS | 4e7235e | controller 127 绿（+rfc3339 形状测试）、clippy 0；NIGHTLY VERIFY PASS（5 段含 native [12][13][14]）；协议输出实测纯三行（HELLO/READY/RESULT done） |

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
| M25 | 配置脚枪防护：skill 期望引用未配置探针 → 启动即 WARNING（此前只能靠超时失败）；`SkillDefinition::referenced_probes()` / `LayeredPerception::probe_names()` 支撑 | PASS | （下方 commit） | 127 测试；实机 smoke 打印 WARNING |
| M26 | 采集→训练闭环桥：`frames_to_dataset.py`（controller --record 帧目录 → YOLO 数据集布局 + source manifest，stdlib-only）；实机端到端：4 帧真实录制 → 转换 → prepare_dataset 校验切分通过 | PASS | 15cc4be | --help/合成/真实帧三路验证 |
| M27 | 会话 TSV `skill_state` 列：`--skill` 驱动的会话每行追加第 9 列（离线分析技能时间线 × 检测联合），无 skill 会话保持经典 8 列不变（双向断言） | PASS | bcae0cf | 127 测试；实机验证第 9 列 |
| M28 | NCC 粗到细精修（NC2）：stride-N 粗扫后于 ±stride 邻域做 stride-1 重评分——精度等同全扫描、成本大降；测试用非周期纹理证明非网格偏移 (37,21) 精确恢复 score≥0.99（棋盘格周期性会致粗扫混叠，已换纹理并留注释） | PASS | f919f2e | 127→128 测试 |
| M29 | SUMMARY 行增强：追加式 key=value 字段（inference / cache_hits / skill_state），经典四字段前缀匹配保持兼容；双向测试断言 | PASS | ad9e0f6 | 127→130 测试 |
| M30 | Go 侧整夜审计 + 全链路冒烟复验：逐行走读 runner/scheduler/monitor/task-service/api-security（截图端点防穿越、恒时 token、安全头、过载迟滞、WAL/busy_timeout）——零 P0/P1 发现；`examples/windows_smoke.ps1` 11/11 步 PASS（隔离临时实例：health→discover→game→route→task→preflight→假执行→日志→统计→运行中删除无孤儿） | PASS | （下方 commit） | 本夜 Go 零改动零回归；全链路证据齐全 |
| M31 | 真实帧 × 探针技能验收（NC2+NC3 微型闭环）：录制探针窗口真实帧 → L0 探针对准归一化锚定红盒 → 回放驱动技能 → Done（计划动作与状态转移断言）；服务会话诚实跳过 | PASS | eff15a4 | 128→129 测试 |
| M32 | 测试缺口补齐（审计发现）：genshin/wuwa/r1999 适配器此前无直接测试（仅共享 builder 测试覆盖）——各补 BuildCommand/Validate 直测（onedragon 参数、raw_args 覆盖优先级、farm 路由、r1999 working_dir 必需等 11 个用例） | PASS | 0ecc871 | go test 全绿 |
| M31b | clippy 清理：移除 l1_real_frames 未使用导入（上一次提交遗留） | PASS | （下方 commit） | clippy 0 |
### Go 审计扩展结论（凌晨维护轮）

- `internal/events`（52 行 bus：合流通知/once 取消/nil 安全）✅ 零发现
- `internal/store/planner_import`（单事务 + deferred Rollback + dry_run 计数同逻辑）✅ 与 ROADMAP 声明一致
- 推荐生命周期终态守卫（`recOpen` + errRecommendationClosed）✅ 代码与测试俱在
- **发现并修复**：README 中英 controller 章节 `cargo test` 计数漂移（97 → 实际 130）
### Go 审计完结（03:40 前后，全仓）

覆盖：runner、scheduler、monitor、task-service、api（安全面）、store（Open/migrate/WAL/ensureColumn/去重唯一索引）、planner_store、planner_import、events、cmd/server 生命周期。**结论：零 P0/P1 发现**；graceful shutdown 的 LIFO defers、推荐终态守卫、截图端点防穿越等关键声明全部与代码一致。整夜 Go 零改动。
| M33 | 一键夜审电池 `scripts/nightly-verify.ps1`（ci-local + controller-smoke + 隔离临时服务器全链路 windows_smoke + 30s ONNX soak，分段判定）；**P1 修复**：PS `Set-Content -Encoding UTF8` 的 BOM 使 config.Load 的 json.Unmarshal 失败、服务器启动即死——Load 剥 BOM + 守护测试（该问题由新电池首次串联时暴露） | PASS | 84c1003 | NIGHTLY VERIFY PASS 实机全段 |
| M10 | NC6 协议 EVENT + manifest 回显：`--protocol` 模式下 skill 状态转移/回退/终态时发射 EVENT（D1 语义=仅语义变化，含 cycle/state/触发探针/client 检测/planned actions）；READY 携带 manifest 摘要（DetectorChoice 新增 ManifestSummary，Onnx 与 ManifestPending 两路均回显）；Go 侧 executeNative 把 EVENT 轨迹（有界 32KB）写进 Execution stdout 字段——调度器侧可观测原生会话时间线；新增协议一致性集成测试（CARGO_BIN_EXE 拉起真实二进制，逐行按 schema 解析断言 HELLO 首行/READY 存在/RESULT 末行 done） | PASS | 239de61 | controller 127 绿 + 协议集成测试 1；Go task/native 全绿；NIGHTLY VERIFY PASS |
| M11 | README 双语同步：native executor 任务契约（params 示例/启用前提/输入双闸/RESULT 映射/NC9 学习管线指引） | PASS | 207c9b3 | 文档一致性核对 |

| M12 | 会话 TSV 探针列：`probes_fired` 尾随列（逗号连接的触发探针名；配置未触发=空，未配置=列不存在——与 skill_state 相同的 append-only 兼容模式），NC9 离线分析可联合对齐 探针触发×检测×状态 三时间线 | PASS | e4ee0c1 | 10 列/8 列双向断言；实机 TSV 验证 |
| M13 | 60s 全表面协议 soak（GDI 真实捕获+probes+skill+record200+缓存+协议流）：780 周期零泄漏（WS 12.4→9.7MB、句柄 111）、缓存命中 755/780=96.8%、退出干净。**soak 抓出真 bug：终态 skill（Done/Failed）EVENT 每周期重复发射（574 条 failed）**——违反 D1；修复=终态事件门控只发首次 | PASS | 8a60cb7 | 修复后事件流 = HELLO/READY/EVENT×2（语义转移）/RESULT，符合 D1 |
| M14 | device(provider) 配置化（NC1 最后一个 deferred 项）：`--device cpu|gpu` + native params `device` 透传；GPU=DirectX provider，在服务/RDP 会话经既有降级链诚实失败（open 失败→Mock+原因打印），CPU 路径 soak 覆盖不变 | PASS | dd64c65 | 全部 ONNX 测试经 Cpu 路径回归；parse 校验测试；NIGHTLY VERIFY PASS |
| M15 | native 会话取消路径验收：windows_smoke 新增 [15] 中途 cancel 步骤——controller 进程树被杀、Execution 落 cancelled、无孤儿 | PASS | (见下方 commit) | NIGHTLY VERIFY PASS 全 15 步 |

| M17 | 操作者快速上手文档 `docs/native-quickstart.md`:编译→录帧→学习→转换→接入调度器→安全边界→故障排查表,全路径可复制粘贴;README 链接 | PASS | bf8022e | **文档逐字执行真值测试**:按文档步骤录 34 帧→learn(静态场景诚实判单段)→convert(平凡技能告警)→回放 DONE 全通 |
| M18 | NC6 剩余项盘点:dashboard 可视化(→M19)、auto 模式(需 NC7 skill 可用性建模,暂缓)、SSE 打磨(D1 事件稀疏,执行后 trail+TSV 已可观测,暂缓)——两项暂缓均有书面理由 | PASS | — | — |
| M19 | dashboard native 任务表单:**修复真实 footgun**——native 任务经 dashboard 编辑时 renderTaskFields 回退到首个 adapter 类型,保存会用错误字段静默覆盖 params(丢失 executor:native);新增 NATIVE_TASK_TYPE schema(9 字段含双闸说明)加入每个游戏的类型下拉,编辑/创建均走同一 collectParams 路径;API 测试 + JS 语法检查过 | PASS | c00007e | go test ./internal/api 全绿;node --check JS 语法通过 |

| M20 | 稳定性验证轮：①定向 `-race`（internal/native+task,新并发面）干净;②`cargo test --release` 157 绿（新浮点/时序代码 opt 下无回归）;③300s 协议全表面 soak:4008 周期、WS 12.4→9.9MB 平坦、句柄 110→111、缓存 96.9%、协议流恰 4 行（HELLO/READY/EVENT×1/RESULT done）——事件门控正确。**异常记录（诚实）**:该轮探针未触发（skill 10s 超时 failed）,与随后 30s 同参复跑（即触发,expected 行为）矛盾,判定为夜机负载瞬态;引擎超时/重试语义有单测背书,留观察 | PASS | (本轮无代码变更,+2 测试) | -race 干净;release 157 绿;see 左 |
| M20b | 敌意复审今晚 Rust 新增面:发现 **Drag steps 无上界**（u32::MAX → 巨量 SendInput 事件分配）——clamp(1,64)+镜像测试;README 测试计数漂移改 150+ 写法 | PASS | 0c712a7 | clamp 断言测试;clippy 0 |

| M21 | controller-smoke 扩容:协议模式段（stdout 逐行可解析+HELLO 首行+RESULT done 终行）+ 新旗标校验段（--infer-timeout 0→2、--device warp→2、cpu→0）+ WDAC 预热重试环（环境性拦截,与 ci-local 同源策略）。**过程教训（诚实记录）**:调试中 python 补丁把 `	` 展开成真实 TAB 损坏 4 处 `.	arget` 路径（症状=exit 1 无输出,与 WDAC 混淆）——已修复并把此类补丁改走 Edit 工具/明确转义 | PASS | 19733b2 | NIGHTLY VERIFY PASS 全段;CONTROLLER SMOKE PASS 含 2 新段 |

| M23 | **worker panic 兜底（敌意复审真发现）**：调度 worker goroutine 此前无 recover——执行路径深处任何 panic（native sink/store/映射）都会让 Execution 行永久卡 running；新增恢复路径=行落 failed + `internal panic` 标记（双保险防恢复自身二次 panic，store 失败仅记日志不炸服务器）；panicAdapter 确定性注入测试锁定 | PASS | e5345d6 | panic 注入测试：行落 failed + 标记断言；task/native 全绿 |
| M24 | 1800s（30 分钟）协议全表面长 soak:25616 周期、governor max_session(30min) 到期→RESULT stopped（安全上限真实生效,端到端验证）;WS/句柄全程平坦（t60/t105/t435 采样）;record 封顶后 CPU 44%→10%。**重大环境发现:隐藏控制台启动 → GDI 捕获黑帧**（录制帧像素级验证:矩形中心 (0,0,0);可见控制台对照=正常 (200,40,16) 且探针即触发）——与 WGC 静默同属 RDP/锁屏环境约束族,已写入 quickstart;探针/检测类验收必须在可见控制台执行 | PASS | (随 M23 批次,终值本轮补记) | 像素级对照证据;governor 30min 上限端到端验证 |

| M25 | 黑帧可观测化：`Frame::is_black_sampled(步长采样)` + 会话循环连续 10 周期全黑→响亮 WARNING（一次性）+ SUMMARY `black_cycles` 计数——把「RDP/隐藏控制台黑帧」从静默失败变成显式告警;隐藏启动 4s 实测告警打印、可见对照无误报 | PASS | (下方 commit) | 单测 2（全黑/含点亮像素/采样间隙语义）;实机双对照 |

| M28 | nightly-verify 增设 dashboard JS 语法守卫（78KB 内嵌 JS 为夜班高频编辑面,语法损坏=整 UI 静默失效;node --check 逐 script 块解析）| PASS | e7e29b1 | 守卫实测通过（1 script block OK）|

| M30 | `--device gpu` 实机验证：本机 NVIDIA GPU 上 DirectX provider 会话真实建立,25 次推理 RESULT done;ONNX banner 从硬编码 device=cpu 改为回显实际 provider;READY manifest 回显同场实测生效。smoke 不加 gpu 段（无 GPU 节点经降级链诚实降 Mock,非失败） | PASS | (下方 commit) | 实机 gpu 会话 2s 干净退出;协议流 HELLO/READY(manifest)/RESULT |
| M30b | GPU provider 30s 持续 soak:290 周期/250 次 DirectX 真实推理零失败,RESULT done——gpu 路径从「实现+单测」升级为「实机持续验证」;CPU/GPU 双 provider 均已覆盖 | PASS | (随 M30) | soak TSV 留档 |

| M31 | secret 扫描快照回退：节点快照若剥离 .git,git grep 会「静默零扫描」——门禁弱化;新增非仓库回退=递归文本扫描（≤2MB、排除 .git/target/.nightly/dist、源码扩展名白名单）+ 显式模式公告;仓库内路径行为不变 | PASS | 425e51b | 本地仓库模式实测 PASS;回退路径为节点设计 |
| M31b | REMOTE CI 诊断:win-devops 3 连 FAIL（exit=1,52-94s,确定性;本地同码全绿）——节点侧日志不可达（agent 无日志端点,SSH publickey 拒绝）,候选根因=节点 gosec 版本漂移或 govulncheck DB 环境;晨间运维清单#1=调取节点作业日志。LOCAL CI 为验收门槛（dispatcher 规则）,不以远端环境失败伪装代码失败 | PARTIAL | 425e51b | 远端 3 数据点;nightly-verify PASS ×2 复验 |

| M32 | 1800s→7200s 长 soak 尝试（隐藏控制台,3fps+probes+skill）：20.3 分钟处探针窗口被外部销毁（IsWindow=false）→ 循环诚实停止（"window gone"→SUMMARY completed,1216s/3644 周期）——窗口消失检测与干净退出路径得到长会话验证；黑帧（隐藏控制台）导致 skill 超时 failed 属预期（探针无内容可匹配）。终止原因系外部销毁而非代码缺陷,与 M24 黑帧发现同族 | PASS | (本轮无代码变更) | TSV 全程解析;3644 周期零异常;SUMMARY completed |

| M31c | NC9 资源验收基准实测:600 帧 640×480 合成序列（背景阶梯+矩形场景,5 切分）learn 全程 60.8s（~101ms/帧）——「10 分钟 @1fps 采集 ≈ 600 帧预算内」成立,切分数与场景构造一致 | PASS | (记录于 M31 轮) | 实测数据 |


### 夜班收尾（2026-09-12 08:40 close）

- **Session**:START_COMMIT `6e596a7`(2026-09-11 23:18 handoff 生成,dispatch 23:35)→ END_COMMIT 见 git log;工作窗口全程 RUN,08:40 起收尾。
- **计划 vs 完成**：零遗留 milestone。今晚主题=「NC6 功能面收官 + NC7 首片 + 协议契约加固」——
  - **M1 安全/契约**：master 基线 LOCAL CI 红被当场抓获并修复（D1 终态 EVENT 双发:落终态转移不进闩锁,时序脆弱型漏网）,120s soak 432 周期验证恰好一次;失败路径补锁;
  - **M2/M4/M6 auto 执行器**（NC6 最后功能项）:触发时决议 native↔external、只降级不升级、决议三面可观测（Preflight resolution/执行轨迹/日志）;type=native+auto 严格报错;表单/服务端两条 footgun 路径根治;17 步实机 smoke 锁定双分支;
  - **M3/M7/M9 NC7 首片**:recommendation skill 绑定（attach-skill + ctl + 看板）、create-task 产出 auto 任务、bind-after-create 回灌、dashboard 编辑保 params（footgun 家族第三例）;
  - **M5/M14/M15 三项 soak**:D1 协议 120s（432 周期恰 1 EVENT）、Go 侧调度链 300s（100/100 精确）与 3.5h 长程（1176/1176,integrity ok）——Go 调度链首次获得 soak 级证据;
  - **M8/M12/M13 审计**:main.rs 全文、planner 引擎、四适配器、vision/guide/notify/discover/shellcmd/session/replay——**零 P0/P1**;全仓零 TODO/FIXME。
- **验证签名**：最终 LOCAL CI PASS（gofmt/vet/go test/go build/govulncheck/gosec/secret scan/cargo fmt+clippy+test+build）;NIGHTLY VERIFY PASS（ci-local+controller smoke+17 步全链 smoke+30s ONNX soak+NC9 selftests+JS 守卫）;cargo test --release 163 绿;定向 `-race`（task+native）干净;`cargo test` 全量 164 绿。
- **Remote acceptance**：win-devops `after_local_pass` 策略,M1/M2/M3-M4/M9 四轮全 PASS（3m4s/1m48s/3m43s/2m2s）——昨夜 05:xx 3 连 FAIL 未复现,节点恢复,晨间运维清单该项解除。
- **安全**：govulncheck 0 可调用漏洞;gosec HIGH×HIGH 0;secret 扫描 0 命中;零真实游戏输入（auto 只降级不升级,双闸全链路保持;`--input-selftest` 仍未无人值守执行）。
- **环境注记**：01:4x 本机桌面转锁（OpenInputDesktop FAIL）——窗口类测试按约定诚实跳过;此前全部实机验证（M1 三连/M5 soak/nightly-verify）在锁屏前真实跑毕。
- **已知问题/Deferred**：①首个真实 nano 模型未训练（白天工作,tools/vision 就绪）;②NC7 反馈统计需产品语义定义（一次成功 run ≠ 材料入账）;③无路线纯 skill 推荐与 EVENT→SSE 打磨暂缓（理由在案）;④WGC 实体控制台复验长期 BLOCKED（RDP）;⑤历史 win-devops 3 连 FAIL 根因未追（节点已恢复,非阻塞）。
- **下一夜班建议**：①白天训练真实 nano 模型后走 NC1 收官对拍+NC5 首 skill;②NC9 换真实游戏录屏素材跑学习闭环;③NC7 反馈语义定向设计评审;④若夜间仍无实机方向,维持审计/soak 轮换即可（当前仓库审计面已全覆盖）。
- **推送状态（08:45 注记）**：close 记录 `f55e6f7` 因网络层 SSH 中断（github.com:22 与 ssh.github.com:443 同报 connection closed,SSH 探针同失败——传输层而非凭据）暂存本地未推送,M1-M15 全部 milestone 提交已在远端（至 `58ccf73`）。推送恢复后 `git push origin master` 即可,无需任何代码动作。

### 夜班收尾（2026-09-11 08:40 close）

- **Session**:START_COMMIT `6ad2efb`(2026-09-10 23:38 dispatch)→ END_COMMIT 见 git log;工作窗口 23:00-08:40 全程 RUN,08:40 起收尾。
- **计划 vs 完成**：夜班主线全部落地并验收（M1-M28,零遗留 milestone）——
  - **M1 安全**：govulncheck+gosec+secret 扫描接入本地 CI（M21 节点环境分类修正）;
  - **M2/M13-M14 NC4 输入层**：SendInput 封装+governor 硬前置+双闸,真实输入 selftest 留操作者;
  - **M3/M7/M9/M13 协议**：schema v1 冻结（Rust serde+Go 镜像双向锁定）、--protocol 线模式、RESULT 闩锁语义;
  - **M4/M5/M26 NC9 学习管线**：帧→场景切分→归一化锚点→draft→probes/skill→回放 DONE 最小闭环全通（含 quickstart 文档逐字真值测试）;
  - **M6**：推理超时线程化（独占 worker+预算,挂死不再冻结会话）;
  - **M7/M8 NC6 调度集成**：Go 协议镜像+流式会话执行器+native 任务分发+config 双闸+RESULT→Execution 映射+fake/real controller 双验证;
  - **M15**：native 会话中途取消=进程树击杀+cancelled 落库（smoke 步骤锁定）;
  - **M20-M28 审计修复**：drag 步数上界、终态 EVENT 门控（soak 抓出的每周期重复）、worker panic 兜底、RGB PNG 解码 panic 修复、黑帧环境发现可观测化、dashboard native 表单（params 覆盖 footgun）、RFC3339 已知日期断言。
- **Remote acceptance**：`after_local_pass` 策略执行;win-devops 节点 3 次 FAIL（exit=1,~52s,确定性,本地全绿同码）——节点侧日志暂不可达,候选根因=节点 gosec 版本漂移或 DB 不可达类环境项;晨间运维清单含「调取节点作业日志」条目。LOCAL CI（含 -Race 定向）为验收门槛。
- **安全**：govulncheck 0 可调用漏洞;gosec HIGH×HIGH 门禁 0 发现（G115 双处书面注销,13 项 MEDIUM 审查入档）;secret 扫描零命中（唯一合成 token 显式 allowlist）;**零真实游戏输入**（--input-selftest 仅实现未执行——本机有交互桌面且操作者在场,无人值守发送真实输入违反夜班纪律）。
- **已知问题/Deferred**：①首个真实 nano 模型未训练（白天:采集→标注→train→export）;②WGC 非实体控制台复验 BLOCKED（同前）;③**黑帧环境发现**：隐藏控制台启动→GDI 全黑捕获（像素级验证）,已由黑帧看门狗可观测化,规避=可见控制台运行宿主;④REMOTE win-devops 失败根因（需节点日志）;⑤NC7 skill 绑定与 auto 模式（待真实技能存在,分发层已就绪）。
- **下一夜班建议**：①晨间优先——调取 win-devops 作业日志定位远端失败根因（疑似 gosec 版本漂移/DB 不可达类）;②白天训练首个真实 nano 模型后走 NC1 收官对拍（`tools/vision/` 全链路已就绪）;③NC9 用真实游戏录屏素材跑学习→转换→回放闭环（管线已验证）;④NC7 设计评审（recommendation skill 绑定语义,分发层已就绪）。


### 夜班收尾（2026-09-10 04:20 close）

- **计划 vs 完成**：主线 NC1 运行时侧全部落地（ROADMAP §3 NC1 状态 🚧，剩余=首个真实模型）；NC2 L0/L1 地基、NC3 状态机引擎地基提前落地；另交付 tools/vision 训练脚手架、打包集成、nightly-verify 一键电池。共 33 个 milestone 记录，67 个 commit（7e1576e..1dfa956）。
- **最终验证**：LOCAL CI PASS ×3（gofmt/vet/go test 18 包/go build + cargo fmt --check/clippy 0/cargo test 130 绿/cargo build --locked）；CONTROLLER SMOKE PASS ×2；NIGHTLY VERIFY PASS；REMOTE CI（win-devops）PASS ×6；windows_smoke 11/11；30s/60s/120s soak ×3 零泄漏。
- **测试**：controller 67→130（+63：manifest 11、inference 7、缓存 4、NMS 4、skill 6、感知 6、回放 4、CLI 6、ONNX 运行时 7、L1 真实帧 2、技能管线 2 等）；Go 18 包全绿无改动。
- **安全**：零输入发送（input 模块仍为空 stub）；无注入/无内存读写/无抓包/无反检测；新增代码经逐提交安全扫描；config BOM 容错修复反而提升了启动健壮性。
- **已知问题/Deferred**：①首个真实 nano 模型未训练（白天：采集→标注→train→export）；②WGC 非 RDP 复验仍 BLOCKED；③推理超时线程化（WinML 同步调用不可取消，以连续失败熔断代替）；④`device(provider)` 配置化（当前固定 CPU）；⑤YOLOv8 真实模型端到端对拍待模型就绪。
- **下一夜班建议**：白天完成首个真实模型后，NC2 用真实 UI 数据补全验收；NC4 输入控制器安全设计评审（SendInput 封装 + governor 硬前置 + dummy window 验证）；NC6 协议草案（docs/controller-protocol-draft.md）评审定稿。
