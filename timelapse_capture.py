#!/usr/bin/env python
"""
Inkscape timelapse capture daemon.
Captures SVG frames from a running Inkscape instance via D-Bus at regular intervals.
Records incremental diffs to a single timestamped JSON file.

Usage:
  timelapse_capture.py start --interval=5 --save-dir=/path
  timelapse_capture.py stop
  timelapse_capture.py pause
  timelapse_capture.py resume
"""
import argparse
import json
import os
import signal
import sys
import tempfile
import time
from datetime import datetime, timezone
from difflib import unified_diff
from pathlib import Path

from dasbus.connection import SessionMessageBus
from gi.repository import GLib


STATE_KEY = "_state"
STATE_DIR = Path.home() / ".inkscape-timelapse"
STATE_FILE = STATE_DIR / "state.json"
DBUS_DEST = "org.inkscape.Inkscape"
DBUS_PATH = "/org/inkscape/Inkscape"

_loop_rec_path = None
_loop_proxy = None


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


# ── State management ──────────────────────────────────────────────

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


# ── Recording file management ─────────────────────────────────────

def _recording_id():
    return datetime.now(timezone.utc).strftime("timelapse_%Y%m%dT%H%M%SZ")


def _recording_path(save_dir, recording_id):
    return Path(save_dir).expanduser().resolve() / f"{recording_id}.json"


def _read_recording(path):
    if path.exists():
        return json.loads(path.read_text(encoding="utf-8"))
    return None


def _write_recording(path, data):
    path.write_text(json.dumps(data, indent=2), encoding="utf-8")


# ── SVG capture via D-Bus ─────────────────────────────────────────

def _get_dbus_proxy():
    bus = SessionMessageBus()
    return bus.get_proxy(DBUS_DEST, DBUS_PATH)


def _capture_svg():
    tmp = tempfile.NamedTemporaryFile(suffix=".svg", delete=False)
    tmp_path = tmp.name
    tmp.close()

    try:
        proxy = _get_dbus_proxy()
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


# ── Commands ──────────────────────────────────────────────────────

def _cmd_start(interval, save_dir):
    save_dir = Path(save_dir or STATE_DIR).expanduser().resolve()
    save_dir.mkdir(parents=True, exist_ok=True)

    if _read_state() is not None:
        print("Recording already active. Stop it first.", file=sys.stderr)
        sys.exit(1)

    svg_str = _capture_svg()
    if not svg_str:
        print("Failed to capture initial SVG. Is Inkscape running with an open document?",
              file=sys.stderr)
        sys.exit(1)

    recording_id = _recording_id()
    rec_path = _recording_path(save_dir, recording_id)

    recording = {
        "initial_svg": svg_str,
        "captures": [],
        STATE_KEY: {
            "status": "recording",
            "start_time": datetime.now(timezone.utc).isoformat(),
            "interval": interval,
            "save_dir": str(save_dir),
            "recording_id": recording_id,
            "last_svg": svg_str,
        },
    }
    _write_recording(rec_path, recording)
    _write_state({"save_dir": str(save_dir), "recording_id": recording_id})

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

            recording = _read_recording(rec_path)
            if recording is None or STATE_KEY not in recording:
                print("Recording data missing, stopping.", file=sys.stderr)
                break

            rec_state = recording[STATE_KEY]
            status = rec_state.get("status")

            if status == "stopped":
                print("Recording stopped by request.")
                break
            elif status == "paused":
                time.sleep(0.5)
                continue
            elif status == "recording":
                interval = rec_state.get("interval", 5.0)
                time.sleep(interval)

                svg_str = _capture_svg_via_proxy()
                if svg_str is None:
                    continue

                previous_svg = rec_state["last_svg"]
                if svg_str == previous_svg:
                    continue

                diff_lines = list(unified_diff(
                    previous_svg.splitlines(keepends=True),
                    svg_str.splitlines(keepends=True),
                    fromfile="previous.svg", tofile="current.svg",
                ))
                diff_text = "".join(diff_lines)

                timestamp = datetime.now(timezone.utc).isoformat()
                recording["captures"].append({"timestamp": timestamp, "diff": diff_text})
                rec_state["last_svg"] = svg_str
                _write_recording(rec_path, recording)

                print(f"Capture at {timestamp} ({len(diff_text)} bytes)")
            else:
                time.sleep(0.5)

    except KeyboardInterrupt:
        print("\nStopped by user.")
    finally:
        _finalize(rec_path)


def _capture_svg_via_proxy():
    """Capture SVG using the cached D-Bus proxy."""
    tmp = tempfile.NamedTemporaryFile(suffix=".svg", delete=False)
    tmp_path = tmp.name
    tmp.close()

    try:
        _loop_proxy.Activate("export-filename", [GLib.Variant("s", tmp_path)], {})
        _loop_proxy.Activate("export-plain-svg", [GLib.Variant("b", True)], {})
        _loop_proxy.Activate("export-do", [], {})

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


def _finalize(rec_path):
    global _loop_rec_path
    recording = _read_recording(rec_path)
    if recording and STATE_KEY in recording:
        del recording[STATE_KEY]
        _write_recording(rec_path, recording)
        print(f"Recording finalized: {rec_path}")
    _clear_state()
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

    save_dir = Path(state["save_dir"]).expanduser().resolve()
    recording_id = state["recording_id"]
    rec_path = _recording_path(save_dir, recording_id)

    recording = _read_recording(rec_path)
    if recording and STATE_KEY in recording:
        recording[STATE_KEY]["status"] = "stopped"
        _write_recording(rec_path, recording)

    print("Stop signal sent to capture loop.")


def _cmd_pause():
    state = _read_state()
    if state is None:
        print("No active recording.", file=sys.stderr)
        sys.exit(1)

    save_dir = Path(state["save_dir"]).expanduser().resolve()
    recording_id = state["recording_id"]
    rec_path = _recording_path(save_dir, recording_id)

    recording = _read_recording(rec_path)
    if recording and STATE_KEY in recording:
        recording[STATE_KEY]["status"] = "paused"
        _write_recording(rec_path, recording)
        print("Recording paused.")


def _cmd_resume():
    state = _read_state()
    if state is None:
        print("No active recording.", file=sys.stderr)
        sys.exit(1)

    save_dir = Path(state["save_dir"]).expanduser().resolve()
    recording_id = state["recording_id"]
    rec_path = _recording_path(save_dir, recording_id)

    recording = _read_recording(rec_path)
    if recording and STATE_KEY in recording:
        recording[STATE_KEY]["status"] = "recording"
        _write_recording(rec_path, recording)
        print("Recording resumed.")


if __name__ == "__main__":
    main()
