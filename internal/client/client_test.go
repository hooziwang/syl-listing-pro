package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestReadReqBodyAndTraceBody(t *testing.T) {
	if readReqBody(nil) != "" {
		t.Fatal("nil request should be empty")
	}
	req, err := http.NewRequest(http.MethodPost, "http://x", bytes.NewReader([]byte("abc")))
	if err != nil {
		t.Fatal(err)
	}
	if got := readReqBody(req); got != "abc" {
		t.Fatalf("got=%q", got)
	}

	if got := traceBody([]byte("中文")); got != "中文" {
		t.Fatalf("got=%q", got)
	}
	if got := traceBody([]byte{0xff, 0x00, 0x01}); !strings.Contains(got, "<binary bytes=3 sha256=") {
		t.Fatalf("got=%q", got)
	}
}

func TestRetryHelpers(t *testing.T) {
	if retryBackoff(0) != retryBackoffBase {
		t.Fatalf("unexpected backoff(0)")
	}
	if retryBackoff(10) != retryBackoffMax {
		t.Fatalf("expected capped backoff")
	}

	if isRetryableRequestErr(nil) {
		t.Fatal("nil should not retryable")
	}
	if !isRetryableRequestErr(io.EOF) {
		t.Fatal("EOF should retryable")
	}
	nerr := &net.DNSError{IsTimeout: true}
	if !isRetryableRequestErr(nerr) {
		t.Fatal("timeout net error should retryable")
	}
	if !isRetryableRequestErr(errors.New("connection refused")) {
		t.Fatal("message-based retry expected")
	}
	if isRetryableRequestErr(errors.New("permission denied")) {
		t.Fatal("non-retry message should be false")
	}

	for _, code := range []int{408, 425, 429, 500, 502, 503, 504} {
		err := &httpStatusError{statusCode: code, status: "x", body: "y"}
		if !isRetryableRequestErr(err) {
			t.Fatalf("status %d should retryable", code)
		}
	}
	if isRetryableRequestErr(&httpStatusError{statusCode: 400, status: "400", body: "bad"}) {
		t.Fatal("400 should not retryable")
	}
}

func TestCloneRequest(t *testing.T) {
	if _, err := cloneRequest(nil); err == nil {
		t.Fatal("expected nil request error")
	}
	req, _ := http.NewRequest(http.MethodPost, "http://x", bytes.NewReader([]byte("abc")))
	cloned, err := cloneRequest(req)
	if err != nil {
		t.Fatalf("cloneRequest error: %v", err)
	}
	b, _ := io.ReadAll(cloned.Body)
	if string(b) != "abc" {
		t.Fatalf("cloned body=%q", string(b))
	}

	req2, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	req2.GetBody = func() (io.ReadCloser, error) { return nil, errors.New("boom") }
	if _, err := cloneRequest(req2); err == nil {
		t.Fatal("expected getBody error")
	}
}

func TestDoJSONWithRetry(t *testing.T) {
	var tries atomic.Int32
	api := &API{
		baseURL: "http://x",
		http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			n := tries.Add(1)
			if n == 1 {
				return nil, io.EOF
			}
			return newResp(200, `{"ok":1}`), nil
		})},
	}
	ctx := context.Background()
	out := map[string]any{}
	err := api.doJSONWithRetry(ctx, 2, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://x/p", nil)
	}, &out)
	if err != nil {
		t.Fatalf("doJSONWithRetry error: %v", err)
	}
	if tries.Load() != 2 {
		t.Fatalf("tries=%d want=2", tries.Load())
	}

}

