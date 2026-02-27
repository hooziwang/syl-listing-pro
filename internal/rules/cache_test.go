package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeTenantID(t *testing.T) {
	if _, err := sanitizeTenantID(" "); err == nil {
		t.Fatal("expected empty error")
	}
	for _, bad := range []string{"a/b", `a\\b`, ".."} {
		if _, err := sanitizeTenantID(bad); err == nil {
			t.Fatalf("expected invalid for %q", bad)
		}
	}
	id, err := sanitizeTenantID("demo")
	if err != nil || id != "demo" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestStateAndArchiveLifecycle(t *testing.T) {
	cacheDir := t.TempDir()
	tenant := "demo"
	state := CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: "/x.tar.gz"}

	loaded, err := LoadState(cacheDir, tenant)
	if err != nil {
		t.Fatalf("LoadState empty error: %v", err)
	}
	if loaded.RulesVersion != "" {
		t.Fatalf("expected empty state: %+v", loaded)
	}

	if err := SaveState(cacheDir, tenant, state); err != nil {
		t.Fatalf("SaveState error: %v", err)
	}
	loaded, err = LoadState(cacheDir, tenant)
	if err != nil {
		t.Fatalf("LoadState error: %v", err)
	}
	if loaded.RulesVersion != "v1" || loaded.ManifestSHA != "sha" {
		t.Fatalf("loaded=%+v", loaded)
	}

	archivePath, err := SaveArchive(cacheDir, tenant, "v1", []byte("abc"))
	if err != nil {
		t.Fatalf("SaveArchive error: %v", err)
	}
	if !HasArchive(archivePath) {
		t.Fatalf("expected archive exists: %s", archivePath)
	}
	if HasArchive("") {
		t.Fatal("empty path should be false")
	}

	if err := Clear(cacheDir, tenant); err != nil {
		t.Fatalf("Clear error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, tenant)); !os.IsNotExist(err) {
		t.Fatalf("expected tenant dir removed, err=%v", err)
	}
	if err := Clear(cacheDir, tenant); err != nil {
		t.Fatalf("Clear absent should be nil, got: %v", err)
	}
}

func TestClearInvalidTenant(t *testing.T) {
	if err := Clear(t.TempDir(), "../bad"); err == nil {
		t.Fatal("expected invalid tenant error")
	}
}

func TestStateAndArchiveErrorBranches(t *testing.T) {
	tenant := "demo"
	// cacheDir 是文件，触发目录创建失败分支。
	base := filepath.Join(t.TempDir(), "cache-file")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(base, tenant, CacheState{RulesVersion: "v1"}); err == nil {
		t.Fatal("expected SaveState mkdir error")
	}
	if _, err := SaveArchive(base, tenant, "v1", []byte("abc")); err == nil {
		t.Fatal("expected SaveArchive mkdir error")
	}

	// 非法 JSON 触发 LoadState 解析失败分支。
	cacheDir := t.TempDir()
	p := filepath.Join(cacheDir, tenant, "current.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(cacheDir, tenant); err == nil {
		t.Fatal("expected LoadState unmarshal error")
	}
}
