package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSYLKeyForRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := loadSYLKeyForRun()
	if err == nil || !strings.Contains(err.Error(), "尚未配置 KEY") {
		t.Fatalf("unexpected err: %v", err)
	}

	envPath := filepath.Join(home, ".syl-listing-pro", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("SYL_LISTING_KEY=abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key, err := loadSYLKeyForRun()
	if err != nil {
		t.Fatalf("load key error: %v", err)
	}
	if key != "abc" {
		t.Fatalf("key=%q", key)
	}
}
