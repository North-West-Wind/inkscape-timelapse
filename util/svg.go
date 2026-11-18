package util

import (
	"fmt"
	"io"
	"os"
	"regexp"
)

var (
	wRE   = regexp.MustCompile(`width="(\d+(?:\.\d+)?)"`)
	hRE   = regexp.MustCompile(`height="(\d+(?:\.\d+)?)"`)
	parRE = regexp.MustCompile(`\s*preserveAspectRatio="[^"]*"`)
)

func svgSize(svg string) (int, int) {
	wm := wRE.FindStringSubmatch(svg)
	hm := hRE.FindStringSubmatch(svg)
	if wm == nil || hm == nil {
		return 0, 0
	}
	return Atoi(wm[1]), Atoi(hm[1])
}

func ReconstructSVG(path string) (lastSVG string, captures int, totalBytes int, err error) {
	tr, err := OpenTLDRead(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer tr.Close()

	var svg string
	var rec Record
	for {
		err = tr.ReadRecord(&rec)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Error reading %s at frame %d: %v\n", path, captures, err)
			}
			break
		}

		captures++
		totalBytes += len(rec.Diff)
		if isNonEmpty(rec.Diff) {
			svg = ApplyDiff(svg, rec.Diff)
		}
	}
	return svg, captures, totalBytes, nil
}
