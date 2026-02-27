package rules

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCacheDirAndPublicKeyPath(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	cacheDir, err := DefaultCacheDir()
	if err != nil {
		t.Fatalf("DefaultCacheDir error: %v", err)
	}
	if !strings.Contains(cacheDir, filepath.Join("syl-listing-pro", "rules")) {
		t.Fatalf("unexpected cacheDir: %s", cacheDir)
	}

	must := MustDefaultCacheDir()
	if must == "" {
		t.Fatal("MustDefaultCacheDir empty")
	}

	pub, err := DefaultPublicKeyPath()
	if err != nil {
		t.Fatalf("DefaultPublicKeyPath error: %v", err)
	}
	wantPub := filepath.Join(home, ".syl-listing-pro", "rules_public.pem")
	if pub != wantPub {
		t.Fatalf("pub=%q want=%q", pub, wantPub)
	}
}
