#!/usr/bin/env python
"""
Tests for inkscape-timelapse.
"""
import json
import tempfile
from pathlib import Path

from inkex.tester import TestCase

from timelapse_capture import (
    _recording_id,
    _recording_path,
    _read_recording,
    _write_recording,
    STATE_KEY,
)


class TimelapseTestCase(TestCase):
    def test_recording_id_format(self):
        rid = _recording_id()
        self.assertTrue(rid.startswith("timelapse_"))
        self.assertEqual(len(rid.split("_")), 2)

    def test_recording_path(self):
        path = _recording_path(Path("/tmp"), "test_id")
        self.assertEqual(path, Path("/tmp/test_id.json"))

    def test_write_and_read_recording(self):
        data = {"initial_svg": "<svg></svg>", "captures": []}
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "test.json"
            _write_recording(path, data)
            self.assertTrue(path.exists())
            loaded = _read_recording(path)
            self.assertEqual(loaded["initial_svg"], "<svg></svg>")

    def test_recording_with_state(self):
        data = {
            "initial_svg": "<svg></svg>",
            "captures": [],
            STATE_KEY: {"status": "recording"},
        }
        with tempfile.TemporaryDirectory() as d:
            path = Path(d) / "test.json"
            _write_recording(path, data)
            loaded = _read_recording(path)
            self.assertIn(STATE_KEY, loaded)
            self.assertEqual(loaded[STATE_KEY]["status"], "recording")
