package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var convertMarkdownToDocxFunc = ConvertMarkdownToDocx

type md2docSummaryLine struct {
	Event   string `json:"event"`
	Details struct {
		OutputPath  string   `json:"output_path"`
		OutputPaths []string `json:"output_paths"`
	} `json:"details"`
}

func ConvertMarkdownToDocx(ctx context.Context, markdownPath string, outputPath string, highlightWords []string) (string, error) {
	targetPath := outputPath
	if abs, err := filepath.Abs(outputPath); err == nil {
		targetPath = abs
	}
	args := []string{markdownPath, "--output", targetPath}
	words := dedupeWords(highlightWords)
	if len(words) > 0 {
		args = append(args, "--highlight-words", strings.Join(words, ","))
	}

	cmd := exec.CommandContext(ctx, "syl-md2doc", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("syl-md2doc 执行失败: %w: %s", err, strings.TrimSpace(shortText(string(out), 300)))
	}

	if _, err := os.Stat(targetPath); err == nil {
		return targetPath, nil
	}

	path := parseMD2DocOutputPath(out)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("syl-md2doc 未返回输出路径且目标文件不存在: %s", strings.TrimSpace(shortText(string(out), 300)))
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if path == targetPath {
		return path, nil
	}
	if err := os.Rename(path, targetPath); err != nil {
		return "", fmt.Errorf("Word 输出文件名不一致，重命名失败: got=%s want=%s: %w", path, targetPath, err)
	}
	return targetPath, nil
}

func parseMD2DocOutputPath(raw []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var s md2docSummaryLine
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			continue
		}
		if strings.TrimSpace(s.Details.OutputPath) != "" {
			return strings.TrimSpace(s.Details.OutputPath)
		}
		if len(s.Details.OutputPaths) > 0 && strings.TrimSpace(s.Details.OutputPaths[0]) != "" {
			return strings.TrimSpace(s.Details.OutputPaths[0])
		}
	}
	return ""
}

func dedupeWords(words []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(words))
	for _, raw := range words {
		w := strings.TrimSpace(raw)
		if w == "" {
			continue
		}
		key := strings.ToLower(w)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, w)
	}
	return out
}
