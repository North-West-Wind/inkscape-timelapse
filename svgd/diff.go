package svgd

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
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

func Patch(original, diff string) string {
	if !IsNonEmpty(diff) {
		return original
	}

	files, _, err := gitdiff.Parse(strings.NewReader(diff))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to parse diff: %v", err)
		os.Exit(1)
	}

	var output bytes.Buffer
	if err = gitdiff.Apply(&output, strings.NewReader(original), files[0]); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to apply diff: %v", err)
		os.Exit(1)
	}

	return output.String()
}
