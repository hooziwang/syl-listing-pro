package rules

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTarGz(t *testing.T, entries map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rules.tar.gz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	for name, body := range entries {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestLoadInputMarkerFromArchive(t *testing.T) {
	archive := writeTarGz(t, map[string]string{
		inputContractPathInArchive: "file_discovery:\n  marker: \"#LISTING\"\n",
	})
	marker, err := LoadInputMarkerFromArchive(archive)
	if err != nil {
		t.Fatalf("LoadInputMarkerFromArchive error: %v", err)
	}
	if marker != "#LISTING" {
		t.Fatalf("marker=%q", marker)
	}
}

func TestLoadInputMarkerFromArchive_Errors(t *testing.T) {
	archiveMissing := writeTarGz(t, map[string]string{"x.txt": "1"})
	if _, err := LoadInputMarkerFromArchive(archiveMissing); err == nil {
		t.Fatal("expected missing file error")
	}

	archiveBadYaml := writeTarGz(t, map[string]string{inputContractPathInArchive: ":::bad:::"})
	if _, err := LoadInputMarkerFromArchive(archiveBadYaml); err == nil {
		t.Fatal("expected yaml parse error")
	}

	archiveNoMarker := writeTarGz(t, map[string]string{inputContractPathInArchive: "file_discovery:\n  marker: \"\"\n"})
	if _, err := LoadInputMarkerFromArchive(archiveNoMarker); err == nil {
		t.Fatal("expected marker missing error")
	}
}
