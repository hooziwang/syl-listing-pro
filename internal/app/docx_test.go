package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable failed: %v", err)
	}
}

func TestConvertMarkdownToDocx_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is unix-only")
	}
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(tmp, "args.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" > '" + argsLog + "'\n" +
		"outfile=\"\"\n" +
		"while [ $# -gt 0 ]; do\n" +
		"  if [ \"$1\" = \"--output\" ]; then\n" +
		"    shift\n" +
		"    outfile=\"$1\"\n" +
		"  fi\n" +
		"  shift\n" +
		"done\n" +
		"mkdir -p \"$(dirname \"$outfile\")\"\n" +
		"echo 'docx' > \"$outfile\"\n" +
		"echo '{\"event\":\"summary\",\"details\":{\"output_path\":\"'\"$outfile\"'\",\"output_paths\":[\"'\"$outfile\"'\"]}}'\n"
	writeExecutable(t, filepath.Join(binDir, "syl-md2doc"), script)

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+oldPath)

	md := filepath.Join(tmp, "in.md")
	if err := os.WriteFile(md, []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(tmp, "out", "in.docx")
	path, err := ConvertMarkdownToDocx(context.Background(), md, outPath)
	if err != nil {
		t.Fatalf("ConvertMarkdownToDocx error: %v", err)
	}
	if path != outPath {
		t.Fatalf("unexpected output path: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected docx file exists: %v", err)
	}
	argText, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argText), "--highlight-words") {
		t.Fatalf("unexpected highlight args: %s", string(argText))
	}
}

func TestConvertMarkdownToDocx_Failure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is unix-only")
	}
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho 'boom'\nexit 1\n"
	writeExecutable(t, filepath.Join(binDir, "syl-md2doc"), script)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	md := filepath.Join(tmp, "in.md")
	_ = os.WriteFile(md, []byte("# x\n"), 0o644)
	_, err := ConvertMarkdownToDocx(context.Background(), md, filepath.Join(tmp, "in.docx"))
	if err == nil || !strings.Contains(err.Error(), "syl-md2doc 执行失败") {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestParseMD2DocOutputPath(t *testing.T) {
	out := []byte("not json\n{\"event\":\"summary\",\"details\":{\"output_path\":\"/tmp/a.docx\"}}\n")
	if got := parseMD2DocOutputPath(out); got != "/tmp/a.docx" {
		t.Fatalf("got=%q", got)
	}
	if got := parseMD2DocOutputPath([]byte("{}\n")); got != "" {
		t.Fatalf("got=%q", got)
	}
}

func TestParseMD2DocOutputPath_OutputPathsFallback(t *testing.T) {
	out := []byte("{\"event\":\"summary\",\"details\":{\"output_paths\":[\"/tmp/fallback.docx\"]}}\n")
	if got := parseMD2DocOutputPath(out); got != "/tmp/fallback.docx" {
		t.Fatalf("got=%q", got)
	}
}

func TestConvertMarkdownToDocx_RenameFromSummaryPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is unix-only")
	}
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	reported := filepath.Join(tmp, "raw", "x.docx")
	script := "#!/bin/sh\n" +
		"mkdir -p '" + filepath.Dir(reported) + "'\n" +
		"echo 'docx' > '" + reported + "'\n" +
		"echo '{\"event\":\"summary\",\"details\":{\"output_path\":\"" + reported + "\"}}'\n"
	writeExecutable(t, filepath.Join(binDir, "syl-md2doc"), script)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	md := filepath.Join(tmp, "in.md")
	if err := os.WriteFile(md, []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(tmp, "want", "final.docx")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ConvertMarkdownToDocx(context.Background(), md, target)
	if err != nil {
		t.Fatalf("ConvertMarkdownToDocx error: %v", err)
	}
	if got != target {
		t.Fatalf("got=%q want=%q", got, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target docx missing: %v", err)
	}
	if _, err := os.Stat(reported); !os.IsNotExist(err) {
		t.Fatalf("reported file should be renamed away, err=%v", err)
	}
}

func TestConvertMarkdownToDocx_NoOutputPathAndMissingTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script test is unix-only")
	}
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho '{\"event\":\"summary\",\"details\":{}}'\n"
	writeExecutable(t, filepath.Join(binDir, "syl-md2doc"), script)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	md := filepath.Join(tmp, "in.md")
	_ = os.WriteFile(md, []byte("# x\n"), 0o644)
	_, err := ConvertMarkdownToDocx(context.Background(), md, filepath.Join(tmp, "no", "out.docx"))
	if err == nil || !strings.Contains(err.Error(), "未返回输出路径") {
		t.Fatalf("unexpected err: %v", err)
	}
}
