#!/usr/bin/env python3
"""train.py — Ultralytics training wrapper with an explicit plan gate.

Without --yes the script only PRINTS what it would do (including which
base model it would download). Training runs only when --yes is passed
AND ultralytics imports. Night/unattended runs must never surprise-
download models or occupy the GPU: run training deliberately, in the
day, with resources you own (ROADMAP §4, NIGHTOPS resource limits).
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import sys
from pathlib import Path

DEFAULTS = {
    "model": "yolov8n.pt",   # nano first (轻量优先)
    "imgsz": 640,
    "epochs": 60,
    "batch": 8,
    "device": "cpu",         # opt into GPU explicitly; never by accident
}


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("data_yaml", help="dataset yaml (Ultralytics format); build datasets with prepare_dataset.py")
    p.add_argument("--model", default=DEFAULTS["model"], help=f"base model (default: {DEFAULTS['model']})")
    p.add_argument("--imgsz", type=int, default=DEFAULTS["imgsz"], help=f"training imgsz (default: {DEFAULTS['imgsz']})")
    p.add_argument("--epochs", type=int, default=DEFAULTS["epochs"], help=f"epochs (default: {DEFAULTS['epochs']})")
    p.add_argument("--batch", type=int, default=DEFAULTS["batch"], help=f"batch size (default: {DEFAULTS['batch']})")
    p.add_argument("--device", default=DEFAULTS["device"], help=f"torch device (default: {DEFAULTS['device']} — opt into GPU explicitly)")
    p.add_argument("--project", default="runs/vision", help="run output directory (git-ignored)")
    p.add_argument("--name", default=None, help="run name (default: timestamp)")
    p.add_argument("--yes", action="store_true", help="actually run (downloads the base model if missing)")
    return p.parse_args()


def plan(args: argparse.Namespace) -> dict:
    return {
        "data": str(Path(args.data_yaml).resolve()),
        "model": args.model,
        "imgsz": args.imgsz,
        "epochs": args.epochs,
        "batch": args.batch,
        "device": args.device,
        "project": args.project,
        "will_download": args.model.startswith(("yolov8", "yolo11")),
        "runs_dir_git_ignored": True,
    }


def main() -> int:
    args = parse_args()
    if not Path(args.data_yaml).is_file():
        print(f"train: ERROR data yaml not found: {args.data_yaml}", file=sys.stderr)
        return 2

    print("train: PLAN " + json.dumps(plan(args), indent=2))
    if not args.yes:
        print("train: dry plan only — pass --yes to execute (may download the base model)")
        return 0

    if importlib.util.find_spec("ultralytics") is None:
        print("train: ERROR ultralytics is not installed in this environment "
              "(pip install ultralytics); refusing to auto-install", file=sys.stderr)
        return 2

    from ultralytics import YOLO  # imported only after the gate

    model = YOLO(args.model)
    model.train(
        data=args.data_yaml,
        imgsz=args.imgsz,
        epochs=args.epochs,
        batch=args.batch,
        device=args.device,
        project=args.project,
        name=args.name,
    )
    print("train: done — export with export_onnx.py (weights stay out of Git)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
