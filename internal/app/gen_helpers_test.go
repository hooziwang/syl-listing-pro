package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syl-listing-pro/internal/client"
)

func TestSkipTraceHelpers(t *testing.T) {
	if !shouldSkipVerboseHTTPTrace(false, client.TraceEvent{}) {
		t.Fatal("non-verbose should skip")
	}
	if !shouldSkipVerboseHTTPTrace(true, client.TraceEvent{Method: "GET", URL: "https://x/v1/jobs/1"}) {
		t.Fatal("GET /v1/jobs should skip")
	}
	if shouldSkipVerboseHTTPTrace(true, client.TraceEvent{Method: "POST", URL: "https://x/v1/jobs/1"}) {
		t.Fatal("POST should not skip")
	}

	if !shouldSkipVerboseWorkerTrace(client.JobTraceItem{Source: "api", Event: "job_status_read"}) {
		t.Fatal("job_status_read should skip")
	}
	if shouldSkipVerboseWorkerTrace(client.JobTraceItem{Source: "engine", Event: "job_status_read"}) {
		t.Fatal("non-api should not skip")
	}
}

func TestRenderWorkerTraceLine(t *testing.T) {
	if got := renderWorkerTraceLine(client.JobTraceItem{Source: "api", Event: "job_status_read"}, false); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}

	it := client.JobTraceItem{Event: "generate_queued", JobID: "job_1", Payload: map[string]any{}}
	if got := renderWorkerTraceLine(it, false); got != "任务已加入队列 job_1" {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "rules_loaded", Payload: map[string]any{"rules_version": "v1"}}
	if got := renderWorkerTraceLine(it, false); got != "规则已加载 v1" {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "section_generate_ok", Payload: map[string]any{"label": "标题", "step": "title_attempt_1", "duration_ms": 1540}}
	got := renderWorkerTraceLine(it, false)
	if !strings.Contains(got, "标题已生成") || !strings.Contains(got, "1.54s") {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "section_generate_ok", Payload: map[string]any{"label": "标题", "step": "title_judge_repair_round_2", "duration_ms": 300}}
	got = renderWorkerTraceLine(it, false)
	if !strings.Contains(got, "标题一致性修复（第2轮）完成") {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "job_retry_scheduled", Payload: map[string]any{"attempt": 1, "max_attempts": 3, "next_attempt": 2, "error": strings.Repeat("e", 140)}}
	got = renderWorkerTraceLine(it, false)
	if !strings.Contains(got, "任务重试计划：第 1/3 次失败") || !strings.Contains(got, "...") {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "job_succeeded", Payload: map[string]any{"duration_ms": 61000}}
	if got = renderWorkerTraceLine(it, false); !strings.Contains(got, "执行完成") || !strings.Contains(got, "1.02m") {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "generation_ok", Payload: map[string]any{"timing_ms": 1000}}
	if got = renderWorkerTraceLine(it, false); got != "生成阶段完成 1.00s" {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "x", Payload: map[string]any{"message": "  hello  "}}
	if got = renderWorkerTraceLine(it, false); got != "hello" {
		t.Fatalf("got=%q", got)
	}
}

func TestGenericWorkerTraceLine(t *testing.T) {
	it := client.JobTraceItem{Event: "abc_repair_needed", Payload: map[string]any{"label": "五点描述", "errors": []any{"e1", "e2"}}}
	got := genericWorkerTraceLine(it, false)
	if !strings.Contains(got, "五点描述规则校验失败") || !strings.Contains(got, "e1") {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "abc_validate_fail", Payload: map[string]any{"label": "标题", "errors": []string{"e1"}}}
	if got = genericWorkerTraceLine(it, false); !strings.Contains(got, "标题规则校验失败") {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "title_repair_ok", Payload: map[string]any{"label": "标题"}}
	if got = genericWorkerTraceLine(it, false); got != "标题修复完成" {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "title_ok", Payload: map[string]any{"label": "标题", "step": "title_attempt_1", "duration_ms": 10}}
	if got = genericWorkerTraceLine(it, false); got != "标题完成 10ms" {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "section_failed", Payload: map[string]any{"error": "boom"}}
	if got = genericWorkerTraceLine(it, false); got != "section failed失败：boom" {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "custom", Payload: map[string]any{"error": "bad"}}
	if got = genericWorkerTraceLine(it, false); got != "custom：bad" {
		t.Fatalf("got=%q", got)
	}

	it = client.JobTraceItem{Event: "unknown", Payload: map[string]any{}}
	if got = genericWorkerTraceLine(it, false); got != "" {
		t.Fatalf("got=%q", got)
	}
}

