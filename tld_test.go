package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTLDRoundtrip(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := CreateTLD(path)
	tw.WriteCapture(100, "diff1")
	tw.WriteCapture(200, "diff2")
	tw.Close()

	records, _ := ReadRecords(path)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Diff != "diff1" {
		t.Fatalf("bad diff1: %q", records[0].Diff)
	}
	if records[1].TS != 200 {
		t.Fatalf("bad ts2: %d", records[1].TS)
	}
}

func TestCrashTruncated(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := CreateTLD(path)
	tw.WriteCapture(100, "diff1")
	tw.WriteCapture(200, "diff2")
	tw.Close()

	fi, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	os.WriteFile(path, data[:fi.Size()-10], 0644)

	records, _ := ReadRecords(path)
	if len(records) < 1 {
		t.Fatalf("expected at least 1 record after truncation, got %d", len(records))
	}
}

func TestApplyDiff(t *testing.T) {
	original := "line1\nline2\nline3\n"
	diff := "--- previous.svg\n+++ current.svg\n@@ -1,3 +1,3 @@\n line1\n-line2\n+line2-modified\n line3\n"
	result := ApplyDiff(original, diff)
	expected := "line1\nline2-modified\nline3\n"
	if result != expected {
		t.Fatalf("apply diff:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestReconstructSVG(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := CreateTLD(path)
	// First capture: diff from "" to "<svg><rect/></svg>"
	diff1 := generateDiff("", "<svg><rect/><circle/></svg>\n")
	tw.WriteCapture(100, diff1)
	tw.Close()

	lastSVG, captures, _, _ := ReconstructSVG(path)
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
	diff := generateDiff(a, b)
	if !strings.Contains(diff, "line2") {
		t.Fatalf("diff missing changed line: %q", diff)
	}
}

func TestAnalyze(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := CreateTLD(path)
	// First capture: initial SVG
	diff1 := generateDiff("", `<svg width="100" height="50"></svg>`)
	tw.WriteCapture(100, diff1)
	tw.WriteCapture(200, "")
	tw.WriteCapture(300, "")
	tw.Close()

	records, _ := ReadRecords(path)
	ana := analyzeRecords(records, false)
	if ana.count != 3 {
		t.Fatalf("expected 3 frames, got %d", ana.count)
	}
	if ana.initW != 100 || ana.initH != 50 {
		t.Fatalf("bad size: %dx%d", ana.initW, ana.initH)
	}

	ana2 := analyzeRecords(records, true)
	if ana2.count != 1 {
		t.Fatalf("expected 1 frame with skip, got %d", ana2.count)
	}
}

func TestAppendCompat(t *testing.T) {
	dir, _ := os.MkdirTemp("", "tld-*")
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "test.tld")

	tw, _ := CreateTLD(path)
	tw.WriteCapture(100, "diff1")
	tw.Close()

	tw2, lastSVG, captures, prevBytes, err := OpenTLDAppend(path)
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

	records, _ := ReadRecords(path)
	if len(records) != 2 {
		t.Fatalf("expected 2 total records, got %d", len(records))
	}
	if records[1].Diff != "diff2" {
		t.Fatalf("appended diff mismatch: %q", records[1].Diff)
	}
}

func TestApplyEdgeCases(t *testing.T) {
	result := ApplyDiff("same\n", "")
	if result != "same\n" {
		t.Fatalf("empty diff should return original: %q", result)
	}

	result = ApplyDiff("same\n", "   \n")
	if result != "same\n" {
		t.Fatalf("whitespace diff should return original: %q", result)
	}

	diff := "--- previous.svg\n+++ current.svg\n@@ -1 +1,2 @@\n line1\n+line2\n"
	result = ApplyDiff("line1\n", diff)
	if result != "line1\nline2\n" {
		t.Fatalf("insert at end:\n  got:  %q\n  want: %q", result, "line1\nline2\n")
	}
}

func TestReplaceFirst(t *testing.T) {
	re := regexp.MustCompile(`width="(\d+(?:\.\d+)?)"`)
	result := replaceFirst(re, `<svg width="100" height="200"><rect width="50"/></svg>`, `width="800"`)
	expected := `<svg width="800" height="200"><rect width="50"/></svg>`
	if result != expected {
		t.Fatalf("replaceFirst:\n  got:  %q\n  want: %q", result, expected)
	}
}
