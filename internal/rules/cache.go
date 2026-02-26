package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CacheState struct {
	RulesVersion string `json:"rules_version"`
	ManifestSHA  string `json:"manifest_sha256"`
	ArchivePath  string `json:"archive_path"`
}

func sanitizeTenantID(tenantID string) (string, error) {
	id := strings.TrimSpace(tenantID)
	if id == "" {
		return "", fmt.Errorf("tenant_id 不能为空")
	}
	if strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return "", fmt.Errorf("tenant_id 非法")
	}
	return id, nil
}

func tenantDir(cacheDir, tenantID string) (string, error) {
	id, err := sanitizeTenantID(tenantID)
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, id), nil
}

func stateFile(cacheDir, tenantID string) (string, error) {
	dir, err := tenantDir(cacheDir, tenantID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "current.json"), nil
}

func LoadState(cacheDir, tenantID string) (CacheState, error) {
	var s CacheState
	p, err := stateFile(cacheDir, tenantID)
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	return s, nil
}

func SaveState(cacheDir, tenantID string, s CacheState) error {
	dir, err := tenantDir(cacheDir, tenantID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	p, err := stateFile(cacheDir, tenantID)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

func SaveArchive(cacheDir, tenantID, version string, data []byte) (string, error) {
	root, err := tenantDir(cacheDir, tenantID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "rules.tar.gz")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func HasArchive(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func Clear(cacheDir, tenantID string) error {
	dir, err := tenantDir(cacheDir, tenantID)
	if err != nil {
		return err
	}
	if dir == "" || dir == "/" {
		return fmt.Errorf("cacheDir 非法")
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(dir)
}
