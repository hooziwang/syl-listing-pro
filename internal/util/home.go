package util

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultAppDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("读取用户目录失败: %w", err)
	}
	return filepath.Join(home, ".syl-listing-pro"), nil
}

func DefaultEnvPath() (string, error) {
	base, err := DefaultAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ".env"), nil
}
