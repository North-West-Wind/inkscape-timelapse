package cmd

import (
	"fmt"
	"io"
	"os"

	"codeberg.org/NorthWestWind/inkscape-timelapse/svgd"
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
			tr, err := svgd.OpenSVGDRead(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", path, err)
				os.Exit(1)
			}
			defer tr.Close()

			cr := svgd.NewCheckpointReader(tr)
			svg, _, err := cr.ReadSVG(extractFrame)
			if err != nil {
				if err != io.EOF {
					fmt.Fprintf(os.Stderr, "Error reading %s at frame %d: %v\n", path, cr.Position(), err)
				} else {
					fmt.Fprintf(os.Stderr, "Frame %d not found (max: %d).\n", extractFrame, cr.Position())
				}
				os.Exit(1)
			}

			if output != "" {
				if err := os.WriteFile(output, []byte(svg), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "Write error: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Print(svg)
			}
		},
	}
)

func init() {
	extractCmd.Flags().IntVarP(&extractFrame, "frame", "f", 0, "Frame to extract")
}
