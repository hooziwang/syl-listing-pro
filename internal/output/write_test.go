package output

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniquePair_Success(t *testing.T) {
	dir := t.TempDir()
	s, en, cn, err := UniquePair(dir)
	if err != nil {
		t.Fatalf("UniquePair error: %v", err)
	}
	if len(s) != 8 {
		t.Fatalf("id len=%d want=8", len(s))
	}
	if !strings.HasPrefix(filepath.Base(en), "listing_") || !strings.HasSuffix(en, "_en.md") {
		t.Fatalf("unexpected en path: %s", en)
	}
	if !strings.HasPrefix(filepath.Base(cn), "listing_") || !strings.HasSuffix(cn, "_cn.md") {
		t.Fatalf("unexpected cn path: %s", cn)
	}
}

func TestUniquePair_MkdirError(t *testing.T) {
	dir := t.TempDir()
	fileAsDir := filepath.Join(dir, "file")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := UniquePair(fileAsDir); err == nil {
		t.Fatal("expected mkdir error")
	}
}
