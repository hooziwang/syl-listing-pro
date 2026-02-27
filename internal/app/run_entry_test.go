package app

import (
	"context"
	"strings"
	"testing"
)

func TestRunGenAndUpdateRules_MissingKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	err := RunGen(context.Background(), GenOptions{Inputs: []string{"a.md"}})
	if err == nil || !strings.Contains(err.Error(), "尚未配置 KEY") {
		t.Fatalf("RunGen err=%v", err)
	}

	err = RunUpdateRules(context.Background(), UpdateRulesOptions{})
	if err == nil || !strings.Contains(err.Error(), "尚未配置 KEY") {
		t.Fatalf("RunUpdateRules err=%v", err)
	}
}
