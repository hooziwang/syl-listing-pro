package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

func New(baseURL string) *API {
	return &API{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Transport: &http.Transport{
				Proxy: nil,
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/auth/exchange", nil)
	if err != nil {
		return ExchangeResp{}, err
	}
	req.Header.Set("Authorization", "Bearer "+sylKey)
	var out ExchangeResp
	if err := a.doJSON(req, &out); err != nil {
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
	if err := a.doJSON(req, &out); err != nil {
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
	if err := a.doJSON(req, &out); err != nil {
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
	if err := a.doJSON(req, &out); err != nil {
		return JobStatusResp{}, err
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
	if err := a.doJSON(req, &out); err != nil {
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
		return nil, "", fmt.Errorf("下载失败: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	h := sha256.Sum256(body)
	return body, hex.EncodeToString(h[:]), nil
}

func (a *API) doJSON(req *http.Request, out any) error {
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
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}
	return nil
}
