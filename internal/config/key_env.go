package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"syl-listing-pro/internal/util"
)

const sylKeyEnvName = "SYL_LISTING_KEY"

var ErrSYLKeyNotConfigured = errors.New("syl_listing_key_not_configured")

func LoadSYLListingKey() (string, error) {
	p, err := util.DefaultEnvPath()
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrSYLKeyNotConfigured
		}
		return "", fmt.Errorf("读取 .env 失败: %w", err)
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) != sylKeyEnvName {
			continue
		}
		value := strings.TrimSpace(v)
		value = strings.Trim(value, `"'`)
		if value == "" {
			return "", ErrSYLKeyNotConfigured
		}
		return value, nil
	}
	return "", ErrSYLKeyNotConfigured
}

func SaveSYLListingKey(key string) error {
	p, err := util.DefaultEnvPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	line := fmt.Sprintf("%s=%s", sylKeyEnvName, key)

	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(p, []byte(line+"\n"), 0o644)
		}
		return fmt.Errorf("读取 .env 失败: %w", err)
	}

	lines := strings.Split(string(b), "\n")
	replaced := false
	for i, raw := range lines {
		txt := strings.TrimSpace(raw)
		if txt == "" || strings.HasPrefix(txt, "#") {
			continue
		}
		k, _, ok := strings.Cut(txt, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(k) == sylKeyEnvName {
			lines[i] = line
			replaced = true
		}
	}
	if !replaced {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, line)
	}
	content := strings.Join(lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写 .env 失败: %w", err)
	}
	return nil
}
