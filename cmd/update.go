package cmd

import (
	"github.com/spf13/cobra"
	"syl-listing-pro/internal/app"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "更新资源",
}

var updateRulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "清除本地规则缓存并下载最新规则",
	RunE: func(cmd *cobra.Command, args []string) error {
		return app.RunUpdateRules(cmd.Context(), app.UpdateRulesOptions{Verbose: verbose, LogFile: logFile, Force: true})
	},
}

func init() {
	updateCmd.AddCommand(updateRulesCmd)
}
