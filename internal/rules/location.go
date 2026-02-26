package rules

import (
	"fmt"
	"os"
	"path/filepath"

	"syl-listing-pro/internal/util"
)

func DefaultCacheDir() (string, error) {
	cacheRoot, err := os.UserCacheDir()
	if err == nil && cacheRoot != "" {
		return filepath.Join(cacheRoot, "syl-listing-pro", "rules"), nil
	}
	base, err := util.DefaultAppDir()
	if err != nil {
		return "", err
	}
	// 兜底到旧目录，避免极端环境下无法定位系统缓存目录。
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
