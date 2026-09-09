# datasets — 数据集约定（ROADMAP §4）

大图片数据与数据集文件**不进 Git**；Git 只保留本 README 与
`manifests/` 下的数据集清单（由 `tools/vision/prepare_dataset.py` 生成）。
真实图片/标注存放在外部存储，manifest 里的 `root` 字段指向其位置。

## 目录

```text
datasets/
  README.md            本文件：数据规范、覆盖矩阵、隐私
  manifests/           <name>.manifest.json 数据集清单 + train/val 切分列表
```

## 数据规范

- 布局：`<root>/images/` + `<root>/labels/`（YOLO txt：`class cx cy w h`，
  归一化 0..1）；用 `prepare_dataset.py` 校验后入库清单。
- 类名与索引的对应关系记录在各数据集清单 `classes` 字段；导出模型时
  `export_onnx.py --labels` 必须与之一致（顺序即索引）。

## 覆盖矩阵（目标，逐游戏打透时逐步补齐）

- 分辨率：1080p / 1440p / 4K
- 宽高比：16:9 / 21:9（letterbox 由 controller 变换层处理，模型只认 imgsz）
- 窗口模式：windowed / borderless
- 亮度 / 对比度扰动；（如需）不同 UI 语言
- 增强：scale / crop / brightness / contrast / mild blur（训练期由
  Ultralytics 完成，见 tools/vision/train.py）

## 隐私与红线

- 不含真实账号信息；不采集带账号 ID / 聊天内容的画面。
- 素材仅用于本项目感知模型训练；不标注/训练任何绕过类目标（§7 红线）。
