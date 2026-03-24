package worker

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func skipIfNoPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		if _, err := exec.LookPath("python"); err != nil {
			t.Skip("python not available")
		}
	}
}

func TestNewWorkerCreatesSocket(t *testing.T) {
	skipIfNoPython(t)
	appDir := t.TempDir()
	writeTestApp(t, appDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := Start(ctx, "python3", appDir, "app", t.TempDir())
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
	defer w.Stop()

	if _, err := os.Stat(w.SocketPath); err != nil {
		t.Fatalf("socket not created: %v", err)
	}

	conn, err := net.Dial("unix", w.SocketPath)
	if err != nil {
		t.Fatalf("socket not connectable: %v", err)
	}
	conn.Close()
}

func TestWorkerStopCleansUp(t *testing.T) {
	skipIfNoPython(t)
	appDir := t.TempDir()
	writeTestApp(t, appDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := Start(ctx, "python3", appDir, "app", t.TempDir())
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}

	socketPath := w.SocketPath
	w.Stop()

	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(socketPath); err == nil {
		t.Fatalf("socket not cleaned up")
	}
}

func TestWorkerExtractsScript(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath, err := ExtractScript(tmpDir)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("script not extracted: %v", err)
	}
	data, _ := os.ReadFile(scriptPath)
	if !strings.Contains(string(data), "UnixWSGIServer") {
		t.Fatalf("script missing UnixWSGIServer class")
	}
}

func TestWorkerExtractScriptFailure(t *testing.T) {
	_, err := ExtractScript("/dev/null/impossible")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestWorkerFailsWithBadApp(t *testing.T) {
	skipIfNoPython(t)
	appDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := Start(ctx, "python3", appDir, "nonexistent", t.TempDir())
	if err == nil {
		t.Fatalf("expected error for missing app")
	}
}

func TestWorkerFailsWithBadPython(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := Start(ctx, "/nonexistent/python", t.TempDir(), "app", t.TempDir())
	if err == nil {
		t.Fatalf("expected error for bad python path")
	}
}

func TestWorkerWaitNilCmd(t *testing.T) {
	w := &Worker{}
	if err := w.Wait(); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestWorkerStopNilCmd(t *testing.T) {
	w := &Worker{}
	w.Stop() // should not panic
}

func TestWorkerReadyTimeout(t *testing.T) {
	skipIfNoPython(t)

	origTimeout := ReadyTimeout
	ReadyTimeout = 100 * time.Millisecond
	defer func() { ReadyTimeout = origTimeout }()

	appDir := t.TempDir()
	// Write an app that hangs (never prints "ready" because worker.py can't import it properly)
	os.WriteFile(filepath.Join(appDir, "app.py"), []byte("import time; time.sleep(10)"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := Start(ctx, "python3", appDir, "app", t.TempDir())
	if err == nil {
		t.Fatalf("expected timeout error")
	}
}

func TestWorkerWaitReturnsError(t *testing.T) {
	skipIfNoPython(t)
	appDir := t.TempDir()
	writeTestApp(t, appDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w, err := Start(ctx, "python3", appDir, "app", t.TempDir())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Kill the process, then Wait should return
	w.cmd.Process.Kill()
	w.Wait()
}

func TestWorkerSocketNotReadyAfterReady(t *testing.T) {
	skipIfNoPython(t)

	origSocketTimeout := SocketTimeout
	SocketTimeout = 100 * time.Millisecond
	defer func() { SocketTimeout = origSocketTimeout }()

	appDir := t.TempDir()
	// Write an app that makes the worker print "ready" but exit immediately after
	// (before binding the socket). We do this by creating a fake worker script.
	tmpDir := t.TempDir()
	fakeWorker := filepath.Join(tmpDir, "fabric_worker.py")
	os.WriteFile(fakeWorker, []byte("import sys; print('ready', flush=True); sys.exit(0)"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Directly call the command instead of Start, to use our fake worker
	cmd := exec.CommandContext(ctx, "python3", fakeWorker, "app", filepath.Join(tmpDir, "worker-0.sock"))
	cmd.Dir = appDir
	cmd.Stderr = os.Stderr
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()

	// Read "ready"
	buf := make([]byte, 64)
	n, _ := stdout.Read(buf)
	if !strings.Contains(string(buf[:n]), "ready") {
		t.Fatalf("expected ready signal")
	}

	// Now waitForSocket should timeout
	err := waitForSocket(filepath.Join(tmpDir, "worker-0.sock"), 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected socket timeout")
	}
	cmd.Process.Kill()
	cmd.Wait()
}

func TestWorkerStartBadTmpDir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a file as tmpDir — should fail on socket cleanup or extract
	tmpFile := filepath.Join(t.TempDir(), "notadir")
	os.WriteFile(tmpFile, []byte("x"), 0o644)

	_, err := Start(ctx, "python3", t.TempDir(), "app", tmpFile)
	if err == nil {
		t.Fatalf("expected error for bad tmpdir")
	}
}

func TestWaitForSocketTimeout(t *testing.T) {
	err := waitForSocket("/nonexistent/socket.sock", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout: %v", err)
	}
}

func writeTestApp(t *testing.T, dir string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, "app.py"), []byte(`
def app(environ, start_response):
    start_response("200 OK", [("Content-Type", "text/plain")])
    return [b"ok"]
`), 0o644)
}
