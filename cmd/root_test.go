package cmd

import (
	"bytes"
	"testing"
)

func TestRootRunE_ShowVersion(t *testing.T) {
	oldShow := showVersion
	defer func() { showVersion = oldShow }()

	showVersion = true
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	if err := rootCmd.RunE(rootCmd, nil); err != nil {
		t.Fatalf("RunE error: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected version output")
	}
}

func TestRootRunE_NoArgsShowsHelp(t *testing.T) {
	oldShow := showVersion
	defer func() { showVersion = oldShow }()

	showVersion = false
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	if err := rootCmd.RunE(rootCmd, nil); err != nil {
		t.Fatalf("RunE no args error: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected help output")
	}
}
