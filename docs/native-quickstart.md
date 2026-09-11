# Native Controller 快速上手:从录帧到调度执行(NC9 + NC6)

> 本文是一条**可复制粘贴的最短路径**:把 controller 编译出来、录一段
> 帧学成技能草案、跑成调度器里的一个 native 任务。全程不碰真实游戏、
> 不发送真实输入(输入双闸默认关闭,见下)。

## 0. 前置

- Windows 10 1903+(WinML 内建);Rust 工具链(controller 侧);Go 1.22+(调度器)。
- 不需要:Python/PyTorch/CUDA/GPU/外部 ONNX Runtime 下载。

## 1. 编译

```powershell
# 调度器(Go)
go build ./...

# controller(Rust)
cd controller
cargo build
cd ..
# 产物: controller\target\debug\controller.exe
```

## 2. 录帧(NC9 学习原料)

`--window` 换成你的目标窗口标题子串;不传则默认 `@probe`(自带
640×480 探针窗口,画面有归一化锚定的红盒,适合先走通流程):

```powershell
controller\target\debug\controller.exe --dry-run --window @probe `
    --backend gdi --duration 10 --fps 15 --record C:\temp\frames --record-max 150
```

(也可以用 ffmpeg 从攻略视频抽帧:
`ffmpeg -i video.mp4 -vf fps=1 C:\temp\frames\frame_%05d.png`。)

## 3. 学习:帧 → 路线草案(NC9)

```powershell
cd tools\vision
python learn_route.py C:\temp\frames --inspect
# 产物: C:\temp\frames\route-draft.json
#  - 场景切分(全局均值 + 峰值块双阈值,抓得住弹窗/菜单这类局部变化)
#  - 每段进入时变化区域的【归一化锚点】+ 主色
python draft_to_skill.py C:\temp\frames\route-draft.json --out-dir C:\temp\skill
# 产物: C:\temp\skill\probes.json + skill.json(NC3 SkillDefinition 契约)
```

验证草案能被 controller 走查(离线回放,零输入):

```powershell
controller\target\debug\controller.exe --dry-run --replay C:\temp\frames `
    --probes C:\temp\skill\probes.json --skill C:\temp\skill\skill.json
# 期望轨迹: skill: -> step_01 ... -> done → DONE
```

## 4. 接入调度器(NC6)

config.json:

```json
{ "native_controller_path": "D:/codes/game-scheduler/controller/target/debug/controller.exe" }
```

建任务(params 即契约):

```powershell
$body = @'
{
  "game_id": "genshin",
  "name": "native-smoke",
  "type": "native",
  "params": {
    "executor": "native",
    "probes": "C:/temp/skill/probes.json",
    "skill": "C:/temp/skill/skill.json",
    "window": "@probe",
    "backend": "gdi",
    "duration_sec": 30
  },
  "enabled": true
}
'@
ctl -data $body tasks add
ctl tasks preflight <task_id>     # 应为 ready
ctl tasks run <task_id>           # Execution 落库,RESULT done → success
```

会话 TSV 在 `<data_dir>/native/exec-<id>.tsv`,EVENT 轨迹在执行记录的
stdout 字段,取消(`ctl execs cancel <id>`)会杀掉整棵 controller 进程树。

### auto 执行器(NC6):native 可用才走 native

把 `executor` 写成 `"auto"` 并配一个**适配器自有**的任务类型,任务就在
触发时实时决议:controller 已配置+可执行+声明的文件都在 → 走 native;
否则回退到该类型的外部命令(上例若配 `type=script` + `params.script`,
回退分支就是 BetterGI 脚本)。决议结果在 preflight 的 `resolution`
字段与执行记录 stdout 首行可见:

```powershell
ctl tasks preflight <task_id>     # resolution = auto→native / auto→external (原因)
```

注意:`executor=auto` 需要**有真实回退分支**——type=native(原生专用)
配 auto 会在 preflight/execute 显式报错,请直接用 `executor=native`。
auto 只降级不升级:它永远不会替你打开真实输入,双闸照旧。

## 5. 安全边界(为什么要双闸)

- 本任务形态**默认 dry_run=true**:只观察、只记录计划动作,不发输入。
- 真实输入需要**三闸同时**:
  1. 任务参数 `allow_input: true`;
  2. 任务参数 `dry_run: false`;
  3. config `native_allow_input: true`。
- 即便全开,controller 内的 SafetyGovernor 仍是逐动作最终裁决:
  窗口身份/前台/频率上限/同点连击/急停/会话时长。
- 红线(ROADMAP §7):无注入、无内存读写、无抓包改包、无反检测,永不实现。

## 6. 环境约束与故障排查

> ⚠️ **RDP/隐藏控制台黑帧（本节点实测,2026-09-11）**:调度器(或任何宿主)
> 以**隐藏控制台**启动时,GDI PrintWindow 捕获到的是**黑帧**(与 WGC 在
> RDP 会话静默同属环境约束族,非代码缺陷)。症状:`--capture-gdi`/dry-run
> 正常退出但检测/探针全不触发。规避:让 server 与 controller 运行在
> **可见控制台**上下文;长期方案待 WGC 实体控制台复验。探针类验收请
> 在可见控制台执行。

## 7. 故障排查

| 症状 | 含义 |
|---|---|
| `config native_controller_path is not set` | config 没配 controller 路径 |
| `no interactive desktop` / unsupported | 服务会话/Session 0 无法发输入与建窗,诚实跳过 |
| `protocol json error` | stdout 混入了非协议行——controller 必须 `--protocol` 且人读输出走 stderr |
| skill 一直 Waiting 后 failed | 期望(探针/标签)在录像里不成立:核对锚点坐标与主色容差 |
| RESULT stopped(reason=session exceeded) | governor 会话时长到达,属正常业务终态 |
