package app

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"syl-listing-pro/internal/client"
	"syl-listing-pro/internal/input"
	"syl-listing-pro/internal/output"
	"syl-listing-pro/internal/rules"
)

type GenOptions struct {
	Verbose   bool
	LogFile   string
	OutputDir string
	Num       int
	Inputs    []string
}

func RunGen(ctx context.Context, opts GenOptions) error {
	if opts.Num <= 0 {
		opts.Num = 1
	}
	cacheDir, err := rules.DefaultCacheDir()
	if err != nil {
		return err
	}
	sylKey, err := loadSYLKeyForRun()
	if err != nil {
		return err
	}
	log, err := NewLogger(opts.Verbose, opts.LogFile)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	startAll := time.Now()

	api := client.New(workerBaseURL)
	api.SetTrace(func(ev client.TraceEvent) {
		if shouldSkipVerboseHTTPTrace(opts.Verbose, ev) {
			return
		}
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

	// 启动前同步规则；失败时按策略回退。
	st, _ := rules.LoadState(cacheDir, ex.TenantID)
	res, err := api.ResolveRules(ctx, ex.AccessToken, st.RulesVersion)
	if err != nil {
		if st.RulesVersion == "" || !rules.HasArchive(st.ArchivePath) {
			return fmt.Errorf("规则中心不可达且首次运行无缓存")
		}
		log.Info(fmt.Sprintf("规则中心不可达，继续使用本地规则（%s）", st.RulesVersion))
	} else {
		needDownload := !res.UpToDate || !rules.HasArchive(st.ArchivePath) || st.RulesVersion != res.RulesVersion
		if needDownload {
			data, gotSHA, dErr := api.Download(ctx, ex.AccessToken, res.DownloadURL)
			if dErr != nil {
				if st.RulesVersion == "" || !rules.HasArchive(st.ArchivePath) {
					return fmt.Errorf("首次拉规则失败: %w", dErr)
				}
				log.Info(fmt.Sprintf("规则下载失败，继续使用本地规则（%s）", st.RulesVersion))
			} else if gotSHA != res.ManifestSHA {
				if st.RulesVersion == "" || !rules.HasArchive(st.ArchivePath) {
					return fmt.Errorf("首次拉规则 sha256 不匹配")
				}
				log.Info(fmt.Sprintf("规则校验失败，继续使用本地规则（%s）", st.RulesVersion))
			} else {
				p, sErr := rules.SaveArchive(cacheDir, ex.TenantID, res.RulesVersion, data)
				if sErr != nil {
					return sErr
				}
				if err := rules.VerifySignatureOpenSSL(cacheDir, res.SignatureBase64, p); err != nil {
					if st.RulesVersion == "" || !rules.HasArchive(st.ArchivePath) {
						return fmt.Errorf("首次拉规则签名校验失败: %w", err)
					}
					log.Info(fmt.Sprintf("规则签名校验失败，继续使用本地规则（%s）", st.RulesVersion))
				} else {
					st = rules.CacheState{RulesVersion: res.RulesVersion, ManifestSHA: res.ManifestSHA, ArchivePath: p}
					if err := rules.SaveState(cacheDir, ex.TenantID, st); err != nil {
						return err
					}
					log.Info(fmt.Sprintf("规则中心：规则中心更新成功（%s）", res.RulesVersion))
				}
			}
		}
	}
	if st.RulesVersion == "" || !rules.HasArchive(st.ArchivePath) {
		return fmt.Errorf("本地规则不可用")
	}
	marker, err := rules.LoadInputMarkerFromArchive(st.ArchivePath)
	if err != nil {
		return err
	}

	files, err := input.Discover(opts.Inputs, marker)
	if err != nil {
		return err
	}

	success := 0
	failed := 0
	for _, f := range files {
		for i := 1; i <= opts.Num; i++ {
			tenantForLog := ex.TenantID
			var elapsedForLog int64

			resp, err := api.Generate(ctx, ex.AccessToken, client.GenerateReq{InputMarkdown: f.Content, CandidateCount: 1})
			if err != nil {
				failed++
				log.Info(fmt.Sprintf("%s 生成失败：%v", tracePrefix(tenantForLog, elapsedForLog), err))
				continue
			}

			traceOffset := 0
			traceWarned := false
			drainTrace := func() {
				for i := 0; i < 3; i++ {
					tr, trErr := api.JobTrace(ctx, ex.AccessToken, resp.JobID, traceOffset, 300)
					if trErr != nil {
						if opts.Verbose {
							log.Event("worker_trace_error", map[string]any{
								"job_id": resp.JobID,
								"error":  trErr.Error(),
							})
						} else if !traceWarned {
							traceWarned = true
							log.Info(fmt.Sprintf("%s 过程拉取失败，继续执行：%v", tracePrefix(tenantForLog, elapsedForLog), trErr))
						}
						return
					}
					traceWarned = false
					if len(tr.Items) == 0 {
						traceOffset = tr.NextOffset
						return
					}
					traceOffset = tr.NextOffset
					for _, item := range tr.Items {
						if strings.TrimSpace(item.TenantID) != "" {
							tenantForLog = item.TenantID
						}
						if item.ElapsedMS >= 0 {
							elapsedForLog = item.ElapsedMS
						}
						if opts.Verbose {
							if shouldSkipVerboseWorkerTrace(item) {
								continue
							}
							log.Event("worker_trace", map[string]any{
								"job_id":     item.JobID,
								"tenant_id":  item.TenantID,
								"ts":         item.TS,
								"elapsed_ms": item.ElapsedMS,
								"source":     item.Source,
								"event_name": item.Event,
								"level":      item.Level,
								"req_id":     item.ReqID,
								"payload":    item.Payload,
							})
						}
						msg := renderWorkerTraceLine(item)
						if strings.TrimSpace(msg) != "" {
							log.Info(fmt.Sprintf("%s %s", tracePrefix(tenantForLog, elapsedForLog), msg))
						}
					}
					if !tr.HasMore {
						return
					}
				}
			}

			deadline := time.Now().Add(time.Duration(pollTimeoutSecond) * time.Second)
			for {
				drainTrace()
				if time.Now().After(deadline) {
					failed++
					log.Info(fmt.Sprintf("%s 生成失败：轮询超时", tracePrefix(tenantForLog, elapsedForLog)))
					break
				}
				stResp, err := api.Job(ctx, ex.AccessToken, resp.JobID)
				if err != nil {
					failed++
					log.Info(fmt.Sprintf("%s 生成失败：%v", tracePrefix(tenantForLog, elapsedForLog), err))
					break
				}
				if stResp.Status == "succeeded" {
					drainTrace()
					resData, err := api.Result(ctx, ex.AccessToken, resp.JobID)
					if err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：读取结果失败: %v", tracePrefix(tenantForLog, elapsedForLog), err))
						break
					}
					_, enPath, cnPath, err := output.UniquePair(opts.OutputDir)
					if err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：输出文件名失败: %v", tracePrefix(tenantForLog, elapsedForLog), err))
						break
					}
					if err := os.WriteFile(enPath, []byte(resData.ENMarkdown), 0o644); err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：写 EN 失败: %v", tracePrefix(tenantForLog, elapsedForLog), err))
						break
					}
					if err := os.WriteFile(cnPath, []byte(resData.CNMarkdown), 0o644); err != nil {
						failed++
						log.Info(fmt.Sprintf("%s 生成失败：写 CN 失败: %v", tracePrefix(tenantForLog, elapsedForLog), err))
						break
					}
					success++
					log.Info(fmt.Sprintf("%s EN 已写入：%s", tracePrefix(tenantForLog, elapsedForLog), enPath))
					log.Info(fmt.Sprintf("%s CN 已写入：%s", tracePrefix(tenantForLog, elapsedForLog), cnPath))
					break
				}
				if stResp.Status == "failed" {
					drainTrace()
					failed++
					log.Info(fmt.Sprintf("%s 生成失败：%s", tracePrefix(tenantForLog, elapsedForLog), stResp.Error))
					break
				}
				time.Sleep(time.Duration(pollIntervalMs) * time.Millisecond)
			}
		}
	}

	log.Info(fmt.Sprintf("任务完成：成功 %d，失败 %d，总耗时 %s", success, failed, humanDurationShort(time.Since(startAll))))
	if failed > 0 {
		return fmt.Errorf("存在失败任务")
	}
	return nil
}

