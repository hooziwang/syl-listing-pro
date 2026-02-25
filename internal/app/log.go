package app

import (
	"encoding/json"
	"fmt"
	"time"
)

type Logger struct {
	verbose bool
}

func NewLogger(verbose bool) Logger {
	return Logger{verbose: verbose}
}

func (l Logger) Info(msg string) { fmt.Println(msg) }

func (l Logger) Event(event string, fields map[string]any) {
	if !l.verbose {
		return
	}
	m := map[string]any{"ts": time.Now().Format(time.RFC3339Nano), "event": event}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	fmt.Println(string(b))
}
