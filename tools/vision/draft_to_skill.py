#!/usr/bin/env python3
"""draft_to_skill.py — NC9 step 2: turn a route-learning draft into the
probe + skill definition pair the controller can actually execute in a
dry-run replay (ROADMAP §3 NC9 → NC3 bridge).

The draft's anchors are NORMALIZED (resolution-independent by contract);
L0 probes are CLIENT-PIXEL, so the converter bakes them out against the
draft's recorded frame size — the normalized source of truth stays in the
draft, the emitted probe pair documents the frame size it was baked for.

Output pair:
- probes JSON: one L0 probe per route step (anchor region + learned
  dominant color + generous tolerance)
- skill JSON: a linear state machine step_00 → step_01 → … → done, where
  step_i's expectation is "probe step_i fired" (i.e. the change that
  defines this route step is visible). Segment 0 is the recording's
  baseline and needs no probe: its expectations are vacuously met.

Validate the pair end to end with the controller, fully offline:

    controller --dry-run --replay <frames_dir> \
        --probes probes.json --skill skill.json --session-log replay.tsv
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

DEFAULT_TOLERANCE = 24
DEFAULT_MIN_FRACTION = 0.4
DEFAULT_TIMEOUT_MS = 15000


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("draft", help="route-draft.json from learn_route.py")
    p.add_argument("--out-dir", required=True, help="directory to write probes/skill JSON into (created)")
    p.add_argument("--name", default="", help="skill name (default: route_draft_<frames dir name>)")
    p.add_argument("--tolerance", type=int, default=DEFAULT_TOLERANCE,
                   help=f"probe color tolerance (default {DEFAULT_TOLERANCE})")
    return p.parse_args()


def convert(draft: dict, name: str, tolerance: int) -> tuple[dict, dict, list[str]]:
    width, height = draft.get("source", {}).get("frame_size", [0, 0])
    if width <= 0 or height <= 0:
        raise SystemExit("draft_to_skill: draft has no usable source.frame_size")
    segments = draft.get("segments", [])
    if not segments:
        raise SystemExit("draft_to_skill: draft has no segments")

    warnings: list[str] = []
    probes = []
    states = []

    steps = []  # (probe_name or None, anchor)
    for seg in segments:
        anchor = seg.get("anchor") or {}
        if not anchor:
            if seg.get("index", 0) == 0:
                steps.append((None, None))  # baseline: vacuous expectation
            else:
                warnings.append(
                    f"segment {seg.get('index')} has no anchor; emitting vacuous step"
                )
                steps.append((None, None))
            continue
        color = anchor.get("dominant_rgb")
        if not color:
            warnings.append(f"segment {seg.get('index')} anchor lacks dominant_rgb; "
                            f"emitting vacuous step (re-run learn_route to add colors)")
            steps.append((None, None))
            continue
        i = seg["index"]
        pname = f"step_{i:02d}"
        # normalized -> client pixels of THIS recording (see module doc)
        px = round(anchor["x"] * width)
        py = round(anchor["y"] * height)
        pw = max(4, round(anchor["w"] * width))
        ph = max(4, round(anchor["h"] * height))
        r, g, b = color
        probes.append({
            "name": pname,
            "x": px, "y": py, "w": pw, "h": ph,
            # PixelProbe.expected is BGRA-order (BGRA8 frames, alpha ignored)
            # while the draft stores RGB — flip here, once, with a comment.
            "expected": [b, g, r],
            "tolerance": tolerance,
            "min_fraction": DEFAULT_MIN_FRACTION,
            "step": 2,
        })
        steps.append((pname, anchor))

    if len(steps) == 1:
        # degenerate: single baseline segment (static recording)
        warnings.append("single-segment draft: emitting one terminal state (trivial skill)")

    for i, (pname, anchor) in enumerate(steps):
        is_last = i == len(steps) - 1
        state = {
            "name": f"step_{i:02d}",
            # step_00 is the baseline: no expectation -> vacuously holds,
            # the engine advances immediately and the route walk begins.
            "expect": ([{"probe": pname}] if pname else []),
            "actions": ([f"click(anchor {anchor['x']:.3f},{anchor['y']:.3f})"]
                        if anchor and pname else []),
            "next": f"step_{i + 1:02d}" if not is_last else "done",
            "timeout_ms": DEFAULT_TIMEOUT_MS,
            "max_retries": 1,
            "terminal": False,
        }
        states.append(state)
    states.append({"name": "done", "expect": [], "actions": [],
                   "next": "done", "timeout_ms": 1000, "max_retries": 1,
                   "terminal": True})

    skill = {"name": name or "route_draft", "start": "step_00", "states": states}
    return probes, skill, warnings


def selftest() -> int:
    """Build a two-step draft inline, convert, and validate the emitted
    pair structurally (probe fields, skill walk order, JSON round-trip)."""
    draft = {
        "schema_version": 1,
        "kind": "route-learning-draft",
        "source": {"frames_dir": "synthetic", "frame_count": 12, "frame_size": [160, 120]},
        "segments": [
            {"index": 0, "start_frame": 0, "end_frame": 5, "frames": 6,
             "entry_mean_diff": 0.0, "entry_peak_diff": 0.0, "anchor": {}},
            {"index": 1, "start_frame": 6, "end_frame": 11, "frames": 6,
             "entry_mean_diff": 2.4, "entry_peak_diff": 61.0,
             "anchor": {"normalized": True, "x": 0.625, "y": 0.625, "w": 0.125,
                        "h": 0.125, "peak_block_score": 61.0,
                        "dominant_rgb": [200, 40, 16]}},
        ],
    }
    probes, skill, warnings = convert(draft, "selftest_route", DEFAULT_TOLERANCE)
    ok = True
    if len(probes) != 1:
        print(f"selftest FAIL: expected 1 probe, got {len(probes)}")
        ok = False
    pr = probes[0]
    if (pr["x"], pr["y"], pr["w"], pr["h"]) != (100, 75, 20, 15):
        print(f"selftest FAIL: probe bake-out wrong: {pr}")
        ok = False
    if pr["expected"] != [16, 40, 200]:  # BGRA order: RGB(200,40,16) flipped
        print(f"selftest FAIL: probe color wrong (want BGRA): {pr['expected']}")
        ok = False
    names = [s["name"] for s in skill["states"]]
    if names != ["step_00", "step_01", "done"]:
        print(f"selftest FAIL: state walk wrong: {names}")
        ok = False
    if not skill["states"][-1]["terminal"] or skill["states"][0]["expect"]:
        print("selftest FAIL: terminal/baseline state misconfigured")
        ok = False
    # JSON round-trip
    json.dumps(probes), json.dumps(skill)
    if ok:
        print("draft_to_skill selftest: PASS (bake-out 100,75 20x15, BGRA [16,40,200], walk step_00->step_01->done)")
        return 0
    return 1


def main() -> int:
    args = parse_args()
    if getattr(args, "selftest", False):
        return selftest()
    draft_path = Path(args.draft)
    if not draft_path.is_file():
        print(f"draft_to_skill: ERROR no such draft: {draft_path}", file=sys.stderr)
        return 2
    draft = json.loads(draft_path.read_text(encoding="utf-8"))
    if draft.get("kind") != "route-learning-draft":
        print("draft_to_skill: ERROR input is not a route-learning-draft", file=sys.stderr)
        return 2
    name = args.name or f"route_draft_{draft_path.parent.name}"
    probes, skill, warnings = convert(draft, name, args.tolerance)
    out = Path(args.out_dir)
    out.mkdir(parents=True, exist_ok=False)
    (out / "probes.json").write_text(
        json.dumps(probes, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    (out / "skill.json").write_text(
        json.dumps(skill, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    for w in warnings:
        print(f"draft_to_skill: WARNING {w}")
    print(f"draft_to_skill: {len(probes)} probe(s), {len(skill['states'])} state(s) -> {out}")
    print(f"next: controller --dry-run --replay <frames> --probes {out / 'probes.json'} "
          f"--skill {out / 'skill.json'}")
    return 0


if __name__ == "__main__":
    # selftest flag shared with the other tools' convention
    if "--selftest" in sys.argv:
        sys.exit(selftest())
    sys.exit(main())
