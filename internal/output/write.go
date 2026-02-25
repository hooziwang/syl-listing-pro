package output

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
)

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func random8() (string, error) {
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
}

func UniquePair(outDir string) (string, string, string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", "", err
	}
	for i := 0; i < 100; i++ {
		s, err := random8()
		if err != nil {
			return "", "", "", err
		}
		en := filepath.Join(outDir, fmt.Sprintf("listing_%s_en.md", s))
		cn := filepath.Join(outDir, fmt.Sprintf("listing_%s_cn.md", s))
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
