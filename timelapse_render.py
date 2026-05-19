#!/usr/bin/env python
"""
Render a timelapse recording into a .webm video with transparency.

Applies incremental diffs to reconstruct each SVG frame, renders each
frame to PNG via Inkscape's pipe mode, and streams directly to ffmpeg.

Usage:
  timelapse_render.py recording.json --framerate=10 --output=timelapse.webm
"""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path


HUNK_RE = re.compile(r"^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@")
SKIP_RE = re.compile(r"^(---|\+\+\+)")


def apply_diff(original: str, diff_text: str) -> str:
    """Apply a unified diff to *original* and return the result."""
    if not diff_text.strip():
        return original

    lines = list(original.splitlines(keepends=True))
    diff_lines = diff_text.splitlines(keepends=True)

    # Parse hunks: (old_start, old_count, body_lines)
    hunks: list[tuple[int, int, list[str]]] = []
    i = 0
    while i < len(diff_lines):
        m = HUNK_RE.match(diff_lines[i])
        if m:
            old_start = int(m.group(1))
            old_count = int(m.group(2)) if m.group(2) else 1
            body: list[str] = []
            i += 1
            while i < len(diff_lines) and not HUNK_RE.match(diff_lines[i]) and not SKIP_RE.match(diff_lines[i]):
                body.append(diff_lines[i])
                i += 1
            hunks.append((old_start, old_count, body))
        else:
            i += 1

    # Apply hunks in reverse order so earlier line numbers stay valid
    for old_start, old_count, body in reversed(hunks):
        start_idx = max(0, old_start - 1)
        new_content = [
            hl[1:] for hl in body if not hl.startswith("-")
        ]
        lines[start_idx : start_idx + old_count] = new_content

    return "".join(lines)


def svg_to_png(svg_data: str) -> bytes:
    """Render SVG string to PNG bytes via Inkscape pipe mode."""
    proc = subprocess.run(
        ["inkscape", "--pipe", "--export-type=png"],
        input=svg_data.encode("utf-8"),
        capture_output=True,
    )
    if proc.returncode != 0:
        msg = proc.stderr.decode("utf-8", errors="replace").strip()
        raise RuntimeError(f"Inkscape failed: {msg}")
    return proc.stdout


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Render timelapse recording to .webm video"
    )
    parser.add_argument("recording", type=str, help="Recording JSON file")
    parser.add_argument("--framerate", type=float, default=10.0,
                        help="Output video frame rate (default: 10)")
    parser.add_argument("--output", type=str, default="timelapse.webm",
                        help="Output video path (default: timelapse.webm)")
    args = parser.parse_args()

    recording = json.loads(Path(args.recording).read_text(encoding="utf-8"))

    # Reconstruct frame SVGs by applying diffs sequentially
    svg = recording["initial_svg"]
    frames = [svg]
    for capture in recording["captures"]:
        svg = apply_diff(svg, capture["diff"])
        frames.append(svg)

    total = len(frames)
    print(f"Rendering {total} frames at {args.framerate} fps → {args.output}")

    ffmpeg = subprocess.Popen(
        [
            "ffmpeg", "-y",
            "-f", "image2pipe",
            "-framerate", str(args.framerate),
            "-i", "-",
            "-c:v", "libvpx-vp9",
            "-pix_fmt", "yuva420p",
            "-lossless", "1",
            args.output,
        ],
        stdin=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    try:
        for i, frame_svg in enumerate(frames):
            png_data = svg_to_png(frame_svg)
            ffmpeg.stdin.write(png_data)
            print(f"\r  Frame {i + 1}/{total}", end="", file=sys.stderr)
    except BrokenPipeError:
        pass  # ffmpeg may close early on error
    finally:
        ffmpeg.stdin.close()
        ffmpeg.wait()

    print("\nDone" if ffmpeg.returncode == 0
          else f"\nffmpeg exited with code {ffmpeg.returncode}",
          file=sys.stderr)


if __name__ == "__main__":
    main()
