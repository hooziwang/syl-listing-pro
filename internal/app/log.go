package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	verbose bool
	file    *os.File
	mu      sync.Mutex
}

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func NewLogger(verbose bool, logFile string) (*Logger, error) {
	l := &Logger{verbose: verbose}
	if strings.TrimSpace(logFile) == "" {
		return l, nil
	}
	dir := filepath.Dir(logFile)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	l.file = f
	return l, nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

func (l *Logger) writeLine(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Println(line)
	if l.file != nil {
		_, _ = l.file.WriteString(ansiEscape.ReplaceAllString(line, "") + "\n")
	}
}

func (l *Logger) Info(msg string) {
	if l.verbose {
		l.Event("info", map[string]any{"message": msg})
		return
	}
	l.writeLine(msg)
}

func (l *Logger) Event(event string, fields map[string]any) {
	if !l.verbose {
		return
	}
	m := map[string]any{"ts": time.Now().Format(time.RFC3339Nano), "event": event}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	l.writeLine(string(b))
}
