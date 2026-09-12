# tools/vision — training pipeline (NC1, ROADMAP §4) + learning pipeline (NC9)

训练与运行解耦：训练在 Python/Ultralytics 里做，controller 运行时只依赖
Windows 内建的 ONNX Runtime（WinML），不依赖 Python / PyTorch / CUDA。

```text
datasets/                  (仓外权重与大图, 见 datasets/README.md)
  manifests/               数据集清单 (json)
tools/vision/
  frames_to_dataset.py     controller --record 帧目录 → 数据集布局(桥接采集)
  prepare_dataset.py       校验目录结构 + 生成数据集 manifest + train/val 切分
  train.py                 Ultralytics 训练封装 (先打印 plan, --yes 才执行)
  export_onnx.py           导出 ONNX + 生成 controller schema-v1 模型 manifest
  pnglite.py               stdlib-only PNG 读写(8-bit RGB/RGBA, NC9 学习管线底座)
  learn_route.py           NC9: 帧序列 → 场景切分 + 归一化锚点 → route-learning
                           draft JSON (纯离线, 无模型/无 GPU/无 ffmpeg 依赖)
  draft_to_skill.py        NC9→NC3 桥: draft → probes.json + skill.json
                           (controller --dry-run --replay 可直接走查)
```

采集→训练闭环：`controller --record` 录帧 → `frames_to_dataset.py` 转
数据集布局 → 标注 labels/ → `prepare_dataset.py` 校验切分 → `train.py`
→ `export_onnx.py` → `controller --model-path` 加载。

## NC9 学习管线（路线学习草案）

`learn_route.py <frames_dir> [--inspect]`：消费帧目录（`controller
--record` 产物，或用户自备抽帧——有 ffmpeg 时
`ffmpeg -i video.mp4 -vf fps=1 frames/frame_%05d.png` 即可），降采样后
做相邻帧差分，按「全局均值 or 峰值块变化」切分场景段，并输出每段进入
时变化区域的**归一化锚点**（0..1，分辨率无关硬约束同样约束学习产物）。

- 产物 `route-draft.json`（schema_version 1）经 `draft_to_skill.py` 转成
  controller 可直接消费的 `probes.json` + `skill.json`（NC3
  SkillDefinition 契约）：每段一个 L0 探针（锚点区域 + 学习到的主色，
  归一化按录制帧尺寸烙定为 client 像素——注意探针 expected 为 BGRA 通道
  序，转换器已处理），状态机 step_00→…→done 线性走查。端到端验证：

  ```text
  controller --dry-run --replay <frames_dir> \
      --probes <out>/probes.json --skill <out>/skill.json
  # 期望轨迹: skill: -> step_01 (planned: ...) → -> done → DONE
  ```

  已在合成两场景序列上实测走查至 DONE（计划动作携带归一化锚点坐标，
  零真实输入）。
- 指标敏感性：全局均值抓整屏切换，峰值块阈值抓局部 UI 变化（弹窗/
  菜单展开只占画面几个百分点，全局均值会把它稀释掉——恰是 NC9 要抓
  的事件）。两阈值均可 CLI 调。
- 资源形态：逐帧流式解码，降采样到 ≤64px 网格后再思考；无 GPU、无
  模型下载、常数量级内存。`python learn_route.py --selftest` 是合成
  端到端守护（nightly-verify 已接入）。
- 链式夹具 e2e：`python pipeline_e2e.py --controller <controller.exe>`
  在临时目录生成**确定性** 4 场景合成录像（16 帧，无随机无时间戳），
  走完 learn → convert → controller replay 全链并断言 RESULT done
  （nightly-verify 已接入；生成脚本即入库的夹具定义，仓库不放二进制帧）。
- 真实游戏测试按 ROADMAP NC9 显式 deferred：本管线全部工作在帧目录上。

## 流程

1. **prepare_dataset.py**: 校验 `images/ + labels/` (YOLO txt) 布局，
   写 `datasets/manifests/<name>.json`，输出 train/val 切分文件。
2. **train.py**: 不带 `--yes` 只打印将要做的事（含将要下载的基模型）；
   带 `--yes` 且 ultralytics 可导入才真正训练。权重产物留在仓外。
3. **export_onnx.py**: `.pt → .onnx`（opset/imgsz 显式指定），并在导出
   旁边生成 **schema-v1 manifest**（labels / input_size / version /
   default_confidence / game_profile）——与 `controller/src/manifest.rs`
   的解析器逐字段对齐，可用
   `controller.exe --manifest-check <path>` 验证。

## 约定

- 大图片数据与模型权重**不进 Git**（`.gitignore` 已全局忽略 `*.onnx`）；
  Git 里只有 manifest 与本脚手架。
- 目标是 resolution-independent：数据集覆盖矩阵见 datasets/README.md；
  坐标一律走 controller 的四坐标系变换，模型只认 imgsz 空间。
- 模型轻量优先：nano/small，imgsz 适度（640 起步可降），idle 低频触发。
