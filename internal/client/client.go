package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode/utf8"
)

type TraceEvent struct {
	Stage      string
	Method     string
	URL        string
	StatusCode int
	DurationMs int64
	Request    string
	Response   string
	Error      string
}

type API struct {
	baseURL string
	http    *http.Client
	trace   func(TraceEvent)
}

const (
	connectTimeout        = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
	expectContinueTimeout = 1 * time.Second
	keepAliveTimeout      = 30 * time.Second
	idleConnTimeout       = 90 * time.Second
	maxIdleConns          = 100
	maxIdleConnsPerHost   = 10
	retryBackoffBase      = 300 * time.Millisecond
	retryBackoffMax       = 4 * time.Second
	defaultMaxAttempts    = 3
	exchangeMaxAttempts   = 5
	generateMaxAttempts   = 3
	jobPollMaxAttempts    = 5
)

type httpStatusError struct {
	statusCode int
	status     string
	body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s: %s", e.status, strings.TrimSpace(e.body))
}

func New(baseURL string) *API {
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: keepAliveTimeout,
	}
	return &API{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   tlsHandshakeTimeout,
				ResponseHeaderTimeout: responseHeaderTimeout,
				ExpectContinueTimeout: expectContinueTimeout,
				IdleConnTimeout:       idleConnTimeout,
				MaxIdleConns:          maxIdleConns,
				MaxIdleConnsPerHost:   maxIdleConnsPerHost,
			},
			Timeout: 120 * time.Second,
		},
	}
}

func (a *API) SetTrace(fn func(TraceEvent)) {
	a.trace = fn
}

func (a *API) emitTrace(ev TraceEvent) {
	if a.trace != nil {
		a.trace(ev)
	}
}

func readReqBody(req *http.Request) string {
	if req == nil || req.GetBody == nil {
		return ""
	}
	rc, err := req.GetBody()
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 2<<20))
	if err != nil {
		return ""
	}
	return string(b)
}

func traceBody(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("<binary bytes=%d sha256=%s>", len(b), hex.EncodeToString(sum[:]))
}

func (a *API) Exchange(ctx context.Context, sylKey string) (ExchangeResp, error) {
	var out ExchangeResp
	err := a.doJSONWithRetry(ctx, exchangeMaxAttempts, func() (*http.Request, error) {
		url := a.baseURL + "/v1/auth/exchange"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+sylKey)
		return req, nil
	}, &out)
	if err != nil {
		return ExchangeResp{}, err
	}
	return out, nil
}

