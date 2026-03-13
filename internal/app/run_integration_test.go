package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeKeyEnvForTest(t *testing.T, home string) {
	t.Helper()
	envPath := filepath.Join(home, ".syl-listing-pro", ".env")
	if err := os.MkdirAll(filepath.Dir(envPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envPath, []byte("SYL_LISTING_KEY=test-key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureStdoutRun(t *testing.T, fn func() error) (string, error) {
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
	errRun := fn()
	_ = w.Close()
	os.Stdout = old
	return <-done, errRun
}

func prepareRunGenHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeKeyEnvForTest(t, home)
}

func TestRunGen_SuccessWithoutLocalRules(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenHome(t)

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("# 任意输入\n\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_no_rules","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_no_rules/events":
			writeSSETrace(t, w, 1, `{"job_id":"job_no_rules","tenant_id":"demo","offset":1,"item":{"source":"generation","event":"rules_loaded","tenant_id":"demo","job_id":"job_no_rules","elapsed_ms":1,"payload":{"rules_version":"rules-syl-20260313"}}}`)
			writeSSEEvent(t, w, "status", `{"job_id":"job_no_rules","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-13T00:00:02Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_no_rules/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
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

	if err := RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{inputPath}}); err != nil {
		t.Fatalf("RunGen error: %v", err)
	}
}

func TestRunGen_RetryTraceOutput(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenHome(t)

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("# 输入\n\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_retry","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_retry/events":
			writeSSETrace(t, w, 1, `{"job_id":"job_retry","tenant_id":"demo","offset":1,"item":{"source":"generation","event":"job_retry_scheduled","tenant_id":"demo","job_id":"job_retry","elapsed_ms":1200,"payload":{"attempt":1,"max_attempts":3,"next_attempt":2,"error":"boom"}}}`)
			writeSSEEvent(t, w, "status", `{"job_id":"job_retry","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-13T00:00:02Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_retry/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldTimeout := streamTimeoutSecond
	workerBaseURL = ts.URL
	streamTimeoutSecond = 3
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
	}()

	out, err := captureStdoutRun(t, func() error {
		return RunGen(context.Background(), GenOptions{
			Inputs:    []string{inputPath},
			OutputDir: t.TempDir(),
		})
	})
	if err != nil {
		t.Fatalf("RunGen error: %v\nout=%s", err, out)
	}
	if !strings.Contains(out, "任务重试计划：第 1/3 次失败，准备第 2 次") {
		t.Fatalf("stdout missing retry trace output: %s", out)
	}
}

func TestRunGen_CallsDocxConverter(t *testing.T) {
	prepareRunGenHome(t)

	type call struct{ mdPath string }
	var calls []call
	oldConvert := convertMarkdownToDocxFunc
	convertMarkdownToDocxFunc = func(_ context.Context, markdownPath string, outputPath string) (string, error) {
		calls = append(calls, call{mdPath: markdownPath})
		return outputPath, nil
	}
	defer func() { convertMarkdownToDocxFunc = oldConvert }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_meta","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_meta/events":
			writeSSEEvent(t, w, "status", `{"job_id":"job_meta","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-12T00:00:02Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_meta/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldTimeout := streamTimeoutSecond
	workerBaseURL = ts.URL
	streamTimeoutSecond = 1
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
	}()

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("# 输入\n\ninput content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{inputPath}}); err != nil {
		t.Fatalf("RunGen error: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("convert calls=%d want=2", len(calls))
	}
	if !(strings.HasSuffix(calls[0].mdPath, "_en.md") || strings.HasSuffix(calls[1].mdPath, "_en.md")) {
		t.Fatalf("missing en docx conversion call: %+v", calls)
	}
	if !(strings.HasSuffix(calls[0].mdPath, "_cn.md") || strings.HasSuffix(calls[1].mdPath, "_cn.md")) {
		t.Fatalf("missing cn docx conversion call: %+v", calls)
	}
}

func TestRunGen_GenerateAndJobFailurePaths(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenHome(t)

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("# 输入\n\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("generate_failed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "bad req")
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		oldBase := workerBaseURL
		workerBaseURL = ts.URL
		defer func() { workerBaseURL = oldBase }()

		err := RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{inputPath}})
		if err == nil || !strings.Contains(err.Error(), "存在失败任务") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("job_failed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
				_, _ = io.WriteString(w, `{"job_id":"job_failed","status":"queued"}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_failed/events":
				writeSSEEvent(t, w, "status", `{"job_id":"job_failed","tenant_id":"demo","status":"failed","error":"engine failed","updated_at":"2026-03-12T00:00:02Z"}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		oldBase := workerBaseURL
		oldTimeout := streamTimeoutSecond
		workerBaseURL = ts.URL
		streamTimeoutSecond = 1
		defer func() {
			workerBaseURL = oldBase
			streamTimeoutSecond = oldTimeout
		}()

		err := RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{inputPath}})
		if err == nil || !strings.Contains(err.Error(), "存在失败任务") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestRunGen_Timeout(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenHome(t)

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("# 输入\n\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_timeout","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_timeout/events":
			w.Header().Set("Content-Type", "text/event-stream")
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldTimeout := streamTimeoutSecond
	workerBaseURL = ts.URL
	streamTimeoutSecond = 1
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
	}()

	err := RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{inputPath}})
	if err == nil || !strings.Contains(err.Error(), "存在失败任务") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestRunGen_ContextCancelReturnsQuickly(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenHome(t)

	input := filepath.Join(t.TempDir(), "one.md")
	if err := os.WriteFile(input, []byte("# 输入\n\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	workerBaseURL = ts.URL
	defer func() { workerBaseURL = oldBase }()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := RunGen(ctx, GenOptions{OutputDir: t.TempDir(), Inputs: []string{input}, Num: 3})
	if err == nil {
		t.Fatal("expected RunGen error on canceled context")
	}
	if !strings.Contains(err.Error(), "存在失败任务") {
		t.Fatalf("unexpected err: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("RunGen should return quickly on cancel")
	}
}
