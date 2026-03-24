package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watch polls for file changes in dir matching exts, calling onChange when detected.
// Changes are debounced — multiple changes within 100ms trigger one callback.
func Watch(ctx context.Context, dir string, exts []string, onChange func(string)) {
	mtimes := snapshot(dir, exts)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		current := snapshot(dir, exts)
		var changed string

		// Detect modifications and new files
		for path, mtime := range current {
			if prev, ok := mtimes[path]; !ok || !mtime.Equal(prev) {
				changed = path
			}
		}
		// Detect deletions
		for path := range mtimes {
			if _, ok := current[path]; !ok {
				changed = path
			}
		}

		if changed != "" {
			onChange(changed)
			// Debounce: wait 100ms and re-snapshot to absorb rapid changes
			time.Sleep(100 * time.Millisecond)
			current = snapshot(dir, exts)
		}
		mtimes = current
	}
}

func snapshot(dir string, exts []string) map[string]time.Time {
	result := map[string]time.Time{}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".venv" || info.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		for _, ext := range exts {
			if strings.HasSuffix(path, ext) {
				result[path] = info.ModTime()
			}
		}
		return nil
	})
	return result
}
