package cmd

import (
	"fmt"
	"os"

	"codeberg.org/NorthWestWind/inkscape-timelapse/util"
	"github.com/spf13/cobra"
)

var (
	analyzeCmd = &cobra.Command{
		Use:   "analyze <input>",
		Short: "Analyzes a given SVGD flie",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path := args[0]
			tr, err := util.OpenSVGDRead(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", path, err)
				os.Exit(1)
			}
			defer tr.Close()

			ana := tr.Analyze()

			fmt.Printf("Frames:\n")
			fmt.Printf("  total:              %d\n", ana.Frames)
			fmt.Printf("  skip-unchanged:     %d\n", ana.Frames-ana.EmptyFrames)
			fmt.Printf("Resolution:\n")
			fmt.Printf("  initial:            %dx%d\n", ana.InitialWidth, ana.InitialHeight)
			fmt.Printf("  min:                %dx%d\n", ana.MinWidth, ana.MinHeight)
			fmt.Printf("  max:                %dx%d\n", ana.MaxWidth, ana.MaxHeight)
			if len(ana.ResolutionChanges) > 1 {
				fmt.Printf("Resolution changes:\n")
				for _, resChange := range ana.ResolutionChanges {
					fmt.Printf("  at %d: %dx%d->%dx%d\n", resChange.Frame, resChange.FromWidth, resChange.FromHeight, resChange.ToWidth, resChange.ToHeight)
				}
			}
		},
	}
)
