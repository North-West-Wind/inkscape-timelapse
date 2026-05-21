package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
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
	return atoi(wm[1]), atoi(hm[1])
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

func setCanvas(svg string, cw, ch int, alignX, alignY string, stretch bool) string {
	svg = parRE.ReplaceAllString(svg, "")
	svg = replaceFirst(wRE, svg, fmt.Sprintf(`width="%d"`, cw))
	svg = replaceFirst(hRE, svg, fmt.Sprintf(`height="%d"`, ch))

	xMap := map[string]string{"left": "Min", "center": "Mid", "right": "Max"}
	yMap := map[string]string{"top": "Min", "center": "Mid", "bottom": "Max"}

	var par string
	if stretch {
		par = "none"
	} else {
		par = fmt.Sprintf("x%sY%s meet", xMap[alignX], yMap[alignY])
	}

	svg = parRE.ReplaceAllString(svg, "")
	reSvg := regexp.MustCompile(`(<svg[^>]*>)`)
	svg = replaceFirst(reSvg, svg, fmt.Sprintf(`$1 preserveAspectRatio="%s"`, par))
	return svg
}

func svgToPNG(svgData, engine string) ([]byte, error) {
	var cmd *exec.Cmd
	if engine == "rsvg" {
		cmd = exec.Command("rsvg-convert", "--format", "png")
	} else {
		cmd = exec.Command("inkscape", "--pipe", "--export-type=png")
	}
	cmd.Stdin = strings.NewReader(svgData)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s failed: %s", engine, string(ee.Stderr))
		}
		return nil, fmt.Errorf("%s failed: %w", engine, err)
	}
	return out, nil
}

// ── Frames types ─────────────────────────────────────────────────

type frameJob struct {
	num int
	svg string
}

type frameResult struct {
	num int
	png []byte
	err error
}

// ── Analyze ───────────────────────────────────────────────────────

type analysis struct {
	count        int
	initW, initH int
	maxW, maxH   int
	minW, minH   int
}

func analyzeRecords(records []Record, skipUnchanged bool) analysis {
	var a analysis
	var svg string

	for _, rec := range records {
		if rec.Type == RInitial {
			svg = rec.SVG
			w, h := svgSize(svg)
			if w > 0 && h > 0 {
				a.initW, a.initH = w, h
				a.maxW, a.maxH = w, h
				a.minW, a.minH = w, h
			}
			a.count++
		} else {
			if isNonEmpty(rec.Diff) {
				svg = ApplyDiff(svg, rec.Diff)
			}
			if !isNonEmpty(rec.Diff) && skipUnchanged {
				continue
			}
			w, h := svgSize(svg)
			if w > 0 && h > 0 {
				if w > a.maxW {
					a.maxW = w
				}
				if h > a.maxH {
					a.maxH = h
				}
				if w < a.minW {
					a.minW = w
				}
				if h < a.minH {
					a.minH = h
				}
			}
			a.count++
		}
	}
	return a
}

// ── Render command ────────────────────────────────────────────────