func shouldSkipVerboseHTTPTrace(verbose bool, ev client.TraceEvent) bool {
	if !verbose {
		return true
	}
	if strings.EqualFold(ev.Method, "GET") &&
		(strings.Contains(ev.URL, "/v1/jobs/") || strings.Contains(ev.URL, "/v1/admin/logs/trace/")) {
		return true
	}
	return false
}

func shouldSkipVerboseWorkerTrace(item client.JobTraceItem) bool {
	if item.Source != "api" {
		return false
	}
	switch item.Event {
	case "job_status_read", "job_result_not_ready":
		return true
	default:
		return false
	}
}

func renderWorkerTraceLine(item client.JobTraceItem) string {
	if item.Source == "api" {
		switch item.Event {
		case "job_status_read", "job_result_not_ready":
			return ""
		}
	}
	if msg := stringPayload(item.Payload, "message"); strings.TrimSpace(msg) != "" {
		return strings.TrimSpace(msg)
	}
	switch item.Event {
	case "generate_queued":
		if strings.TrimSpace(item.JobID) != "" {
			return fmt.Sprintf("任务已加入队列 %s", item.JobID)
		}
		return "任务已加入队列"
	case "rules_loaded":
		return fmt.Sprintf("规则已加载 %s", stringPayload(item.Payload, "rules_version"))
	case "api_request":
		// 底层 LLM 调用事件不在普通输出展示；可通过 --verbose 查看 NDJSON 细节。
		return ""
	case "api_retry":
		return ""
	case "api_failed":
		return ""
	case "job_retry_scheduled":
		return fmt.Sprintf("任务重试计划：第 %d/%d 次失败，准备第 %d 次（等待由队列退避控制）：%s",
			intPayload(item.Payload, "attempt"),
			intPayload(item.Payload, "max_attempts"),
			intPayload(item.Payload, "next_attempt"),
			shortText(stringPayload(item.Payload, "error"), 100))
	case "job_succeeded":
		return fmt.Sprintf("执行完成（%s）", durationLabel(item.Payload, "duration_ms"))
	case "job_failed":
		return fmt.Sprintf("执行失败：%s", shortText(stringPayload(item.Payload, "error"), 120))
	case "generation_ok":
		return fmt.Sprintf("生成阶段完成（%s）", durationLabel(item.Payload, "timing_ms"))
	}
	return genericWorkerTraceLine(item)
}

