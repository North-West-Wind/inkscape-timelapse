#!/usr/bin/env python
"""
Inspect, decode, and extract frames from .tld recording files.

Subcommands:
  jsonl    Decode to uncompressed JSONL (stdout).
  analyze  Print frame count, resolution stats.
  extract  Write a specific frame as SVG (--frame N, default 0).

Usage:
  timelapse_decode.py jsonl recording.tld > recording.jsonl
  timelapse_decode.py analyze recording.tld
  timelapse_decode.py extract recording.tld --frame 42 --output frame.svg
"""
import argparse
import json
import re
import struct
import sys
import zlib
from pathlib import Path

MAGIC = b"TLTL"
R_INITIAL = 0
R_CAPTURE = 1

_W_RE = re.compile(r'width="(\d+(?:\.\d+)?)"')
_H_RE = re.compile(r'height="(\d+(?:\.\d+)?)"')
_HUNK_RE = re.compile(r"^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@")
_SKIP_RE = re.compile(r"^(---|\+\+\+)")


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


def apply_diff(original, diff_text):
    if not diff_text.strip():
        return original
    lines = list(original.splitlines(keepends=True))
    diff_lines = diff_text.splitlines(keepends=True)
    hunks = []
    i = 0
    while i < len(diff_lines):
        m = _HUNK_RE.match(diff_lines[i])
        if m:
            old_start = int(m.group(1))
            old_count = int(m.group(2)) if m.group(2) else 1
            body = []
            i += 1
            while i < len(diff_lines) and not _HUNK_RE.match(diff_lines[i]) and not _SKIP_RE.match(diff_lines[i]):
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


def _svg_size(svg):
    wm = _W_RE.search(svg)
    hm = _H_RE.search(svg)
    return (int(float(wm.group(1))), int(float(hm.group(1)))) if wm and hm else (None, None)


# ── Subcommands ───────────────────────────────────────────────────

def cmd_jsonl(path, output):
    for rec_type, data in _read_raw_records(path):
        if rec_type == R_INITIAL:
            output.write(json.dumps({"initial_svg": data}, ensure_ascii=False) + "\n")
        else:
            output.write(json.dumps(data, ensure_ascii=False) + "\n")


def cmd_analyze(path, output):
    svg = None
    total = 0
    total_noempty = 0
    iw = ih = maw = mah = miw = mih = 0

    for rec_type, data in _read_raw_records(path):
        if rec_type == R_INITIAL:
            svg = data
            w, h = _svg_size(svg)
            if w and h:
                iw = ih = maw = mah = miw = mih = w
            total += 1
        else:
            if data["diff"].strip():
                svg = apply_diff(svg, data["diff"])
            w, h = _svg_size(svg)
            if w and h:
                maw = max(maw, w)
                mah = max(mah, h)
                miw = min(miw, w)
                mih = min(mih, h)
            total += 1
            if data["diff"].strip():
                total_noempty += 1

    output.write(f"Frames:\n")
    output.write(f"  total:              {total}\n")
    output.write(f"  skip-unchanged:     {total_noempty}\n")
    output.write(f"Resolution:\n")
    output.write(f"  initial:            {iw}x{ih}\n")
    output.write(f"  min:                {miw}x{mih}\n")
    output.write(f"  max:                {maw}x{mah}\n")


def cmd_extract(path, args):
    svg = None
    frame = 0

    for rec_type, data in _read_raw_records(path):
        if rec_type == R_INITIAL:
            svg = data
        else:
            if data["diff"].strip():
                svg = apply_diff(svg, data["diff"])

        if frame == args.frame:
            if args.output:
                Path(args.output).write_text(svg, encoding="utf-8")
            else:
                sys.stdout.write(svg)
            return
        frame += 1

    print(f"Frame {args.frame} not found (max: {frame - 1}).", file=sys.stderr)
    sys.exit(1)


def main():
    parser = argparse.ArgumentParser(
        description="Inspect and extract frames from .tld recording files."
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_jsonl = sub.add_parser("jsonl", help="Decode to uncompressed JSONL (stdout)")
    p_jsonl.add_argument("recording", type=str)

    p_analyze = sub.add_parser("analyze", help="Print frame count and resolution stats")
    p_analyze.add_argument("recording", type=str)

    p_extract = sub.add_parser("extract", help="Write a specific frame as SVG")
    p_extract.add_argument("recording", type=str)
    p_extract.add_argument("--frame", type=int, default=0,
                           help="Frame index (0 = initial, default: 0)")
    p_extract.add_argument("--output", type=str, default=None,
                           help="Output file (default: stdout)")

    args = parser.parse_args()
    path = Path(args.recording)

    if args.command == "jsonl":
        cmd_jsonl(path, sys.stdout)
    elif args.command == "analyze":
        cmd_analyze(path, sys.stdout)
    elif args.command == "extract":
        cmd_extract(path, args)


if __name__ == "__main__":
    main()
