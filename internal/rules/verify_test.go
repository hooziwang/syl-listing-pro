package rules

import (
	"archive/tar"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeArchiveForVerify(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnsureRootPublicKey(t *testing.T) {
	cache := t.TempDir()
	p, err := ensureRootPublicKey(cache)
	if err != nil {
		t.Fatalf("ensureRootPublicKey error: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(embeddedRootPublicKeyPEM) {
		t.Fatal("root public key content mismatch")
	}

	p2, err := ensureRootPublicKey(cache)
	if err != nil {
		t.Fatalf("ensureRootPublicKey second error: %v", err)
	}
	if p2 != p {
		t.Fatalf("path changed: %s vs %s", p2, p)
	}
}

func TestExtractFileFromTarGz(t *testing.T) {
	archive := writeArchiveForVerify(t, "tenant/pub.pem", []byte("PEM"))
	b, err := extractFileFromTarGz(archive, "tenant/pub.pem")
	if err != nil {
		t.Fatalf("extractFileFromTarGz error: %v", err)
	}
	if string(b) != "PEM" {
		t.Fatalf("content=%q", string(b))
	}

	if _, err := extractFileFromTarGz(archive, ""); err == nil {
		t.Fatal("expected empty target error")
	}
	if _, err := extractFileFromTarGz(archive, "tenant/notfound.pem"); err == nil {
		t.Fatal("expected not found error")
	}

	emptyArchive := writeArchiveForVerify(t, "tenant/empty.pem", []byte("   \n\t"))
	if _, err := extractFileFromTarGz(emptyArchive, "tenant/empty.pem"); err == nil {
		t.Fatal("expected empty key error")
	}
}

func mustSignSHA256(t *testing.T, priv *rsa.PrivateKey, payload []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(payload)
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign failed: %v", err)
	}
	return sig
}

func mustRSAPublicKeyPEM(t *testing.T, pub *rsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func TestVerifyArchiveSignatureWithBundledKeyOpenSSL(t *testing.T) {
	work := t.TempDir()
	cacheDir := filepath.Join(work, "cache")
	archiveSigPath := filepath.Join(work, "archive.sig")

	rootPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate root key failed: %v", err)
	}
	signPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key failed: %v", err)
	}
	rootPubPEM := mustRSAPublicKeyPEM(t, &rootPriv.PublicKey)
	signPubPEM := mustRSAPublicKeyPEM(t, &signPriv.PublicKey)

	archivePath := writeArchiveForVerify(t, "tenant/keys/signing_public.pem", signPubPEM)
	archiveBytes := mustRead(t, archivePath)

	keySig := mustSignSHA256(t, rootPriv, signPubPEM)
	archiveSig := mustSignSHA256(t, signPriv, archiveBytes)
	if err := os.WriteFile(archiveSigPath, archiveSig, 0o644); err != nil {
		t.Fatalf("write archive sig failed: %v", err)
	}

	oldRoot := embeddedRootPublicKeyPEM
	embeddedRootPublicKeyPEM = rootPubPEM
	defer func() { embeddedRootPublicKeyPEM = oldRoot }()

	keySigB64 := base64.StdEncoding.EncodeToString(keySig)
	archiveSigB64 := base64.StdEncoding.EncodeToString(archiveSig)

	if err := VerifyArchiveSignatureWithBundledKeyOpenSSL(
		cacheDir,
		archivePath,
		archiveSigB64,
		"tenant/keys/signing_public.pem",
		keySigB64,
	); err != nil {
		t.Fatalf("VerifyArchiveSignatureWithBundledKeyOpenSSL error: %v", err)
	}

	if err := VerifyArchiveSignatureWithBundledKeyOpenSSL(cacheDir, archivePath, "", "tenant/keys/signing_public.pem", keySigB64); err == nil {
		t.Fatal("expected missing archive signature error")
	}
	badArchiveSig := archiveSigB64[:len(archiveSigB64)-4] + "AAAA"
	err = VerifyArchiveSignatureWithBundledKeyOpenSSL(
		cacheDir,
		archivePath,
		badArchiveSig,
		"tenant/keys/signing_public.pem",
		keySigB64,
	)
	if err == nil || !strings.Contains(err.Error(), "规则包验签失败") {
		t.Fatalf("expected archive verify error, got: %v", err)
	}

	if err := verifyWithOpenSSL(filepath.Join(work, "missing.pem"), archivePath, archiveSigPath); err == nil {
		t.Fatal("expected verifyWithOpenSSL error for missing key")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", path)
	}
	return b
}

func TestVerifyArchiveSignatureWithBundledKeyOpenSSL_FieldValidation(t *testing.T) {
	err := VerifyArchiveSignatureWithBundledKeyOpenSSL(t.TempDir(), "x.tar.gz", "abc", "", "sig")
	if err == nil || !strings.Contains(err.Error(), "规则签名公钥路径缺失") {
		t.Fatalf("unexpected err: %v", err)
	}
	err = VerifyArchiveSignatureWithBundledKeyOpenSSL(t.TempDir(), "x.tar.gz", "abc", "a.pem", "")
	if err == nil || !strings.Contains(err.Error(), "规则签名公钥签名缺失") {
		t.Fatalf("unexpected err: %v", err)
	}
}
