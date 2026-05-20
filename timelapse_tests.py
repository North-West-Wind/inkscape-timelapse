#!/usr/bin/env python
"""
Tests for inkscape-timelapse.
"""
import json
import subprocess
import tempfile
from pathlib import Path

from inkex.tester import TestCase

from timelapse_capture import (
    _recording_id,
    _recording_path,
    _write_initial_record,
    _append_capture,
)


class TimelapseTestCase(TestCase):
    def test_recording_id_format(self):
        rid = _recording_id()
        self.assertRegex(rid, r"^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$")

    def test_recording_path(self):
        path = _recording_path(Path("/tmp"), "test_id")
        self.assertEqual(path.suffix, ".tld")

    def test_write_and_read(self):
        d = tempfile.mkdtemp()
        try:
            path = Path(d) / "test.tld"
            _write_initial_record(path, "<svg></svg>")
            _append_capture(path, "t1", "diff1")
            _append_capture(path, "t2", "diff2")

            r = subprocess.run(
                ["uv", "run", "python", "timelapse_decode.py", "jsonl", str(path)],
                capture_output=True, text=True,
            )
            self.assertEqual(r.returncode, 0)
            lines = [json.loads(l) for l in r.stdout.strip().splitlines()]
            self.assertEqual(lines[0]["initial_svg"], "<svg></svg>")
            self.assertEqual(lines[1]["diff"], "diff1")
            self.assertEqual(lines[2]["timestamp"], "t2")
        finally:
            import shutil
            shutil.rmtree(d)

    def test_crash_truncated(self):
        d = tempfile.mkdtemp()
        try:
            path = Path(d) / "test.tld"
            _write_initial_record(path, "<svg></svg>")
            _append_capture(path, "t1", "diff1")

            # Append partial record simulating crash
            with open(path, "ab") as f:
                f.write(b"\x01" + b"\x05\x00\x00\x00" + b"hello")

            r = subprocess.run(
                ["uv", "run", "python", "timelapse_decode.py", "jsonl", str(path)],
                capture_output=True, text=True,
            )
            lines = [json.loads(l) for l in r.stdout.strip().splitlines()]
            self.assertEqual(len(lines), 2)  # initial + 1 good capture
        finally:
            import shutil
            shutil.rmtree(d)
