package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "inkord",
	Short: "Inkord is an Inkscape recorder",
	Long:  "An Inkscape vector graphics creation process recorder utilizing D-Bus. Intended for recording timelapses.",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(recordCmd, renderCmd, jsonlCmd, analyzeCmd, extractCmd)
}
