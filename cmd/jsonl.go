package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"codeberg.org/NorthWestWind/inkscape-timelapse/svgd"
	"github.com/spf13/cobra"
)

var (
	jsonlOutput string
	jsonlCmd    = &cobra.Command{
		Use:   "jsonl <input>",
		Short: "Converts SVGD format into JSONL format",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			path := args[0]
			tr, err := svgd.OpenSVGDRead(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", path, err)
				os.Exit(1)
			}
			defer tr.Close()

			var writer io.Writer
			if jsonlOutput == "" || jsonlOutput == "-" {
				writer = os.Stdout
			} else {
				file, err := os.Create(jsonlOutput)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error creating %s: %v\n", jsonlOutput, err)
					os.Exit(1)
				}
				defer file.Close()
				writer = file
			}
			enc := json.NewEncoder(writer)
			enc.SetEscapeHTML(false)

			var count int
			var rec svgd.Record
			for {
				err = tr.ReadRecord(&rec)
				if err != nil {
					if err != io.EOF {
						fmt.Fprintf(os.Stderr, "Error reading %s at frame %d: %v\n", path, count, err)
					}
					break
				}
				obj := map[string]interface{}{
					"timestamp_ms": rec.Timestamp,
					"diff":         rec.Diff,
				}
				if err := enc.Encode(obj); err != nil {
					fmt.Fprintf(os.Stderr, "JSON encode error: %v\n", err)
					os.Exit(1)
				}
				count++
			}
		},
	}
)

func init() {
	jsonlCmd.Flags().StringVarP(&jsonlOutput, "output", "o", "", "JSONL output file. Omit for stdout")
}
