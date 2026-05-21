#!/usr/bin/env python
"""
Render a timelapse recording into a .webm video with transparency.

Usage:
  timelapse_render.py recording.tld --framerate=10 --output=timelapse.webm
"""
import argparse
import os
import queue
import re
import struct
import subprocess
import sys
import threading
import time
import zlib
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path


MAGIC = b"TLTL"
R_INITIAL = 0
R_CAPTURE = 1

_W_RE = re.compile(r'width="(\d+(?:\.\d+)?)"')
_H_RE = re.compile(r'height="(\d+(?:\.\d+)?)"')
_PAR_RE = re.compile(r'\s*preserveAspectRatio="[^"]*"')

_X_MAP = {"left": "Min", "center": "Mid", "right": "Max"}
_Y_MAP = {"top": "Min", "center": "Mid", "bottom": "Max"}


def _read_raw_records(path):
    """Yield (type, data) from a .tld file. Skips truncated trailing data."""
    with open(path, "rb") as f:
        if f.read(4) != MAGIC:
            return
        rest = f.read()
    decomp = zlib.decompressobj(wbits=-15)
    try:
        raw = decomp.decompress(rest)
        try:
            raw += decomp.flush()
        except zlib.error:
            pass
    except zlib.error:
        raw = b""

    i = 0
    while i < len(raw):
        if i + 5 > len(raw):
            break
        t = raw[i]
        if t == R_INITIAL:
            slen = struct.unpack("<I", raw[i+1:i+5])[0]
            end = i + 5 + slen
            if end > len(raw):
                break
            yield (t, raw[i+5:end].decode("utf-8"))
            i = end
        elif t == R_CAPTURE:
            tslen = struct.unpack("<I", raw[i+1:i+5])[0]
            if i + 5 + tslen + 4 > len(raw):
                break
            ts = raw[i+5:i+5+tslen].decode("utf-8")
            dlen = struct.unpack("<I", raw[i+5+tslen:i+5+tslen+4])[0]
            end = i + 5 + tslen + 4 + dlen
            if end > len(raw):
                break
            diff = raw[i+5+tslen+4:end].decode("utf-8")
            yield (t, {"timestamp": ts, "diff": diff})
            i = end
        else:
            break


def apply_diff(original: str, diff_text: str) -> str:
    if not diff_text.strip():
        return original
    lines = list(original.splitlines(keepends=True))
    diff_lines = diff_text.splitlines(keepends=True)

    HUNK_RE = re.compile(r"^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@")
    SKIP_RE = re.compile(r"^(---|\+\+\+)")

    hunks = []
    i = 0
    while i < len(diff_lines):
        m = HUNK_RE.match(diff_lines[i])
        if m:
            old_start = int(m.group(1))
            old_count = int(m.group(2)) if m.group(2) else 1
            body = []
            i += 1
            while i < len(diff_lines) and not HUNK_RE.match(diff_lines[i]) and not SKIP_RE.match(diff_lines[i]):
                body.append(diff_lines[i])
                i += 1
            hunks.append((old_start, old_count, body))
        else:
            i += 1

    for old_start, old_count, body in reversed(hunks):
        start_idx = max(0, old_start - 1)
        new_content = [hl[1:] for hl in body if not hl.startswith("-")]
        lines[start_idx: start_idx + old_count] = new_content

    return "".join(lines)


def svg_to_png(svg_data: str, engine: str = "rsvg") -> bytes:
    if engine == "rsvg":
        proc = subprocess.run(
            ["rsvg-convert", "--format", "png"],
            input=svg_data.encode("utf-8"),
            capture_output=True,
        )
        if proc.returncode != 0:
            msg = proc.stderr.decode("utf-8", errors="replace").strip()
            raise RuntimeError(f"rsvg-convert failed: {msg}")
        return proc.stdout
    else:
        proc = subprocess.run(
            ["inkscape", "--pipe", "--export-type=png"],
            input=svg_data.encode("utf-8"),
            capture_output=True,
        )
        if proc.returncode != 0:
            msg = proc.stderr.decode("utf-8", errors="replace").strip()
            raise RuntimeError(f"Inkscape failed: {msg}")
        return proc.stdout


def _svg_size(svg):
    wm = _W_RE.search(svg)
    hm = _H_RE.search(svg)
    if wm and hm:
        return int(float(wm.group(1))), int(float(hm.group(1)))
    return None, None


def _set_canvas(svg, w, h, align_x="center", align_y="center", stretch=False):
    svg = _PAR_RE.sub("", svg)
    svg = _W_RE.sub(f'width="{w}"', svg, count=1)
    svg = _H_RE.sub(f'height="{h}"', svg, count=1)
    par = "none" if stretch else f"x{_X_MAP[align_x]}Y{_Y_MAP[align_y]} meet"
    svg = re.sub(r'(<svg[^>]*>)', f'\\1 preserveAspectRatio="{par}"', svg, count=1)
    return svg


def _frames(path, skip_unchanged=False, canvas=None):
    """Yield (svg, frame_number). Optionally modify SVG canvas."""
    svg = None
    frame = 0

    for rec_type, data in _read_raw_records(path):
        if rec_type == R_INITIAL:
            svg = data
            if canvas:
                svg = _set_canvas(svg, *canvas)
            yield svg, frame
            frame += 1
        else:
            if data["diff"].strip():
                svg = apply_diff(svg, data["diff"])
            if not data["diff"].strip() and skip_unchanged:
                continue
            if canvas:
                svg = _set_canvas(svg, *canvas)
            yield svg, frame
            frame += 1


