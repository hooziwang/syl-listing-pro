package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionAndPrint(t *testing.T) {
	Version = "1.2.3"
	Commit = "abc"
	BuildTime = "2026-02-27"

	vt := versionText()
	if !strings.Contains(vt, "1.2.3") || !strings.Contains(vt, "abc") {
		t.Fatalf("versionText unexpected: %s", vt)
	}

	buf := &bytes.Buffer{}
	printVersion(buf)
	out := buf.String()
	if !strings.Contains(out, "syl-listing-pro 版本：1.2.3") {
		t.Fatalf("missing version line: %s", out)
	}
	if !strings.Contains(out, "DADDYLOVESYL") {
		t.Fatalf("missing banner: %s", out)
	}
}
