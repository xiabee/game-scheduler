#!/usr/bin/env python3
"""prepare_dataset.py — validate a YOLO-style dataset and emit its manifest.

Stdlib only, no heavy dependencies. Layout:

    <dataset_root>/
      images/  *.png|*.jpg|*.jpeg|*.bmp
      labels/  *.txt   (one per image, YOLO: class cx cy w h, normalized)

Outputs (next to --out):
    <out>/<name>.manifest.json   dataset manifest (source, split, counts)
    <out>/<name>_train.txt       image list for training
    <out>/<name>_val.txt         image list for validation
"""

from __future__ import annotations

import argparse
import json
import random
import sys
from pathlib import Path

IMAGE_EXTS = {".png", ".jpg", ".jpeg", ".bmp"}


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("root", help="dataset root containing images/ and labels/")
    p.add_argument("--name", required=True, help="dataset name (used in output file names)")
    p.add_argument("--out", default="datasets/manifests", help="output directory (default: datasets/manifests)")
    p.add_argument("--val-ratio", type=float, default=0.2, help="validation fraction in (0, 1) (default: 0.2)")
    p.add_argument("--seed", type=int, default=7, help="shuffle seed (default: 7)")
    p.add_argument("--resolution", default="unknown", help="primary capture resolution, e.g. 1920x1080")
    p.add_argument("--aspect", default="16:9", help="primary aspect ratio (default: 16:9)")
    p.add_argument("--annotation-version", default="v1", help="annotation version tag (default: v1)")
    return p.parse_args()


def fail(msg: str) -> "NoReturn":  # type: ignore[name-defined]
    print(f"prepare_dataset: ERROR: {msg}", file=sys.stderr)
    sys.exit(2)


def main() -> int:
    args = parse_args()
    root = Path(args.root)
    images_dir, labels_dir = root / "images", root / "labels"
    if not images_dir.is_dir() or not labels_dir.is_dir():
        fail(f"{root} must contain images/ and labels/")

    images = sorted(p for p in images_dir.iterdir() if p.suffix.lower() in IMAGE_EXTS)
    if not images:
        fail(f"no images found under {images_dir}")

    missing_labels, bad_labels = [], []
    for img in images:
        label = labels_dir / (img.stem + ".txt")
        if not label.is_file():
            missing_labels.append(img.name)
            continue
        for lineno, line in enumerate(label.read_text(encoding="utf-8").splitlines(), 1):
            parts = line.split()
            if not parts:
                continue
            if len(parts) != 5:
                bad_labels.append(f"{label.name}:{lineno} (want 'class cx cy w h')")
                continue
            cls, cx, cy, w, h = parts[0], *(float(v) for v in parts[1:])
            if not (0.0 <= cx <= 1.0 and 0.0 <= cy <= 1.0 and 0.0 < w <= 1.0 and 0.0 < h <= 1.0):
                bad_labels.append(f"{label.name}:{lineno} (boxes must be normalized 0..1)")
            try:
                int(cls)
            except ValueError:
                bad_labels.append(f"{label.name}:{lineno} (class must be an integer index)")

    if bad_labels:
        fail("invalid label lines: " + "; ".join(bad_labels[:5]) + (" ..." if len(bad_labels) > 5 else ""))

    usable = [img for img in images if (labels_dir / (img.stem + ".txt")).is_file()]
    if missing_labels:
        print(f"prepare_dataset: WARNING {len(missing_labels)} images without labels are excluded "
              f"(first: {missing_labels[0]})")
    if len(usable) < 2:
        fail("need at least 2 labeled images to split train/val")

    if not (0.0 < args.val_ratio < 1.0):
        fail("--val-ratio must be in (0, 1)")

    rng = random.Random(args.seed)
    shuffled = usable[:]
    rng.shuffle(shuffled)
    val_count = max(1, round(len(shuffled) * args.val_ratio))
    val, train = shuffled[:val_count], shuffled[val_count:]

    out_dir = Path(args.out)
    out_dir.mkdir(parents=True, exist_ok=True)
    train_list = out_dir / f"{args.name}_train.txt"
    val_list = out_dir / f"{args.name}_val.txt"
    train_list.write_text("\n".join(str(p.resolve()) for p in train) + "\n", encoding="utf-8")
    val_list.write_text("\n".join(str(p.resolve()) for p in val) + "\n", encoding="utf-8")

    classes = sorted({int(line.split()[0]) for img in usable
                      for line in (labels_dir / (img.stem + ".txt")).read_text(encoding="utf-8").splitlines()
                      if line.strip()})
    manifest = {
        "schema_version": 1,
        "name": args.name,
        "root": str(root.resolve()),
        "resolution": args.resolution,
        "aspect": args.aspect,
        "annotation_version": args.annotation_version,
        "images_total": len(images),
        "images_usable": len(usable),
        "images_train": len(train),
        "images_val": len(val),
        "classes": classes,
        "notes": "Images/labels stay external to Git; this manifest is the tracked reference.",
    }
    manifest_path = out_dir / f"{args.name}.manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    print(f"prepare_dataset: OK {len(train)} train / {len(val)} val images, classes={classes}")
    print(f"prepare_dataset: manifest {manifest_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
