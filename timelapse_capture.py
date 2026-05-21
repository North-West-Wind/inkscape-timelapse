#!/usr/bin/env python
"""
Inkscape timelapse capture daemon.

Usage:
  timelapse_capture.py start --interval=5 --save-dir=/path
  timelapse_capture.py start --append existing.tld --interval=5
  timelapse_capture.py stop
"""
import argparse
import os
import re
import signal
import struct
import sys
import tempfile
import time
import xml.etree.ElementTree as ET
import zlib
from datetime import datetime, timezone
from difflib import unified_diff
from pathlib import Path

from dasbus.connection import SessionMessageBus
from gi.repository import GLib


DBUS_DEST = "org.inkscape.Inkscape"
DBUS_PATH = "/org/inkscape/Inkscape"
TL_ATTR = "data-tlid"
MAGIC = b"TLTL"
R_INITIAL = 0
R_CAPTURE = 1

_status = "stopped"
_interval = 5.0
_tlid = ""
_last_svg = ""
_rec_path = None
_loop_proxy = None

_RE_TL_ATTR = re.compile(r'\s+data-tlid="[^"]*"')
_record_unchanged = False
_time_fmt = "%Y-%m-%dT%H:%M:%SZ"


def main():
    parser = argparse.ArgumentParser(description="Inkscape timelapse capture daemon")
    parser.add_argument("action", choices=("start", "stop"),
                        help="Action to perform")
    parser.add_argument("--interval", type=float, default=5.0,
                        help="Capture interval in seconds")
    parser.add_argument("--save-dir", type=str, default=None,
                        help="Recording save directory")
    parser.add_argument("--record-unchanged", action="store_true",
                        help="Record captures even when window unfocused (empty diff)")
    parser.add_argument("--time-format", type=str, default=_time_fmt,
                        help="strftime format for capture timestamps (default: %(default)s)")
    parser.add_argument("--append", type=str, default=None,
                        help="Continue recording to an existing .tld file")
    args = parser.parse_args()

    if args.action == "start":
        _cmd_start(args.interval, args.save_dir, args.record_unchanged,
                   args.time_format, args.append)
    elif args.action == "stop":
        _cmd_stop()


# ── Recording file management ─────────────────────────────────────

def _recording_id():
    return datetime.now(timezone.utc).strftime("%Y-%m-%d_%H-%M-%S")


def _recording_path(save_dir, recording_id):
    return Path(save_dir).expanduser().resolve() / f"{recording_id}.tld"


def _write_initial_record(path, svg_text):
    svg_bytes = svg_text.encode("utf-8")
    with open(path, "wb") as f:
        f.write(MAGIC)
        f.write(struct.pack("<B", R_INITIAL))
        f.write(struct.pack("<I", len(svg_bytes)))
        f.write(svg_bytes)


def _append_capture(path, timestamp, diff_text):
    ts_bytes = timestamp.encode("utf-8")
    diff_bytes = zlib.compress(diff_text.encode("utf-8"))
    with open(path, "ab") as f:
        f.write(struct.pack("<B", R_CAPTURE))
        f.write(struct.pack("<I", len(ts_bytes)))
        f.write(ts_bytes)
        f.write(struct.pack("<I", len(diff_bytes)))
        f.write(diff_bytes)


def _reconstruct(path):
    """Return (initial_svg, last_svg, capture_count) from a .tld file."""
    initial = last = None
    count = 0
    with open(path, "rb") as f:
        if f.read(4) != MAGIC:
            return None, None, 0
        while True:
            tb = f.read(1)
            if len(tb) < 1:
                break
            if tb[0] == R_INITIAL:
                buf = f.read(4)
                if len(buf) < 4:
                    break
                slen = struct.unpack("<I", buf)[0]
                raw = f.read(slen)
                if len(raw) < slen:
                    break
                initial = last = raw.decode("utf-8")
            elif tb[0] == R_CAPTURE:
                count += 1
                buf = f.read(4)
                if len(buf) < 4:
                    break
                tslen = struct.unpack("<I", buf)[0]
                if len(f.read(tslen)) < tslen:
                    break
                buf = f.read(4)
                if len(buf) < 4:
                    break
                dlen = struct.unpack("<I", buf)[0]
                diff_raw = f.read(dlen)
                if len(diff_raw) < dlen:
                    break
                diff = zlib.decompress(diff_raw).decode("utf-8")
                if diff.strip():
                    last = _apply_diff(last, diff)
            else:
                break
    return initial, last, count


