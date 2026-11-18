package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"codeberg.org/NorthWestWind/inkscape-timelapse/util"
	"github.com/spf13/cobra"
)

var (
	extractFrame int
	extractCmd   = &cobra.Command{
		Use:   "extract [options] <input> <output>",
		Short: "Extracts a single frame from an SVGD file into an SVG file",
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			path := args[0]
			output := args[1]
			tr, err := util.OpenSVGDRead(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", path, err)
				os.Exit(1)
			}
			defer tr.Close()

			svg := ""
			current := 0
			var rec util.Record
			for {
				err = tr.ReadRecord(&rec)
				if err != nil {
					if err != io.EOF {
						fmt.Fprintf(os.Stderr, "Error reading %s at frame %d: %v\n", path, current, err)
					}
					break
				}
				if strings.TrimSpace(rec.Diff) != "" {
					svg = util.ApplyDiff(svg, rec.Diff)
				}
				if current == extractFrame {
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

			fmt.Fprintf(os.Stderr, "Frame %d not found (max: %d).\n", extractFrame, current-1)
			os.Exit(1)
		},
	}
)

func init() {
	extractCmd.Flags().IntVarP(&extractFrame, "frame", "f", 0, "Frame to extract")
}
