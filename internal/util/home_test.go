package util

import (
	"path/filepath"
	"testing"
)

func TestDefaultAppDirAndEnvPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	appDir, err := DefaultAppDir()
	if err != nil {
		t.Fatalf("DefaultAppDir error: %v", err)
	}
	wantApp := filepath.Join(home, ".syl-listing-pro")
	if appDir != wantApp {
		t.Fatalf("appDir=%q want=%q", appDir, wantApp)
	}

	envPath, err := DefaultEnvPath()
	if err != nil {
		t.Fatalf("DefaultEnvPath error: %v", err)
	}
	wantEnv := filepath.Join(wantApp, ".env")
	if envPath != wantEnv {
		t.Fatalf("envPath=%q want=%q", envPath, wantEnv)
	}
}
