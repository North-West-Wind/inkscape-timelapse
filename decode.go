package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// ── jsonl ─────────────────────────────────────────────────────────

func runJSONL(path string) {
	tr, err := OpenTLDRead(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", path, err)
		os.Exit(1)
	}
	defer tr.Close()

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	var count int
	var rec Record
	for {
		err = tr.ReadRecord(&rec)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Error reading %s at frame %d: %v\n", path, count, err)
			}
			break
		}
		obj := map[string]interface{}{
			"timestamp_ms": rec.TS,
			"diff":         rec.Diff,
		}
		if err := enc.Encode(obj); err != nil {
			fmt.Fprintf(os.Stderr, "JSON encode error: %v\n", err)
			os.Exit(1)
		}
		count++
	}
}

// ── analyze ───────────────────────────────────────────────────────

func runAnalyze(path string) {
	tr, err := OpenTLDRead(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", path, err)
		os.Exit(1)
	}
	defer tr.Close()

	ana := tr.Analyze()

	fmt.Printf("Frames:\n")
	fmt.Printf("  total:              %d\n", ana.count)
	fmt.Printf("  skip-unchanged:     %d\n", ana.count-ana.emptyCount)
	fmt.Printf("Resolution:\n")
	fmt.Printf("  initial:            %dx%d\n", ana.initW, ana.initH)
	fmt.Printf("  min:                %dx%d\n", ana.minW, ana.minH)
	fmt.Printf("  max:                %dx%d\n", ana.maxW, ana.maxH)
}

// ── extract ───────────────────────────────────────────────────────

func runExtract(path string, frame int, output string) {
	tr, err := OpenTLDRead(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", path, err)
		os.Exit(1)
	}
	defer tr.Close()

	svg := ""
	current := 0
	var rec Record
	for {
		err = tr.ReadRecord(&rec)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "Error reading %s at frame %d: %v\n", path, current, err)
			}
			break
		}
		if isNonEmpty(rec.Diff) {
			svg = ApplyDiff(svg, rec.Diff)
		}
		if current == frame {
			if output != "" {
				if err := os.WriteFile(output, []byte(svg), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "Write error: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Print(svg)
			}
			return
		}
		current++
	}

	fmt.Fprintf(os.Stderr, "Frame %d not found (max: %d).\n", frame, current-1)
	os.Exit(1)
}
