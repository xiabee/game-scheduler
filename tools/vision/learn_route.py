#!/usr/bin/env python3
"""learn_route.py — NC9 learning-pipeline skeleton: frames → scene
segments → route-learning draft (ROADMAP §3 NC9).

Input is a directory of numbered PNG frames — what `controller --record`
produces, or any extraction the user brings (ffmpeg one-liners live in
tools/vision/README.md). Everything here is stdlib-only (no PIL/numpy/
ffmpeg at run time), downsamples hard before thinking, and emits a
schema'd draft whose anchor coordinates are NORMALIZED 0..1 — the
resolution-independence rule (NIGHTOPS hard constraint) applies to
learned data too, not just runtime transforms.

What this milestone deliberately does NOT do: OCR, YOLO, semantic
labels, real-game anything. It produces the structural draft that later
NC9 milestones annotate into SkillDefinitions (NC3 schema).

Resource shape: one PNG decode per frame (streamed, constant memory
beyond one frame + one downsampled grid). A 10-minute 1fps capture
(~600 frames) stays in the seconds-to-tens-of-seconds range on CPU.
"""

from __future__ import annotations

import argparse
import json
import sys
import tempfile
from pathlib import Path

import pnglite

DEFAULT_MAX_SIDE = 64       # downsampled grid cap (largest dimension)
DEFAULT_THRESHOLD = 6.0     # global mean-abs gray-diff (0..255) for a cut
DEFAULT_BLOCK_THRESHOLD = 24.0  # peak block diff — catches localized UI pops
DEFAULT_BLOCK = 8           # anchor localizer grid (blocks per axis)


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("frames", nargs="?", help="directory of numbered PNG frames")
    p.add_argument("--out", help="draft JSON path (default: <frames>/route-draft.json)")
    p.add_argument("--max-side", type=int, default=DEFAULT_MAX_SIDE,
                   help=f"downsample cap, largest side (default {DEFAULT_MAX_SIDE})")
    p.add_argument("--threshold", type=float, default=DEFAULT_THRESHOLD,
                   help=f"scene-cut global mean-abs-diff threshold (default {DEFAULT_THRESHOLD})")
    p.add_argument("--block-threshold", type=float, default=DEFAULT_BLOCK_THRESHOLD,
                   help=f"scene-cut peak-block-diff threshold — catches localized UI "
                        f"changes like dialogs (default {DEFAULT_BLOCK_THRESHOLD})")
    p.add_argument("--inspect", action="store_true",
                   help="print the human-readable segment timeline")
    p.add_argument("--selftest", action="store_true",
                   help="run the synthetic end-to-end check and exit")
    return p.parse_args()


