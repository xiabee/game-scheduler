# L0 probes & skills (NC2/NC3 groundwork)

坐标规则：探针永远写 **client 像素**（窗口客户区左上角为原点），跨分辨率
的正确性由 controller 的四坐标系变换保证；禁止任何硬编码绝对像素出现在
action 计划之外。

## L0 probes（`--probes probes.json`）

一个 JSON 数组，每个探针声明"客户区某区域内多大比例的像素接近期望颜色"：

| 字段 | 类型 | 说明 |
|---|---|---|
| `name` | string | 非 blank；skill 的 `expect.probe` 按名字引用 |
| `x`,`y`,`w`,`h` | u32 | 客户区采样区域 |
| `expected` | [b,g,r] | 期望颜色（alpha 忽略） |
| `tolerance` | u8 | 每通道容差（双向） |
| `min_fraction` | (0,1] | 触发所需的匹配像素占比 |
| `step` | u32 | 区域内采样步长（限界采样成本） |

校验失败（越界、比例非法、数量 >256）在启动时 exit 1，绝不静默忽略。

## Skills（`--skill skill.json`）

状态机定义（见 `examples/skills/daily_claim.example.json`）：

- 每个状态声明 `expect`（全部 AND 成立才转移）：
  - `{ "probe": "名字" }` —— 引用 `--probes` 中已配置的探针；
  - `{ "label": "检测标签", "min_confidence": 0.6 }` —— 检测结果匹配。
- `actions` 是**计划**的只读描述，dry-run 只打印、NC4 之前不存在执行。
- `timeout_ms` + `max_retries` 组成预算；耗尽后走 `fallback` 状态，无
  fallback 则技能 FAILED。
- `terminal: true` 的状态到达即技能 DONE——无论经 `next` 转移还是经
  `fallback` 回退落入,都算完成;引擎在终态后每周期重复返回的 Done 不再
  发协议 EVENT（D1,恰好一次由落入终态的那次转移宣告）。
- 校验：状态名唯一、`next`/`fallback` 可解析、`start` 必须存在。

## 会话组合

```powershell
controller.exe --dry-run --backend gdi `
  --probes examples\probes.example.json `
  --skill examples\skills\daily_claim.example.json `
  --session-log ..\.nightly\skill-session.tsv
```

技能每周期由真实管线证据驱动（探针触发 + 检测结果），转移/回退/完成都
写入会话输出；`--record`/`--replay` 可以把这一过程离线复现。
