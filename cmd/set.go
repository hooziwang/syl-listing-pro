package cmd

import (
	"github.com/spf13/cobra"
	"syl-listing-pro/internal/app"
)

var setCmd = &cobra.Command{
	Use:   "set",
	Short: "设置配置",
}

var setKeyCmd = &cobra.Command{
	Use:   "key <syl_listing_key>",
	Short: "设置 SYL_LISTING_KEY",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return app.RunSetKey(cmd.Context(), cfgPath, args[0])
	},
}

func init() {
	setCmd.AddCommand(setKeyCmd)
}