func TestAPIEndpointsAndTrace(t *testing.T) {
	var gotAuth []string
	var gotGenerateContentType string
	var gotGenerateBody string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			gotGenerateContentType = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			gotGenerateBody = string(b)
			_, _ = io.WriteString(w, `{"job_id":"j1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/j1/result":
			_, _ = io.WriteString(w, `{"en_markdown":"en","cn_markdown":"cn"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	api := New(ts.URL)
	if api.baseURL != ts.URL {
		t.Fatalf("baseURL=%q", api.baseURL)
	}
	var traces []TraceEvent
	api.SetTrace(func(ev TraceEvent) { traces = append(traces, ev) })

	ex, err := api.Exchange(context.Background(), "sk")
	if err != nil || ex.AccessToken != "at" || ex.TenantID != "demo" {
		t.Fatalf("Exchange got=%+v err=%v", ex, err)
	}

	genResp, err := api.Generate(context.Background(), "at", GenerateReq{InputMarkdown: "abc", CandidateCount: 1})
	if err != nil || genResp.JobID != "j1" {
		t.Fatalf("Generate got=%+v err=%v", genResp, err)
	}
	if gotGenerateContentType != "application/json" {
		t.Fatalf("content-type=%q", gotGenerateContentType)
	}
	var genReq GenerateReq
	if err := json.Unmarshal([]byte(gotGenerateBody), &genReq); err != nil || genReq.InputMarkdown != "abc" {
		t.Fatalf("generate body invalid: %s err=%v", gotGenerateBody, err)
	}

	res, err := api.Result(context.Background(), "at", "j1")
	if err != nil || res.ENMarkdown != "en" || res.CNMarkdown != "cn" {
		t.Fatalf("Result got=%+v err=%v", res, err)
	}

	for _, h := range gotAuth {
		if !strings.HasPrefix(h, "Bearer ") {
			t.Fatalf("auth header invalid: %q", h)
		}
	}
	if len(traces) == 0 {
		t.Fatal("expected trace events")
	}
}

func TestJobEvents(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/j1/events":
			if got := r.Header.Get("Authorization"); got != "Bearer at" {
				t.Fatalf("authorization=%q", got)
			}
			if got := r.Header.Get("Accept"); got != "text/event-stream" {
				t.Fatalf("accept=%q", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer does not support flushing")
			}
			_, _ = io.WriteString(w, ": ping\n\n")
			_, _ = io.WriteString(w, "event: trace\n")
			_, _ = io.WriteString(w, "id: 1\n")
			_, _ = io.WriteString(w, "data: {\"job_id\":\"j1\",\"tenant_id\":\"demo\",\"offset\":1,\"item\":{\"ts\":\"2026-03-12T00:00:00Z\",\"source\":\"generation\",\"event\":\"rules_loaded\",\"tenant_id\":\"demo\",\"job_id\":\"j1\",\"elapsed_ms\":10,\"payload\":{\"rules_version\":\"v1\"}}}\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "event: status\n")
			_, _ = io.WriteString(w, "data: {\"job_id\":\"j1\",\"tenant_id\":\"demo\",\"status\":\"retrying\",\"updated_at\":\"2026-03-12T00:00:01Z\"}\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "event: status\n")
			_, _ = io.WriteString(w, "data: {\"job_id\":\"j1\",\"tenant_id\":\"demo\",\"status\":\"succeeded\",\"updated_at\":\"2026-03-12T00:00:02Z\"}\n\n")
			flusher.Flush()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	api := New(ts.URL)
	var events []JobEvent
	status, err := api.JobEvents(context.Background(), "at", "j1", func(ev JobEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("JobEvents error: %v", err)
	}
	if status.JobID != "j1" || status.Status != "succeeded" {
		t.Fatalf("status=%+v", status)
	}
	if len(events) != 3 {
		t.Fatalf("events=%d want=3", len(events))
	}
	if events[0].Type != "trace" || events[0].Trace == nil || events[0].Trace.Item.Event != "rules_loaded" {
		t.Fatalf("first event=%+v", events[0])
	}
	if events[1].Type != "status" || events[1].Status == nil || events[1].Status.Status != "retrying" {
		t.Fatalf("second event=%+v", events[1])
	}
	if events[2].Type != "status" || events[2].Status == nil || events[2].Status.Status != "succeeded" {
		t.Fatalf("third event=%+v", events[2])
	}
}

func TestJobEventsReconnectsFromLastOffset(t *testing.T) {
	var requestCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/jr/events":
			n := requestCount.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer does not support flushing")
			}
			switch n {
			case 1:
				if got := r.URL.Query().Get("offset"); got != "" {
					t.Fatalf("first offset=%q want empty", got)
				}
				_, _ = io.WriteString(w, "event: trace\n")
				_, _ = io.WriteString(w, "id: 1\n")
				_, _ = io.WriteString(w, "data: {\"job_id\":\"jr\",\"tenant_id\":\"demo\",\"offset\":1,\"item\":{\"ts\":\"2026-03-12T00:00:00Z\",\"source\":\"generation\",\"event\":\"generate_queued\",\"tenant_id\":\"demo\",\"job_id\":\"jr\",\"elapsed_ms\":0,\"payload\":{}}}\n\n")
				flusher.Flush()
				return
			case 2:
				if got := r.URL.Query().Get("offset"); got != "1" {
					t.Fatalf("second offset=%q want 1", got)
				}
				_, _ = io.WriteString(w, "event: trace\n")
				_, _ = io.WriteString(w, "id: 2\n")
				_, _ = io.WriteString(w, "data: {\"job_id\":\"jr\",\"tenant_id\":\"demo\",\"offset\":2,\"item\":{\"ts\":\"2026-03-12T00:00:01Z\",\"source\":\"generation\",\"event\":\"rules_loaded\",\"tenant_id\":\"demo\",\"job_id\":\"jr\",\"elapsed_ms\":10,\"payload\":{\"rules_version\":\"v1\"}}}\n\n")
				flusher.Flush()
				_, _ = io.WriteString(w, "event: status\n")
				_, _ = io.WriteString(w, "data: {\"job_id\":\"jr\",\"tenant_id\":\"demo\",\"status\":\"succeeded\",\"updated_at\":\"2026-03-12T00:00:02Z\"}\n\n")
				flusher.Flush()
				return
			default:
				t.Fatalf("unexpected request count=%d", n)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	api := New(ts.URL)
	var events []JobEvent
	status, err := api.JobEvents(context.Background(), "at", "jr", func(ev JobEvent) {
		events = append(events, ev)
	})
	if err != nil {
		t.Fatalf("JobEvents error: %v", err)
	}
	if status.JobID != "jr" || status.Status != "succeeded" {
		t.Fatalf("status=%+v", status)
	}
	if requestCount.Load() != 2 {
		t.Fatalf("requestCount=%d want=2", requestCount.Load())
	}
	if len(events) != 3 {
		t.Fatalf("events=%d want=3", len(events))
	}
	if events[0].Type != "trace" || events[0].Trace == nil || events[0].Trace.Offset != 1 {
		t.Fatalf("first event=%+v", events[0])
	}
	if events[1].Type != "trace" || events[1].Trace == nil || events[1].Trace.Offset != 2 {
		t.Fatalf("second event=%+v", events[1])
	}
	if events[2].Type != "status" || events[2].Status == nil || events[2].Status.Status != "succeeded" {
		t.Fatalf("third event=%+v", events[2])
	}
}

func TestDoJSONOnceAndDownloadOnceErrorPaths(t *testing.T) {
	api := &API{http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/timeout" {
			return nil, &net.DNSError{IsTimeout: true}
		}
		if req.URL.Path == "/badjson" {
			return newResp(200, "{bad json"), nil
		}
		if req.URL.Path == "/status" {
			return newResp(500, "oops"), nil
		}
		if req.URL.Path == "/bin" {
			return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(bytes.NewReader([]byte{0xff, 0x00})), Header: make(http.Header)}, nil
		}
		return newResp(200, `{}`), nil
	})}}

	ctx := context.Background()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/timeout", nil)
	if err := api.doJSONOnce(req, nil); err == nil {
		t.Fatal("expected timeout error")
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://x/badjson", nil)
	var out map[string]any
	if err := api.doJSONOnce(req, &out); err == nil || !strings.Contains(err.Error(), "解析响应失败") {
		t.Fatalf("expected json error, got %v", err)
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://x/status", nil)
	err := api.doJSONOnce(req, nil)
	var hs *httpStatusError
	if !errors.As(err, &hs) || hs.statusCode != 500 {
		t.Fatalf("expected httpStatusError 500, got %v", err)
	}
	if !strings.Contains(hs.Error(), "oops") {
		t.Fatalf("unexpected status error text: %v", hs.Error())
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://x/bin", nil)
	if _, _, err := api.downloadOnce(req); err != nil {
		t.Fatalf("downloadOnce binary should pass, err=%v", err)
	}
}

func TestContextCanceledInRetry(t *testing.T) {
	api := &API{http: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, io.EOF
	})}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := api.doJSONWithRetry(ctx, 3, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://x", nil)
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("canceled context should return quickly")
	}
}
