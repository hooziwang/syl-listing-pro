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

func TestDoJSONWithRetryAndDownloadWithRetry(t *testing.T) {
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

	tries.Store(0)
	api.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := tries.Add(1)
		if n == 1 {
			return newResp(503, "busy"), nil
		}
		return newResp(200, "bin"), nil
	})}
	b, sha, err := api.downloadWithRetry(ctx, 2, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, "http://x/d", nil)
	})
	if err != nil {
		t.Fatalf("downloadWithRetry error: %v", err)
	}
	if string(b) != "bin" || sha == "" {
		t.Fatalf("unexpected download result b=%q sha=%q", string(b), sha)
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			if r.URL.Query().Get("current") != "v1" {
				t.Fatalf("current query missing")
			}
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			gotAuth = append(gotAuth, r.Header.Get("Authorization"))
			gotGenerateContentType = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			gotGenerateBody = string(b)
			_, _ = io.WriteString(w, `{"job_id":"j1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/j1":
			_, _ = io.WriteString(w, `{"job_id":"j1","status":"succeeded"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/j1/trace":
			if r.URL.Query().Get("offset") != "1" || r.URL.Query().Get("limit") != "2" {
				t.Fatalf("offset/limit query missing")
			}
			_, _ = io.WriteString(w, `{"ok":true,"job_id":"j1","job_status":"running","tenant_id":"demo","trace_count":1,"limit":2,"offset":1,"next_offset":2,"has_more":false,"items":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/j1/result":
			_, _ = io.WriteString(w, `{"en_markdown":"en","cn_markdown":"cn"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/download":
			_, _ = w.Write([]byte{0xff, 0x01})
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

	rulesResp, err := api.ResolveRules(context.Background(), "at", "v1")
	if err != nil || !rulesResp.UpToDate {
		t.Fatalf("ResolveRules got=%+v err=%v", rulesResp, err)
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

	jobResp, err := api.Job(context.Background(), "at", "j1")
	if err != nil || jobResp.Status != "succeeded" {
		t.Fatalf("Job got=%+v err=%v", jobResp, err)
	}
	_, err = api.JobTrace(context.Background(), "at", "j1", 1, 2)
	if err != nil {
		t.Fatalf("JobTrace err=%v", err)
	}
	res, err := api.Result(context.Background(), "at", "j1")
	if err != nil || res.ENMarkdown != "en" || res.CNMarkdown != "cn" {
		t.Fatalf("Result got=%+v err=%v", res, err)
	}
	b, sha, err := api.Download(context.Background(), "at", ts.URL+"/download")
	if err != nil || len(b) != 2 || sha == "" {
		t.Fatalf("Download b=%v sha=%q err=%v", b, sha, err)
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
