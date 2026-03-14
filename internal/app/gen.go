package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
	"syl-listing-pro/internal/client"
	"syl-listing-pro/internal/input"
	"syl-listing-pro/internal/output"
)

var (
	lineLengthConstraintPattern = regexp.MustCompile(`^第(\d+)条长度不满足约束:\s*(\d+)（规则区间 \[(\d+),(\d+)\]，容差区间 \[(\d+),(\d+)\]）$`)
	textLengthConstraintPattern = regexp.MustCompile(`^长度不满足约束:\s*(\d+)（规则区间 \[(\d+),(\d+)\]，容差区间 \[(\d+),(\d+)\]）$`)
	keywordOrderPattern         = regexp.MustCompile(`^第(\d+)个关键词未按顺序原样出现:\s*(.+)$`)
	runtimeCandidateStepPattern = regexp.MustCompile(`_candidate_(\d+)$`)
)

type GenOptions struct {
	Verbose   bool
	LogFile   string
	OutputDir string
	Num       int
	Inputs    []string
}

type generateTask struct {
	file  input.RequirementFile
	index int
	label string
}

type submittedJob struct {
	jobID string
	label string
}

type submittedJobRegistry struct {
	mu   sync.Mutex
	jobs map[string]submittedJob
}

func newSubmittedJobRegistry() *submittedJobRegistry {
	return &submittedJobRegistry{jobs: make(map[string]submittedJob)}
}

func (r *submittedJobRegistry) add(jobID, label string) {
	id := strings.TrimSpace(jobID)
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.jobs[id] = submittedJob{jobID: id, label: label}
}

func (r *submittedJobRegistry) snapshot() []submittedJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]submittedJob, 0, len(r.jobs))
	for _, item := range r.jobs {
		out = append(out, item)
	}
	return out
}

func RunGen(ctx context.Context, opts GenOptions) error {
	if opts.Num <= 0 {
		opts.Num = 1
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
	runDone := make(chan struct{})
	defer close(runDone)
	startAll := time.Now()

	api := client.New(resolveWorkerBaseURL())
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

	files, err := input.Discover(opts.Inputs)
	if err != nil {
		return err
	}

	tasks := buildGenerateTasks(files, opts.Num)
	submitted := newSubmittedJobRegistry()
	var cancelOnce sync.Once
	cancelDone := make(chan struct{})
	cancelSubmittedTasks := func() {
		cancelOnce.Do(func() {
			defer close(cancelDone)
			jobs := submitted.snapshot()
			if len(jobs) == 0 {
				return
			}
			log.Info(fmt.Sprintf("检测到中断，开始取消已提交任务（%d）", len(jobs)))
			cancelCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var okCount atomic.Int64
			var failCount atomic.Int64
			var cwg sync.WaitGroup
			cancelSem := semaphore.NewWeighted(8)
			for _, item := range jobs {
				item := item
				cwg.Add(1)
				go func() {
					defer cwg.Done()
					if err := cancelSem.Acquire(cancelCtx, 1); err != nil {
						failCount.Add(1)
						return
					}
					defer cancelSem.Release(1)
					resp, err := api.CancelJob(cancelCtx, ex.AccessToken, item.jobID)
					if err != nil {
						failCount.Add(1)
						log.Info(fmt.Sprintf("%s 取消失败：%v", taskPrefix(ex.TenantID, 0, item.label), err))
						return
					}
					okCount.Add(1)
					if resp.Cancelled || strings.EqualFold(resp.Status, "cancelled") {
						log.Info(fmt.Sprintf("%s 已取消（job_id=%s）", taskPrefix(ex.TenantID, 0, item.label), item.jobID))
						return
					}
					log.Info(fmt.Sprintf("%s 已提交取消请求（job_id=%s）", taskPrefix(ex.TenantID, 0, item.label), item.jobID))
				}()
			}
			cwg.Wait()
			log.Info(fmt.Sprintf("取消完成：成功 %d，失败 %d", okCount.Load(), failCount.Load()))
		})
	}

	go func() {
		select {
		case <-ctx.Done():
			if isContextCanceledErr(ctx.Err()) {
				cancelSubmittedTasks()
			}
		case <-runDone:
			return
		}
	}()

	var successCount atomic.Int64
	var failedCount atomic.Int64
	var wg sync.WaitGroup
	sem := semaphore.NewWeighted(int64(maxConcurrentTasks))

	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				if isContextCanceledErr(err) {
					log.Info(fmt.Sprintf("%s 已取消", taskPrefix(ex.TenantID, 0, task.label)))
					return
				}
				failedCount.Add(1)
				log.Info(fmt.Sprintf("%s 生成失败：%v", taskPrefix(ex.TenantID, 0, task.label), err))
				return
			}
			defer sem.Release(1)

			if runGenerateTask(ctx, api, ex, log, opts, task, func(jobID string) {
				submitted.add(jobID, task.label)
			}) {
				successCount.Add(1)
				return
			}
			if isContextCanceledErr(ctx.Err()) {
				return
			}
			failedCount.Add(1)
		}()
	}
	wg.Wait()
	if isContextCanceledErr(ctx.Err()) {
		cancelSubmittedTasks()
		select {
		case <-cancelDone:
		case <-time.After(25 * time.Second):
			log.Info("取消等待超时，已退出")
		}
		return context.Canceled
	}

	success := int(successCount.Load())
	failed := int(failedCount.Load())
	log.Info(fmt.Sprintf("任务完成：成功 %d，失败 %d，总耗时 %s", success, failed, humanDurationShort(time.Since(startAll))))
	if failed > 0 {
		return fmt.Errorf("存在失败任务")
	}
	return nil
}

