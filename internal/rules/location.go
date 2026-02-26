package rules

import (
	"fmt"
	"path/filepath"

	"syl-listing-pro/internal/util"
)

func DefaultCacheDir() (string, error) {
	base, err := util.DefaultAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ".rules"), nil
}

func DefaultPublicKeyPath() (string, error) {
	base, err := util.DefaultAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "rules_public.pem"), nil
}

func MustDefaultCacheDir() string {
	dir, err := DefaultCacheDir()
	if err != nil {
		panic(fmt.Sprintf("读取规则缓存目录失败: %v", err))
	}
	return dir
}
