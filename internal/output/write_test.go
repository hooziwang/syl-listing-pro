package output

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUniquePair_Success(t *testing.T) {
	dir := t.TempDir()
	s, en, cn, err := UniquePair(dir)
	if err != nil {
		t.Fatalf("UniquePair error: %v", err)
	}
	if len(s) != 8 {
		t.Fatalf("id len=%d want=8", len(s))
	}
	if !strings.HasPrefix(filepath.Base(en), "listing_") || !strings.HasSuffix(en, "_en.md") {
		t.Fatalf("unexpected en path: %s", en)
	}
	if !strings.HasPrefix(filepath.Base(cn), "listing_") || !strings.HasSuffix(cn, "_cn.md") {
		t.Fatalf("unexpected cn path: %s", cn)
	}
}

func TestUniquePair_MkdirError(t *testing.T) {
	dir := t.TempDir()
	fileAsDir := filepath.Join(dir, "file")
	if err := os.WriteFile(fileAsDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := UniquePair(fileAsDir); err == nil {
		t.Fatal("expected mkdir error")
	}
}

func TestUniquePair_Concurrent_NoCollision(t *testing.T) {
	dir := t.TempDir()
	const n = 80
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]struct{}{}
	errs := make([]error, 0)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, en, cn, err := UniquePair(dir)
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			// 立即创建文件，模拟并发写入阶段，放大潜在冲突。
			_ = os.WriteFile(en, []byte("en"), 0o644)
			_ = os.WriteFile(cn, []byte("cn"), 0o644)
			mu.Lock()
			if _, ok := seen[s]; ok {
				errs = append(errs, os.ErrExist)
			}
			seen[s] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent UniquePair errors=%v", errs)
	}
	if len(seen) != n {
		t.Fatalf("unique id count=%d want=%d", len(seen), n)
	}
}