func TestLabelsAndSteps(t *testing.T) {
	if got := tracePrefix("demo", 65_000); got != "demo:01:05" {
		t.Fatalf("got=%q", got)
	}
	if got := tracePrefix("", -1); got != "-:00:00" {
		t.Fatalf("got=%q", got)
	}
	if got := tracePrefix("demo", 3_661_000); got != "demo:01:01:01" {
		t.Fatalf("got=%q", got)
	}

	if got := stepLabel(""); got != "任务步骤" {
		t.Fatalf("got=%q", got)
	}
	if got := stepLabel("translate_title"); got != "title翻译" {
		t.Fatalf("got=%q", got)
	}
	if got := stepLabel("title_attempt_2"); got != "title" {
		t.Fatalf("got=%q", got)
	}
	if got := stepLabel("title_whole_repair"); got != "title整段修复" {
		t.Fatalf("got=%q", got)
	}
	if got := stepLabel("custom_value"); got != "custom value" {
		t.Fatalf("got=%q", got)
	}

	if l, ok := judgeRoundStepLabel("title_judge_repair_round_2"); !ok || l != "title一致性修复（第2轮）" {
		t.Fatalf("label=%q ok=%v", l, ok)
	}
	if _, ok := judgeRoundStepLabel("bad_round"); ok {
		t.Fatal("expected false")
	}

	if r, ok := judgeRoundOfStep("title_judge_repair_round_3"); !ok || r != 3 {
		t.Fatalf("round=%d ok=%v", r, ok)
	}
	if _, ok := judgeRoundOfStep("title_judge_repair_round_x"); ok {
		t.Fatal("expected false")
	}

	if got := sectionLabel(map[string]any{"label": "标题", "step": "translate_title"}, false); got != "标题翻译" {
		t.Fatalf("got=%q", got)
	}
	if got := sectionLabel(map[string]any{"display": "分类", "step": "category_judge_repair_round_1"}, false); got != "分类一致性修复（第1轮）" {
		t.Fatalf("got=%q", got)
	}
	if got := sectionLabel(map[string]any{"step": "translate_bullets"}, false); got != "bullets翻译" {
		t.Fatalf("got=%q", got)
	}
	if got := sectionLabel(map[string]any{"section": "description"}, false); got != "description" {
		t.Fatalf("got=%q", got)
	}
	if got := sectionLabel(map[string]any{}, false); got != "步骤" {
		t.Fatalf("got=%q", got)
	}

	colored := colorLabel("标题", true)
	if !strings.Contains(colored, "\x1b[92m") {
		t.Fatalf("not colored: %q", colored)
	}
	if got := colorLabel("", true); got != "" {
		t.Fatalf("got=%q", got)
	}
}

