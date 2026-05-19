#!/usr/bin/env python
"""
Inkscape timelapse capture daemon.
Captures SVG frames from a running Inkscape instance via D-Bus at regular intervals.
Records incremental diffs to an append-only binary file.

File format (.tld):
  "TLTL"                    magic (4 bytes)
  [00] [svg_len: u32 LE]    initial SVG record
  [01] [ts_len: u32 LE]     capture records (repeat)
       [timestamp: UTF-8]
       [diff_len: u32 LE]
       [diff_data: zlib]

No footer. Append-only. Crash-safe: records beyond EOF ignored.

Usage:
  timelapse_capture.py start --interval=5 --save-dir=/path
  timelapse_capture.py stop
  timelapse_capture.py pause
  timelapse_capture.py resume
"""
import argparse
import json
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


STATE_DIR = Path.home() / ".inkscape-timelapse"
STATE_FILE = STATE_DIR / "state.json"
DBUS_DEST = "org.inkscape.Inkscape"
DBUS_PATH = "/org/inkscape/Inkscape"
TL_ATTR = "data-tlid"
MAGIC = b"TLTL"
R_INITIAL = 0
R_CAPTURE = 1

_loop_rec_path = None
_loop_proxy = None

_RE_TL_ATTR = re.compile(r'\s+data-tlid="[^"]*"')


def main():
    parser = argparse.ArgumentParser(description="Inkscape timelapse capture daemon")
    parser.add_argument("action", choices=("start", "stop", "pause", "resume"),
                        help="Action to perform")
    parser.add_argument("--interval", type=float, default=5.0,
                        help="Capture interval in seconds")
    parser.add_argument("--save-dir", type=str, default=None,
                        help="Recording save directory")
    args = parser.parse_args()

    if args.action == "start":
        _cmd_start(args.interval, args.save_dir)
    elif args.action == "stop":
        _cmd_stop()
    elif args.action == "pause":
        _cmd_pause()
    elif args.action == "resume":
        _cmd_resume()


# ── State file management ─────────────────────────────────────────

