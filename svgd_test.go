package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/NorthWestWind/inkscape-timelapse/util"
)

func TestSVGDRoundtrip(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := util.CreateSVGD(path)
	tw.WriteCapture(100, "diff1")
	tw.WriteCapture(200, "diff2")
	tw.Close()

	tr, _ := util.OpenSVGDRead(path)
	defer tr.Close()

	records := make([]util.Record, 0)
	var rec util.Record
	for {
		err := tr.ReadRecord(&rec)
		if err != nil {
			break
		}
		records = append(records, rec)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Diff != "diff1" {
		t.Fatalf("bad diff1: %q", records[0].Diff)
	}
	if records[1].Timestamp != 200 {
		t.Fatalf("bad ts2: %d", records[1].Timestamp)
	}
}

func TestCrashTruncated(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := util.CreateSVGD(path)
	tw.WriteCapture(100, "diff1")
	tw.WriteCapture(200, "diff2")
	tw.Close()

	fi, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	os.WriteFile(path, data[:fi.Size()-10], 0644)

	tr, _ := util.OpenSVGDRead(path)
	defer tr.Close()

	records := make([]util.Record, 0)
	var rec util.Record
	for {
		err := tr.ReadRecord(&rec)
		if err != nil {
			break
		}
		records = append(records, rec)
	}

	if len(records) < 1 {
		t.Fatalf("expected at least 1 record after truncation, got %d", len(records))
	}
}

func TestApplyDiff(t *testing.T) {
	original := "line1\nline2\nline3\n"
	diff := "--- previous.svg\n+++ current.svg\n@@ -1,3 +1,3 @@\n line1\n-line2\n+line2-modified\n line3\n"
	result := util.Patch(original, diff)
	expected := "line1\nline2-modified\nline3\n"
	if result != expected {
		t.Fatalf("apply diff:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestReconstructSVG(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := util.CreateSVGD(path)
	// First capture: diff from "" to "<svg><rect/></svg>"
	diff1 := util.Diff("", "<svg><rect/><circle/></svg>\n")
	tw.WriteCapture(100, diff1)
	tw.Close()

	lastSVG, captures, _, _ := util.ReconstructSVG(path)
	if captures != 1 {
		t.Fatalf("expected 1 capture, got %d", captures)
	}
	if lastSVG != "<svg><rect/><circle/></svg>\n" {
		t.Fatalf("reconstructed: %q", lastSVG)
	}
}

func TestGenerateDiff(t *testing.T) {
	a := "line1\nline2\nline3\n"
	b := "line1\nline2-modified\nline3\n"
	diff := util.Diff(a, b)
	if !strings.Contains(diff, "line2") {
		t.Fatalf("diff missing changed line: %q", diff)
	}
}

func TestAnalyze(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := util.CreateSVGD(path)
	// First capture: initial SVG
	diff1 := util.Diff("", `<svg width="100" height="50"></svg>`)
	tw.WriteCapture(100, diff1)
	tw.WriteCapture(200, "")
	tw.WriteCapture(300, "")
	tw.Close()

	tr, _ := util.OpenSVGDRead(path)
	defer tr.Close()

	ana := tr.Analyze()
	if ana.Frames != 3 {
		t.Fatalf("expected 3 frames, got %d", ana.Frames)
	}
	if ana.InitialWidth != 100 || ana.InitialHeight != 50 {
		t.Fatalf("bad size: %dx%d", ana.InitialWidth, ana.InitialHeight)
	}

	if ana.Frames-ana.EmptyFrames != 1 {
		t.Fatalf("expected 1 frame with skip, got %d", ana.Frames-ana.EmptyFrames)
	}
}

func TestAppendCompat(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := util.CreateSVGD(path)
	tw.WriteCapture(100, "diff1")
	tw.Close()

	tw2, lastSVG, captures, prevBytes, err := util.OpenSVGDAppend(path)
	if err != nil {
		t.Fatalf("OpenTLDAppend failed: %v", err)
	}
	if captures != 1 {
		t.Fatalf("expected 1 capture, got %d", captures)
	}
	if prevBytes != 5 {
		t.Fatalf("expected 5 previous bytes (len('diff1')), got %d", prevBytes)
	}
	if lastSVG != "" {
		t.Fatalf("lastSVG mismatch: %q", lastSVG)
	}
	tw2.WriteCapture(200, "diff2")
	tw2.Close()

	tr, _ := util.OpenSVGDRead(path)
	defer tr.Close()

	records := make([]util.Record, 0)
	var rec util.Record
	for {
		err := tr.ReadRecord(&rec)
		if err != nil {
			break
		}
		records = append(records, rec)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 total records, got %d", len(records))
	}
	if records[1].Diff != "diff2" {
		t.Fatalf("appended diff mismatch: %q", records[1].Diff)
	}
}

func TestApplyEdgeCases(t *testing.T) {
	result := util.Patch("same\n", "")
	if result != "same\n" {
		t.Fatalf("empty diff should return original: %q", result)
	}

	result = util.Patch("same\n", "   \n")
	if result != "same\n" {
		t.Fatalf("whitespace diff should return original: %q", result)
	}

	diff := "--- previous.svg\n+++ current.svg\n@@ -1 +1,2 @@\n line1\n+line2\n"
	result = util.Patch("line1\n", diff)
	if result != "line1\nline2\n" {
		t.Fatalf("insert at end:\n  got:  %q\n  want: %q", result, "line1\nline2\n")
	}
}

func TestResizeSVG(t *testing.T) {
	result := util.ResizeSVG(`<svg width="100" height="200"><rect width="50"/></svg>`, 800, 200, "none")
	expected := `<svg width="800" height="200" preserveAspectRatio="none"><rect width="50"/></svg>`
	if result != expected {
		t.Fatalf("replaceFirst:\n  got:  %q\n  want: %q", result, expected)
	}
}
