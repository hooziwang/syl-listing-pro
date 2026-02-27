package cmd

import (
	"bytes"
	"os"
	"testing"
)

func TestExecute_WithVersionFlag(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	showVersion = false
	bufOut := &bytes.Buffer{}
	bufErr := &bytes.Buffer{}
	rootCmd.SetOut(bufOut)
	rootCmd.SetErr(bufErr)

	os.Args = []string{"syl-listing-pro", "-v"}
	Execute()

	if bufOut.Len() == 0 {
		t.Fatal("expected version output")
	}
}
