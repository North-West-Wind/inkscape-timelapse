#!/usr/bin/env python
"""
Render a timelapse recording into a .webm video with transparency.

Usage:
  timelapse_render.py recording.tld --framerate=10 --output=timelapse.webm
"""
import argparse
import struct
import subprocess
import sys
import zlib
from pathlib import Path


MAGIC = b"TLTL"
R_INITIAL = 0
R_CAPTURE = 1


def _read_exactly(f, n):
    buf = f.read(n)
    if len(buf) < n:
        return None
    return buf


def apply_diff(original: str, diff_text: str) -> str:
    if not diff_text.strip():
        return original
    lines = list(original.splitlines(keepends=True))
    diff_lines = diff_text.splitlines(keepends=True)

    import re
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


def svg_to_png(svg_data: str) -> bytes:
    proc = subprocess.run(
        ["inkscape", "--pipe", "--export-type=png"],
        input=svg_data.encode("utf-8"),
        capture_output=True,
    )
    if proc.returncode != 0:
        msg = proc.stderr.decode("utf-8", errors="replace").strip()
        raise RuntimeError(f"Inkscape failed: {msg}")
    return proc.stdout


def _frames(path: Path, skip_unchanged=False):
    """Yield (svg, frame_number) reading .tld records sequentially (O(1) memory)."""
    with open(path, "rb") as f:
        if _read_exactly(f, 4) != MAGIC:
            return

        svg = None
        frame = 0

        while True:
            type_byte = _read_exactly(f, 1)
            if type_byte is None:
                break

            if type_byte[0] == R_INITIAL:
                buf = _read_exactly(f, 4)
                if buf is None:
                    break
                slen = struct.unpack("<I", buf)[0]
                raw = _read_exactly(f, slen)
                if raw is None:
                    break
                svg = raw.decode("utf-8")
                yield svg, frame
                frame += 1

            elif type_byte[0] == R_CAPTURE:
                buf = _read_exactly(f, 4)
                if buf is None:
                    break
                _tslen = struct.unpack("<I", buf)[0]  # timestamp, unused during streaming
                if _read_exactly(f, _tslen) is None:
                    break

                buf = _read_exactly(f, 4)
                if buf is None:
                    break
                dlen = struct.unpack("<I", buf)[0]
                diff_raw = _read_exactly(f, dlen)
                if diff_raw is None:
                    break

                diff = zlib.decompress(diff_raw).decode("utf-8")
                if diff.strip():
                    svg = apply_diff(svg, diff)
                if not diff.strip() and skip_unchanged:
                    continue  # skip unchanged frame entirely
                yield svg, frame
                frame += 1

            else:
                break


def _count_frames(path: Path) -> int:
    """Count capture records (not initial SVG) by scanning the file."""
    count = 0
    with open(path, "rb") as f:
        if _read_exactly(f, 4) != MAGIC:
            return 0
        while True:
            type_byte = _read_exactly(f, 1)
            if type_byte is None:
                break
            if type_byte[0] == R_INITIAL:
                buf = _read_exactly(f, 4)
                if buf is None:
                    break
                _read_exactly(f, struct.unpack("<I", buf)[0])
            elif type_byte[0] == R_CAPTURE:
                count += 1
                buf = _read_exactly(f, 4)
                if buf is None:
                    break
                _read_exactly(f, struct.unpack("<I", buf)[0])  # timestamp
                buf = _read_exactly(f, 4)
                if buf is None:
                    break
                _read_exactly(f, struct.unpack("<I", buf)[0])  # diff
            else:
                break
    return count


def main() -> None:
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
    args = parser.parse_args()

    path = Path(args.recording)
    skip = args.skip_unchanged

    if not skip:
        total = _count_frames(path) + 1
        label = str(total)
    else:
        total = None
        label = "?"
    print(f"Rendering {label} frames at {args.framerate} fps → {args.output}")

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
        for svg, frame in _frames(path, skip_unchanged=skip):
            png_data = svg_to_png(svg)
            ffmpeg.stdin.write(png_data)
            print(f"\r  Frame {frame + 1}/{label}", end="", file=sys.stderr)
    except BrokenPipeError:
        pass
    finally:
        ffmpeg.stdin.close()
        ffmpeg.wait()

    print("\nDone" if ffmpeg.returncode == 0
          else f"\nffmpeg exited with code {ffmpeg.returncode}",
          file=sys.stderr)


if __name__ == "__main__":
    main()
