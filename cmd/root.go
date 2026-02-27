package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"syl-listing-pro/internal/app"
)

var (
	verbose     bool
	logFile     string
	outDir      string
	num         int
	showVersion bool
)

var rootCmd = &cobra.Command{
	Use:   "syl-listing-pro [file_or_dir ...]",
	Short: "生成双语 listing（新架构 CLI）",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if showVersion {
			printVersion(cmd.OutOrStdout())
			return nil
		}
		if len(args) == 0 {
			return cmd.Help()
		}
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

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.SilenceUsage = true
	rootCmd.SilenceErrors = true
	rootCmd.CompletionOptions.HiddenDefaultCmd = true

	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "输出 NDJSON 详细日志")
	rootCmd.PersistentFlags().StringVar(&logFile, "log-file", "", "日志文件路径")
	rootCmd.PersistentFlags().StringVarP(&outDir, "out", "o", ".", "输出目录")
	rootCmd.PersistentFlags().IntVarP(&num, "num", "n", 1, "每个需求文件生成候选数量")
	rootCmd.PersistentFlags().BoolVarP(&showVersion, "version", "v", false, "显示版本信息")

	rootCmd.AddCommand(genCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(setCmd)
	rootCmd.AddCommand(updateCmd)
}
