package util

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
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
	width, _ := strconv.ParseInt(wm[1], 10, 32)
	height, _ := strconv.ParseInt(hm[1], 10, 32)
	return int(width), int(height)
}

func replaceFirst(re *regexp.Regexp, s, template string) string {
	idx := re.FindStringIndex(s)
	if idx == nil {
		return s
	}
	match := s[idx[0]:idx[1]]
	replacement := re.ReplaceAllString(match, template)
	return s[:idx[0]] + replacement + s[idx[1]:]
}

func ResizeSVG(svg string, width, height int, par string) string {
	svg = parRE.ReplaceAllString(svg, "")
	svg = replaceFirst(wRE, svg, fmt.Sprintf(`width="%d"`, width))
	svg = replaceFirst(hRE, svg, fmt.Sprintf(`height="%d"`, height))

	svg = parRE.ReplaceAllString(svg, "")
	reSvg := regexp.MustCompile(`(<svg[^>]*)>`)
	svg = replaceFirst(reSvg, svg, fmt.Sprintf(`$1 preserveAspectRatio="%s">`, par))
	return svg
}

func ReconstructSVG(path string) (lastSVG string, captures int, totalBytes int, err error) {
	tr, err := OpenSVGDRead(path)
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
		svg = Patch(svg, rec.Diff)
	}
	return svg, captures, totalBytes, nil
}