func startRender(f renderFlags) {
	records, err := ReadRecords(f.recording)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read recording: %v\n", err)
		os.Exit(1)
	}

	ana := analyzeRecords(records, f.skipUnchanged)
	if ana.count == 0 {
		fmt.Fprintln(os.Stderr, "No frames found.")
		os.Exit(1)
	}

	cw, ch := 0, 0
	if f.resolution != "" {
		var w, h int
		if n, _ := fmt.Sscanf(f.resolution, "%dx%d", &w, &h); n != 2 || w <= 0 || h <= 0 {
			fmt.Fprintf(os.Stderr, "Invalid --resolution: %s (expected WxH)\n", f.resolution)
			os.Exit(1)
		}
		cw, ch = w, h
	} else {
		switch f.resolutionPolicy {
		case "max":
			cw, ch = ana.maxW, ana.maxH
		case "min":
			cw, ch = ana.minW, ana.minH
		default:
			cw, ch = ana.initW, ana.initH
		}
	}

	if cw <= 0 || ch <= 0 {
		fmt.Fprintln(os.Stderr, "Could not determine canvas resolution.")
		os.Exit(1)
	}

	total := ana.count
	fmt.Printf("Canvas: %dx%d, %d frames at %.1f fps → %s\n", cw, ch, total, f.framerate, f.output)

	workers := f.workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	ffmpeg := exec.Command(
		"ffmpeg", "-y",
		"-f", "image2pipe",
		"-framerate", fmt.Sprintf("%g", f.framerate),
		"-color_range", "full",
		"-colorspace", "bt709",
		"-color_trc", "iec61966-2-1",
		"-color_primaries", "bt709",
		"-i", "-",
		"-c:v", "libvpx-vp9",
		"-lossless", "1",
		"-vf", "format=yuva420p",
		"-color_range", "full",
		"-colorspace", "bt709",
		"-color_trc", "iec61966-2-1",
		"-color_primaries", "bt709",
		f.output,
	)

	ffmpeg.Stderr = io.Discard
	stdin, err := ffmpeg.StdinPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ffmpeg stdin pipe: %v\n", err)
		os.Exit(1)
	}

	if err := ffmpeg.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "ffmpeg start: %v\n", err)
		os.Exit(1)
	}

	tStart := time.Now()
	rendered := renderToFFmpeg(records, f.skipUnchanged, cw, ch, f.alignX, f.alignY, f.stretch, f.engine, workers, stdin)
	elapsed := time.Since(tStart)

	stdin.Close()
	err = ffmpeg.Wait()

	if rendered > 0 {
		fmt.Fprintf(os.Stderr, "\nDone %d frames in %.1fs (%.1f fps)\n", rendered, elapsed.Seconds(), float64(rendered)/elapsed.Seconds())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ffmpeg exited with error: %v\n", err)
	}
}

func renderToFFmpeg(
	records []Record, skipUnchanged bool,
	cw, ch int, alignX, alignY string, stretch bool,
	engine string, workers int, stdin io.WriteCloser,
) int {
	jobs := make(chan frameJob, workers*2)
	results := make(chan frameResult, workers*2)

	// Workers
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				svg := setCanvas(j.svg, cw, ch, alignX, alignY, stretch)
				png, err := svgToPNG(svg, engine)
				results <- frameResult{j.num, png, err}
			}
		}()
	}

	// Producer
	go func() {
		defer close(jobs)
		num := 0
		svg := ""
		for _, rec := range records {
			var frameSVG string
			if rec.Type == RInitial {
				frameSVG = rec.SVG
				svg = rec.SVG
			} else {
				if isNonEmpty(rec.Diff) {
					svg = ApplyDiff(svg, rec.Diff)
				}
				if !isNonEmpty(rec.Diff) && skipUnchanged {
					continue
				}
				frameSVG = svg
			}
			jobs <- frameJob{num: num, svg: frameSVG}
			num++
		}
	}()

	// Close results when workers done
	go func() {
		wg.Wait()
		close(results)
	}()

	// Ordered writer (main goroutine)
	type pending struct {
		png []byte
	}
	buf := make(map[int]pending)
	next := 0
	sent := 0

	for r := range results {
		if r.err != nil {
			fmt.Fprintf(os.Stderr, "\nRender error (frame %d): %v\n", r.num, r.err)
			continue
		}

		if r.num == next {
			stdin.Write(r.png)
			sent++
			next++
			for {
				p, ok := buf[next]
				if !ok {
					break
				}
				stdin.Write(p.png)
				delete(buf, next)
				next++
				sent++
			}
		} else {
			buf[r.num] = pending{png: r.png}
		}
		if sent%10 == 0 {
			fmt.Fprintf(os.Stderr, "\r  Frame %d", sent)
		}
	}

	return sent
}
