package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSetKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := RunSetKey(nil, "new-key"); err != nil {
		t.Fatalf("RunSetKey error: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".syl-listing-pro", ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "SYL_LISTING_KEY=new-key") {
		t.Fatalf("unexpected env content: %s", string(b))
	}
}
