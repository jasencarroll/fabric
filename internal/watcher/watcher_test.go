package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatcherDetectsChange(t *testing.T) {
	dir := t.TempDir()
	pyFile := filepath.Join(dir, "app.py")
	os.WriteFile(pyFile, []byte("v1"), 0o644)

	changed := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Watch(ctx, dir, []string{".py", ".html"}, func(path string) {
		select {
		case changed <- path:
		default:
		}
	})

	time.Sleep(200 * time.Millisecond)
	os.WriteFile(pyFile, []byte("v2"), 0o644)

	select {
	case p := <-changed:
		if !strings.Contains(p, "app.py") {
			t.Fatalf("expected app.py, got: %s", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for change")
	}
}

func TestWatcherIgnoresNonMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.csv"), []byte("v1"), 0o644)

	changed := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Watch(ctx, dir, []string{".py"}, func(path string) {
		changed <- path
	})

	time.Sleep(200 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "data.csv"), []byte("v2"), 0o644)

	select {
	case p := <-changed:
		t.Fatalf("should not have detected change for csv: %s", p)
	case <-time.After(1 * time.Second):
		// expected — no change detected
	}
}

func TestWatcherStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		Watch(ctx, dir, []string{".py"}, func(path string) {})
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("watcher did not stop on cancel")
	}
}

func TestWatcherSkipsVenv(t *testing.T) {
	dir := t.TempDir()
	venvDir := filepath.Join(dir, ".venv")
	os.MkdirAll(venvDir, 0o755)
	os.WriteFile(filepath.Join(venvDir, "lib.py"), []byte("v1"), 0o644)

	changed := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Watch(ctx, dir, []string{".py"}, func(path string) {
		changed <- path
	})

	time.Sleep(200 * time.Millisecond)
	os.WriteFile(filepath.Join(venvDir, "lib.py"), []byte("v2"), 0o644)

	select {
	case p := <-changed:
		t.Fatalf("should not detect .venv changes: %s", p)
	case <-time.After(1 * time.Second):
	}
}

func TestSnapshotHandlesErrors(t *testing.T) {
	// Snapshot on non-existent dir should return empty map, not panic
	result := snapshot("/nonexistent/dir", []string{".py"})
	if len(result) != 0 {
		t.Fatalf("expected empty, got %d", len(result))
	}
}

func TestSnapshotSkipsPycache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "__pycache__")
	os.MkdirAll(cacheDir, 0o755)
	os.WriteFile(filepath.Join(cacheDir, "mod.py"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "app.py"), []byte("x"), 0o644)

	result := snapshot(dir, []string{".py"})
	if len(result) != 1 {
		t.Fatalf("expected 1 (app.py only), got %d", len(result))
	}
}

func TestSnapshotReturnsMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "data.csv"), []byte("x"), 0o644)

	result := snapshot(dir, []string{".py"})
	if len(result) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result))
	}
}
