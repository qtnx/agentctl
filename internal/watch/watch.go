package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
)

func Poll(ctx context.Context, root string, interval time.Duration, excludes []string, onChange func(context.Context) error) error {
	if interval <= 0 {
		interval = time.Second
	}
	previous, err := snapshot(root, excludes)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			next, err := snapshot(root, excludes)
			if err != nil {
				return err
			}
			if next == previous {
				continue
			}
			if err := onChange(ctx); err != nil {
				return err
			}
			previous = next
		}
	}
}

func snapshot(root string, excludes []string) (string, error) {
	root = filepath.Clean(root)
	excluded := excludeSet(excludes)
	hash := sha256.New()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() && excluded[entry.Name()] {
			return filepath.SkipDir
		}
		if excludedPath(rel, excluded) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%d\x00%t\n", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano(), entry.IsDir())
		return nil
	})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func excludeSet(excludes []string) map[string]bool {
	set := map[string]bool{}
	for _, exclude := range excludes {
		exclude = strings.Trim(strings.TrimSpace(exclude), "/")
		if exclude != "" {
			set[exclude] = true
		}
	}
	return set
}

func excludedPath(rel string, excluded map[string]bool) bool {
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if excluded[part] {
			return true
		}
	}
	return false
}