def _read_state():
    if STATE_FILE.exists():
        try:
            return json.loads(STATE_FILE.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            pass
    return None


def _write_state(state):
    STATE_DIR.mkdir(parents=True, exist_ok=True)
    STATE_FILE.write_text(json.dumps(state, indent=2), encoding="utf-8")


def _clear_state():
    STATE_FILE.unlink(missing_ok=True)


# ── Recording file management (binary, append-only) ──────────────

def _recording_id():
    return datetime.now(timezone.utc).strftime("timelapse_%Y%m%dT%H%M%SZ")


def _recording_path(save_dir, recording_id):
    return Path(save_dir).expanduser().resolve() / f"{recording_id}.tld"


def _read_recording(path):
    """Parse a .tld file into {initial_svg, captures}. Skips truncated records."""
    data = {"initial_svg": "", "captures": []}
    if not path.exists():
        return None
    with open(path, "rb") as f:
        if f.read(4) != MAGIC:
            return data
        while True:
            type_byte = f.read(1)
            if len(type_byte) < 1:
                break
            if type_byte[0] == R_INITIAL:
                buf = f.read(4)
                if len(buf) < 4:
                    break
                slen = struct.unpack("<I", buf)[0]
                svg = f.read(slen)
                if len(svg) < slen:
                    break
                data["initial_svg"] = svg.decode("utf-8")
            elif type_byte[0] == R_CAPTURE:
                buf = f.read(4)
                if len(buf) < 4:
                    break
                tslen = struct.unpack("<I", buf)[0]
                ts = f.read(tslen)
                if len(ts) < tslen:
                    break
                buf = f.read(4)
                if len(buf) < 4:
                    break
                dlen = struct.unpack("<I", buf)[0]
                diff_raw = f.read(dlen)
                if len(diff_raw) < dlen:
                    break
                diff = zlib.decompress(diff_raw).decode("utf-8")
                data["captures"].append({"timestamp": ts.decode("utf-8"), "diff": diff})
            else:
                break
    return data


def _write_initial_record(path, svg_text):
    """Write magic + initial SVG record (overwrites existing file)."""
    svg_bytes = svg_text.encode("utf-8")
    with open(path, "wb") as f:
        f.write(MAGIC)
        f.write(struct.pack("<B", R_INITIAL))
        f.write(struct.pack("<I", len(svg_bytes)))
        f.write(svg_bytes)


def _append_capture(path, timestamp, diff_text):
    """Append one capture record (timestamp + zlib-compressed diff)."""
    ts_bytes = timestamp.encode("utf-8")
    diff_bytes = zlib.compress(diff_text.encode("utf-8"))
    with open(path, "ab") as f:
        f.write(struct.pack("<B", R_CAPTURE))
        f.write(struct.pack("<I", len(ts_bytes)))
        f.write(ts_bytes)
        f.write(struct.pack("<I", len(diff_bytes)))
        f.write(diff_bytes)


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
        proxy.Activate(
            "object-set-attribute",
            [GLib.Variant("s", f"{TL_ATTR},{tlid}")],
            {},
        )
        proxy.Activate("selection-restore-backup", [], {})
        return True
    except Exception as exc:
        print(f"Marker injection failed: {exc}", file=sys.stderr)
        return False


def _strip_marker(svg_text):
    return _RE_TL_ATTR.sub("", svg_text)


# ── Commands ──────────────────────────────────────────────────────

def _check_window_count():
    bus = SessionMessageBus()
    dbus_proxy = bus.get_proxy(
        "org.freedesktop.DBus", "/org/freedesktop/DBus",
    )
    names = dbus_proxy.ListNames()
    if "org.inkscape.Inkscape" not in names:
        return 0

    try:
        window_proxy = bus.get_proxy(
            "org.inkscape.Inkscape", "/org/inkscape/Inkscape/window",
        )
        xml_data = window_proxy.Introspect()
        root = ET.fromstring(xml_data)
        return sum(1 for child in root if child.tag == "node")
    except Exception:
        return 0


def _cmd_start(interval, save_dir):
    save_dir = Path(save_dir or STATE_DIR).expanduser().resolve()
    save_dir.mkdir(parents=True, exist_ok=True)

    if _read_state() is not None:
        print("Recording already active. Stop it first.", file=sys.stderr)
        sys.exit(1)

    win_count = _check_window_count()
    if win_count == 0:
        print("No Inkscape window detected on D-Bus.", file=sys.stderr)
        sys.exit(1)
    elif win_count > 1:
        print(
            f"Warning: {win_count} Inkscape windows open. "
            "Focus will auto-track the correct window via marker.",
            file=sys.stderr,
        )

    proxy = _get_dbus_proxy()
    tlid = _recording_id()

    if not _inject_marker(proxy, tlid):
        print("Failed to inject recording marker.", file=sys.stderr)
        sys.exit(1)

    svg_raw = _capture_svg(proxy)
    if not svg_raw:
        print(
            "Failed to capture initial SVG. "
            "Is Inkscape running with an open document?",
            file=sys.stderr,
        )
        sys.exit(1)

    if f'{TL_ATTR}="{tlid}"' not in svg_raw:
        print(
            "Recording marker not found in exported SVG. "
            "Was the focused window switched before initial capture?",
            file=sys.stderr,
        )
        sys.exit(1)

    svg_clean = _strip_marker(svg_raw)
    print(f"Recording document (marker: {tlid})")

    recording_id = _recording_id()
    rec_path = _recording_path(save_dir, recording_id)

    _write_initial_record(rec_path, svg_clean)
    _write_state({
        "rec_path": str(rec_path),
        "status": "recording",
        "interval": interval,
        "last_svg": svg_clean,
        "tlid": tlid,
    })

    print(f"Recording started: {rec_path.name}")
    _capture_loop(rec_path)


def _capture_loop(rec_path):
    global _loop_rec_path, _loop_proxy
    _loop_rec_path = rec_path
    _loop_proxy = _get_dbus_proxy()

    signal.signal(signal.SIGTERM, _signal_handler)
    signal.signal(signal.SIGINT, _signal_handler)

    try:
        while True:
            state = _read_state()
            if state is None:
                print("State file removed, stopping.", file=sys.stderr)
                break

            status = state.get("status")

            if status == "stopped":
                print("Recording stopped by request.")
                break
            elif status == "paused":
                time.sleep(0.5)
                continue
            elif status == "recording":
                interval = state.get("interval", 5.0)
                tlid = state.get("tlid", "")
                last_svg = state.get("last_svg", "")

                time.sleep(interval)

                svg_raw = _capture_svg(_loop_proxy)
                if not svg_raw:
                    continue

                if f'{TL_ATTR}="{tlid}"' not in svg_raw:
                    print("Marker missing — wrong window.", file=sys.stderr)
                    continue

                svg_str = _strip_marker(svg_raw)

                if svg_str == last_svg:
                    continue

                diff_lines = list(unified_diff(
                    last_svg.splitlines(keepends=True),
                    svg_str.splitlines(keepends=True),
                    fromfile="previous.svg", tofile="current.svg",
                ))
                diff_text = "".join(diff_lines)

                timestamp = datetime.now(timezone.utc).isoformat()
                _append_capture(rec_path, timestamp, diff_text)

                state["last_svg"] = svg_str
                _write_state(state)

                print(f"Capture at {timestamp} ({len(diff_text)} bytes)")
            else:
                time.sleep(0.5)

    except KeyboardInterrupt:
        print("\nStopped by user.")
    finally:
        _finalize(rec_path)


def _finalize(rec_path):
    global _loop_rec_path
    _clear_state()
    print(f"Recording finalized: {rec_path}")
    _loop_rec_path = None


def _signal_handler(signum, frame):
    if _loop_rec_path is not None:
        _finalize(_loop_rec_path)
    sys.exit(0)


def _cmd_stop():
    state = _read_state()
    if state is None:
        print("No active recording.", file=sys.stderr)
        sys.exit(1)
    state["status"] = "stopped"
    _write_state(state)
    print("Stop signal sent to capture loop.")


def _cmd_pause():
    state = _read_state()
    if state is None:
        print("No active recording.", file=sys.stderr)
        sys.exit(1)
    state["status"] = "paused"
    _write_state(state)
    print("Recording paused.")


def _cmd_resume():
    state = _read_state()
    if state is None:
        print("No active recording.", file=sys.stderr)
        sys.exit(1)
    state["status"] = "recording"
    _write_state(state)
    print("Recording resumed.")


if __name__ == "__main__":
    main()
