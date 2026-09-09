#!/usr/bin/env python3
"""frames_to_dataset.py — turn a controller --record frame directory into
a dataset layout ready for annotation and prepare_dataset.py.

The controller records numbered PNGs (frame_00000.png...). This script
copies them into `<out>/images/`, creates one empty YOLO label file per
image under `<out>/labels/` (fill them in with your annotation tool:
`class cx cy w h`, normalized 0..1), and writes a source manifest.

Bridge: controller --record → THIS → annotate → prepare_dataset.py →
train.py → export_onnx.py → controller --model-path (the full NC1/§4
loop, training data flowing one way, weights never entering Git).
"""

from __future__ import annotations

import argparse
import json
import shutil
import sys
from pathlib import Path

IMAGE_EXTS = {".png", ".jpg", ".jpeg", ".bmp"}


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("frames", help="directory of frame_*.png from `controller --record`")
    p.add_argument("--out", required=True, help="dataset root to create (images/ + labels/)")
    p.add_argument("--name", default="recorded", help="dataset name recorded in the manifest")
    p.add_argument("--source-session", default="", help="session TSV path for traceability")
    p.add_argument("--resolution", default="unknown", help="capture resolution, e.g. 1920x1080")
    p.add_argument("--aspect", default="16:9", help="aspect tag (default: 16:9)")
    p.add_argument("--move", action="store_true", help="move frames instead of copying")
    return p.parse_args()


def main() -> int:
    args = parse_args()
    frames_dir = Path(args.frames)
    if not frames_dir.is_dir():
        print(f"frames_to_dataset: ERROR not a directory: {frames_dir}", file=sys.stderr)
        return 2
    images = sorted(
        p for p in frames_dir.iterdir()
        if p.is_file() and p.suffix.lower() in IMAGE_EXTS
    )
    if not images:
        print("frames_to_dataset: ERROR no image frames found", file=sys.stderr)
        return 2

    out = Path(args.out)
    if out.exists():
        print(f"frames_to_dataset: ERROR output exists: {out} (refusing to merge)", file=sys.stderr)
        return 2
    (out / "images").mkdir(parents=True)
    (out / "labels").mkdir(parents=True)

    for src in images:
        dst = out / "images" / src.name
        if args.move:
            shutil.move(str(src), str(dst))
        else:
            shutil.copy2(str(src), str(dst))
        (out / "labels" / (dst.stem + ".txt")).write_text("", encoding="utf-8")

    manifest = {
        "schema_version": 1,
        "name": args.name,
        "root": str(out.resolve()),
        "resolution": args.resolution,
        "aspect": args.aspect,
        "annotation_version": "unannotated",
        "source": {
            "kind": "controller-record",
            "frames": len(images),
            "session_tsv": args.source_session,
        },
        "notes": "Labels are empty placeholders; annotate before prepare_dataset.py.",
    }
    manifest_path = out / f"{args.name}.source-manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")

    print(f"frames_to_dataset: {len(images)} frames -> {out}")
    print(f"frames_to_dataset: manifest {manifest_path}")
    print("next: annotate labels/ (YOLO txt), then validate with prepare_dataset.py")
    return 0


if __name__ == "__main__":
    sys.exit(main())
