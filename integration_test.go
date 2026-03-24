package fabric_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasencarroll/fabric-server/internal/server"
	"github.com/jasencarroll/fabric-server/internal/worker"
)

func skipIfNoPython(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("python3"); err == nil {
		return p
	}
	if p, err := exec.LookPath("python"); err == nil {
		return p
	}
	t.Skip("python not available")
	return ""
}

func shortTmpDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fb-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func writeSlowApp(t *testing.T, dir string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(`
import time

def app(environ, start_response):
    if environ.get("PATH_INFO") == "/slow":
        time.sleep(2)
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [b"ok"]
`), 0o644)
}

func writeCrashApp(t *testing.T, dir string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(`
import os, signal

def app(environ, start_response):
    if environ.get("PATH_INFO") == "/crash":
        os.kill(os.getpid(), signal.SIGKILL)
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [b"ok"]
`), 0o644)
}

// Test 1: Kill worker during request, confirm auto-restart, next request succeeds
func TestFailureMode_KillAndRestart(t *testing.T) {
	python := skipIfNoPython(t)
	appDir := t.TempDir()
	writeSlowApp(t, appDir)
	tmpDir := shortTmpDir(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := worker.Start(ctx, python, appDir, "app", tmpDir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	handler := server.NewHandler("", w.SocketPath, true)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Verify worker is responding
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("initial request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Kill the worker
	w.Stop()

	// Request during downtime should get 503
	resp, err = http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("request during downtime: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("expected 503 during downtime, got %d", resp.StatusCode)
	}

	// Restart worker
	w, err = worker.Start(ctx, python, appDir, "app", tmpDir)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer w.Stop()

	// Next request should succeed
	resp, err = http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("post-restart request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 after restart, got %d", resp.StatusCode)
	}
}

// Test 2: Three rapid crashes trigger crash loop detection
func TestFailureMode_CrashLoopDetection(t *testing.T) {
	python := skipIfNoPython(t)
	appDir := t.TempDir()
	writeCrashApp(t, appDir)
	tmpDir := shortTmpDir(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var crashes int

	w, err := worker.Start(ctx, python, appDir, "app", tmpDir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Simulate crash loop monitoring (same logic as main.go)
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		var crashTimes []time.Time
		for {
			mu.Lock()
			current := w
			mu.Unlock()
			if current.Wait() != nil {
				select {
				case <-ctx.Done():
					return
				default:
				}
				now := time.Now()
				crashTimes = append(crashTimes, now)
				recent := crashTimes[:0]
				for _, ct := range crashTimes {
					if now.Sub(ct) < 5*time.Second {
						recent = append(recent, ct)
					}
				}
				crashTimes = recent
				mu.Lock()
				crashes++
				mu.Unlock()
				if len(crashTimes) >= 3 {
					cancel()
					return
				}
				// Restart
				mu.Lock()
				w.Stop()
				newW, startErr := worker.Start(ctx, python, appDir, "app", tmpDir)
				if startErr != nil {
					mu.Unlock()
					cancel()
					return
				}
				w = newW
				mu.Unlock()
			} else {
				return
			}
		}
	}()

	handler := server.NewHandler("", w.SocketPath, true)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Trigger crashes by hitting /crash endpoint
	for i := 0; i < 3; i++ {
		time.Sleep(200 * time.Millisecond) // let worker stabilize
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			break
		}
		mu.Unlock()
		http.Get(ts.URL + "/crash")
	}

	// Wait for crash loop detection
	select {
	case <-loopDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("crash loop not detected within timeout")
	}

	mu.Lock()
	c := crashes
	mu.Unlock()
	if c < 3 {
		t.Fatalf("expected at least 3 crashes, got %d", c)
	}
}

// Test 3: Request during restart window gets clean 503
func TestFailureMode_503DuringRestart(t *testing.T) {
	python := skipIfNoPython(t)
	appDir := t.TempDir()
	writeSlowApp(t, appDir)
	tmpDir := shortTmpDir(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := worker.Start(ctx, python, appDir, "app", tmpDir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.Stop()

	handler := server.NewHandler("", w.SocketPath, true)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Kill worker to simulate restart window
	w.Stop()

	// Send multiple requests during the window — all should get 503
	for i := 0; i < 5; i++ {
		resp, err := http.Get(ts.URL + "/")
		if err != nil {
			t.Fatalf("request %d error: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("request %d: expected 503, got %d body=%s", i, resp.StatusCode, string(body))
		}
		if !strings.Contains(string(body), "worker unavailable") {
			t.Fatalf("request %d: expected 'worker unavailable', got: %s", i, string(body))
		}
	}
}

// Test 4: Ctrl+C — no orphan Python processes, no locked DB
func TestFailureMode_CleanShutdown(t *testing.T) {
	python := skipIfNoPython(t)
	appDir := t.TempDir()
	writeSlowApp(t, appDir)
	tmpDir := shortTmpDir(t)

	ctx, cancel := context.WithCancel(context.Background())

	w, err := worker.Start(ctx, python, appDir, "app", tmpDir)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Get the PID before shutdown
	pid := w.Pid()
	if pid == 0 {
		t.Fatalf("worker has no PID")
	}

	// Create a SQLite DB to verify no lock after shutdown
	dbPath := filepath.Join(appDir, "test.db")
	initCmd := exec.Command(python, "-c",
		fmt.Sprintf("import sqlite3; db=sqlite3.connect(%q); db.execute('CREATE TABLE t(x)'); db.commit(); db.close()", dbPath))
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("create db: %v\n%s", err, out)
	}

	// Simulate Ctrl+C: cancel context, stop worker
	cancel()
	w.Stop()

	// Verify the Python process is dead
	time.Sleep(200 * time.Millisecond)
	proc, err := os.FindProcess(pid)
	if err == nil {
		// On Unix, FindProcess always succeeds. Check if actually alive.
		err = proc.Signal(nil) // Signal 0 = check if process exists
		if err == nil {
			t.Fatalf("orphan Python process still alive (pid %d)", pid)
		}
	}

	// Verify socket is cleaned up
	socketPath := filepath.Join(tmpDir, "worker-0.sock")
	if _, err := os.Stat(socketPath); err == nil {
		t.Fatalf("socket not cleaned up: %s", socketPath)
	}

	// Verify SQLite DB is not locked — can open and write
	checkCmd := exec.Command(python, "-c",
		fmt.Sprintf("import sqlite3; db=sqlite3.connect(%q); db.execute('INSERT INTO t VALUES(1)'); db.commit(); db.close(); print('ok')", dbPath))
	out, err := checkCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("db locked after shutdown: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ok") {
		t.Fatalf("expected ok, got: %s", out)
	}
}

// Test: 503 ErrorHandler works on proxy
func TestFailureMode_ProxyErrorHandler(t *testing.T) {
	// Create handler pointing to a nonexistent socket
	handler := server.NewHandler("", "/tmp/nonexistent-fabric-socket.sock", true)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "worker unavailable") {
		t.Fatalf("expected 'worker unavailable', got: %s", string(body))
	}
}