func genericWorkerTraceLine(item client.JobTraceItem) string {
	step := stringPayload(item.Payload, "step")
	errText := stringPayload(item.Payload, "error")
	label := sectionLabel(item.Payload)
	switch {
	case strings.HasSuffix(item.Event, "_start") && step != "":
		return ""
	case strings.Contains(item.Event, "repair_needed"):
		return fmt.Sprintf("%s规则校验失败（%s）：%s", label, errorCountLabel(item.Payload), errorPreview(item.Payload, 0))
	case strings.Contains(item.Event, "validate_fail"):
		return fmt.Sprintf("%s规则校验失败：%s", label, errorPreview(item.Payload, 0))
	case strings.HasSuffix(item.Event, "_repair_ok"):
		return fmt.Sprintf("%s修复完成", label)
	case strings.HasSuffix(item.Event, "_ok") && step != "":
		return fmt.Sprintf("%s完成（%s）", label, durationLabel(item.Payload, "duration_ms"))
	case strings.HasSuffix(item.Event, "_failed"):
		if errText != "" {
			return fmt.Sprintf("%s失败：%s", eventLabel(item.Event), shortText(errText, 120))
		}
		return fmt.Sprintf("%s失败", eventLabel(item.Event))
	case errText != "":
		return fmt.Sprintf("%s：%s", eventLabel(item.Event), shortText(errText, 120))
	default:
		return ""
	}
}

func tracePrefix(tenantID string, elapsedMs int64) string {
	tenant := strings.TrimSpace(tenantID)
	if tenant == "" {
		tenant = "-"
	}
	if elapsedMs < 0 {
		elapsedMs = 0
	}
	totalSec := elapsedMs / 1000
	hh := totalSec / 3600
	mm := (totalSec % 3600) / 60
	ss := totalSec % 60
	if hh > 0 {
		return fmt.Sprintf("%s:%02d:%02d:%02d", tenant, hh, mm, ss)
	}
	return fmt.Sprintf("%s:%02d:%02d", tenant, mm, ss)
}

func stepLabel(step string) string {
	if step == "" {
		return "任务步骤"
	}
	if label, ok := judgeRoundStepLabel(step); ok {
		return label
	}
	if strings.HasPrefix(step, "translate_") {
		return sectionDisplayName(strings.TrimPrefix(step, "translate_")) + "翻译"
	}
	if idx := strings.Index(step, "_attempt_"); idx > 0 {
		return stepLabel(step[:idx])
	}
	if strings.HasSuffix(step, "_whole_repair") {
		base := strings.TrimSuffix(step, "_whole_repair")
		return stepLabel(base) + "整段修复"
	}
	return sectionDisplayName(step)
}

