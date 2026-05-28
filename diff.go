package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

var hunkRe = regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@`)
var skipRe = regexp.MustCompile(`^(---|\+\+\+)`)

// ── Diff generation (gotextdiff) ─────────────────────────────────

func generateDiff(aStr, bStr string) string {
	if aStr == bStr {
		return ""
	}
	uri := span.URIFromPath("")
	edits := myers.ComputeEdits(uri, aStr, bStr)
	u := gotextdiff.ToUnified("previous.svg", "current.svg", aStr, edits)
	return fmt.Sprint(u)
}

// ── Diff application ──────────────────────────────────────────────

func ApplyDiff(original, diffText string) string {
	if !isNonEmpty(diffText) {
		return original
	}

	lines := splitKeep(original)
	dl := splitKeep(diffText)
	var hunks []hunk

	i := 0
	for i < len(dl) {
		m := hunkRe.FindStringSubmatch(dl[i])
		if m != nil {
			oldStart := atoi(m[1])
			oldCount := 1
			if m[2] != "" {
				oldCount = atoi(m[2])
			}
			i++
			var body []string
			for i < len(dl) && !hunkRe.MatchString(dl[i]) && !skipRe.MatchString(dl[i]) {
				body = append(body, dl[i])
				i++
			}
			hunks = append(hunks, hunk{oldStart, oldCount, body})
		} else {
			i++
		}
	}

	for hi := len(hunks) - 1; hi >= 0; hi-- {
		h := hunks[hi]
		startIdx := h.oldStart - 1
		if startIdx < 0 {
			startIdx = 0
		}
		var newContent []string
		for _, hl := range h.body {
			if hl == "" {
				continue
			}
			if !strings.HasPrefix(hl, "-") {
				newContent = append(newContent, hl[1:])
			}
		}
		end := startIdx + h.oldCount
		if end > len(lines) {
			end = len(lines)
		}
		lines = append(lines[:startIdx], append(newContent, lines[end:]...)...)
	}

	return strings.Join(lines, "")
}

// ── Helpers ───────────────────────────────────────────────────────

type hunk struct {
	oldStart int
	oldCount int
	body     []string
}

func isNonEmpty(s string) bool {
	return strings.TrimSpace(s) != ""
}

func splitKeep(s string) []string {
	if s == "" {
		return nil
	}
	return strings.SplitAfter(s, "\n")
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		} else {
			break
		}
	}
	return n
}