def downsample_gray(width: int, height: int, channels: int, pix: bytes, max_side: int):
    """Average-pool the image into a grid whose largest side is <= max_side;
    returns (gw, gh, luminance bytes)."""
    sx = max(1, width // max_side)
    sy = max(1, height // max_side)
    gw = max(1, width // sx)
    gh = max(1, height // sy)
    gray = bytearray(gw * gh)
    for gy in range(gh):
        for gx in range(gw):
            acc = n = 0
            for py in range(gy * sy, min((gy + 1) * sy, height), max(1, sy // 4)):
                row = py * width
                for px in range(gx * sx, min((gx + 1) * sx, width), max(1, sx // 4)):
                    o = (row + px) * channels
                    # luminance approximation, integer weights
                    acc += (pix[o] * 299 + pix[o + 1] * 587 + pix[o + 2] * 114) // 1000
                    n += 1
            gray[gy * gw + gx] = acc // max(1, n)
    return gw, gh, bytes(gray)


def block_diffs(gw: int, gh: int, a: bytes, b: bytes, blocks: int):
    """Per-block mean abs diff between two grids; returns
    [(score, bx, by, bx1, by1)] in grid cells."""
    bw = max(1, gw // blocks)
    bh = max(1, gh // blocks)
    out = []
    for by in range(0, gh, bh):
        for bx in range(0, gw, bw):
            acc = n = 0
            for y in range(by, min(by + bh, gh)):
                for x in range(bx, min(bx + bw, gw)):
                    i = y * gw + x
                    acc += abs(a[i] - b[i])
                    n += 1
            out.append((acc / max(1, n), bx, by, min(bx + bw, gw), min(by + bh, gh)))
    return out


def anchor_between(gw: int, gh: int, a: bytes, b: bytes, blocks: int) -> dict | None:
    """Localize WHERE two segment representatives differ: bounding box of
    the hot blocks, in NORMALIZED coordinates (0..1)."""
    scores = block_diffs(gw, gh, a, b, blocks)
    if not scores:
        return None
    peak = max(s[0] for s in scores)
    if peak <= 0:
        return None
    hot = [s for s in scores if s[0] >= peak * 0.5]
    x0 = min(s[1] for s in hot) / gw
    y0 = min(s[2] for s in hot) / gh
    x1 = max(s[3] for s in hot) / gw
    y1 = max(s[4] for s in hot) / gh
    return {
        "normalized": True,
        "x": round(x0, 4), "y": round(y0, 4),
        "w": round(x1 - x0, 4), "h": round(y1 - y0, 4),
        "peak_block_score": round(peak, 2),
    }


def learn(frames_dir: Path, max_side: int, threshold: float, block_threshold: float) -> dict:
    frames = sorted(p for p in frames_dir.iterdir()
                    if p.is_file() and p.suffix.lower() == ".png")
    if len(frames) < 2:
        raise SystemExit(f"learn_route: need >= 2 PNG frames in {frames_dir}, found {len(frames)}")

    grids = []
    src_size = None
    for f in frames:
        w, h, ch, pix = pnglite.read_png(f)
        src_size = (w, h)
        grids.append(downsample_gray(w, h, ch, pix, max_side))
    gw, gh, _ = grids[0]
    for g in grids:
        if (g[0], g[1]) != (gw, gh):
            raise SystemExit("learn_route: frames differ in size after downsampling")

    # Scene cuts: adjacent-grid change crossing EITHER threshold —
    # global mean (full-frame swaps) or peak block (localized UI pops:
    # a dialog opening may cover only a few percent of the frame, which a
    # global mean would dilate away, yet it is exactly the event NC9 must
    # catch).
    pair_stats = []
    for i in range(len(grids) - 1):
        blocks = block_diffs(gw, gh, grids[i][-1], grids[i + 1][-1], DEFAULT_BLOCK)
        mean = sum(s[0] for s in blocks) / max(1, len(blocks))
        peak = max(s[0] for s in blocks)
        pair_stats.append((round(mean, 2), round(peak, 2)))
    cuts = [i + 1 for i, (mean, peak) in enumerate(pair_stats)
            if mean > threshold or peak > block_threshold]
    bounds = [0] + cuts + [len(grids)]

    segments = []
    for si in range(len(bounds) - 1):
        start, end = bounds[si], bounds[si + 1]
        if start >= end:
            continue
        rep = grids[end - 1][-1]
        # Anchor = WHAT CHANGED when entering this segment (previous
        # segment's representative vs this one) — that difference is the
        # route step. Segment 0 has no entry, so no anchor.
        if start > 0:
            prev_rep = grids[start - 1][-1]
            anchor = anchor_between(gw, gh, prev_rep, rep, DEFAULT_BLOCK) or {}
        else:
            anchor = {}
        mean_at_entry, peak_at_entry = pair_stats[start - 1] if start > 0 else (0.0, 0.0)
        segments.append({
            "index": len(segments),
            "start_frame": start,
            "end_frame": end - 1,
            "frames": end - start,
            "entry_mean_diff": mean_at_entry,
            "entry_peak_diff": peak_at_entry,
            "anchor": anchor,
        })

    return {
        "schema_version": 1,
        "kind": "route-learning-draft",
        "source": {
            "frames_dir": str(frames_dir.resolve()),
            "frame_count": len(grids),
            "frame_size": list(src_size or (0, 0)),
        },
        "params": {"max_side": max_side, "threshold": threshold,
                   "block_threshold": block_threshold, "block": DEFAULT_BLOCK},
        "grid": {"w": gw, "h": gh},
        "segment_count": len(segments),
        "segments": segments,
    }


def selftest() -> int:
    """Synthetic two-scene sequence must split at the cut, anchor near the
    painted region's normalized center; an identical-frame sequence must
    not split at all."""
    def frame(w: int, h: int, paint: bool) -> bytes:
        buf = bytearray(w * h * 4)
        for y in range(h):
            for x in range(w):
                o = (y * w + x) * 4
                buf[o:o + 4] = (24, 24, 28, 255)
        if paint:
            x0, y0, x1, y1 = int(w * 0.6), int(h * 0.6), int(w * 0.8), int(h * 0.8)
            for y in range(y0, y1):
                for x in range(x0, x1):
                    buf[(y * w + x) * 4:(y * w + x) * 4 + 4] = (200, 40, 16, 255)
        return bytes(buf)

    with tempfile.TemporaryDirectory(prefix="learn-route-selftest-") as td:
        d = Path(td)
        w, h = 160, 120
        seq = [frame(w, h, False)] * 8 + [frame(w, h, True)] * 8
        for i, pix in enumerate(seq):
            pnglite.write_png(d / f"frame_{i:05d}.png", w, h, pix)
        draft = learn(d, max_side=DEFAULT_MAX_SIDE, threshold=DEFAULT_THRESHOLD,
                      block_threshold=DEFAULT_BLOCK_THRESHOLD)

        ok = True
        if draft["segment_count"] != 2:
            print(f"selftest FAIL: expected 2 segments, got {draft['segment_count']}")
            ok = False
        cut = draft["segments"][1] if draft["segment_count"] == 2 else {}
        if cut.get("start_frame") != 8:
            print(f"selftest FAIL: cut expected at frame 8, got {cut.get('start_frame')}")
            ok = False
        anchor = cut.get("anchor") or {}
        cx = anchor.get("x", 0) + anchor.get("w", 0) / 2
        cy = anchor.get("y", 0) + anchor.get("h", 0) / 2
        if abs(cx - 0.7) > 0.15 or abs(cy - 0.7) > 0.15:
            print(f"selftest FAIL: anchor center ({cx:.2f},{cy:.2f}) not near (0.70,0.70)")
            ok = False

        same = d / "same"
        same.mkdir()
        for i in range(10):
            pnglite.write_png(same / f"frame_{i:05d}.png", w, h, frame(w, h, False))
        draft2 = learn(same, max_side=DEFAULT_MAX_SIDE, threshold=DEFAULT_THRESHOLD,
                       block_threshold=DEFAULT_BLOCK_THRESHOLD)
        if draft2["segment_count"] != 1:
            print(f"selftest FAIL: static sequence must not split, got {draft2['segment_count']}")
            ok = False

        # PNG round-trip check: our own writer → our own reader, byte-equal
        probe = frame(37, 23, True)
        p = d / "probe.png"
        pnglite.write_png(p, 37, 23, probe)
        rw, rh, rc, rpix = pnglite.read_png(p)
        if (rw, rh, rc, rpix) != (37, 23, 4, probe):
            print("selftest FAIL: PNG round-trip mismatch")
            ok = False

        if ok:
            print("learn_route selftest: PASS (2-segment split at 8, anchor ~(0.7,0.7), static no-split, PNG round-trip)")
            return 0
        return 1


def main() -> int:
    args = parse_args()
    if args.selftest:
        return selftest()
    frames_dir = Path(args.frames) if args.frames else None
    if frames_dir is None or not frames_dir.is_dir():
        print("learn_route: ERROR provide a frames directory (or --selftest)", file=sys.stderr)
        return 2
    draft = learn(frames_dir, args.max_side, args.threshold, args.block_threshold)
    out = Path(args.out) if args.out else frames_dir / "route-draft.json"
    out.write_text(json.dumps(draft, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"learn_route: {draft['source']['frame_count']} frames -> "
          f"{draft['segment_count']} segment(s), grid {draft['grid']['w']}x{draft['grid']['h']}")
    print(f"learn_route: draft {out}")
    if args.inspect:
        for s in draft["segments"]:
            a = s.get("anchor") or {}
            print(f"  [{s['start_frame']:>5}..{s['end_frame']:>5}] "
                  f"entry(mean/peak)={s['entry_mean_diff']:>6}/{s['entry_peak_diff']:>6} "
                  f"anchor=({a.get('x', 0):.2f},{a.get('y', 0):.2f} {a.get('w', 0):.2f}x{a.get('h', 0):.2f})")
    print("next: annotate segments into SkillDefinition drafts (NC9 -> NC3), "
          "or validate a UI page with L0 probes on the anchor region")
    return 0


if __name__ == "__main__":
    sys.exit(main())