func judgeRoundStepLabel(step string) (string, bool) {
	parts := strings.Split(step, "_")
	// 期望格式: <section>_judge_repair_round_<n>
	if len(parts) != 5 {
		return "", false
	}
	if parts[1] != "judge" || parts[2] != "repair" || parts[3] != "round" {
		return "", false
	}
	round, err := strconv.Atoi(parts[4])
	if err != nil || round <= 0 {
		return "", false
	}
	sectionName := sectionDisplayName(parts[0])
	return fmt.Sprintf("%s一致性修复（第%d轮）", sectionName, round), true
}

func sectionDisplayName(token string) string {
	clean := strings.TrimSpace(strings.ReplaceAll(token, "_", " "))
	if clean == "" {
		return "步骤"
	}
	return clean
}

func eventLabel(name string) string {
	clean := strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	if clean == "" {
		return "事件"
	}
	return clean
}

func sectionLabel(payload map[string]any) string {
	if label := stringPayload(payload, "label"); strings.TrimSpace(label) != "" {
		return strings.TrimSpace(label)
	}
	if label := stringPayload(payload, "display"); strings.TrimSpace(label) != "" {
		return strings.TrimSpace(label)
	}
	step := stringPayload(payload, "step")
	if step != "" {
		return stepLabel(step)
	}
	section := stringPayload(payload, "section")
	if section == "" {
		return "步骤"
	}
	return stepLabel(section)
}

func intPayload(payload map[string]any, key string) int {
	v, ok := payload[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func stringPayload(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func firstError(payload map[string]any) string {
	v, ok := payload["errors"]
	if !ok || v == nil {
		return "未知错误"
	}
	if arr, ok := v.([]any); ok && len(arr) > 0 {
		return shortText(fmt.Sprintf("%v", arr[0]), 140)
	}
	if arr, ok := v.([]string); ok && len(arr) > 0 {
		return shortText(arr[0], 140)
	}
	return shortText(fmt.Sprintf("%v", v), 140)
}

func allErrors(payload map[string]any) []string {
	v, ok := payload["errors"]
	if !ok || v == nil {
		return nil
	}
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			s := strings.TrimSpace(fmt.Sprintf("%v", item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	if arr, ok := v.([]string); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			s := strings.TrimSpace(item)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "" {
		return nil
	}
	return []string{s}
}

func errorCountLabel(payload map[string]any) string {
	errs := allErrors(payload)
	if len(errs) == 0 {
		return "1条"
	}
	return fmt.Sprintf("%d条", len(errs))
}

func errorPreview(payload map[string]any, max int) string {
	errs := allErrors(payload)
	if len(errs) == 0 {
		return firstError(payload)
	}
	// max<=0 表示完整输出全部错误，不做省略。
	if max <= 0 || len(errs) <= max {
		return strings.Join(errs, "；")
	}
	head := strings.Join(errs[:max], "；")
	return fmt.Sprintf("%s；...（其余%d条）", head, len(errs)-max)
}

func targetsLabel(payload map[string]any) string {
	v, ok := payload["targets"]
	if !ok || v == nil {
		return "-"
	}
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			switch n := item.(type) {
			case float64:
				out = append(out, fmt.Sprintf("%d", int(n)))
			case int:
				out = append(out, fmt.Sprintf("%d", n))
			case int64:
				out = append(out, fmt.Sprintf("%d", n))
			default:
				s := strings.TrimSpace(fmt.Sprintf("%v", item))
				if s != "" {
					out = append(out, s)
				}
			}
		}
		if len(out) == 0 {
			return "-"
		}
		return strings.Join(out, ",")
	}
	if arr, ok := v.([]string); ok {
		if len(arr) == 0 {
			return "-"
		}
		return strings.Join(arr, ",")
	}
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "" {
		return "-"
	}
	return s
}

func durationLabel(payload map[string]any, key string) string {
	ms := intPayload(payload, key)
	if ms <= 0 {
		return "-"
	}
	if ms >= 60_000 {
		return fmt.Sprintf("%.2fm", float64(ms)/60_000.0)
	}
	if ms >= 1_000 {
		return fmt.Sprintf("%.2fs", float64(ms)/1_000.0)
	}
	return fmt.Sprintf("%dms", ms)
}

func shortText(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}

func humanDurationShort(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	sec := int64(d.Round(time.Second) / time.Second)
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
