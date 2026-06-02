package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type dashWriter struct {
	w io.Writer
}

func (d *dashWriter) Write(p []byte) (int, error) {
	s := string(p)
	// Replace "  -" at line start with "  --" (first line or after newline)
	s = strings.ReplaceAll(s, "\n  -", "\n  --")
	if len(s) >= 3 && s[:3] == "  -" {
		s = "  --" + s[3:]
	}
	return d.w.Write([]byte(s))
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.SetOutput(&dashWriter{w: os.Stderr})
	return fs
}

// extractPosArg removes and returns the first positional (non-flag) argument.
// This allows flags to appear before or after the positional arg, matching
// the flexibility the Python version had.
func extractPosArg(args []string, fs *flag.FlagSet) (string, []string) {
	// Collect the names of boolean flags so we know which don't consume
	// the next argument as a value.
	boolFlags := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		// Use flag.UnquoteUsage or check the value type.
		// A bool flag's Value implements flag.boolFlag interface.
		// We check via type assertion on the underlying type.
		if _, ok := f.Value.(interface {
			IsBoolFlag() bool
		}); ok {
			boolFlags[f.Name] = true
		}
	})

	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			return a, append(args[:i], args[i+1:]...)
		}
		// It's a flag. Skip its value if it takes one.
		name := strings.TrimLeft(a, "-")
		// Handle --flag=value syntax
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		if !boolFlags[name] {
			i++ // skip the value arg
		}
	}
	return "", args
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "record":
		cmdRecord(args)
	case "render":
		cmdRender(args)
	case "jsonl":
		cmdJSONL(args)
	case "analyze":
		cmdAnalyze(args)
	case "extract":
		cmdExtract(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	name := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, `Usage:
  %[1]s record [flags]            Record a timelapse
  %[1]s render <recording> [flags]  Render .tld to .webm
  %[1]s jsonl <recording>        Decode .tld to JSONL on stdout
  %[1]s analyze <recording>      Print frame count and resolution stats
  %[1]s extract <recording> --frame=N [--output file]  Extract a frame as SVG

Run '%[1]s <command> --help' for command-specific flags.
`, name)
}

type recordFlags struct {
	interval      time.Duration
	saveDir       string
	appendPath    string
	skipUnchanged bool
}

func cmdRecord(args []string) {
	fs := newFlagSet("record")
	interval := fs.Float64("interval", 5.0, "Capture interval in seconds")
	saveDir := fs.String("save-dir", "", "Recording save directory")
	appendPath := fs.String("append", "", "Continue recording to an existing .tld file")
	skipUnchanged := fs.Bool("skip-unchanged", false, "Skip capture where document didn't change")
	fs.Parse(args)

	recordCapture(recordFlags{
		interval:      time.Duration(*interval * float64(time.Second)),
		saveDir:       *saveDir,
		appendPath:    *appendPath,
		skipUnchanged: *skipUnchanged,
	})
}

type renderFlags struct {
	recording        string
	framerate        float64
	output           string
	skipUnchanged    bool
	resolutionPolicy string
	resolution       string
	alignX           string
	alignY           string
	stretch          bool
	workers          int
}

func cmdRender(args []string) {
	fs := newFlagSet("render")
	framerate := fs.Float64("framerate", 10.0, "Output video frame rate")
	output := fs.String("output", "timelapse.webm", "Output video path")
	skipUnchanged := fs.Bool("skip-unchanged", false, "Skip frames where document didn't change")
	resolutionPolicy := fs.String("resolution-policy", "max", "max|min|initial")
	resolution := fs.String("resolution", "", "Explicit WxH (overrides --resolution-policy)")
	alignX := fs.String("align-x", "center", "left|center|right")
	alignY := fs.String("align-y", "center", "top|center|bottom")
	stretch := fs.Bool("stretch", false, "Stretch SVG to fill canvas")
	workers := fs.Int("workers", 0, "Render workers (default: CPU count)")

	recPath, flagArgs := extractPosArg(args, fs)
	fs.Parse(flagArgs)

	if recPath == "" && fs.NArg() > 0 {
		recPath = fs.Arg(0)
	}
	if recPath == "" {
		fmt.Fprintln(os.Stderr, "Missing recording path")
		fs.Usage()
		os.Exit(1)
	}

	startRender(renderFlags{
		recording:        recPath,
		framerate:        *framerate,
		output:           *output,
		skipUnchanged:    *skipUnchanged,
		resolutionPolicy: *resolutionPolicy,
		resolution:       *resolution,
		alignX:           *alignX,
		alignY:           *alignY,
		stretch:          *stretch,
		workers:          *workers,
	})
}

func cmdJSONL(args []string) {
	fs := newFlagSet("jsonl")
	recPath, flagArgs := extractPosArg(args, fs)
	fs.Parse(flagArgs)
	if recPath == "" && fs.NArg() > 0 {
		recPath = fs.Arg(0)
	}
	if recPath == "" {
		fmt.Fprintln(os.Stderr, "Missing recording path")
		fs.Usage()
		os.Exit(1)
	}
	runJSONL(recPath)
}

func cmdAnalyze(args []string) {
	fs := newFlagSet("analyze")
	recPath, flagArgs := extractPosArg(args, fs)
	fs.Parse(flagArgs)
	if recPath == "" && fs.NArg() > 0 {
		recPath = fs.Arg(0)
	}
	if recPath == "" {
		fmt.Fprintln(os.Stderr, "Missing recording path")
		fs.Usage()
		os.Exit(1)
	}
	runAnalyze(recPath)
}

func cmdExtract(args []string) {
	fs := newFlagSet("extract")
	frame := fs.Int("frame", 0, "Frame index (0 = initial)")
	output := fs.String("output", "", "Output file (default: stdout)")
	recPath, flagArgs := extractPosArg(args, fs)
	fs.Parse(flagArgs)
	if recPath == "" && fs.NArg() > 0 {
		recPath = fs.Arg(0)
	}
	if recPath == "" {
		fmt.Fprintln(os.Stderr, "Missing recording path")
		fs.Usage()
		os.Exit(1)
	}
	runExtract(recPath, *frame, *output)
}
