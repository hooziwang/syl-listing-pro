package rules

import (
	"archive/tar"
	"compress/gzip"
	"encoding/base64"
	"os"
	"os/exec"
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

func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run %s %v failed: %v, out=%s", name, args, err, string(out))
	}
}

func TestVerifyArchiveSignatureWithBundledKeyOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not found")
	}

	work := t.TempDir()
	cacheDir := filepath.Join(work, "cache")
	rootPriv := filepath.Join(work, "root_priv.pem")
	rootPub := filepath.Join(work, "root_pub.pem")
	signPriv := filepath.Join(work, "sign_priv.pem")
	signPub := filepath.Join(work, "sign_pub.pem")
	signPubSig := filepath.Join(work, "sign_pub.sig")
	archiveSigPath := filepath.Join(work, "archive.sig")

	mustRun(t, "openssl", "genpkey", "-algorithm", "RSA", "-out", rootPriv, "-pkeyopt", "rsa_keygen_bits:2048")
	mustRun(t, "openssl", "rsa", "-in", rootPriv, "-pubout", "-out", rootPub)
	mustRun(t, "openssl", "genpkey", "-algorithm", "RSA", "-out", signPriv, "-pkeyopt", "rsa_keygen_bits:2048")
	mustRun(t, "openssl", "rsa", "-in", signPriv, "-pubout", "-out", signPub)

	archivePath := writeArchiveForVerify(t, "tenant/keys/signing_public.pem", mustRead(t, signPub))

	mustRun(t, "openssl", "dgst", "-sha256", "-sign", rootPriv, "-out", signPubSig, signPub)
	mustRun(t, "openssl", "dgst", "-sha256", "-sign", signPriv, "-out", archiveSigPath, archivePath)

	rootPubBytes := mustRead(t, rootPub)
	oldRoot := embeddedRootPublicKeyPEM
	embeddedRootPublicKeyPEM = rootPubBytes
	defer func() { embeddedRootPublicKeyPEM = oldRoot }()

	keySigB64 := base64.StdEncoding.EncodeToString(mustRead(t, signPubSig))
	archiveSigB64 := base64.StdEncoding.EncodeToString(mustRead(t, archiveSigPath))

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
	err := VerifyArchiveSignatureWithBundledKeyOpenSSL(
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
