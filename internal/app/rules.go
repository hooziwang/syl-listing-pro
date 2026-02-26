package app

import (
	"context"
	"fmt"

	"syl-listing-pro/internal/client"
	"syl-listing-pro/internal/rules"
)

type UpdateRulesOptions struct {
	Verbose bool
	LogFile string
	Force   bool
}

func RunUpdateRules(ctx context.Context, opts UpdateRulesOptions) error {
	log, err := NewLogger(opts.Verbose, opts.LogFile)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		return err
	}
	sylKey, err := loadSYLKeyForRun()
	if err != nil {
		return err
	}
	api := client.New(workerBaseURL)
	api.SetTrace(func(ev client.TraceEvent) {
		log.Event("worker_http_"+ev.Stage, map[string]any{
			"method":      ev.Method,
			"url":         ev.URL,
			"status_code": ev.StatusCode,
			"duration_ms": ev.DurationMs,
			"request":     ev.Request,
			"response":    ev.Response,
			"error":       ev.Error,
		})
	})
	ex, err := api.Exchange(ctx, sylKey)
	if err != nil {
		return err
	}
	if opts.Force {
		_ = rules.Clear(cacheDir, ex.TenantID)
	}
	st, err := rules.LoadState(cacheDir, ex.TenantID)
	if err != nil {
		return err
	}
	res, err := api.ResolveRules(ctx, ex.AccessToken, st.RulesVersion)
	if err != nil {
		if st.RulesVersion != "" {
			log.Info(fmt.Sprintf("规则中心不可达，回退本地规则（%s）", st.RulesVersion))
			return nil
		}
		return fmt.Errorf("规则中心不可达且本地无规则缓存")
	}
	if res.UpToDate {
		if !rules.HasArchive(st.ArchivePath) {
			data, gotSHA, err := api.Download(ctx, ex.AccessToken, res.DownloadURL)
			if err != nil {
				return err
			}
			if gotSHA != res.ManifestSHA {
				return fmt.Errorf("规则包 sha256 不匹配: got=%s want=%s", gotSHA, res.ManifestSHA)
			}
			p, err := rules.SaveArchive(cacheDir, ex.TenantID, res.RulesVersion, data)
			if err != nil {
				return err
			}
			if err := rules.VerifySignatureOpenSSL(cacheDir, res.SignatureBase64, p); err != nil {
				return err
			}
			if err := rules.SaveState(cacheDir, ex.TenantID, rules.CacheState{RulesVersion: res.RulesVersion, ManifestSHA: res.ManifestSHA, ArchivePath: p}); err != nil {
				return err
			}
		}
		fmt.Println(res.RulesVersion)
		return nil
	}
	data, gotSHA, err := api.Download(ctx, ex.AccessToken, res.DownloadURL)
	if err != nil {
		return err
	}
	if gotSHA != res.ManifestSHA {
		return fmt.Errorf("规则包 sha256 不匹配: got=%s want=%s", gotSHA, res.ManifestSHA)
	}
	p, err := rules.SaveArchive(cacheDir, ex.TenantID, res.RulesVersion, data)
	if err != nil {
		return err
	}
	if err := rules.VerifySignatureOpenSSL(cacheDir, res.SignatureBase64, p); err != nil {
		return err
	}
	if err := rules.SaveState(cacheDir, ex.TenantID, rules.CacheState{RulesVersion: res.RulesVersion, ManifestSHA: res.ManifestSHA, ArchivePath: p}); err != nil {
		return err
	}
	fmt.Println(res.RulesVersion)
	return nil
}
