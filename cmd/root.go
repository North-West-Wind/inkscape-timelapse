package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "inkord",
	Short: "Inkord is an Inkscape recorder",
	Long:  "An Inkscape vector graphics creation process recorder utilizing D-Bus. Intended for recording timelapses.",
	Run: func(cmd *cobra.Command, args []string) {

	},
}

func Execute() error {
	return rootCmd.Execute()
}
