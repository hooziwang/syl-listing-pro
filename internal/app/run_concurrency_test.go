package app

import (
	"context"
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

	"syl-listing-pro/internal/rules"
)

func prepareRunGenCachedRules(t *testing.T, marker string) {
	t.Helper()
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", makeRulesArchiveBytes(t, marker))
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{
		RulesVersion: "v1",
		ManifestSHA:  "sha",
		ArchivePath:  archivePath,
	}); err != nil {
		t.Fatal(err)
	}
}

func newRunGenFastSuccessServer(t *testing.T) *httptest.Server {
	t.Helper()
	var seq atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			id := seq.Add(1)
			_, _ = io.WriteString(w, fmt.Sprintf(`{"job_id":"job_%d","status":"queued"}`, id))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/job_") && strings.HasSuffix(r.URL.Path, "/trace"):
			jobID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/jobs/"), "/trace")
			_, _ = io.WriteString(w, fmt.Sprintf(`{"ok":true,"job_id":"%s","job_status":"running","tenant_id":"demo","trace_count":3,"limit":300,"offset":0,"next_offset":3,"has_more":false,"items":[{"source":"engine","event":"generate_queued","tenant_id":"demo","job_id":"%s","elapsed_ms":0,"payload":{}},{"source":"engine","event":"rules_loaded","tenant_id":"demo","job_id":"%s","elapsed_ms":1,"payload":{"rules_version":"v1"}},{"source":"engine","event":"generation_ok","tenant_id":"demo","job_id":"%s","elapsed_ms":2,"payload":{"timing_ms":2}}]}`, jobID, jobID, jobID, jobID))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/job_") &&
			!strings.HasSuffix(r.URL.Path, "/trace") && !strings.HasSuffix(r.URL.Path, "/result"):
			_, _ = io.WriteString(w, `{"job_id":"x","status":"succeeded"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/jobs/job_") && strings.HasSuffix(r.URL.Path, "/result"):
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestRunGen_TaskLabelFormats(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenCachedRules(t, "#MARK")
	ts := newRunGenFastSuccessServer(t)
	defer ts.Close()

	oldBase := workerBaseURL
	oldPollMs := pollIntervalMs
	oldPollTimeout := pollTimeoutSecond
	oldMax := maxConcurrentTasks
	workerBaseURL = ts.URL
	pollIntervalMs = 1
	pollTimeoutSecond = 5
	maxConcurrentTasks = 16
	defer func() {
		workerBaseURL = oldBase
		pollIntervalMs = oldPollMs
		pollTimeoutSecond = oldPollTimeout
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
	prepareRunGenCachedRules(t, "#MARK")
	var seq atomic.Int64
	var current atomic.Int64
	var maxSeen atomic.Int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
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
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/trace"):
			_, _ = io.WriteString(w, `{"ok":true,"job_id":"x","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":0,"next_offset":0,"has_more":false,"items":[]}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/v1/jobs/job_") && !strings.HasSuffix(r.URL.Path, "/result") && !strings.HasSuffix(r.URL.Path, "/trace"):
			_, _ = io.WriteString(w, `{"job_id":"x","status":"succeeded"}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/result"):
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldPollMs := pollIntervalMs
	oldPollTimeout := pollTimeoutSecond
	oldMax := maxConcurrentTasks
	workerBaseURL = ts.URL
	pollIntervalMs = 1
	pollTimeoutSecond = 10
	maxConcurrentTasks = 2
	defer func() {
		workerBaseURL = oldBase
		pollIntervalMs = oldPollMs
		pollTimeoutSecond = oldPollTimeout
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
	prepareRunGenCachedRules(t, "#MARK")
	var jobReads atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1/trace":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "bad trace")
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1":
			n := jobReads.Add(1)
			if n < 3 {
				_, _ = io.WriteString(w, `{"job_id":"job_1","status":"running"}`)
				return
			}
			_, _ = io.WriteString(w, `{"job_id":"job_1","status":"succeeded"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldPoll := pollIntervalMs
	oldTimeout := pollTimeoutSecond
	workerBaseURL = ts.URL
	pollIntervalMs = 1
	pollTimeoutSecond = 5
	defer func() {
		workerBaseURL = oldBase
		pollIntervalMs = oldPoll
		pollTimeoutSecond = oldTimeout
	}()

	input := filepath.Join(t.TempDir(), "one.md")
	if err := os.WriteFile(input, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdoutRun(t, func() error {
		return RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{input}})
	})
	if err != nil {
		t.Fatalf("RunGen error: %v", err)
	}
	if c := strings.Count(out, "过程拉取失败，继续执行"); c != 1 {
		t.Fatalf("trace warning count=%d want=1, out=%s", c, out)
	}
}

func TestRunGen_ContextCancelWithQueuedTasks(t *testing.T) {
	stubDocxConverter(t)
	prepareRunGenCachedRules(t, "#MARK")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
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
	if !strings.Contains(err.Error(), "存在失败任务") {
		t.Fatalf("unexpected err: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("RunGen should return quickly on cancel")
	}
}
