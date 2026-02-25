package util

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("读取用户目录失败: %w", err)
	}
	return filepath.Join(home, ".syl-listing-pro", "config.yaml"), nil
}

func DefaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		home, e2 := os.UserHomeDir()
		if e2 != nil {
			return "", fmt.Errorf("读取缓存目录失败: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "syl-listing-pro", "rules"), nil
}
