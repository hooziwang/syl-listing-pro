package input

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const Marker = "===Listing Requirements==="

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
				if d.IsDir() {
					return nil
				}
				ok, content, err := readIfRequirement(path)
				if err != nil {
					return err
				}
				if ok {
					if _, exists := seen[path]; !exists {
						seen[path] = struct{}{}
						out = append(out, RequirementFile{Path: path, Content: content})
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		ok, content, err := readIfRequirement(in)
		if err != nil {
			return nil, err
		}
		if ok {
			if _, exists := seen[in]; !exists {
				seen[in] = struct{}{}
				out = append(out, RequirementFile{Path: in, Content: content})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未发现 listing 要求文件（首行需为 %s）", Marker)
	}
	return out, nil
}

func readIfRequirement(path string) (bool, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	line1, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, "", err
	}
	if strings.TrimSpace(strings.TrimPrefix(line1, "\ufeff")) != Marker {
		return false, "", nil
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		return false, "", err
	}
	return true, string(rest), nil
}
