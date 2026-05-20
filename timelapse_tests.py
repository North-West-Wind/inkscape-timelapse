#!/usr/bin/env python
"""
Tests for inkscape-timelapse.
"""
from pathlib import Path

from inkex.tester import TestCase

from timelapse_capture import (
    _recording_id,
    _recording_path,
    _read_recording,
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
        import tempfile
        d = tempfile.mkdtemp()
        try:
            path = Path(d) / "test.tld"
            _write_initial_record(path, "<svg></svg>")
            _append_capture(path, "t1", "diff1")
            _append_capture(path, "t2", "diff2")

            loaded = _read_recording(path)
            self.assertEqual(loaded["initial_svg"], "<svg></svg>")
            self.assertEqual(len(loaded["captures"]), 2)
            self.assertEqual(loaded["captures"][0]["diff"], "diff1")
            self.assertEqual(loaded["captures"][1]["timestamp"], "t2")
        finally:
            import shutil
            shutil.rmtree(d)

    def test_crash_truncated(self):
        import tempfile, shutil
        d = tempfile.mkdtemp()
        try:
            path = Path(d) / "test.tld"
            _write_initial_record(path, "<svg></svg>")
            _append_capture(path, "t1", "diff1")

            # Append partial record simulating crash
            with open(path, "ab") as f:
                f.write(b"\x01" + b"\x05\x00\x00\x00" + b"hello")

            loaded = _read_recording(path)
            self.assertEqual(len(loaded["captures"]), 1)
        finally:
            shutil.rmtree(d)
