# tools/vision — training pipeline (NC1, ROADMAP §4)

训练与运行解耦：训练在 Python/Ultralytics 里做，controller 运行时只依赖
Windows 内建的 ONNX Runtime（WinML），不依赖 Python / PyTorch / CUDA。

```text
datasets/                  (仓外权重与大图, 见 datasets/README.md)
  manifests/               数据集清单 (json)
tools/vision/
  prepare_dataset.py       校验目录结构 + 生成数据集 manifest + train/val 切分
  train.py                 Ultralytics 训练封装 (先打印 plan, --yes 才执行)
  export_onnx.py           导出 ONNX + 生成 controller schema-v1 模型 manifest
```

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
