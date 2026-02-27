package output

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
)

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func randomN(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("随机长度必须大于0")
	}
	b := make([]byte, n)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

func outputBaseName(inputPath string) string {
	base := filepath.Base(strings.TrimSpace(inputPath))
	if base == "" || base == "." {
		return "listing"
	}
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	base = strings.TrimSpace(base)
	if base == "" || base == "." {
		return "listing"
	}
	return base
}

func UniquePair(outDir string, inputPath string) (string, string, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", "", err
	}
	base := outputBaseName(inputPath)
	for i := 0; i < 200; i++ {
		s, err := randomN(4)
		if err != nil {
			return "", "", "", err
		}
		en := filepath.Join(outDir, fmt.Sprintf("%s_%s_en.md", base, s))
		cn := filepath.Join(outDir, fmt.Sprintf("%s_%s_cn.md", base, s))
		if _, err := os.Stat(en); err == nil {
			continue
		}
		if _, err := os.Stat(cn); err == nil {
			continue
		}
		return s, en, cn, nil
	}
	return "", "", "", fmt.Errorf("生成唯一文件名失败")
}
