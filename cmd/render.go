package cmd

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

	"codeberg.org/NorthWestWind/inkscape-timelapse/svgd"
	"github.com/spf13/cobra"
)

type ResolutionPolicy = int
type Alignment = int

type frameJob struct {
	num int
	svg string
}

type frameResult struct {
	num int
	png []byte
}

var (
	wRE   = regexp.MustCompile(`width="(\d+(?:\.\d+)?)"`)
	hRE   = regexp.MustCompile(`height="(\d+(?:\.\d+)?)"`)
	parRE = regexp.MustCompile(`\s*preserveAspectRatio="[^"]*"`)

	xMap = map[string]string{"left": "Min", "center": "Mid", "right": "Max"}
	yMap = map[string]string{"top": "Min", "center": "Mid", "bottom": "Max"}

	renderOptions struct {
		framerate        float64
		output           string
		skipUnchanged    bool
		resolutionPolicy string
		width            int
		height           int
		alignX           string
		alignY           string
		stretch          bool
		workers          int
	}
	renderCmd = &cobra.Command{
		Use:   "render <input>",
		Short: "Render an Inkord recording into a video",
		Args:  cobra.ExactArgs(1),
		PreRun: func(cmd *cobra.Command, args []string) {
			if renderOptions.resolutionPolicy != "max" && renderOptions.resolutionPolicy != "min" && renderOptions.resolutionPolicy != "initial" {
				fmt.Fprintf(os.Stderr, "Unknown resolution-policy \"%s\"", renderOptions.resolutionPolicy)
				os.Exit(1)
			}
			if renderOptions.alignX != "left" && renderOptions.alignX != "center" && renderOptions.alignX != "right" {
				fmt.Fprintf(os.Stderr, "Unknown align-x \"%s\"", renderOptions.alignX)
				os.Exit(1)
			}
			if renderOptions.alignY != "top" && renderOptions.alignY != "center" && renderOptions.alignY != "bottom" {
				fmt.Fprintf(os.Stderr, "Unknown align-y \"%s\"", renderOptions.alignY)
				os.Exit(1)
			}
			if renderOptions.width > 0 && renderOptions.height <= 0 || renderOptions.width <= 0 && renderOptions.height > 0 {
				fmt.Fprint(os.Stderr, "Both width and height must be supplied")
				os.Exit(1)
			}
		},
		Run: func(cmd *cobra.Command, args []string) {
			tr, err := svgd.OpenSVGDRead(args[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to open recording: %v\n", err)
				os.Exit(1)
			}
			defer tr.Close()

			analysis := tr.Analyze()
			relevantFrames := analysis.Frames
			if renderOptions.skipUnchanged {
				relevantFrames -= analysis.EmptyFrames
			}
			if relevantFrames == 0 {
				fmt.Fprintln(os.Stderr, "No frames found.")
				os.Exit(1)
			}

			if renderOptions.width <= 0 || renderOptions.height <= 0 {
				switch renderOptions.resolutionPolicy {
				case "max":
					renderOptions.width, renderOptions.height = analysis.MaxWidth, analysis.MaxHeight
				case "min":
					renderOptions.width, renderOptions.height = analysis.MinWidth, analysis.MinHeight
				default:
					renderOptions.width, renderOptions.height = analysis.InitialWidth, analysis.InitialHeight
				}
			}

			if renderOptions.width <= 0 || renderOptions.height <= 0 {
				fmt.Fprintln(os.Stderr, "Could not determine canvas resolution.")
				os.Exit(1)
			}

			total := relevantFrames
			fmt.Printf("Canvas: %dx%d, %d frames at %.1f fps → %s\n", renderOptions.width, renderOptions.height, total, renderOptions.framerate, renderOptions.output)

			ffmpeg := exec.Command(
				"ffmpeg", "-y",
				"-f", "image2pipe",
				"-framerate", fmt.Sprintf("%g", renderOptions.framerate),
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
				renderOptions.output,
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
			if renderOptions.workers > 1 {
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
					renderOptions.workers = 1
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
			rendered := renderToFFmpeg(tr, stdin, &paused)
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
		},
	}
)

func init() {
	renderCmd.Flags().Float64VarP(&renderOptions.framerate, "framerate", "r", 10, "Output video frame rate")
	renderCmd.Flags().StringVarP(&renderOptions.output, "output", "o", "timelapse.webm", "Output video path")
	renderCmd.Flags().BoolVarP(&renderOptions.skipUnchanged, "skip-unchanged", "s", false, "Skip frames where document didn't change")
	renderCmd.Flags().StringVar(&renderOptions.resolutionPolicy, "resolution-policy", "max", "Behaviour if SVG resolution changed (max|min|initial)")
	renderCmd.Flags().IntVarP(&renderOptions.width, "wiidth", "W", 0, "Width of the output video (overrides --resolution-policy)")
	renderCmd.Flags().IntVarP(&renderOptions.height, "height", "H", 0, "Height of the output video (overrides --resolution-policy)")
	renderCmd.Flags().StringVar(&renderOptions.alignX, "align-x", "center", "Horizontal position alignment (left|center|right)")
	renderCmd.Flags().StringVar(&renderOptions.alignY, "align-y", "center", "vertical position alignment (top|center|bottom)")
	renderCmd.Flags().BoolVar(&renderOptions.stretch, "stetch", false, "Stretch SVG to fill canvas")
	renderCmd.Flags().IntVar(&renderOptions.workers, "workers", runtime.NumCPU(), "Render workers")
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

func renderToFFmpeg(tr *svgd.SVGDReader, stdin io.WriteCloser, paused *bool) int {
	jobs := make(chan frameJob, renderOptions.workers*2)
	results := make(chan frameResult, renderOptions.workers*2)

	// Workers
	var wg sync.WaitGroup
	for range renderOptions.workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				var par string
				if renderOptions.stretch {
					par = "none"
				} else {
					par = fmt.Sprintf("x%sY%s meet", xMap[renderOptions.alignX], yMap[renderOptions.alignY])
				}
				svg := svgd.ResizeSVG(job.svg, renderOptions.width, renderOptions.height, par)
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
		cr := svgd.NewCheckpointReader(tr)
		for {
			svg, diff, err := cr.ReadSVG(num)
			if err != nil {
				break
			}
			if !svgd.IsNonEmpty(diff) && skipUnchanged {
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
