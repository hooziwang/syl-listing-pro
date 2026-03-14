package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func prepareRunGenEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeKeyEnvForTest(t, home)
}

func newRunGenFastSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()
	var seq atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			id := seq.Add(1)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"job_id":"job_%d","status":"queued"}`, id))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/job_") && strings.HasSuffix(r.URL.Path, "/events"):
			jobID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"), "/events")
			writeSSETrace(t, w, 1, fmt.Sprintf(`{"job_id":"%s","tenant_id":"demo","offset":1,"item":{"source":"generation","event":"generate_queued","tenant_id":"demo","job_id":"%s","elapsed_ms":0,"payload":{}}}`, jobID, jobID))
			writeSSETrace(t, w, 2, fmt.Sprintf(`{"job_id":"%s","tenant_id":"demo","offset":2,"item":{"source":"generation","event":"rules_loaded","tenant_id":"demo","job_id":"%s","elapsed_ms":1,"payload":{"rules_version":"v1"}}}`, jobID, jobID))
			writeSSEEvent(t, w, "status", fmt.Sprintf(`{"job_id":"%s","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-12T00:00:02Z"}`, jobID))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/job_") && strings.HasSuffix(r.URL.Path, "/result"):
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRunGen_TaskLabelFormats(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenEnv(t)
	ts := newRunGenFastSuccessServer(t)
	defer ts.Close()

	oldBase := workerBaseURL
	oldTimeout := streamTimeoutSecond
	oldMax := maxConcurrentTasks
	workerBaseURL = ts.URL
	streamTimeoutSecond = 5
	maxConcurrentTasks = 16
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
		maxConcurrentTasks = oldMax
	}()

	makeInput := func(name string) string {
		p := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(p, []byte("#MARK\ncontent"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	t.Run("single_file_with_num", func(t *testing.T) {
		input := makeInput("one.md")
		out, err := captureStdoutRun(t, func() error {
			return RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{input}, Num: 2})
		})
		if err != nil {
			t.Fatalf("RunGen error: %v", err)
		}
		if !strings.Contains(out, "[#1]") || !strings.Contains(out, "[#2]") {
			t.Fatalf("expected [#1]/[#2], got: %s", out)
		}
	})

	t.Run("multi_file_single_num", func(t *testing.T) {
		a := makeInput("a.md")
		b := makeInput("b.md")
		out, err := captureStdoutRun(t, func() error {
			return RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{a, b}, Num: 1})
		})
		if err != nil {
			t.Fatalf("RunGen error: %v", err)
		}
		if !strings.Contains(out, "[a.md]") || !strings.Contains(out, "[b.md]") {
			t.Fatalf("expected [a.md]/[b.md], got: %s", out)
		}
	})

	t.Run("multi_file_with_num", func(t *testing.T) {
		a := makeInput("a.md")
		b := makeInput("b.md")
		out, err := captureStdoutRun(t, func() error {
			return RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{a, b}, Num: 2})
		})
		if err != nil {
			t.Fatalf("RunGen error: %v", err)
		}
		if !strings.Contains(out, "[a.md#1]") || !strings.Contains(out, "[b.md#2]") {
			t.Fatalf("expected [a.md#1]/[b.md#2], got: %s", out)
		}
	})
}

func TestRunGen_ConcurrencyLimit(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenEnv(t)
	var seq atomic.Int64
	var current atomic.Int64
	var maxSeen atomic.Int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			n := current.Add(1)
			for {
				prev := maxSeen.Load()
				if n <= prev || maxSeen.CompareAndSwap(prev, n) {
					break
				}
			}
			time.Sleep(80 * time.Millisecond)
			_ = current.Add(-1)
			id := seq.Add(1)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"job_id":"job_%d","status":"queued"}`, id))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/job_") && strings.HasSuffix(r.URL.Path, "/events"):
			jobID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"), "/events")
			writeSSEEvent(t, w, "status", fmt.Sprintf(`{"job_id":"%s","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-12T00:00:02Z"}`, jobID))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/result"):
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldTimeout := streamTimeoutSecond
	oldMax := maxConcurrentTasks
	workerBaseURL = ts.URL
	streamTimeoutSecond = 10
	maxConcurrentTasks = 2
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
		maxConcurrentTasks = oldMax
	}()

	input := filepath.Join(t.TempDir(), "one.md")
	if err := os.WriteFile(input, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{input}, Num: 6}); err != nil {
		t.Fatalf("RunGen error: %v", err)
	}
	if got := maxSeen.Load(); got > 2 {
		t.Fatalf("max concurrent generate=%d exceeds limit=2", got)
	}
	if got := maxSeen.Load(); got < 2 {
		t.Fatalf("expected effective concurrency, got max=%d", got)
	}
}

func TestRunGen_TraceWarnOnlyOnce(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenEnv(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1/events":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "bad events")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldTimeout := streamTimeoutSecond
	workerBaseURL = ts.URL
	streamTimeoutSecond = 5
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
	}()

	input := filepath.Join(t.TempDir(), "one.md")
	if err := os.WriteFile(input, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdoutRun(t, func() error {
		return RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{input}})
	})
	if err == nil {
		t.Fatal("expected RunGen to fail when SSE stream returns 400")
	}
	if c := strings.Count(out, "过程流式接收失败"); c != 1 {
		t.Fatalf("trace warning count=%d want=1, out=%s", c, out)
	}
}

func TestRunGen_ContextCancelWithQueuedTasks(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenEnv(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			select {
			case <-time.After(2 * time.Second):
				_, _ = io.WriteString(w, `{"job_id":"job_1","status":"queued"}`)
			case <-r.Context().Done():
				return
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldMax := maxConcurrentTasks
	workerBaseURL = ts.URL
	maxConcurrentTasks = 1
	defer func() {
		workerBaseURL = oldBase
		maxConcurrentTasks = oldMax
	}()

	input := filepath.Join(t.TempDir(), "one.md")
	if err := os.WriteFile(input, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RunGen(ctx, GenOptions{OutputDir: t.TempDir(), Inputs: []string{input}, Num: 3})
	if err == nil {
		t.Fatal("expected RunGen error on canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected err: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("RunGen should return quickly on cancel")
	}
}

func TestRunGen_SuccessDoesNotEmitInterruptCleanupAfterReturn(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenEnv(t)
	ts := newRunGenFastSuccessServer(t)
	defer ts.Close()

	oldBase := workerBaseURL
	oldTimeout := streamTimeoutSecond
	oldMax := maxConcurrentTasks
	workerBaseURL = ts.URL
	streamTimeoutSecond = 5
	maxConcurrentTasks = 16
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
		maxConcurrentTasks = oldMax
	}()

	input := filepath.Join(t.TempDir(), "one.md")
	if err := os.WriteFile(input, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	oldStdout := os.Stdout
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

	runErr := RunGen(ctx, GenOptions{OutputDir: t.TempDir(), Inputs: []string{input}})
	cancel()
	time.Sleep(50 * time.Millisecond)
	_ = w.Close()
	os.Stdout = oldStdout
	out := <-done

	if runErr != nil {
		t.Fatalf("RunGen error: %v", runErr)
	}
	if strings.Contains(out, "检测到中断，开始取消已提交任务") {
		t.Fatalf("unexpected interrupt cleanup log after success: %s", out)
	}
}