func TestPayloadAndErrorsHelpers(t *testing.T) {
	p := map[string]any{"a": float64(2), "b": int64(3), "c": 4, "d": "x"}
	if intPayload(p, "a") != 2 || intPayload(p, "b") != 3 || intPayload(p, "c") != 4 || intPayload(p, "d") != 0 {
		t.Fatalf("intPayload unexpected")
	}
	if stringPayload(p, "d") != "x" || stringPayload(p, "a") != "2" || stringPayload(p, "none") != "" {
		t.Fatalf("stringPayload unexpected")
	}

	if got := firstError(map[string]any{}); got != "未知错误" {
		t.Fatalf("got=%q", got)
	}
	if got := firstError(map[string]any{"errors": []any{"e1"}}); got != "e1" {
		t.Fatalf("got=%q", got)
	}

	errPayload := map[string]any{"errors": []any{"e1", " e2 "}}
	errList := allErrors(errPayload)
	if len(errList) != 2 || errList[1] != "e2" {
		t.Fatalf("allErrors=%v", errList)
	}
	if got := errorCountLabel(errPayload); got != "2条" {
		t.Fatalf("got=%q", got)
	}
	if got := errorCountLabel(map[string]any{}); got != "1条" {
		t.Fatalf("got=%q", got)
	}

	if got := errorPreview(errPayload, 1); got != "e1；...（其余1条）" {
		t.Fatalf("got=%q", got)
	}
	if got := errorPreview(errPayload, 0); got != "e1；e2" {
		t.Fatalf("got=%q", got)
	}

	multiline := errorPreviewMultiline(map[string]any{"errors": []any{"第1条长度不满足约束: 166（规则区间 [235,300]，容差区间 [215,320]）", "x"}})
	if !strings.Contains(multiline, "166 < [215[235,300]320] 低于下限") || !strings.Contains(multiline, "\n           x") {
		t.Fatalf("multiline=%q", multiline)
	}

	if got := formatValidationError("长度不满足约束: 1718（规则区间 [450,1500]，容差区间 [430,1520]）"); got != "长度不满足约束: [430[450,1500]1520] < 1718 高于上限" {
		t.Fatalf("got=%q", got)
	}
	if got := formatLengthConstraintRange("bad", "x", "y", "z", "w"); got != "bad ? [z[x,y]w]" {
		t.Fatalf("got=%q", got)
	}

	if got := targetsLabel(map[string]any{"targets": []any{1.0, "2", int64(3)}}); got != "1,2,3" {
		t.Fatalf("got=%q", got)
	}
	if got := targetsLabel(map[string]any{"targets": []string{"a", "b"}}); got != "a,b" {
		t.Fatalf("got=%q", got)
	}
	if got := targetsLabel(map[string]any{}); got != "-" {
		t.Fatalf("got=%q", got)
	}
}

func TestDurationAndTextHelpers(t *testing.T) {
	if got := durationLabel(map[string]any{"d": 10}, "d"); got != "10ms" {
		t.Fatalf("got=%q", got)
	}
	if got := durationLabel(map[string]any{"d": 1500}, "d"); got != "1.50s" {
		t.Fatalf("got=%q", got)
	}
	if got := durationLabel(map[string]any{"d": 60000}, "d"); got != "1.00m" {
		t.Fatalf("got=%q", got)
	}
	if got := durationLabel(map[string]any{}, "d"); got != "-" {
		t.Fatalf("got=%q", got)
	}

	if got := tailDuration(map[string]any{"d": 1}, "d", false); got != " 1ms" {
		t.Fatalf("got=%q", got)
	}
	if got := tailDuration(map[string]any{"d": 1}, "d", true); !strings.Contains(got, "\x1b[90m") {
		t.Fatalf("got=%q", got)
	}
	if got := tailDuration(map[string]any{}, "d", false); got != "" {
		t.Fatalf("got=%q", got)
	}

	if got := shortText("abcdef", 4); got != "abcd..." {
		t.Fatalf("got=%q", got)
	}
	if got := shortText("abc", 4); got != "abc" {
		t.Fatalf("got=%q", got)
	}
	if got := shortText("abc", 0); got != "abc" {
		t.Fatalf("got=%q", got)
	}

	if got := humanDurationShort(1500 * time.Millisecond); got != "2s" {
		t.Fatalf("got=%q", got)
	}
	if got := humanDurationShort(65 * time.Second); got != "1m5s" {
		t.Fatalf("got=%q", got)
	}
	if got := humanDurationShort(3665 * time.Second); got != "1h1m5s" {
		t.Fatalf("got=%q", got)
	}

	rel := "./x"
	abs := mustAbsPath(rel)
	if !filepath.IsAbs(abs) {
		t.Fatalf("want abs path, got %q", abs)
	}
	if got := mustAbsPath(""); got == "" {
		t.Fatalf("expected non-empty for empty input")
	}

	// Keep current directory stable for the abs path assertion above.
	_ = os.Chdir(".")
}
