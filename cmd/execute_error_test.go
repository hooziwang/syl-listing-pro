package cmd

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestExecute_ErrorPath(t *testing.T) {
	if os.Getenv("SYL_EXEC_CRASHER") == "1" {
		os.Args = []string{"syl-listing-pro", "set", "key"}
		Execute()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestExecute_ErrorPath")
	cmd.Env = append(os.Environ(), "SYL_EXEC_CRASHER=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, out=%s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected err type: %T, err=%v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code=%d, out=%s", exitErr.ExitCode(), string(out))
	}
	if !strings.Contains(string(out), "accepts 1 arg") {
		t.Fatalf("unexpected stderr: %s", string(out))
	}
}
