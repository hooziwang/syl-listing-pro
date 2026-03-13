package input

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type RequirementFile struct {
	Path    string
	Content string
}

var generatedOutputMarkdownPattern = regexp.MustCompile(`(?i)_[a-z0-9]{4}_((en)|(cn))\.(md|markdown)$`)

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
				if d.IsDir() {
					if shouldSkipDir(d.Name()) && path != in {
						return filepath.SkipDir
					}
					return nil
				}
				if !shouldIncludeMarkdownFile(path, d.Name()) {
					return nil
				}
				return appendRequirementFile(path, seen, &out)
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if !shouldIncludeMarkdownFile(in, filepath.Base(in)) {
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

func shouldSkipDir(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, ".") {
		return true
	}
	return trimmed == "node_modules"
}

func shouldIncludeMarkdownFile(path string, name string) bool {
	base := strings.TrimSpace(name)
	if base == "" {
		base = filepath.Base(path)
	}
	if strings.HasPrefix(base, ".") {
		return false
	}
	if !isMarkdownFile(path) {
		return false
	}
	return !generatedOutputMarkdownPattern.MatchString(strings.ToLower(base))
}
