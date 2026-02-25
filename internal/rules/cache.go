package rules

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type CacheState struct {
	RulesVersion string `json:"rules_version"`
	ManifestSHA  string `json:"manifest_sha256"`
	ArchivePath  string `json:"archive_path"`
}

func stateFile(cacheDir string) string {
	return filepath.Join(cacheDir, "current.json")
}

func LoadState(cacheDir string) (CacheState, error) {
	var s CacheState
	b, err := os.ReadFile(stateFile(cacheDir))
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

func SaveState(cacheDir string, s CacheState) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(stateFile(cacheDir), b, 0o644)
}

func SaveArchive(cacheDir, version string, data []byte) (string, error) {
	dir := filepath.Join(cacheDir, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "rules.tar.gz")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func Clear(cacheDir string) error {
	if cacheDir == "" || cacheDir == "/" {
		return fmt.Errorf("cacheDir 非法")
	}
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		return nil
	}
	return os.RemoveAll(cacheDir)
}