func buildGenerateTasks(files []input.RequirementFile, num int) []generateTask {
	tasks := make([]generateTask, 0, len(files)*num)
	fileCount := len(files)
	for _, f := range files {
		for i := 1; i <= num; i++ {
			tasks = append(tasks, generateTask{
				file:  f,
				index: i,
				label: taskDisplayLabel(fileCount, num, f.Path, i),
			})
		}
	}
	return tasks
}

func taskDisplayLabel(fileCount, num int, path string, index int) string {
	base := filepath.Base(path)
	switch {
	case fileCount > 1 && num > 1:
		return fmt.Sprintf("%s#%d", base, index)
	case fileCount > 1:
		return base
	case num > 1:
		return fmt.Sprintf("#%d", index)
	default:
		return ""
	}
}

func taskPrefix(tenantID string, elapsedMs int64, taskLabel string) string {
	p := tracePrefix(tenantID, elapsedMs)
	if strings.TrimSpace(taskLabel) == "" {
		return p
	}
	return fmt.Sprintf("%s [%s]", p, strings.TrimSpace(taskLabel))
}

func runGenerateTask(
	ctx context.Context,
	api *client.API,
	ex client.ExchangeResp,
	log *Logger,
	opts GenOptions,
	task generateTask,
	onJobSubmitted func(jobID string),
) bool {
	tenantForLog := ex.TenantID
	var elapsedForLog int64

	resp, err := api.Generate(ctx, ex.AccessToken, client.GenerateReq{
		InputMarkdown:  task.file.Content,
		InputFilename:  filepath.Base(task.file.Path),
		CandidateCount: 1,
	})
	if err != nil {
		if isContextCanceledErr(err) {
			log.Info(fmt.Sprintf("%s 已取消", taskPrefix(tenantForLog, elapsedForLog, task.label)))
			return false
		}
		log.Info(fmt.Sprintf("%s 生成失败：%v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
		return false
	}
	if onJobSubmitted != nil {
		onJobSubmitted(resp.JobID)
	}

	traceWarned := false
	lastTraceLine := ""
	streamCtx, cancelStream := context.WithTimeout(ctx, time.Duration(streamTimeoutSecond)*time.Second)
	defer cancelStream()

	handleTraceItem := func(item client.JobTraceItem) {
		if strings.TrimSpace(item.TenantID) != "" {
			tenantForLog = item.TenantID
		}
		if item.ElapsedMS >= 0 {
			elapsedForLog = item.ElapsedMS
		}
		if opts.Verbose {
			if shouldSkipVerboseWorkerTrace(item) {
				return
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
				"task":       task.label,
			})
		}
		msg := renderWorkerTraceLine(item, !opts.Verbose)
		if strings.TrimSpace(msg) == "" {
			return
		}
		if !opts.Verbose {
			if msg == lastTraceLine {
				return
			}
			lastTraceLine = msg
		}
		log.Info(fmt.Sprintf("%s %s", taskPrefix(tenantForLog, elapsedForLog, task.label), msg))
	}

	stResp, err := api.JobEvents(streamCtx, ex.AccessToken, resp.JobID, func(ev client.JobEvent) {
		switch ev.Type {
		case "trace":
			if ev.Trace == nil {
				return
			}
			traceWarned = false
			handleTraceItem(ev.Trace.Item)
		case "status":
			if ev.Status == nil {
				return
			}
			if strings.TrimSpace(ev.Status.TenantID) != "" {
				tenantForLog = ev.Status.TenantID
			}
		}
	})
	if err != nil {
		if isContextCanceledErr(err) {
			log.Info(fmt.Sprintf("%s 已取消", taskPrefix(tenantForLog, elapsedForLog, task.label)))
			return false
		}
		if errors.Is(err, context.DeadlineExceeded) {
			log.Info(fmt.Sprintf("%s 生成失败：SSE 超时", taskPrefix(tenantForLog, elapsedForLog, task.label)))
			return false
		}
		if opts.Verbose {
			log.Event("worker_trace_error", map[string]any{
				"job_id": resp.JobID,
				"error":  err.Error(),
				"task":   task.label,
			})
		} else if !traceWarned {
			traceWarned = true
			log.Info(fmt.Sprintf("%s 过程流式接收失败：%v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
		}
		log.Info(fmt.Sprintf("%s 生成失败：%v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
		return false
	}

	if stResp.Status == "succeeded" {
		resData, err := api.Result(ctx, ex.AccessToken, resp.JobID)
		if err != nil {
			log.Info(fmt.Sprintf("%s 生成失败：读取结果失败: %v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
			return false
		}
		_, enPath, cnPath, err := output.UniquePair(opts.OutputDir, task.file.Path)
		if err != nil {
			log.Info(fmt.Sprintf("%s 生成失败：输出文件名失败: %v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
			return false
		}
		if err := os.WriteFile(enPath, []byte(resData.ENMarkdown), 0o644); err != nil {
			log.Info(fmt.Sprintf("%s 生成失败：写 EN 失败: %v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
			return false
		}
		if err := os.WriteFile(cnPath, []byte(resData.CNMarkdown), 0o644); err != nil {
			log.Info(fmt.Sprintf("%s 生成失败：写 CN 失败: %v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
			return false
		}
		log.Info(fmt.Sprintf("%s EN 已写入：%s", taskPrefix(tenantForLog, elapsedForLog, task.label), mustAbsPath(enPath)))
		log.Info(fmt.Sprintf("%s CN 已写入：%s", taskPrefix(tenantForLog, elapsedForLog, task.label), mustAbsPath(cnPath)))

		enDocxTargetPath := strings.TrimSuffix(enPath, filepath.Ext(enPath)) + ".docx"
		enDocxPath, err := convertMarkdownToDocxFunc(ctx, enPath, enDocxTargetPath)
		if err != nil {
			log.Info(fmt.Sprintf("%s 生成失败：EN Word 转换失败: %v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
			return false
		}
		cnDocxTargetPath := strings.TrimSuffix(cnPath, filepath.Ext(cnPath)) + ".docx"
		cnDocxPath, err := convertMarkdownToDocxFunc(ctx, cnPath, cnDocxTargetPath)
		if err != nil {
			log.Info(fmt.Sprintf("%s 生成失败：CN Word 转换失败: %v", taskPrefix(tenantForLog, elapsedForLog, task.label), err))
			return false
		}
		log.Info(fmt.Sprintf("%s EN Word 已写入：%s", taskPrefix(tenantForLog, elapsedForLog, task.label), mustAbsPath(enDocxPath)))
		log.Info(fmt.Sprintf("%s CN Word 已写入：%s", taskPrefix(tenantForLog, elapsedForLog, task.label), mustAbsPath(cnDocxPath)))
		return true
	}
	if stResp.Status == "failed" {
		log.Info(fmt.Sprintf("%s 生成失败：%s", taskPrefix(tenantForLog, elapsedForLog, task.label), stResp.Error))
		return false
	}
	if stResp.Status == "cancelled" {
		log.Info(fmt.Sprintf("%s 生成已取消", taskPrefix(tenantForLog, elapsedForLog, task.label)))
		return false
	}
	log.Info(fmt.Sprintf("%s 生成失败：SSE 未返回终态", taskPrefix(tenantForLog, elapsedForLog, task.label)))
	return false
}

func isContextCanceledErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "interrupt signal received") {
		return true
	}
	if strings.Contains(msg, "operation was canceled") || strings.Contains(msg, "operation was cancelled") {
		return true
	}
	return false
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
	if item.Event == "agent_team_candidate_failed" {
		return true
	}
	if item.Source != "api" {
		return false
	}
	switch item.Event {
	case "job_result_not_ready":
		return true
	default:
		return false
	}
}

func renderWorkerTraceLine(item client.JobTraceItem, colorizeLabel bool) string {
	if item.Source == "api" {
		switch item.Event {
		case "job_result_not_ready":
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
		rulesVersion := stringPayload(item.Payload, "rules_version")
		workerVersion := stringPayload(item.Payload, "worker_version")
		if strings.TrimSpace(workerVersion) != "" {
			return fmt.Sprintf("规则已加载 %s | worker %s", rulesVersion, workerVersion)
		}
		return fmt.Sprintf("规则已加载 %s", rulesVersion)
	case "section_generate_ok":
		step := stringPayload(item.Payload, "step")
		if _, ok := judgeRoundOfStep(step); ok {
			return fmt.Sprintf("%s完成%s", sectionLabel(item.Payload, colorizeLabel), tailDuration(item.Payload, "duration_ms", colorizeLabel))
		}
		return fmt.Sprintf("%s已生成%s", sectionLabel(item.Payload, colorizeLabel), tailDuration(item.Payload, "duration_ms", colorizeLabel))
	case "section_sentence_step_ok":
		label := sectionLabel(item.Payload, colorizeLabel)
		idx := intPayload(item.Payload, "sentence_index")
		total := intPayload(item.Payload, "sentence_total")
		if idx > 0 && total > 0 {
			return fmt.Sprintf("%s逐句生成（第%d/%d句）完成%s", label, idx, total, tailDuration(item.Payload, "duration_ms", colorizeLabel))
		}
		return fmt.Sprintf("%s逐句生成完成%s", label, tailDuration(item.Payload, "duration_ms", colorizeLabel))
	case "section_sentence_step_validate_fail":
		label := sectionLabel(item.Payload, colorizeLabel)
		idx := intPayload(item.Payload, "sentence_index")
		total := intPayload(item.Payload, "sentence_total")
		errText := shortText(stringPayload(item.Payload, "error"), 140)
		if idx > 0 && total > 0 {
			return fmt.Sprintf("%s逐句校验失败（第%d/%d句）：%s", label, idx, total, errText)
		}
		return fmt.Sprintf("%s逐句校验失败：%s", label, errText)
	case "api_request":
		// 底层 LLM 调用事件不在普通输出展示；可通过 --verbose 查看 NDJSON 细节。
		return ""
	case "api_ok":
		return ""
	case "api_retry":
		return ""
	case "api_failed":
		return ""
	case "agent_team_candidate_failed":
		return ""
	case "agent_team_ok":
		return fmt.Sprintf("%s%s生成完成%s", runtimeSectionLabel(item.Payload, colorizeLabel), runtimeCandidateLabel(item.Payload), tailDuration(item.Payload, "latency_ms", colorizeLabel))
	case "runtime_candidate_selection":
		label := runtimeSectionLabel(item.Payload, colorizeLabel)
		scores := runtimeCandidateScores(item.Payload)
		if len(scores) == 0 {
			return ""
		}
		selected := intPayload(item.Payload, "selected_candidate_index")
		if selected > 0 {
			return fmt.Sprintf("%s候选评分：%s，已选 #%d%s", label, strings.Join(scores, "，"), selected, tailDuration(item.Payload, "duration_ms", colorizeLabel))
		}
		return fmt.Sprintf("%s候选评分：%s%s", label, strings.Join(scores, "，"), tailDuration(item.Payload, "duration_ms", colorizeLabel))
	case "job_retry_scheduled":
		return fmt.Sprintf("任务重试计划：第 %d/%d 次失败，准备第 %d 次（等待由队列退避控制）：%s",
			intPayload(item.Payload, "attempt"),
			intPayload(item.Payload, "max_attempts"),
			intPayload(item.Payload, "next_attempt"),
			summarizeRetryError(stringPayload(item.Payload, "error")))
	case "job_succeeded":
		return fmt.Sprintf("执行完成%s", tailDuration(item.Payload, "duration_ms", colorizeLabel))
	case "job_failed":
		return fmt.Sprintf("执行失败：%s", shortText(stringPayload(item.Payload, "error"), 120))
	case "job_cancel_requested":
		return "取消请求已提交"
	case "job_cancelled":
		return "任务已取消"
	case "generation_ok":
		return fmt.Sprintf("生成阶段完成%s", tailDuration(item.Payload, "timing_ms", colorizeLabel))
	}
	return genericWorkerTraceLine(item, colorizeLabel)
}

func genericWorkerTraceLine(item client.JobTraceItem, colorizeLabel bool) string {
	step := stringPayload(item.Payload, "step")
	errText := stringPayload(item.Payload, "error")
	label := sectionLabel(item.Payload, colorizeLabel)
	switch {
	case strings.HasSuffix(item.Event, "_start") && step != "":
		return ""
	case strings.Contains(item.Event, "repair_needed"):
		return fmt.Sprintf("%s规则校验失败：%s", label, errorPreviewMultiline(item.Payload))
	case strings.Contains(item.Event, "validate_fail"):
		return fmt.Sprintf("%s规则校验失败：%s", label, errorPreviewMultiline(item.Payload))
	case strings.HasSuffix(item.Event, "_repair_ok"):
		return fmt.Sprintf("%s修复完成", label)
	case strings.HasSuffix(item.Event, "_ok") && step != "":
		return fmt.Sprintf("%s完成%s", label, tailDuration(item.Payload, "duration_ms", colorizeLabel))
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

func sectionLabel(payload map[string]any, colorizeLabel bool) string {
	step := stringPayload(payload, "step")
	if label := stringPayload(payload, "label"); strings.TrimSpace(label) != "" {
		return formatBaseLabelWithStep(strings.TrimSpace(label), step, colorizeLabel)
	}
	if label := stringPayload(payload, "display"); strings.TrimSpace(label) != "" {
		return formatBaseLabelWithStep(strings.TrimSpace(label), step, colorizeLabel)
	}
	if step != "" {
		// 只有规则中定义的 display_labels 才高亮；无 label 时不着色。
		return stepLabel(step)
	}
	section := stringPayload(payload, "section")
	if section == "" {
		return "步骤"
	}
	return stepLabel(section)
}

func runtimeSectionLabel(payload map[string]any, colorizeLabel bool) string {
	switch strings.TrimSpace(stringPayload(payload, "section")) {
	case "title":
		return colorLabel("标题", colorizeLabel)
	case "bullets":
		return colorLabel("五点描述", colorizeLabel)
	case "description":
		return colorLabel("产品描述", colorizeLabel)
	default:
		return sectionLabel(payload, colorizeLabel)
	}
}

func runtimeCandidateLabel(payload map[string]any) string {
	if candidateIndex := intPayload(payload, "candidate_index"); candidateIndex > 0 {
		return fmt.Sprintf(" [候选#%d] ", candidateIndex)
	}
	step := strings.TrimSpace(stringPayload(payload, "step"))
	if step == "" {
		return ""
	}
	matched := runtimeCandidateStepPattern.FindStringSubmatch(step)
	if len(matched) != 2 {
		return ""
	}
	candidateIndex, err := strconv.Atoi(matched[1])
	if err != nil || candidateIndex <= 0 {
		return ""
	}
	return fmt.Sprintf(" [候选#%d] ", candidateIndex)
}

func formatBaseLabelWithStep(baseLabel, step string, colorizeLabel bool) string {
	base := colorLabel(baseLabel, colorizeLabel)
	if step == "" {
		return base
	}
	if strings.HasPrefix(step, "translate_") {
		return base + "翻译"
	}
	if round, ok := judgeRoundOfStep(step); ok {
		return fmt.Sprintf("%s一致性修复（第%d轮）", base, round)
	}
	if strings.HasSuffix(step, "_whole_repair") {
		return base + "整段修复"
	}
	return base
}

func judgeRoundOfStep(step string) (int, bool) {
	parts := strings.Split(step, "_")
	if len(parts) != 5 {
		return 0, false
	}
	if parts[1] != "judge" || parts[2] != "repair" || parts[3] != "round" {
		return 0, false
	}
	round, err := strconv.Atoi(parts[4])
	if err != nil || round <= 0 {
		return 0, false
	}
	return round, true
}

func colorLabel(label string, enabled bool) string {
	if !enabled {
		return label
	}
	if strings.TrimSpace(label) == "" {
		return label
	}
	return "\x1b[92m" + label + "\x1b[0m"
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

func runtimeCandidateScores(payload map[string]any) []string {
	v, ok := payload["candidates"]
	if !ok || v == nil {
		return nil
	}
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	scores := make([]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		candidateIndex := intPayload(m, "candidate_index")
		if candidateIndex <= 0 {
			continue
		}
		if reason := strings.TrimSpace(stringPayload(m, "failure_reason")); reason != "" {
			scores = append(scores, fmt.Sprintf("#%d失败(%s)", candidateIndex, summarizeCandidateFailure(reason)))
			continue
		}
		score := intPayload(m, "score")
		scores = append(scores, fmt.Sprintf("#%d=%d", candidateIndex, score))
	}
	return scores
}

func summarizeCandidateFailure(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "未知错误"
	}
	reason = strings.TrimPrefix(reason, "section agent team validation failed: ")
	reason = strings.TrimPrefix(reason, "validation failed: ")
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "未知错误"
	}
	parts := splitCandidateFailureReasons(reason)
	if len(parts) > 1 {
		summaries := make([]string, 0, len(parts))
		for _, part := range parts {
			summary := summarizeSingleCandidateFailure(part)
			if summary != "" {
				summaries = append(summaries, summary)
			}
		}
		if len(summaries) > 0 {
			return shortText(strings.Join(summaries, "；"), 72)
		}
	}
	return summarizeSingleCandidateFailure(reason)
}

func summarizeRetryError(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "未知错误"
	}
	if strings.Contains(reason, "validation failed") || strings.Contains(reason, "长度不满足约束") || strings.Contains(reason, "关键词") {
		return summarizeCandidateFailure(reason)
	}
	return shortText(reason, 72)
}

func summarizeSingleCandidateFailure(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "未知错误"
	}
	if matched := lineLengthConstraintPattern.FindStringSubmatch(reason); len(matched) == 7 {
		actual, _ := strconv.Atoi(matched[2])
		tolMin, _ := strconv.Atoi(matched[5])
		tolMax, _ := strconv.Atoi(matched[6])
		if actual < tolMin {
			return fmt.Sprintf("第%s条长度不足: %d<%d", matched[1], actual, tolMin)
		}
		return fmt.Sprintf("第%s条长度超限: %d>%d", matched[1], actual, tolMax)
	}
	if matched := textLengthConstraintPattern.FindStringSubmatch(reason); len(matched) == 6 {
		actual, _ := strconv.Atoi(matched[1])
		tolMin, _ := strconv.Atoi(matched[4])
		tolMax, _ := strconv.Atoi(matched[5])
		if actual < tolMin {
			return fmt.Sprintf("长度不足: %d<%d", actual, tolMin)
		}
		return fmt.Sprintf("长度超限: %d>%d", actual, tolMax)
	}
	if matched := keywordOrderPattern.FindStringSubmatch(reason); len(matched) == 3 {
		return fmt.Sprintf("关键词顺序错误: 第%s个 %s", matched[1], strings.TrimSpace(matched[2]))
	}
	return shortText(formatValidationError(reason), 48)
}

func splitCandidateFailureReasons(reason string) []string {
	fields := strings.FieldsFunc(reason, func(r rune) bool {
		return r == ';' || r == '；'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
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

func errorPreviewMultiline(payload map[string]any) string {
	errs := allErrors(payload)
	if len(errs) == 0 {
		return firstError(payload)
	}
	formatted := make([]string, 0, len(errs))
	for _, errText := range errs {
		formatted = append(formatted, formatValidationError(errText))
	}
	return "\n           " + strings.Join(formatted, "；\n           ")
}

func formatValidationError(errText string) string {
	errText = strings.TrimSpace(errText)
	if errText == "" {
		return "未知错误"
	}
	if matched := lineLengthConstraintPattern.FindStringSubmatch(errText); len(matched) == 7 {
		return fmt.Sprintf("第%s条长度不满足约束: %s", matched[1], formatLengthConstraintRange(matched[2], matched[3], matched[4], matched[5], matched[6]))
	}
	if matched := textLengthConstraintPattern.FindStringSubmatch(errText); len(matched) == 6 {
		return fmt.Sprintf("长度不满足约束: %s", formatLengthConstraintRange(matched[1], matched[2], matched[3], matched[4], matched[5]))
	}
	return errText
}

func formatLengthConstraintRange(actualStr, ruleMinStr, ruleMaxStr, tolMinStr, tolMaxStr string) string {
	actual, err1 := strconv.Atoi(actualStr)
	ruleMin, err2 := strconv.Atoi(ruleMinStr)
	ruleMax, err3 := strconv.Atoi(ruleMaxStr)
	tolMin, err4 := strconv.Atoi(tolMinStr)
	tolMax, err5 := strconv.Atoi(tolMaxStr)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		return fmt.Sprintf("%s ? [%s[%s,%s]%s]", actualStr, tolMinStr, ruleMinStr, ruleMaxStr, tolMaxStr)
	}
	if actual < tolMin {
		return fmt.Sprintf("%d < [%d[%d,%d]%d] 低于下限", actual, tolMin, ruleMin, ruleMax, tolMax)
	}
	return fmt.Sprintf("[%d[%d,%d]%d] < %d 高于上限", tolMin, ruleMin, ruleMax, tolMax, actual)
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

func tailDuration(payload map[string]any, key string, colorize bool) string {
	d := durationLabel(payload, key)
	if d == "-" || strings.TrimSpace(d) == "" {
		return ""
	}
	if colorize {
		return " " + "\x1b[90m" + d + "\x1b[0m"
	}
	return " " + d
}

func shortText(s string, n int) string {
	runes := []rune(s)
	if n <= 0 || len(runes) <= n {
		return s
	}
	return strings.TrimSpace(string(runes[:n])) + "..."
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

func mustAbsPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
