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

	var traceOnce bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
			_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
			_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
			_, _ = io.WriteString(w, `{"job_id":"job_1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1/trace":
			if !traceOnce {
				traceOnce = true
				_, _ = io.WriteString(w, `{"ok":true,"job_id":"job_1","job_status":"running","tenant_id":"demo","trace_count":3,"limit":300,"offset":0,"next_offset":3,"has_more":false,"items":[{"source":"engine","event":"generate_queued","tenant_id":"demo","job_id":"job_1","elapsed_ms":0,"payload":{}},{"source":"engine","event":"rules_loaded","tenant_id":"demo","job_id":"job_1","elapsed_ms":1,"payload":{"rules_version":"v1"}},{"source":"engine","event":"generation_ok","tenant_id":"demo","job_id":"job_1","elapsed_ms":2,"payload":{"timing_ms":2}}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"job_id":"job_1","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":3,"next_offset":3,"has_more":false,"items":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1":
			_, _ = io.WriteString(w, `{"job_id":"job_1","status":"succeeded"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_1/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldPollMs := pollIntervalMs
	oldPollTimeout := pollTimeoutSecond
	workerBaseURL = ts.URL
	pollIntervalMs = 1
	pollTimeoutSecond = 5
	defer func() {
		workerBaseURL = oldBase
		pollIntervalMs = oldPollMs
		pollTimeoutSecond = oldPollTimeout
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

func TestRunGen_PassesHighlightWordsToDocxConverter(t *testing.T) {
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

	type call struct {
		mdPath string
		words  []string
	}
	var calls []call
	oldConvert := convertMarkdownToDocxFunc
	convertMarkdownToDocxFunc = func(_ context.Context, markdownPath string, outputPath string, highlightWords []string) (string, error) {
		calls = append(calls, call{mdPath: markdownPath, words: append([]string(nil), highlightWords...)})
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_meta/trace":
			_, _ = io.WriteString(w, `{"ok":true,"job_id":"job_meta","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":0,"next_offset":0,"has_more":false,"items":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_meta":
			_, _ = io.WriteString(w, `{"job_id":"job_meta","status":"succeeded"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_meta/result":
			_, _ = io.WriteString(w, `{"en_markdown":"# EN","cn_markdown":"# CN","meta":{"highlight_words_en":["A","B"],"highlight_words_cn":["甲","乙"]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	oldBase := workerBaseURL
	oldPoll := pollIntervalMs
	workerBaseURL = ts.URL
	pollIntervalMs = 1
	defer func() {
		workerBaseURL = oldBase
		pollIntervalMs = oldPoll
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
	if strings.HasSuffix(calls[0].mdPath, "_en.md") {
		if got := strings.Join(calls[0].words, ","); got != "A,B" {
			t.Fatalf("en words=%s", got)
		}
		if got := strings.Join(calls[1].words, ","); got != "甲,乙" {
			t.Fatalf("cn words=%s", got)
		}
	} else {
		if got := strings.Join(calls[0].words, ","); got != "甲,乙" {
			t.Fatalf("cn words=%s", got)
		}
		if got := strings.Join(calls[1].words, ","); got != "A,B" {
			t.Fatalf("en words=%s", got)
		}
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

	// 子用例2：job 轮询返回 failed
	t.Run("job_failed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
				_, _ = io.WriteString(w, `{"job_id":"job_failed","status":"queued"}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_failed/trace":
				_, _ = io.WriteString(w, `{"ok":true,"job_id":"job_failed","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":0,"next_offset":0,"has_more":false,"items":[]}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_failed":
				_, _ = io.WriteString(w, `{"job_id":"job_failed","status":"failed","error":"engine failed"}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		oldBase := workerBaseURL
		oldPoll := pollIntervalMs
		workerBaseURL = ts.URL
		pollIntervalMs = 1
		defer func() {
			workerBaseURL = oldBase
			pollIntervalMs = oldPoll
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
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_timeout/trace":
			_, _ = io.WriteString(w, `{"ok":true,"job_id":"job_timeout","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":0,"next_offset":0,"has_more":false,"items":[]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job_timeout":
			_, _ = io.WriteString(w, `{"job_id":"job_timeout","status":"running"}`)
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
	pollTimeoutSecond = 1
	defer func() {
		workerBaseURL = oldBase
		pollIntervalMs = oldPoll
		pollTimeoutSecond = oldTimeout
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
