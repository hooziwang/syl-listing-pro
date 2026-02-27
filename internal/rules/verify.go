package rules

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func VerifyArchiveSignatureWithBundledKeyOpenSSL(
	cacheDir string,
	archivePath string,
	archiveSignatureBase64 string,
	signingPublicKeyPathInArchive string,
	signingPublicKeySignatureBase64 string,
) error {
	if strings.TrimSpace(archiveSignatureBase64) == "" {
		return fmt.Errorf("规则签名缺失")
	}
	if strings.TrimSpace(signingPublicKeyPathInArchive) == "" {
		return fmt.Errorf("规则签名公钥路径缺失")
	}
	if strings.TrimSpace(signingPublicKeySignatureBase64) == "" {
		return fmt.Errorf("规则签名公钥签名缺失")
	}

	rootPublicPath, err := ensureRootPublicKey(cacheDir)
	if err != nil {
		return err
	}

	signingPublicKeyBytes, err := extractFileFromTarGz(archivePath, signingPublicKeyPathInArchive)
	if err != nil {
		return err
	}
	tmpSigningPublicKey := filepath.Join(cacheDir, ".rules_signing_public.pem.tmp")
	if err := os.WriteFile(tmpSigningPublicKey, signingPublicKeyBytes, 0o644); err != nil {
		return fmt.Errorf("写签名公钥临时文件失败: %w", err)
	}
	defer os.Remove(tmpSigningPublicKey)

	keySig, err := base64.StdEncoding.DecodeString(signingPublicKeySignatureBase64)
	if err != nil {
		return fmt.Errorf("解析签名公钥签名失败: %w", err)
	}
	tmpKeySig := filepath.Join(cacheDir, ".rules_signing_public.sig.tmp")
	if err := os.WriteFile(tmpKeySig, keySig, 0o600); err != nil {
		return fmt.Errorf("写签名公钥签名临时文件失败: %w", err)
	}
	defer os.Remove(tmpKeySig)

	if err := verifyWithOpenSSL(rootPublicPath, tmpSigningPublicKey, tmpKeySig); err != nil {
		return fmt.Errorf("规则签名公钥验签失败: %w", err)
	}

	archiveSig, err := base64.StdEncoding.DecodeString(archiveSignatureBase64)
	if err != nil {
		return fmt.Errorf("解析规则包签名失败: %w", err)
	}
	tmpArchiveSig := filepath.Join(cacheDir, ".rules_archive.sig.tmp")
	if err := os.WriteFile(tmpArchiveSig, archiveSig, 0o600); err != nil {
		return fmt.Errorf("写规则包签名临时文件失败: %w", err)
	}
	defer os.Remove(tmpArchiveSig)

	if err := verifyWithOpenSSL(tmpSigningPublicKey, archivePath, tmpArchiveSig); err != nil {
		return fmt.Errorf("规则包验签失败: %w", err)
	}
	return nil
}

func ensureRootPublicKey(cacheDir string) (string, error) {
	if len(bytes.TrimSpace(embeddedRootPublicKeyPEM)) == 0 {
		return "", fmt.Errorf("内置根公钥为空")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("创建规则缓存目录失败: %w", err)
	}
	path := filepath.Join(cacheDir, "rules_root_public.pem")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, embeddedRootPublicKeyPEM) {
		return path, nil
	}
	if err := os.WriteFile(path, embeddedRootPublicKeyPEM, 0o644); err != nil {
		return "", fmt.Errorf("写入根公钥失败: %w", err)
	}
	return path, nil
}

func extractFileFromTarGz(archivePath, targetPath string) ([]byte, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("打开规则包失败: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("读取规则包 gzip 失败: %w", err)
	}
	defer gz.Close()

	target := strings.TrimPrefix(strings.TrimSpace(targetPath), "/")
	if target == "" {
		return nil, fmt.Errorf("规则签名公钥路径无效")
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取规则包 tar 失败: %w", err)
		}
		name := strings.TrimPrefix(strings.TrimSpace(hdr.Name), "/")
		if name != target {
			continue
		}
		if hdr.FileInfo().IsDir() {
			return nil, fmt.Errorf("规则签名公钥路径不是文件: %s", targetPath)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("读取规则签名公钥失败: %w", err)
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return nil, fmt.Errorf("规则签名公钥为空: %s", targetPath)
		}
		return b, nil
	}
	return nil, fmt.Errorf("规则包内未找到签名公钥: %s", targetPath)
}

func verifyWithOpenSSL(publicKeyPath, targetPath, signaturePath string) error {
	cmd := exec.Command("openssl", "dgst", "-sha256", "-verify", publicKeyPath, "-signature", signaturePath, targetPath)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v %s", err, string(b))
	}
	return nil
}
