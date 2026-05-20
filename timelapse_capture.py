#!/usr/bin/env python
"""
Inkscape timelapse capture daemon.

Usage:
  timelapse_capture.py start --interval=5 --save-dir=/path
  timelapse_capture.py stop
  timelapse_capture.py pause
  timelapse_capture.py resume
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

# in-memory state
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
    parser.add_argument("action", choices=("start", "stop", "pause", "resume"),
                        help="Action to perform")
    parser.add_argument("--interval", type=float, default=5.0,
                        help="Capture interval in seconds")
    parser.add_argument("--save-dir", type=str, default=None,
                        help="Recording save directory")
    parser.add_argument("--record-unchanged", action="store_true",
                        help="Record captures even when window unfocused (empty diff)")
    parser.add_argument("--time-format", type=str, default=_time_fmt,
                        help="strftime format for capture timestamps (default: %(default)s)")
    args = parser.parse_args()

    if args.action == "start":
        _cmd_start(args.interval, args.save_dir, args.record_unchanged, args.time_format)
    elif args.action == "stop":
        _cmd_stop()
    elif args.action == "pause":
        _cmd_pause()
    elif args.action == "resume":
        _cmd_resume()


# ── Recording file management (binary, append-only) ──────────────

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
    """Set data-tlid to empty string on all elements."""
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


def _cmd_start(interval, save_dir, record_unchanged, time_fmt):
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
    save_path = (Path(save_dir) if save_dir else Path.cwd()).expanduser().resolve()
    save_path.mkdir(parents=True, exist_ok=True)
    rec_path = _recording_path(save_path, tlid)

    _write_initial_record(rec_path, svg_clean)

    _status = "recording"
    _interval = interval
    _tlid = tlid
    _last_svg = svg_clean
    _rec_path = rec_path
    _loop_proxy = proxy
    _record_unchanged = record_unchanged
    _time_fmt = time_fmt

    print(f"Recording started: {rec_path.name}")
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
            elif _status == "paused":
                time.sleep(0.5)
                continue
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


def _cmd_pause():
    global _status
    if _status == "stopped":
        print("No active recording.", file=sys.stderr)
        sys.exit(1)
    _status = "paused"
    print("Recording paused.")


def _cmd_resume():
    global _status
    if _status == "stopped":
        print("No active recording.", file=sys.stderr)
        sys.exit(1)
    _status = "recording"
    print("Recording resumed.")


if __name__ == "__main__":
    main()
