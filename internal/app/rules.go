package app

import (
	"context"
	"fmt"

	"syl-listing-pro/internal/client"
	"syl-listing-pro/internal/config"
	"syl-listing-pro/internal/rules"
)

type UpdateRulesOptions struct {
	ConfigPath string
	Verbose    bool
	Force      bool
}

func RunUpdateRules(ctx context.Context, opts UpdateRulesOptions) error {
	log := NewLogger(opts.Verbose)
	cfgPath, err := config.ResolvePath(opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.LoadOrInit(cfgPath)
	if err != nil {
		return err
	}
	if cfg.Auth.SYLListingKey == "" {
		return fmt.Errorf("尚未配置 SYL_LISTING_KEY，执行: syl-listing-pro set key <key>")
	}
	if opts.Force {
		_ = rules.Clear(cfg.Rules.CacheDir)
	}
	api := client.New(cfg.Server.BaseURL)
	ex, err := api.Exchange(ctx, cfg.Auth.SYLListingKey)
	if err != nil {
		return err
	}
	st, err := rules.LoadState(cfg.Rules.CacheDir)
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
	p, err := rules.SaveArchive(cfg.Rules.CacheDir, res.RulesVersion, data)
	if err != nil {
		return err
	}
	if err := rules.VerifySignatureOpenSSL(cfg.Rules.CacheDir, cfg.Rules.PublicKeyPath, res.SignatureBase64, p); err != nil {
		return err
	}
	if err := rules.SaveState(cfg.Rules.CacheDir, rules.CacheState{RulesVersion: res.RulesVersion, ManifestSHA: res.ManifestSHA, ArchivePath: p}); err != nil {
		return err
	}
	fmt.Println(res.RulesVersion)
	return nil
}