def _analyze(path, skip_unchanged=False):
    """Return (count, init_w, init_h, max_w, max_h, min_w, min_h)."""
    iw = ih = maw = mah = miw = mih = 0
    n = 0
    svg = None

    for rec_type, data in _read_raw_records(path):
        if rec_type == R_INITIAL:
            svg = data
            w, h = _svg_size(svg)
            if w and h:
                iw = ih = maw = mah = miw = mih = w
            n += 1
        else:
            if data["diff"].strip():
                svg = apply_diff(svg, data["diff"])
            if not data["diff"].strip() and skip_unchanged:
                continue
            w, h = _svg_size(svg)
            if w and h:
                maw = max(maw, w)
                mah = max(mah, h)
                miw = min(miw, w)
                mih = min(mih, h)
            n += 1

    return n, iw, ih, maw, mah, miw, mih


def main():
    parser = argparse.ArgumentParser(
        description="Render timelapse recording to .webm video"
    )
    parser.add_argument("recording", type=str, help="Recording .tld file")
    parser.add_argument("--framerate", type=float, default=10.0,
                        help="Output video frame rate (default: 10)")
    parser.add_argument("--output", type=str, default="timelapse.webm",
                        help="Output video path (default: timelapse.webm)")
    parser.add_argument("--skip-unchanged", action="store_true",
                        help="Skip frames where document didn't change")
    parser.add_argument("--resolution-policy", default="max",
                        choices=("max", "min", "initial"),
                        help="How to pick video resolution across frames with varying sizes (default: max)")
    parser.add_argument("--align-x", default="center",
                        choices=("left", "center", "right"),
                        help="Horizontal alignment of content on canvas (default: center)")
    parser.add_argument("--align-y", default="center",
                        choices=("top", "center", "bottom"),
                        help="Vertical alignment of content on canvas (default: center)")
    parser.add_argument("--stretch", action="store_true",
                        help="Stretch SVG to fill canvas (overrides alignment)")
    parser.add_argument("--engine", default="rsvg", choices=("rsvg", "inkscape"),
                        help="SVG-to-PNG render engine (default: rsvg)")
    parser.add_argument("--workers", type=int, default=os.cpu_count(),
                        help="Parallel render workers (default: CPU count)")
    parser.add_argument("--resolution", type=str, default=None,
                        help="Explicit WxH resolution (overrides --resolution-policy)")
    args = parser.parse_args()

    path = Path(args.recording)
    skip = args.skip_unchanged
    engine = args.engine
    workers = args.workers

    n, iw, ih, maw, mah, miw, mih = _analyze(path, skip_unchanged=skip)
    if n == 0:
        print("No frames found.", file=sys.stderr)
        sys.exit(1)

    policy = args.resolution_policy
    if args.resolution:
        try:
            cw, ch = (int(x) for x in args.resolution.split("x", 1))
        except (ValueError, IndexError):
            print(f"Invalid --resolution: {args.resolution} (expected WxH)", file=sys.stderr)
            sys.exit(1)
        policy = f"{cw}x{ch}"
    elif policy == "max":
        cw, ch = maw, mah
    elif policy == "min":
        cw, ch = miw, mih
    else:
        cw, ch = iw, ih

    canvas = (cw, ch, args.align_x, args.align_y, args.stretch)
    total = n

    print(f"Canvas: {cw}x{ch} ({policy}), {total} frames at {args.framerate} fps → {args.output}")

    ffmpeg = subprocess.Popen(
        [
            "ffmpeg", "-y",
            "-f", "image2pipe",
            "-framerate", str(args.framerate),
            "-color_range", "full",
            "-colorspace", "bt709",
            "-color_trc", "iec61966-2-1",
            "-color_primaries", "bt709",
            "-i", "-",
            "-c:v", "libvpx-vp9",
            "-lossless", "1",
            "-vf", "format=yuva420p",
            "-color_range", "full",
            "-colorspace", "bt709",
            "-color_trc", "iec61966-2-1",
            "-color_primaries", "bt709",
            args.output,
        ],
        stdin=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
    )

    t_start = time.perf_counter()
    rendered = 0

    try:
        with ThreadPoolExecutor(max_workers=workers) as pool:
            pending = {}
            next_out = 0

            try:
                for svg, frame in _frames(path, skip_unchanged=skip, canvas=canvas):
                    pending[frame] = pool.submit(svg_to_png, svg, engine)

                    while next_out in pending and pending[next_out].done():
                        png = pending.pop(next_out).result()
                        ffmpeg.stdin.write(png)
                        rendered += 1
                        print(f"\r  Frame {rendered}/{total}", end="", file=sys.stderr)
                        next_out += 1

                for frame in sorted(pending):
                    png = pending[frame].result()
                    ffmpeg.stdin.write(png)
                    rendered += 1
                    print(f"\r  Frame {rendered}/{total}", end="", file=sys.stderr)

            except BrokenPipeError:
                pass
    finally:
        ffmpeg.stdin.close()
        ffmpeg.wait()

    elapsed = time.perf_counter() - t_start
    fps = rendered / elapsed if elapsed else 0
    print(f"\nDone  {rendered} frames in {elapsed:.1f}s ({fps:.1f} fps)", file=sys.stderr)
    if ffmpeg.returncode != 0:
        print(f"ffmpeg exited with code {ffmpeg.returncode}", file=sys.stderr)


if __name__ == "__main__":
    main()
