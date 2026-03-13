package input

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RequirementFile struct {
	Path    string
	Content string
}

func Discover(inputs []string) ([]RequirementFile, error) {
	var out []RequirementFile
	seen := map[string]struct{}{}
	for _, in := range inputs {
		info, err := os.Stat(in)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			err := filepath.WalkDir(in, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !isMarkdownFile(path) {
					return nil
				}
				return appendRequirementFile(path, seen, &out)
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if err := appendRequirementFile(in, seen, &out); err != nil {
			return nil, err
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未发现 markdown 输入文件")
	}
	return out, nil
}

func appendRequirementFile(path string, seen map[string]struct{}, out *[]RequirementFile) error {
	if _, exists := seen[path]; exists {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	seen[path] = struct{}{}
	*out = append(*out, RequirementFile{
		Path:    path,
		Content: string(content),
	})
	return nil
}

func isMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown":
		return true
	default:
		return false
	}
}
