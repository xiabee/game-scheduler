#!/usr/bin/env python3
"""pipeline_e2e.py — NC9 chained fixture test: frames → learn_route →
draft_to_skill → controller dry-run replay (ROADMAP §3 NC9, "端到端用小体积
录制视频夹具,保证 CI 确定性").

The individual steps ship their own --selftest checks; this script chains
them the way an operator would (quickstart path) against a DETERMINISTIC
synthetic recording: a fixed 4-scene frame sequence (dark baseline, then a
red / blue / green block pops in at a fixed spot). No randomness, no
timestamps, no network — the same bytes every run, so battery failures are
code regressions, not flaky fixtures.

Frames are generated into a temp dir at run time (the generator script is
the committed fixture definition; no binary blobs in git).

Without --controller the chain stops after conversion and validates the
emitted probes/skill pair structurally. With --controller the controller
replays the frames and the last RESULT line must be outcome=done:

    python pipeline_e2e.py --controller ..\\..\\controller\\target\\debug\\controller.exe
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pnglite  # noqa: E402

WIDTH, HEIGHT = 320, 240
FRAMES_PER_SCENE = 4

# (block rect x, y, w, h in pixels, RGB fill) — None = baseline scene.
# Blocks are far larger than the learn grid cells, so anchors and dominant
# colors come out exact and stable.
SCENES = [
    None,                      # baseline (vacuous step_00)
    (24, 20, 80, 60, (200, 40, 40)),    # red, top-left
    (216, 160, 80, 60, (40, 60, 200)),  # blue, bottom-right
    (120, 90, 80, 60, (60, 180, 60)),   # green, center
]

BG = (40, 40, 40)


def build_frame(scene) -> bytes:
    """Render one scene into a WIDTH*HEIGHT*4 RGBA buffer."""
    buf = bytearray()
    for y in range(HEIGHT):
        for x in range(WIDTH):
            r = g = b = 0
            if scene is not None:
                sx, sy, sw, sh, (r, g, b) = scene
            else:
                sx = sy = sw = sh = 0
            if sx <= x < sx + sw and sy <= y < sy + sh:
                buf += bytes((r, g, b, 255))
            else:
                buf += bytes((BG[0], BG[1], BG[2], 255))
    return bytes(buf)


def write_frames(frames_dir: Path) -> int:
    frames_dir.mkdir(parents=True, exist_ok=True)
    n = 0
    for i, scene in enumerate(SCENES):
        px = build_frame(scene)
        for _ in range(FRAMES_PER_SCENE):
            n += 1
            pnglite.write_png(frames_dir / f"frame_{n:04d}.png", WIDTH, HEIGHT, px)
    return n


def run_step(cmd: list[str], what: str) -> str:
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        sys.stderr.write(f"pipeline_e2e: {what} failed (exit {proc.returncode})\n")
        sys.stderr.write(proc.stdout[-4000:])
        sys.stderr.write(proc.stderr[-4000:])
        raise SystemExit(1)
    return proc.stdout


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__,
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--controller", default="", help="controller exe for the replay step")
    p.add_argument("--keep", action="store_true", help="keep the temp work dir")
    args = p.parse_args()

    here = Path(__file__).resolve().parent
    work = Path(tempfile.mkdtemp(prefix="nc9_e2e_"))
    frames = work / "frames"
    out = work / "out"
    try:
        count = write_frames(frames)
        print(f"[1/4] generated {count} deterministic frames in {frames}")

        # draft goes to the work root: learn_route needs the parent to exist,
        # while draft_to_skill creates its own --out-dir (exist_ok=False).
        draft_path = work / "route-draft.json"
        run_step([sys.executable, str(here / "learn_route.py"), str(frames),
                  "--out", str(draft_path)], "learn_route")
        draft = json.loads(draft_path.read_text(encoding="utf-8"))
        segs = len(draft.get("segments", []))
        want = len(SCENES)
        if segs != want:
            sys.stderr.write(f"pipeline_e2e: learn found {segs} segments, want {want}\n")
            return 1
        print(f"[2/4] learn_route: {segs} segments (anchors normalized)")

        run_step([sys.executable, str(here / "draft_to_skill.py"),
                  str(draft_path), "--out-dir", str(out),
                  "--name", "nc9_e2e"], "draft_to_skill")
        skill = json.loads((out / "skill.json").read_text(encoding="utf-8"))
        probes = json.loads((out / "probes.json").read_text(encoding="utf-8"))
        states = [s["name"] for s in skill["states"]]
        if states != ["step_00", "step_01", "step_02", "step_03", "done"] or len(probes) != 3:
            sys.stderr.write(f"pipeline_e2e: unexpected pair states={states} probes={len(probes)}\n")
            return 1
        print(f"[3/4] draft_to_skill: {len(probes)} probes, walk {states}")

        if not args.controller:
            print("[4/4] controller replay SKIPPED (--controller not given)")
            print("NC9 PIPELINE E2E PASS (chain up to conversion)")
            return 0
        proc = subprocess.run(
            [args.controller, "--dry-run", "--protocol",
             "--replay", str(frames),
             "--probes", str(out / "probes.json"),
             "--skill", str(out / "skill.json")],
            capture_output=True, text=True)
        result = ""
        for line in proc.stdout.splitlines():
            if '"RESULT"' in line:
                result = line
        if proc.returncode != 0 or '"outcome":"done"' not in result.replace(" ", ""):
            sys.stderr.write(f"pipeline_e2e: replay did not reach done "
                             f"(exit {proc.returncode})\nlast RESULT: {result or 'none'}\n")
            sys.stderr.write(proc.stdout[-2000:])
            sys.stderr.write(proc.stderr[-2000:])
            return 1
        print("[4/4] controller replay: RESULT done")
        print("NC9 PIPELINE E2E PASS")
        return 0
    finally:
        if args.keep:
            print(f"work dir kept: {work}")
        else:
            import shutil
            shutil.rmtree(work, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
