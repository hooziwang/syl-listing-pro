package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func TestLogger_InfoAndFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "logs", "run.log")
	lg, err := NewLogger(false, logPath)
	if err != nil {
		t.Fatalf("NewLogger error: %v", err)
	}
	defer lg.Close()

	out := captureStdout(t, func() {
		lg.Info("\x1b[92m分类\x1b[0m完成")
	})
	if !strings.Contains(out, "分类") {
		t.Fatalf("stdout missing content: %q", out)
	}

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file error: %v", err)
	}
	s := string(b)
	if strings.Contains(s, "\x1b[") {
		t.Fatalf("log file should strip ansi: %q", s)
	}
	if !strings.Contains(s, "分类完成") {
		t.Fatalf("log file missing message: %q", s)
	}
}

func TestLogger_EventVerbose(t *testing.T) {
	lg, err := NewLogger(true, "")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	out := captureStdout(t, func() {
		lg.Event("test_event", map[string]any{"k": "v"})
	})
	line := strings.TrimSpace(out)
	if line == "" {
		t.Fatal("expected NDJSON output")
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid json: %v, line=%s", err, line)
	}
	if m["event"] != "test_event" {
		t.Fatalf("event=%v", m["event"])
	}
	if m["k"] != "v" {
		t.Fatalf("k=%v", m["k"])
	}
}

func TestLogger_InfoVerboseUsesEvent(t *testing.T) {
	lg, err := NewLogger(true, "")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	out := captureStdout(t, func() {
		lg.Info("hello")
	})
	if !strings.Contains(out, "\"event\":\"info\"") || !strings.Contains(out, "\"message\":\"hello\"") {
		t.Fatalf("unexpected verbose info output: %q", out)
	}
}
