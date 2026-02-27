package app

import (
	"context"
	"path/filepath"
	"testing"
)

func stubDocxConverter(t *testing.T) {
	t.Helper()
	old := convertMarkdownToDocxFunc
	convertMarkdownToDocxFunc = func(_ context.Context, _ string, outputPath string, _ []string) (string, error) {
		if abs, err := filepath.Abs(outputPath); err == nil {
			return abs, nil
		}
		return outputPath, nil
	}
	t.Cleanup(func() { convertMarkdownToDocxFunc = old })
}
