package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"syl-listing-pro/internal/client"
	"syl-listing-pro/internal/config"
	"syl-listing-pro/internal/input"
	"syl-listing-pro/internal/output"
	"syl-listing-pro/internal/rules"
)

type GenOptions struct {
	ConfigPath string
	Verbose    bool
	OutputDir  string
	Num        int
	Inputs     []string
}

func RunGen(ctx context.Context, opts GenOptions) error {
	if opts.Num <= 0 {
		opts.Num = 1
	}
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
	log := NewLogger(opts.Verbose)
	startAll := time.Now()

	api := client.New(cfg.Server.BaseURL)
	ex, err := api.Exchange(ctx, cfg.Auth.SYLListingKey)
	if err != nil {
		return err
	}

	// 启动前同步规则；失败时按策略回退。
	st, _ := rules.LoadState(cfg.Rules.CacheDir)
	res, err := api.ResolveRules(ctx, ex.AccessToken, st.RulesVersion)
	if err != nil {
		if st.RulesVersion == "" {
			return fmt.Errorf("规则中心不可达且首次运行无缓存")
		}
		log.Info(fmt.Sprintf("规则中心不可达，继续使用本地规则（%s）", st.RulesVersion))
	} else if !res.UpToDate {
		data, gotSHA, dErr := api.Download(ctx, ex.AccessToken, res.DownloadURL)
		if dErr != nil {
			if st.RulesVersion == "" {
				return fmt.Errorf("首次拉规则失败: %w", dErr)
			}
			log.Info(fmt.Sprintf("规则下载失败，继续使用本地规则（%s）", st.RulesVersion))
		} else if gotSHA != res.ManifestSHA {
			if st.RulesVersion == "" {
				return fmt.Errorf("首次拉规则 sha256 不匹配")
			}
			log.Info(fmt.Sprintf("规则校验失败，继续使用本地规则（%s）", st.RulesVersion))
		} else {
			p, _ := rules.SaveArchive(cfg.Rules.CacheDir, res.RulesVersion, data)
			if err := rules.VerifySignatureOpenSSL(cfg.Rules.CacheDir, cfg.Rules.PublicKeyPath, res.SignatureBase64, p); err != nil {
				if st.RulesVersion == "" {
					return fmt.Errorf("首次拉规则签名校验失败: %w", err)
				}
				log.Info(fmt.Sprintf("规则签名校验失败，继续使用本地规则（%s）", st.RulesVersion))
				goto RULE_SYNC_DONE
			}
			_ = rules.SaveState(cfg.Rules.CacheDir, rules.CacheState{RulesVersion: res.RulesVersion, ManifestSHA: res.ManifestSHA, ArchivePath: p})
			log.Info(fmt.Sprintf("规则中心：规则中心更新成功（%s）", res.RulesVersion))
		}
	}
RULE_SYNC_DONE:

	files, err := input.Discover(opts.Inputs)
	if err != nil {
		return err
	}

	success := 0
	failed := 0
	for _, f := range files {
		base := filepath.Base(f.Path)
		for i := 1; i <= opts.Num; i++ {
			label := fmt.Sprintf("[%s", base)
			if opts.Num > 1 {
				label += fmt.Sprintf("#%d", i)
			}
			label += "]"

			log.Info(fmt.Sprintf("%s 开始提交生成任务", label))
			resp, err := api.Generate(ctx, ex.AccessToken, client.GenerateReq{InputMarkdown: f.Content, CandidateCount: 1})
			if err != nil {
				failed++
				log.Info(fmt.Sprintf("%s 生成失败：%v", label, err))
				continue
			}

			deadline := time.Now().Add(time.Duration(cfg.Run.PollTimeoutSecond) * time.Second)
			for {
				if time.Now().After(deadline) {
					failed++
					log.Info(fmt.Sprintf("%s 生成失败：轮询超时", label))
					break
				}
				stResp, err := api.Job(ctx, ex.AccessToken, resp.JobID)
				if err != nil {
					failed++
					log.Info(fmt.Sprintf("%s 生成失败：%v", label, err))
					break
				}
				if stResp.Status == "succeeded" {
					resData, err := api.Result(ctx, ex.AccessToken, resp.JobID)
					if err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：读取结果失败: %v", label, err))
						break
					}
					_, enPath, cnPath, err := output.UniquePair(opts.OutputDir)
					if err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：输出文件名失败: %v", label, err))
						break
					}
					if err := os.WriteFile(enPath, []byte(resData.ENMarkdown), 0o644); err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：写 EN 失败: %v", label, err))
						break
					}
					if err := os.WriteFile(cnPath, []byte(resData.CNMarkdown), 0o644); err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：写 CN 失败: %v", label, err))
						break
					}
					success++
					log.Info(fmt.Sprintf("%s EN 已写入：%s", label, enPath))
					log.Info(fmt.Sprintf("%s CN 已写入：%s", label, cnPath))
					break
				}
				if stResp.Status == "failed" {
					failed++
					log.Info(fmt.Sprintf("%s 生成失败：%s", label, stResp.Error))
					break
				}
				time.Sleep(time.Duration(cfg.Run.PollIntervalMs) * time.Millisecond)
			}
		}
	}

	log.Info(fmt.Sprintf("任务完成：成功 %d，失败 %d，总耗时 %.2fs", success, failed, time.Since(startAll).Seconds()))
	if failed > 0 {
		return fmt.Errorf("存在失败任务")
	}
	return nil
}
