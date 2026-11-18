package svgd

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

func Diff(aStr, bStr string) string {
	if aStr == bStr {
		return ""
	}
	uri := span.URIFromPath("")
	edits := myers.ComputeEdits(uri, aStr, bStr)
	u := gotextdiff.ToUnified("previous.svg", "current.svg", aStr, edits)
	return fmt.Sprint(u)
}

var hunkRe = regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@`)
var skipRe = regexp.MustCompile(`^(---|\+\+\+)`)

type hunk struct {
	oldStart int
	oldCount int
	body     []string
}

func Patch(original, diff string) string {
	if !IsNonEmpty(diff) {
		return original
	}

	lines := SplitKeep(original)
	dl := SplitKeep(diff)
	var hunks []hunk

	i := 0
	for i < len(dl) {
		m := hunkRe.FindStringSubmatch(dl[i])
		if m != nil {
			oldStart := Atoi(m[1])
			oldCount := 1
			if m[2] != "" {
				oldCount = Atoi(m[2])
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

	for _, h := range slices.Backward(hunks) {
		startIdx := max(h.oldStart-1, 0)
		var newContent []string
		for _, hl := range h.body {
			if hl == "" {
				continue
			}
			if !strings.HasPrefix(hl, "-") {
				newContent = append(newContent, hl[1:])
			}
		}
		end := min(startIdx+h.oldCount, len(lines))
		lines = append(lines[:startIdx], append(newContent, lines[end:]...)...)
	}

	return strings.Join(lines, "")
}
