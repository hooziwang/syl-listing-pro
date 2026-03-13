package input

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover_FilesAndDirWithDedup(t *testing.T) {
	dir := t.TempDir()

	okFile := filepath.Join(dir, "a.md")
	if err := os.WriteFile(okFile, []byte("# title\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignoredFile := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(ignoredFile, []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	okFile2 := filepath.Join(sub, "c.markdown")
	if err := os.WriteFile(okFile2, []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := Discover([]string{dir, okFile})
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len=%d want=2", len(items))
	}
	seen := map[string]bool{}
	for _, it := range items {
		seen[it.Path] = true
	}
	if !seen[okFile] || !seen[okFile2] {
		t.Fatalf("paths missing: %+v", items)
	}
}

func TestDiscover_Errors(t *testing.T) {
	dir := t.TempDir()

	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("x\ny"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover([]string{dir}); err == nil {
		t.Fatal("expected no-file-found error")
	}

	if _, err := Discover([]string{filepath.Join(dir, "not-exists")}); err == nil {
		t.Fatal("expected stat error")
	}
}

func TestDiscover_SkipsHiddenNodeModulesAndGeneratedOutputs(t *testing.T) {
	dir := t.TempDir()

	keep := filepath.Join(dir, "input.md")
	if err := os.WriteFile(keep, []byte("# source"), 0o644); err != nil {
		t.Fatal(err)
	}

	hiddenDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hiddenDir, "hidden.md"), []byte("# hidden"), 0o644); err != nil {
		t.Fatal(err)
	}

	nodeModulesDir := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(nodeModulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeModulesDir, "dep.md"), []byte("# dep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "input_1234_en.md"), []byte("# generated en"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input_1234_cn.markdown"), []byte("# generated cn"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := Discover([]string{dir})
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len=%d want=1", len(items))
	}
	if items[0].Path != keep {
		t.Fatalf("got=%q want=%q", items[0].Path, keep)
	}
}
