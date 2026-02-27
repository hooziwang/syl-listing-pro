package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSYLListingKey_NotConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := LoadSYLListingKey()
	if !errors.Is(err, ErrSYLKeyNotConfigured) {
		t.Fatalf("err=%v, want ErrSYLKeyNotConfigured", err)
	}
}

func TestSaveAndLoadSYLListingKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := SaveSYLListingKey("abc123"); err != nil {
		t.Fatalf("SaveSYLListingKey error: %v", err)
	}
	got, err := LoadSYLListingKey()
	if err != nil {
		t.Fatalf("LoadSYLListingKey error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("got=%q want=%q", got, "abc123")
	}

	p := filepath.Join(home, ".syl-listing-pro", ".env")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read .env error: %v", err)
	}
	if !strings.Contains(string(b), "SYL_LISTING_KEY=abc123") {
		t.Fatalf(".env content unexpected: %s", string(b))
	}
}

func TestSaveSYLListingKey_ReplaceExisting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	envPath := filepath.Join(home, ".syl-listing-pro", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "# comment\nA=1\nSYL_LISTING_KEY=old\nB=2\n"
	if err := os.WriteFile(envPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := SaveSYLListingKey("new-key"); err != nil {
		t.Fatalf("SaveSYLListingKey error: %v", err)
	}
	got, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "SYL_LISTING_KEY=new-key") {
		t.Fatalf("missing updated key: %s", s)
	}
	if strings.Contains(s, "SYL_LISTING_KEY=old") {
		t.Fatalf("old key still exists: %s", s)
	}
	if !strings.HasSuffix(s, "\n") {
		t.Fatalf("expected trailing newline: %q", s)
	}
}

func TestLoadSYLListingKey_QuotedAndEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	envPath := filepath.Join(home, ".syl-listing-pro", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("SYL_LISTING_KEY='quoted'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSYLListingKey()
	if err != nil {
		t.Fatalf("LoadSYLListingKey error: %v", err)
	}
	if got != "quoted" {
		t.Fatalf("got=%q want=quoted", got)
	}

	if err := os.WriteFile(envPath, []byte("SYL_LISTING_KEY=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadSYLListingKey()
	if !errors.Is(err, ErrSYLKeyNotConfigured) {
		t.Fatalf("err=%v, want ErrSYLKeyNotConfigured", err)
	}
}
