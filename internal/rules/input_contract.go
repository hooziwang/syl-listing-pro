package rules

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const inputContractPathInArchive = "tenant/rules/input.yaml"

type inputContractDoc struct {
	FileDiscovery struct {
		Marker string `yaml:"marker"`
	} `yaml:"file_discovery"`
}

func LoadInputMarkerFromArchive(archivePath string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("读取规则包失败: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("解析规则包失败: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("读取规则包内容失败: %w", err)
		}
		if hdr == nil || hdr.Typeflag != tar.TypeReg {
			continue
		}
		if strings.TrimSpace(hdr.Name) != inputContractPathInArchive {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 2<<20))
		if err != nil {
			return "", fmt.Errorf("读取 input.yaml 失败: %w", err)
		}
		var doc inputContractDoc
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return "", fmt.Errorf("解析 input.yaml 失败: %w", err)
		}
		marker := strings.TrimSpace(doc.FileDiscovery.Marker)
		if marker == "" {
			return "", fmt.Errorf("input.yaml 缺少 file_discovery.marker")
		}
		return marker, nil
	}
	return "", fmt.Errorf("规则包缺少 %s", inputContractPathInArchive)
}