def _apply_diff(original, diff_text):
    if not diff_text.strip():
        return original
    lines = list(original.splitlines(keepends=True))
    dl = diff_text.splitlines(keepends=True)
    HUNK_RE = re.compile(r"^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@")
    SKIP_RE = re.compile(r"^(---|\+\+\+)")
    hunks = []
    i = 0
    while i < len(dl):
        m = HUNK_RE.match(dl[i])
        if m:
            old_start = int(m.group(1))
            old_count = int(m.group(2)) if m.group(2) else 1
            body = []
            i += 1
            while i < len(dl) and not HUNK_RE.match(dl[i]) and not SKIP_RE.match(dl[i]):
                body.append(dl[i])
                i += 1
            hunks.append((old_start, old_count, body))
        else:
            i += 1
    for old_start, old_count, body in reversed(hunks):
        start_idx = max(0, old_start - 1)
        new_content = [hl[1:] for hl in body if not hl.startswith("-")]
        lines[start_idx: start_idx + old_count] = new_content
    return "".join(lines)


# ── SVG capture via D-Bus ─────────────────────────────────────────

def _get_dbus_proxy():
    bus = SessionMessageBus()
    return bus.get_proxy(DBUS_DEST, DBUS_PATH)


def _capture_svg(proxy):
    tmp = tempfile.NamedTemporaryFile(suffix=".svg", delete=False)
    tmp_path = tmp.name
    tmp.close()
    try:
        proxy.Activate("export-filename", [GLib.Variant("s", tmp_path)], {})
        proxy.Activate("export-plain-svg", [GLib.Variant("b", True)], {})
        proxy.Activate("export-do", [], {})
        svg = Path(tmp_path).read_text(encoding="utf-8")
        return svg if svg else None
    except Exception as exc:
        print(f"SVG capture failed: {exc}", file=sys.stderr)
        return None
    finally:
        try:
            os.unlink(tmp_path)
        except OSError:
            pass


def _inject_marker(proxy, tlid):
    try:
        proxy.Activate("selection-set-backup", [], {})
        proxy.Activate("select-all", [GLib.Variant("s", "all")], {})
        proxy.Activate("object-set-attribute",
                       [GLib.Variant("s", f"{TL_ATTR},{tlid}")], {})
        proxy.Activate("selection-restore-backup", [], {})
        return True
    except Exception as exc:
        print(f"Marker injection failed: {exc}", file=sys.stderr)
        return False


def _strip_marker(proxy):
    try:
        proxy.Activate("selection-set-backup", [], {})
        proxy.Activate("select-all", [GLib.Variant("s", "all")], {})
        proxy.Activate("object-set-attribute",
                       [GLib.Variant("s", f"{TL_ATTR},")], {})
        proxy.Activate("selection-restore-backup", [], {})
    except Exception as exc:
        print(f"Marker strip failed: {exc}", file=sys.stderr)


def _clean_svg(svg_text):
    return _RE_TL_ATTR.sub("", svg_text)


# ── Commands ──────────────────────────────────────────────────────

def _check_window_count():
    bus = SessionMessageBus()
    dbus_proxy = bus.get_proxy("org.freedesktop.DBus",
                                "/org/freedesktop/DBus")
    names = dbus_proxy.ListNames()
    if "org.inkscape.Inkscape" not in names:
        return 0
    try:
        wp = bus.get_proxy("org.inkscape.Inkscape",
                            "/org/inkscape/Inkscape/window")
        root = ET.fromstring(wp.Introspect())
        return sum(1 for c in root if c.tag == "node")
    except Exception:
        return 0


