package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// ── jsonl ─────────────────────────────────────────────────────────

func runJSONL(path string) {
	records, err := ReadRecords(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	for _, rec := range records {
		obj := map[string]interface{}{
			"timestamp_ms": rec.TS,
			"diff":         rec.Diff,
		}
		if err := enc.Encode(obj); err != nil {
			fmt.Fprintf(os.Stderr, "JSON encode error: %v\n", err)
			os.Exit(1)
		}
	}
}

// ── analyze ───────────────────────────────────────────────────────

func runAnalyze(path string) {
	records, err := ReadRecords(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
		os.Exit(1)
	}

	ana := analyzeRecords(records, false)

	// Count non-empty diffs separately
	totalNoEmpty := 0
	svg := ""
	for _, rec := range records {
		if isNonEmpty(rec.Diff) {
			svg = ApplyDiff(svg, rec.Diff)
			totalNoEmpty++
		}
	}

	fmt.Printf("Frames:\n")
	fmt.Printf("  total:              %d\n", ana.count)
	fmt.Printf("  skip-unchanged:     %d\n", totalNoEmpty)
	fmt.Printf("Resolution:\n")
	fmt.Printf("  initial:            %dx%d\n", ana.initW, ana.initH)
	fmt.Printf("  min:                %dx%d\n", ana.minW, ana.minH)
	fmt.Printf("  max:                %dx%d\n", ana.maxW, ana.maxH)
}

// ── extract ───────────────────────────────────────────────────────

func runExtract(path string, frame int, output string) {
	records, err := ReadRecords(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
		os.Exit(1)
	}

	svg := ""
	current := 0
	for _, rec := range records {
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
