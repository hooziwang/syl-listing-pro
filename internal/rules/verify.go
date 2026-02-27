package rules

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"os"
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
	rootPublicKeyBytes, err := os.ReadFile(rootPublicPath)
	if err != nil {
		return fmt.Errorf("读取根公钥失败: %w", err)
	}

	signingPublicKeyBytes, err := extractFileFromTarGz(archivePath, signingPublicKeyPathInArchive)
	if err != nil {
		return err
	}

	keySig, err := base64.StdEncoding.DecodeString(signingPublicKeySignatureBase64)
	if err != nil {
		return fmt.Errorf("解析签名公钥签名失败: %w", err)
	}
	if err := verifySignature(rootPublicKeyBytes, signingPublicKeyBytes, keySig); err != nil {
		return fmt.Errorf("规则签名公钥验签失败: %w", err)
	}

	archiveSig, err := base64.StdEncoding.DecodeString(archiveSignatureBase64)
	if err != nil {
		return fmt.Errorf("解析规则包签名失败: %w", err)
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf("读取规则包失败: %w", err)
	}
	if err := verifySignature(signingPublicKeyBytes, archiveBytes, archiveSig); err != nil {
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

func parseRSAPublicKey(publicKeyPEM []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(publicKeyPEM)
	if block == nil {
		return nil, fmt.Errorf("无效 PEM 公钥")
	}
	switch block.Type {
	case "RSA PUBLIC KEY":
		pub, err := x509.ParsePKCS1PublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析 RSA 公钥失败: %w", err)
		}
		return pub, nil
	case "PUBLIC KEY":
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("解析公钥失败: %w", err)
		}
		pub, ok := parsed.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("公钥算法不是 RSA")
		}
		return pub, nil
	default:
		return nil, fmt.Errorf("不支持的公钥类型: %s", block.Type)
	}
}

func verifySignature(publicKeyPEM, payload, signature []byte) error {
	pub, err := parseRSAPublicKey(publicKeyPEM)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], signature); err != nil {
		return err
	}
	return nil
}

// 保留历史函数名，内部改为 Go 原生验签实现，避免依赖 openssl。
func verifyWithOpenSSL(publicKeyPath, targetPath, signaturePath string) error {
	publicKeyBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return fmt.Errorf("读取公钥失败: %w", err)
	}
	payload, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("读取待验签文件失败: %w", err)
	}
	signature, err := os.ReadFile(signaturePath)
	if err != nil {
		return fmt.Errorf("读取签名文件失败: %w", err)
	}
	return verifySignature(publicKeyBytes, payload, signature)
}
