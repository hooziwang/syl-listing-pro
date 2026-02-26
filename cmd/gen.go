package cmd

import (
	"github.com/spf13/cobra"
	"syl-listing-pro/internal/app"
)

var genCmd = &cobra.Command{
	Use:   "gen [file_or_dir ...]",
	Short: "生成 listing",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := app.GenOptions{
			Verbose:   verbose,
			LogFile:   logFile,
			OutputDir: outDir,
			Num:       num,
			Inputs:    args,
		}
		return app.RunGen(cmd.Context(), opts)
	},
}