func (a *API) ResolveRules(ctx context.Context, token, current string) (RulesResolveResp, error) {
	u, err := url.Parse(a.baseURL)
	if err != nil {
		return RulesResolveResp{}, err
	}
	u.Path = path.Join(u.Path, "/v1/rules/resolve")
	q := u.Query()
	if current != "" {
		q.Set("current", current)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return RulesResolveResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var out RulesResolveResp
	if err := a.doJSONWithRetry(ctx, defaultMaxAttempts, func() (*http.Request, error) {
		return cloneRequest(req)
	}, &out); err != nil {
		return RulesResolveResp{}, err
	}
	return out, nil
}

func (a *API) Generate(ctx context.Context, token string, in GenerateReq) (GenerateResp, error) {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/generate", bytes.NewReader(b))
	if err != nil {
		return GenerateResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	var out GenerateResp
	if err := a.doJSONWithRetry(ctx, generateMaxAttempts, func() (*http.Request, error) {
		return cloneRequest(req)
	}, &out); err != nil {
		return GenerateResp{}, err
	}
	return out, nil
}

func (a *API) Job(ctx context.Context, token, jobID string) (JobStatusResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/v1/jobs/"+jobID, nil)
	if err != nil {
		return JobStatusResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var out JobStatusResp
	if err := a.doJSONWithRetry(ctx, jobPollMaxAttempts, func() (*http.Request, error) {
		return cloneRequest(req)
	}, &out); err != nil {
		return JobStatusResp{}, err
	}
	return out, nil
}

func (a *API) JobTrace(ctx context.Context, token, jobID string, offset, limit int) (JobTraceResp, error) {
	u, err := url.Parse(a.baseURL)
	if err != nil {
		return JobTraceResp{}, err
	}
	u.Path = path.Join(u.Path, "/v1/jobs/"+jobID+"/trace")
	q := u.Query()
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return JobTraceResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var out JobTraceResp
	if err := a.doJSONWithRetry(ctx, jobPollMaxAttempts, func() (*http.Request, error) {
		return cloneRequest(req)
	}, &out); err != nil {
		return JobTraceResp{}, err
	}
	return out, nil
}

func (a *API) Result(ctx context.Context, token, jobID string) (ResultResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/v1/jobs/"+jobID+"/result", nil)
	if err != nil {
		return ResultResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var out ResultResp
	if err := a.doJSONWithRetry(ctx, jobPollMaxAttempts, func() (*http.Request, error) {
		return cloneRequest(req)
	}, &out); err != nil {
		return ResultResp{}, err
	}
	return out, nil
}

func (a *API) Download(ctx context.Context, token, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return a.downloadWithRetry(ctx, defaultMaxAttempts, func() (*http.Request, error) {
		return cloneRequest(req)
	})
}

func (a *API) downloadWithRetry(ctx context.Context, maxAttempts int, buildReq func() (*http.Request, error)) ([]byte, string, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := buildReq()
		if err != nil {
			return nil, "", err
		}
		body, sha, err := a.downloadOnce(req)
		if err == nil {
			return body, sha, nil
		}
		if !isRetryableRequestErr(err) || attempt >= maxAttempts {
			return nil, "", err
		}
		backoff := retryBackoff(attempt)
		a.emitTrace(TraceEvent{
			Stage:      "retry",
			Method:     req.Method,
			URL:        req.URL.String(),
			DurationMs: backoff.Milliseconds(),
			Error:      err.Error(),
			Request:    fmt.Sprintf(`{"attempt":%d,"next_attempt":%d}`, attempt, attempt+1),
		})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, "", ctx.Err()
		case <-timer.C:
		}
	}
	return nil, "", fmt.Errorf("download retry exhausted")
}

func (a *API) downloadOnce(req *http.Request) ([]byte, string, error) {
	reqBody := readReqBody(req)
	a.emitTrace(TraceEvent{
		Stage:   "request",
		Method:  req.Method,
		URL:     req.URL.String(),
		Request: reqBody,
	})
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.emitTrace(TraceEvent{
			Stage:      "error",
			Method:     req.Method,
			URL:        req.URL.String(),
			DurationMs: time.Since(start).Milliseconds(),
			Request:    reqBody,
			Error:      err.Error(),
		})
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	a.emitTrace(TraceEvent{
		Stage:      "response",
		Method:     req.Method,
		URL:        req.URL.String(),
		StatusCode: resp.StatusCode,
		DurationMs: time.Since(start).Milliseconds(),
		Request:    reqBody,
		Response:   traceBody(body),
	})
	if resp.StatusCode/100 != 2 {
		return nil, "", &httpStatusError{
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       string(body),
		}
	}
	h := sha256.Sum256(body)
	return body, hex.EncodeToString(h[:]), nil
}

func (a *API) doJSONWithRetry(ctx context.Context, maxAttempts int, buildReq func() (*http.Request, error), out any) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := buildReq()
		if err != nil {
			return err
		}
		err = a.doJSONOnce(req, out)
		if err == nil {
			return nil
		}
		if !isRetryableRequestErr(err) || attempt >= maxAttempts {
			return err
		}
		backoff := retryBackoff(attempt)
		a.emitTrace(TraceEvent{
			Stage:      "retry",
			Method:     req.Method,
			URL:        req.URL.String(),
			DurationMs: backoff.Milliseconds(),
			Error:      err.Error(),
			Request:    fmt.Sprintf(`{"attempt":%d,"next_attempt":%d}`, attempt, attempt+1),
		})
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("request retry exhausted")
}

func (a *API) doJSONOnce(req *http.Request, out any) error {
	reqBody := readReqBody(req)
	a.emitTrace(TraceEvent{
		Stage:   "request",
		Method:  req.Method,
		URL:     req.URL.String(),
		Request: reqBody,
	})
	start := time.Now()
	resp, err := a.http.Do(req)
	if err != nil {
		a.emitTrace(TraceEvent{
			Stage:      "error",
			Method:     req.Method,
			URL:        req.URL.String(),
			DurationMs: time.Since(start).Milliseconds(),
			Request:    reqBody,
			Error:      err.Error(),
		})
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	a.emitTrace(TraceEvent{
		Stage:      "response",
		Method:     req.Method,
		URL:        req.URL.String(),
		StatusCode: resp.StatusCode,
		DurationMs: time.Since(start).Milliseconds(),
		Request:    reqBody,
		Response:   traceBody(body),
	})
	if resp.StatusCode/100 != 2 {
		return &httpStatusError{
			statusCode: resp.StatusCode,
			status:     resp.Status,
			body:       string(body),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	return nil
}

func retryBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return retryBackoffBase
	}
	d := retryBackoffBase * time.Duration(1<<(attempt-1))
	if d > retryBackoffMax {
		return retryBackoffMax
	}
	return d
}

func isRetryableRequestErr(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.statusCode {
		case 408, 425, 429, 500, 502, 503, 504:
			return true
		default:
			return false
		}
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	retryable := []string{
		"timeout",
		"tls handshake",
		"connection reset",
		"connection refused",
		"broken pipe",
		"unexpected eof",
		"eof",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
	}
	for _, key := range retryable {
		if strings.Contains(msg, key) {
			return true
		}
	}
	return false
}

func cloneRequest(req *http.Request) (*http.Request, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}
	cloned := req.Clone(req.Context())
	if req.GetBody == nil {
		return cloned, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	cloned.Body = body
	return cloned, nil
}
