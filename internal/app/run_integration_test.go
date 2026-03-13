package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"syl-listing-pro/internal/rules"
)

type signedRulesArtifact struct {
	ArchiveBytes              []byte
	ManifestSHA               string
	ArchiveSignatureBase64    string
	SigningPublicKeyPathInTar string
	SigningPublicKeySigBase64 string
}

func repoRootPrivateKeyPath() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	// this file: <repo>/cli/internal/app/run_integration_test.go
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
	return filepath.Join(repoRoot, "rules", "keys", "rules_private.pem")
}

func mustSignSHA256(t *testing.T, priv *rsa.PrivateKey, payload []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(payload)
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	return sig
}

func mustLoadRSAPrivateKey(t *testing.T, path string) *rsa.PrivateKey {
	t.Helper()
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read private key failed: %v", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("invalid private key pem")
	}
	switch block.Type {
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			t.Fatalf("parse pkcs8 failed: %v", err)
		}
		priv, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			t.Fatalf("private key is not RSA: %T", parsed)
		}
		return priv
	case "RSA PRIVATE KEY":
		priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			t.Fatalf("parse pkcs1 failed: %v", err)
		}
		return priv
	default:
		t.Fatalf("unsupported private key type: %s", block.Type)
		return nil
	}
}

func mustRSAPublicKeyPEM(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func makeSignedRulesArtifact(t *testing.T, marker string) signedRulesArtifact {
	t.Helper()
	rootPriv := repoRootPrivateKeyPath()
	if _, err := os.Stat(rootPriv); err != nil {
		t.Skipf("未找到 rules 私钥: %s", rootPriv)
	}
	rootPrivateKey := mustLoadRSAPrivateKey(t, rootPriv)

	work := t.TempDir()
	signingPrivateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key failed: %v", err)
	}
	signPubBytes := mustRSAPublicKeyPEM(t, &signingPrivateKey.PublicKey)

	archiveBuf := &bytes.Buffer{}
	gz := gzip.NewWriter(archiveBuf)
	tw := tar.NewWriter(gz)
	inputContent := "file_discovery:\n  marker: \"" + marker + "\"\n"
	entries := map[string][]byte{
		"tenant/rules/input.yaml":        []byte(inputContent),
		"tenant/keys/signing_public.pem": signPubBytes,
	}
	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archiveBytes := archiveBuf.Bytes()
	archivePath := filepath.Join(work, "rules.tar.gz")
	if err := os.WriteFile(archivePath, archiveBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	archiveSig := mustSignSHA256(t, signingPrivateKey, archiveBytes)
	keySig := mustSignSHA256(t, rootPrivateKey, signPubBytes)

	sum := sha256.Sum256(archiveBytes)
	return signedRulesArtifact{
		ArchiveBytes:              archiveBytes,
		ManifestSHA:               hex.EncodeToString(sum[:]),
		ArchiveSignatureBase64:    base64.StdEncoding.EncodeToString(archiveSig),
		SigningPublicKeyPathInTar: "tenant/keys/signing_public.pem",
		SigningPublicKeySigBase64: base64.StdEncoding.EncodeToString(keySig),
	}
}

func makeRulesArchiveBytes(t *testing.T, marker string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	content := "file_discovery:\n  marker: \"" + marker + "\"\n"
	hdr := &tar.Header{Name: "tenant/rules/input.yaml", Mode: 0o644, Size: int64(len(content))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

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

func TestRunGen_SuccessWithCachedRules(t *testing.T) {
	stubDocxConverter(t)
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes := makeRulesArchiveBytes(t, "#MARK")
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", archiveBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1/events":
			writeSSETrace(t, w, 1, `{"job_id":"job_1","tenant_id":"demo","offset":1,"item":{"source":"generation","event":"generate_queued","tenant_id":"demo","job_id":"job_1","elapsed_ms":0,"payload":{}}}`)
			writeSSETrace(t, w, 2, `{"job_id":"job_1","tenant_id":"demo","offset":2,"item":{"source":"generation","event":"rules_loaded","tenant_id":"demo","job_id":"job_1","elapsed_ms":1,"payload":{"rules_version":"v1"}}}`)
			writeSSEEvent(t, w, "status", `{"job_id":"job_1","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-12T00:00:02Z"}`)
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

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("#MARK\ninput content"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	if err := RunGen(context.Background(), GenOptions{OutputDir: outDir, Inputs: []string{inputPath}}); err != nil {
		t.Fatalf("RunGen error: %v", err)
	}

	ents, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("expected 2 output files, got %d", len(ents))
	}
}

func TestRunGen_SuccessWithSSEEvents(t *testing.T) {
	stubDocxConverter(t)
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes := makeRulesArchiveBytes(t, "#MARK")
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", archiveBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}

	var eventRequested bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_sse","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_sse/events":
			eventRequested = true
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatal("response writer does not support flushing")
			}
			_, _ = io.WriteString(w, "event: trace\n")
			_, _ = io.WriteString(w, "id: 1\n")
			_, _ = io.WriteString(w, "data: {\"job_id\":\"job_sse\",\"tenant_id\":\"demo\",\"offset\":1,\"item\":{\"source\":\"generation\",\"event\":\"generate_queued\",\"tenant_id\":\"demo\",\"job_id\":\"job_sse\",\"elapsed_ms\":0,\"payload\":{}}}\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "event: trace\n")
			_, _ = io.WriteString(w, "id: 2\n")
			_, _ = io.WriteString(w, "data: {\"job_id\":\"job_sse\",\"tenant_id\":\"demo\",\"offset\":2,\"item\":{\"source\":\"generation\",\"event\":\"rules_loaded\",\"tenant_id\":\"demo\",\"job_id\":\"job_sse\",\"elapsed_ms\":1,\"payload\":{\"rules_version\":\"v1\"}}}\n\n")
			flusher.Flush()
			_, _ = io.WriteString(w, "event: status\n")
			_, _ = io.WriteString(w, "data: {\"job_id\":\"job_sse\",\"tenant_id\":\"demo\",\"status\":\"succeeded\",\"updated_at\":\"2026-03-12T00:00:02Z\"}\n\n")
			flusher.Flush()
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_sse/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		case r.Method == http.MethodGet && (r.URL.Path == "/v1/jobs/job_sse" || r.URL.Path == "/v1/jobs/job_sse/trace"):
			t.Fatalf("unexpected legacy status request: %s %s", r.Method, r.URL.Path)
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

	outDir := t.TempDir()
	inputPath := filepath.Join(t.TempDir(), "sample.md")
	if err := os.WriteFile(inputPath, []byte("#MARK\n\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdoutRun(t, func() error {
		return RunGen(context.Background(), GenOptions{
			Inputs:    []string{inputPath},
			OutputDir: outDir,
			Num:       1,
		})
	})
	if err != nil {
		t.Fatalf("Run error: %v\nout=%s", err, out)
	}
	if !eventRequested {
		t.Fatal("expected CLI to connect /events SSE endpoint")
	}
	if !strings.Contains(out, "规则已加载 v1") {
		t.Fatalf("stdout missing SSE trace output: %s", out)
	}
}

func TestRunGen_ReconnectsSSEStreamFromLastOffset(t *testing.T) {
	stubDocxConverter(t)
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes := makeRulesArchiveBytes(t, "#MARK")
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", archiveBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}

	var eventsRequestCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_resume","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_resume/events":
			eventsRequestCount++
			switch eventsRequestCount {
			case 1:
				if got := r.URL.Query().Get("offset"); got != "" {
					t.Fatalf("first offset=%q want empty", got)
				}
				writeSSETrace(t, w, 1, `{"job_id":"job_resume","tenant_id":"demo","offset":1,"item":{"source":"generation","event":"generate_queued","tenant_id":"demo","job_id":"job_resume","elapsed_ms":0,"payload":{}}}`)
				return
			case 2:
				if got := r.URL.Query().Get("offset"); got != "1" {
					t.Fatalf("second offset=%q want 1", got)
				}
				writeSSETrace(t, w, 2, `{"job_id":"job_resume","tenant_id":"demo","offset":2,"item":{"source":"generation","event":"rules_loaded","tenant_id":"demo","job_id":"job_resume","elapsed_ms":1,"payload":{"rules_version":"v1"}}}`)
				writeSSEEvent(t, w, "status", `{"job_id":"job_resume","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-12T00:00:02Z"}`)
				return
			default:
				t.Fatalf("unexpected events request count=%d", eventsRequestCount)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_resume/result":
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

	outDir := t.TempDir()
	inputPath := filepath.Join(t.TempDir(), "resume.md")
	if err := os.WriteFile(inputPath, []byte("#MARK\n\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdoutRun(t, func() error {
		return RunGen(context.Background(), GenOptions{
			Inputs:    []string{inputPath},
			OutputDir: outDir,
			Num:       1,
		})
	})
	if err != nil {
		t.Fatalf("Run error: %v\nout=%s", err, out)
	}
	if eventsRequestCount != 2 {
		t.Fatalf("eventsRequestCount=%d want=2", eventsRequestCount)
	}
	if strings.Contains(out, "过程流式接收失败") {
		t.Fatalf("stdout should not contain stream failure warning: %s", out)
	}
	if !strings.Contains(out, "规则已加载 v1") {
		t.Fatalf("stdout missing resumed SSE trace output: %s", out)
	}
}

func TestRunGen_RetryingStatusContinuesUntilSuccess(t *testing.T) {
	stubDocxConverter(t)
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes := makeRulesArchiveBytes(t, "#MARK")
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", archiveBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_retry","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_retry/events":
			writeSSETrace(t, w, 1, `{"job_id":"job_retry","tenant_id":"demo","offset":1,"item":{"source":"generation","event":"generate_queued","tenant_id":"demo","job_id":"job_retry","elapsed_ms":0,"payload":{}}}`)
			writeSSEEvent(t, w, "status", `{"job_id":"job_retry","tenant_id":"demo","status":"retrying","updated_at":"2026-03-12T00:00:01Z"}`)
			writeSSETrace(t, w, 2, `{"job_id":"job_retry","tenant_id":"demo","offset":2,"item":{"source":"runner","event":"job_retry_scheduled","tenant_id":"demo","job_id":"job_retry","elapsed_ms":1000,"payload":{"attempt":1,"max_attempts":3,"next_attempt":2,"error":"temporary upstream timeout"}}}`)
			writeSSEEvent(t, w, "status", `{"job_id":"job_retry","tenant_id":"demo","status":"succeeded","updated_at":"2026-03-12T00:00:05Z"}`)
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
	streamTimeoutSecond = 5
	defer func() {
		workerBaseURL = oldBase
		streamTimeoutSecond = oldTimeout
	}()

	outDir := t.TempDir()
	inputPath := filepath.Join(t.TempDir(), "retry.md")
	if err := os.WriteFile(inputPath, []byte("#MARK\n\nhello"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdoutRun(t, func() error {
		return RunGen(context.Background(), GenOptions{
			Inputs:    []string{inputPath},
			OutputDir: outDir,
			Num:       1,
		})
	})
	if err != nil {
		t.Fatalf("Run error: %v\nout=%s", err, out)
	}
	if !strings.Contains(out, "任务重试计划：第 1/3 次失败，准备第 2 次") {
		t.Fatalf("stdout missing retry trace output: %s", out)
	}
	if !strings.Contains(out, "任务完成：成功 1，失败 0") {
		t.Fatalf("stdout missing success summary: %s", out)
	}
}

func TestRunUpdateRules_UpToDate(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", makeRulesArchiveBytes(t, "#MARK"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	workerBaseURL = ts.URL
	defer func() { workerBaseURL = oldBase }()

	out, err := captureStdoutRun(t, func() error {
		return RunUpdateRules(context.Background(), UpdateRulesOptions{})
	})
	if err != nil {
		t.Fatalf("RunUpdateRules error: %v", err)
	}
	if !strings.Contains(out, "v1") {
		t.Fatalf("expected rules version output, got: %q", out)
	}
}

func TestRunGen_CallsDocxConverterWithoutHighlightWords(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", makeRulesArchiveBytes(t, "#MARK"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}

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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
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
	if err := os.WriteFile(inputPath, []byte("#MARK\ninput content"), 0o644); err != nil {
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
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", makeRulesArchiveBytes(t, "#MARK"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 子用例1：generate 直接失败（400 非重试）
	t.Run("generate_failed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
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

	// 子用例2：job 事件流返回 failed
	t.Run("job_failed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
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
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", makeRulesArchiveBytes(t, "#MARK"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
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

	err = RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{inputPath}})
	if err == nil || !strings.Contains(err.Error(), "存在失败任务") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestRunUpdateRules_ErrorBranches(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	// resolve 失败 + 本地有缓存 => 回退成功
	t.Run("resolve_error_with_cache", func(t *testing.T) {
		cacheDir, err := rules.DefaultCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		archivePath, err := rules.SaveArchive(cacheDir, "demo", "v1", makeRulesArchiveBytes(t, "#MARK"))
		if err != nil {
			t.Fatal(err)
		}
		if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "sha", ArchivePath: archivePath}); err != nil {
			t.Fatal(err)
		}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "bad")
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		oldBase := workerBaseURL
		workerBaseURL = ts.URL
		defer func() { workerBaseURL = oldBase }()

		if err := RunUpdateRules(context.Background(), UpdateRulesOptions{}); err != nil {
			t.Fatalf("RunUpdateRules fallback error: %v", err)
		}
	})

	// resolve 失败 + 本地无缓存 => 报错
	t.Run("resolve_error_no_cache", func(t *testing.T) {
		cacheDir, err := rules.DefaultCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		_ = rules.Clear(cacheDir, "demo")

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "bad")
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		oldBase := workerBaseURL
		workerBaseURL = ts.URL
		defer func() { workerBaseURL = oldBase }()

		err = RunUpdateRules(context.Background(), UpdateRulesOptions{})
		if err == nil || !strings.Contains(err.Error(), "本地无规则缓存") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	// 非 up_to_date + 下载后 SHA 不匹配 => 报错
	t.Run("download_sha_mismatch", func(t *testing.T) {
		var baseURL string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":false,"rules_version":"v2","manifest_sha256":"%s","download_url":"%s/download"}`, strings.Repeat("0", 64), baseURL))
			case r.Method == http.MethodGet && r.URL.Path == "/download":
				_, _ = io.WriteString(w, "not-the-same-sha")
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		baseURL = ts.URL

		oldBase := workerBaseURL
		workerBaseURL = ts.URL
		defer func() { workerBaseURL = oldBase }()

		err := RunUpdateRules(context.Background(), UpdateRulesOptions{Force: true})
		if err == nil || !strings.Contains(err.Error(), "sha256 不匹配") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestRunUpdateRules_DownloadAndVerifySuccess(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	artifact := makeSignedRulesArtifact(t, "#MARK")
	var baseURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":false,"rules_version":"v2","manifest_sha256":"%s","download_url":"%s/download","signature_base64":"%s","signing_public_key_path_in_archive":"%s","signing_public_key_signature_base64":"%s"}`, artifact.ManifestSHA, baseURL, artifact.ArchiveSignatureBase64, artifact.SigningPublicKeyPathInTar, artifact.SigningPublicKeySigBase64))
		case r.Method == http.MethodGet && r.URL.Path == "/download":
			_, _ = w.Write(artifact.ArchiveBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	baseURL = ts.URL

	oldBase := workerBaseURL
	workerBaseURL = ts.URL
	defer func() { workerBaseURL = oldBase }()

	out, err := captureStdoutRun(t, func() error {
		return RunUpdateRules(context.Background(), UpdateRulesOptions{Force: true})
	})
	if err != nil {
		t.Fatalf("RunUpdateRules error: %v", err)
	}
	if !strings.Contains(out, "v2") {
		t.Fatalf("expected version output, got: %q", out)
	}

	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	st, err := rules.LoadState(cacheDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if st.RulesVersion != "v2" || !rules.HasArchive(st.ArchivePath) {
		t.Fatalf("state not updated: %+v", st)
	}
}

func TestRunGen_FirstRunDownloadRulesThenGenerateFail(t *testing.T) {
	stubDocxConverter(t)
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	artifact := makeSignedRulesArtifact(t, "#MARK")
	var baseURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":false,"rules_version":"v3","manifest_sha256":"%s","download_url":"%s/download","signature_base64":"%s","signing_public_key_path_in_archive":"%s","signing_public_key_signature_base64":"%s"}`, artifact.ManifestSHA, baseURL, artifact.ArchiveSignatureBase64, artifact.SigningPublicKeyPathInTar, artifact.SigningPublicKeySigBase64))
		case r.Method == http.MethodGet && r.URL.Path == "/download":
			_, _ = w.Write(artifact.ArchiveBytes)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "bad")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	baseURL = ts.URL

	oldBase := workerBaseURL
	workerBaseURL = ts.URL
	defer func() { workerBaseURL = oldBase }()

	inputPath := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(inputPath, []byte("#MARK\ncontent"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := RunGen(context.Background(), GenOptions{OutputDir: t.TempDir(), Inputs: []string{inputPath}})
	if err == nil || !strings.Contains(err.Error(), "存在失败任务") {
		t.Fatalf("unexpected err: %v", err)
	}
}
