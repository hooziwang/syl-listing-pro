package cmd

import (
	"fmt"
	"io"

	"github.com/hooziwang/daddylovesyl"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func versionText() string {
	return fmt.Sprintf("syl-listing-pro 版本：%s（commit: %s，构建时间: %s）", Version, Commit, BuildTime)
}

func loveBanner(w io.Writer) string {
	return daddylovesyl.Render(w)
}

func printVersion(w io.Writer) {
	fmt.Fprintln(w, versionText())
	fmt.Fprintln(w, loveBanner(w))
}
