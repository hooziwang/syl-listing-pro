package rules

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func VerifySignatureOpenSSL(cacheDir, publicKeyPath, signatureBase64, archivePath string) error {
	if publicKeyPath == "" || signatureBase64 == "" {
		return nil
	}
	sig, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil {
		return fmt.Errorf("解析签名失败: %w", err)
	}
	tmpSig := filepath.Join(cacheDir, ".sig.tmp")
	defer os.Remove(tmpSig)
	if err := os.WriteFile(tmpSig, sig, 0o600); err != nil {
		return fmt.Errorf("写临时签名文件失败: %w", err)
	}
	cmd := exec.Command("openssl", "dgst", "-sha256", "-verify", publicKeyPath, "-signature", tmpSig, archivePath)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("签名校验失败: %v %s", err, string(b))
	}
	return nil
}
