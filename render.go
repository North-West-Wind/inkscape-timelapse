package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

func svgToPNG(svgData string) ([]byte, error) {
	cmd := exec.Command("inkscape", "--pipe", "--export-type=png")
	cmd.Stdin = strings.NewReader(svgData)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("inkscape failed: %s", string(ee.Stderr))
		}
		return nil, fmt.Errorf("inkscape failed: %w", err)
	}
	return out, nil
}

func findInkscapePid() (pid int) {
	cmd := exec.Command("pidof", "inkscape")
	output, err := cmd.Output()

	if err == nil {
		pid, err = strconv.Atoi(strings.TrimSuffix(string(output), "\n"))
		if err != nil {
			pid = 0
		}
		return
	}
	return
}

func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err == nil {
		err = proc.Signal(syscall.Signal(0))
		return err == nil
	}
	return false
}

// ── Frames types ─────────────────────────────────────────────────

type frameJob struct {
	num int
	svg string
}

type frameResult struct {
	num int
	png []byte
}

// ── Render command ────────────────────────────────────────────────

func startRender(f renderFlags) {
	tr, err := OpenTLDRead(f.recording)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open recording: %v\n", err)
		os.Exit(1)
	}
	defer tr.Close()

	ana := tr.Analyze()
	relevantFrames := ana.count
	if f.skipUnchanged {
		relevantFrames -= ana.emptyCount
	}
	if relevantFrames == 0 {
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

	total := relevantFrames
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

	// For multi-worker, we need an Inkscape instance
	paused := false
	done := false
	if workers > 1 {
		pid := findInkscapePid()
		stdin := bufio.NewReader(os.Stdin)
		if pid == 0 {
			fmt.Println("To render with multiple workers, a persistent Inkscape instance must exist")
			fmt.Print("Please launch Inkscape, then press 'Enter'...")
			stdin.ReadBytes('\n')
			pid = findInkscapePid()
		}

		if pid == 0 {
			fmt.Println("Inkscape instance not found. Setting workers to 1...")
			workers = 1
		} else {
			go func() {
				ticker := time.NewTicker(time.Second)
				for range ticker.C {
					if !isProcessRunning(pid) {
						break
					}
				}
				for !done {
					paused = true
					fmt.Printf("\nInkscape process exited. Please spawn a new one, then press 'Enter'...\n")
					stdin.ReadBytes('\n')
					pid = findInkscapePid()
					if pid != 0 {
						fmt.Println("Found Inkscape instance")
						paused = false
						for range ticker.C {
							if !isProcessRunning(pid) {
								break
							}
						}
					}
				}
			}()
		}
	}

	tStart := time.Now()
	rendered := renderToFFmpeg(tr, f.skipUnchanged, cw, ch, f.alignX, f.alignY, f.stretch, workers, stdin, &paused)
	elapsed := time.Since(tStart)
	done = true

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
	tr *TLDReader, skipUnchanged bool,
	cw, ch int, alignX, alignY string, stretch bool,
	workers int, stdin io.WriteCloser, paused *bool,
) int {
	jobs := make(chan frameJob, workers*2)
	results := make(chan frameResult, workers*2)

	// Workers
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				svg := setCanvas(job.svg, cw, ch, alignX, alignY, stretch)
				for *paused {
					time.Sleep(time.Millisecond * 100)
				}
				png, err := svgToPNG(svg)
				if err != nil {
					fmt.Fprintf(os.Stderr, "\nRender error (frame %d): %v\n", job.num, err)
					go func(job frameJob) {
						jobs <- job
					}(job)
				} else {
					results <- frameResult{job.num, png}
				}
			}
		}()
	}

	// Producer
	go func() {
		defer close(jobs)
		num := 0
		svg := ""
		var rec Record
		for {
			err := tr.ReadRecord(&rec)
			if err != nil {
				break
			}
			if isNonEmpty(rec.Diff) {
				svg = ApplyDiff(svg, rec.Diff)
			}
			if !isNonEmpty(rec.Diff) && skipUnchanged {
				continue
			}
			jobs <- frameJob{num: num, svg: svg}
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
		fmt.Fprintf(os.Stderr, "\r  Frame %d (buffered %d)", sent, len(buf))
	}

	return sent
}
