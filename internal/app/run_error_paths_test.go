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
	"testing"

	"syl-listing-pro/internal/rules"
)

func TestRunGen_FirstRunRuleSyncFailureBranches(t *testing.T) {
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
	_ = rules.Clear(cacheDir, "demo")

	t.Run("download_error", func(t *testing.T) {
		var baseURL string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":false,"rules_version":"v1","manifest_sha256":"%s","download_url":"%s/download"}`, strings.Repeat("0", 64), baseURL))
			case r.Method == http.MethodGet && r.URL.Path == "/download":
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

		err := RunGen(context.Background(), GenOptions{Inputs: []string{}})
		if err == nil || !strings.Contains(err.Error(), "首次拉规则失败") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("sha_mismatch", func(t *testing.T) {
		var baseURL string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":false,"rules_version":"v1","manifest_sha256":"%s","download_url":"%s/download"}`, strings.Repeat("0", 64), baseURL))
			case r.Method == http.MethodGet && r.URL.Path == "/download":
				_, _ = io.WriteString(w, "abc")
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()
		baseURL = ts.URL

		oldBase := workerBaseURL
		workerBaseURL = ts.URL
		defer func() { workerBaseURL = oldBase }()

		err := RunGen(context.Background(), GenOptions{Inputs: []string{}})
		if err == nil || !strings.Contains(err.Error(), "首次拉规则 sha256 不匹配") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("signature_verify_fail", func(t *testing.T) {
		artifact := makeSignedRulesArtifact(t, "#MARK")
		badSig := artifact.ArchiveSignatureBase64[:len(artifact.ArchiveSignatureBase64)-8] + "AAAAAAAA"
		var baseURL string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":false,"rules_version":"v1","manifest_sha256":"%s","download_url":"%s/download","signature_base64":"%s","signing_public_key_path_in_archive":"%s","signing_public_key_signature_base64":"%s"}`,
					artifact.ManifestSHA, baseURL, badSig, artifact.SigningPublicKeyPathInTar, artifact.SigningPublicKeySigBase64))
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

		err := RunGen(context.Background(), GenOptions{Inputs: []string{}})
		if err == nil || !strings.Contains(err.Error(), "首次拉规则签名校验失败") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestRunGen_RuntimeFailureBranches(t *testing.T) {
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

	t.Run("result_read_fail", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
				_, _ = io.WriteString(w, `{"job_id":"job1","status":"queued"}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job1/trace":
				_, _ = io.WriteString(w, `{"ok":true,"job_id":"job1","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":0,"next_offset":0,"has_more":false,"items":[]}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job1":
				_, _ = io.WriteString(w, `{"job_id":"job1","status":"succeeded"}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job1/result":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "bad")
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

	t.Run("output_name_fail", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
				_, _ = io.WriteString(w, `{"job_id":"job2","status":"queued"}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job2/trace":
				_, _ = io.WriteString(w, `{"ok":true,"job_id":"job2","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":0,"next_offset":0,"has_more":false,"items":[]}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job2":
				_, _ = io.WriteString(w, `{"job_id":"job2","status":"succeeded"}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job2/result":
				_, _ = io.WriteString(w, `{"en_markdown":"EN","cn_markdown":"CN"}`)
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

		outFile := filepath.Join(t.TempDir(), "as-file")
		if err := os.WriteFile(outFile, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := RunGen(context.Background(), GenOptions{OutputDir: outFile, Inputs: []string{inputPath}})
		if err == nil || !strings.Contains(err.Error(), "存在失败任务") {
			t.Fatalf("unexpected err: %v", err)
		}
	})

	t.Run("job_status_read_fail", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, `{"up_to_date":true,"rules_version":"v1"}`)
			case r.Method == http.MethodPost && r.URL.Path == "/v1/generate":
				_, _ = io.WriteString(w, `{"job_id":"job3","status":"queued"}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job3/trace":
				_, _ = io.WriteString(w, `{"ok":true,"job_id":"job3","job_status":"running","tenant_id":"demo","trace_count":0,"limit":300,"offset":0,"next_offset":0,"has_more":false,"items":[]}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job3":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "bad")
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

func TestRunUpdateRules_UpToDateButMissingArchiveBranches(t *testing.T) {
	home := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	writeKeyEnvForTest(t, home)

	artifact := makeSignedRulesArtifact(t, "#MARK")
	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing.tar.gz")
	if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "old", ArchivePath: missingPath}); err != nil {
		t.Fatal(err)
	}

	t.Run("download_and_verify_success", func(t *testing.T) {
		var baseURL string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":true,"rules_version":"v9","manifest_sha256":"%s","download_url":"%s/download","signature_base64":"%s","signing_public_key_path_in_archive":"%s","signing_public_key_signature_base64":"%s"}`,
					artifact.ManifestSHA, baseURL, artifact.ArchiveSignatureBase64, artifact.SigningPublicKeyPathInTar, artifact.SigningPublicKeySigBase64))
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
			return RunUpdateRules(context.Background(), UpdateRulesOptions{})
		})
		if err != nil {
			t.Fatalf("RunUpdateRules error: %v", err)
		}
		if !strings.Contains(out, "v9") {
			t.Fatalf("unexpected output: %q", out)
		}
	})

	t.Run("verify_fail", func(t *testing.T) {
		cacheDir, err := rules.DefaultCacheDir()
		if err != nil {
			t.Fatal(err)
		}
		missingPath := filepath.Join(t.TempDir(), "missing-again.tar.gz")
		if err := rules.SaveState(cacheDir, "demo", rules.CacheState{RulesVersion: "v1", ManifestSHA: "old", ArchivePath: missingPath}); err != nil {
			t.Fatal(err)
		}

		var baseURL string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == "/v1/auth/exchange":
				_, _ = io.WriteString(w, `{"access_token":"at","tenant_id":"demo","expires_in":3600}`)
			case r.Method == http.MethodGet && r.URL.Path == "/v1/rules/resolve":
				_, _ = io.WriteString(w, fmt.Sprintf(`{"up_to_date":true,"rules_version":"v10","manifest_sha256":"%s","download_url":"%s/download","signature_base64":"","signing_public_key_path_in_archive":"%s","signing_public_key_signature_base64":"%s"}`,
					artifact.ManifestSHA, baseURL, artifact.SigningPublicKeyPathInTar, artifact.SigningPublicKeySigBase64))
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

		err = RunUpdateRules(context.Background(), UpdateRulesOptions{})
		if err == nil || !strings.Contains(err.Error(), "规则签名缺失") {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}