def _cmd_start(interval, save_dir, record_unchanged, time_fmt, append_path):
    global _status, _interval, _tlid, _last_svg, _rec_path, _loop_proxy, _record_unchanged, _time_fmt

    if _status != "stopped":
        print("Recording already active. Stop it first.", file=sys.stderr)
        sys.exit(1)

    win_count = _check_window_count()
    if win_count == 0:
        print("No Inkscape window detected on D-Bus.", file=sys.stderr)
        sys.exit(1)

    proxy = _get_dbus_proxy()
    tlid = _recording_id()

    if not _inject_marker(proxy, tlid):
        print("Failed to inject recording marker.", file=sys.stderr)
        sys.exit(1)

    svg_raw = _capture_svg(proxy)
    if not svg_raw:
        print("Failed to capture initial SVG. "
              "Is Inkscape running with an open document?",
              file=sys.stderr)
        sys.exit(1)

    if f'{TL_ATTR}="{tlid}"' not in svg_raw:
        print("Recording marker not found in exported SVG. "
              "Was the focused window switched before initial capture?",
              file=sys.stderr)
        sys.exit(1)

    svg_clean = _clean_svg(svg_raw)

    if append_path:
        rec_path = Path(append_path).expanduser().resolve()
        if not rec_path.exists():
            print(f"File not found: {rec_path}", file=sys.stderr)
            sys.exit(1)

        _, last_svg, count = _reconstruct(rec_path)
        if last_svg is None:
            print(f"Invalid or empty recording: {rec_path}", file=sys.stderr)
            sys.exit(1)

        _last_svg = last_svg
        _rec_path = rec_path
        print(f"Appending to {rec_path.name} ({count} existing captures)")
    else:
        save_path = (Path(save_dir) if save_dir else Path.cwd()).expanduser().resolve()
        save_path.mkdir(parents=True, exist_ok=True)
        rec_path = _recording_path(save_path, tlid)
        _write_initial_record(rec_path, svg_clean)
        _last_svg = svg_clean
        _rec_path = rec_path
        print(f"Recording started: {rec_path.name}")

    _status = "recording"
    _interval = interval
    _tlid = tlid
    _loop_proxy = proxy
    _record_unchanged = record_unchanged
    _time_fmt = time_fmt

    _capture_loop()


def _capture_loop():
    global _status, _last_svg

    signal.signal(signal.SIGTERM, _signal_handler)
    signal.signal(signal.SIGINT, _signal_handler)

    try:
        while True:
            if _status == "stopped":
                print("Recording stopped by request.")
                break
            elif _status == "recording":
                time.sleep(_interval)

                svg_raw = _capture_svg(_loop_proxy)
                if not svg_raw:
                    continue

                if f'{TL_ATTR}="{_tlid}"' not in svg_raw:
                    if _record_unchanged:
                        ts = datetime.now(timezone.utc).strftime(_time_fmt)
                        _append_capture(_rec_path, ts, "")
                        print(f"Unchanged at {ts}")
                        continue
                    print("Marker missing — wrong window.", file=sys.stderr)
                    continue

                svg_str = _clean_svg(svg_raw)
                if svg_str == _last_svg:
                    continue

                diff_lines = list(unified_diff(
                    _last_svg.splitlines(keepends=True),
                    svg_str.splitlines(keepends=True),
                    fromfile="previous.svg", tofile="current.svg",
                ))
                diff_text = "".join(diff_lines)
                timestamp = datetime.now(timezone.utc).strftime(_time_fmt)
                _append_capture(_rec_path, timestamp, diff_text)
                _last_svg = svg_str

                print(f"Capture at {timestamp} ({len(diff_text)} bytes)")
            else:
                time.sleep(0.5)

    except KeyboardInterrupt:
        print("\nStopped by user.")
    finally:
        _finalize()


def _finalize():
    global _status, _rec_path, _loop_proxy, _last_svg, _tlid, _record_unchanged
    if _loop_proxy is not None and _rec_path is not None:
        _strip_marker(_loop_proxy)
        print(f"Recording finalized: {_rec_path}")
    _status = "stopped"
    _rec_path = None
    _loop_proxy = None
    _last_svg = ""
    _tlid = ""


def _signal_handler(signum, frame):
    _finalize()
    sys.exit(0)


def _cmd_stop():
    global _status
    if _status == "stopped":
        print("No active recording.", file=sys.stderr)
        sys.exit(1)
    _status = "stopped"
    print("Stop signal sent to capture loop.")


if __name__ == "__main__":
    main()
