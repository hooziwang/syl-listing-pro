package app

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

func writeSSEEvent(t *testing.T, w http.ResponseWriter, event string, data string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("response writer does not support flushing")
	}
	_, _ = io.WriteString(w, fmt.Sprintf("event: %s\n", event))
	_, _ = io.WriteString(w, fmt.Sprintf("data: %s\n\n", data))
	flusher.Flush()
}

func writeSSETrace(t *testing.T, w http.ResponseWriter, id int, data string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("response writer does not support flushing")
	}
	_, _ = io.WriteString(w, "event: trace\n")
	_, _ = io.WriteString(w, fmt.Sprintf("id: %d\n", id))
	_, _ = io.WriteString(w, fmt.Sprintf("data: %s\n\n", data))
	flusher.Flush()
}
