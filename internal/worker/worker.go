package worker

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

//go:embed worker.py
var workerScript []byte

// Worker represents a running Python WSGI worker process.
type Worker struct {
	SocketPath string
	cmd        *exec.Cmd
	stopOnce   sync.Once
}

// ExtractScript writes the embedded worker.py to a temp directory.
func ExtractScript(tmpDir string) (string, error) {
	scriptPath := filepath.Join(tmpDir, "fabric_worker.py")
	if err := os.WriteFile(scriptPath, workerScript, 0o644); err != nil {
		return "", fmt.Errorf("extract worker script: %w", err)
	}
	return scriptPath, nil
}

// ReadyTimeout is how long to wait for the worker to signal ready. Injectable for testing.
var ReadyTimeout = 5 * time.Second

// SocketTimeout is how long to wait for the socket to be connectable. Injectable for testing.
var SocketTimeout = 5 * time.Second

// Start spawns a Python worker process that serves a WSGI app on a Unix socket.
// Extra env vars can be passed via the env parameter.
func Start(ctx context.Context, pythonPath, appDir, appRef, tmpDir string, env ...string) (*Worker, error) {
	socketPath := filepath.Join(tmpDir, "worker-0.sock")
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("clean stale socket: %w", err)
	}

	scriptPath, err := ExtractScript(tmpDir)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath, appRef, socketPath)
	cmd.Dir = appDir
	cmd.Env = append(os.Environ(), env...)

	// Capture stderr so we can show Python errors to the user
	var stderrBuf limitedBuffer
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start worker: %w", err)
	}

	readyCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "ready" {
				readyCh <- nil
				// Drain remaining stdout to prevent pipe buffer deadlock
				go io.Copy(io.Discard, stdout)
				return
			}
		}
		errMsg := stderrBuf.String()
		if errMsg != "" {
			readyCh <- fmt.Errorf("worker exited before ready:\n%s", errMsg)
		} else {
			readyCh <- fmt.Errorf("worker exited before ready")
		}
	}()

	select {
	case err := <-readyCh:
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			return nil, err
		}
	case <-time.After(ReadyTimeout):
		cmd.Process.Kill()
		cmd.Wait()
		errMsg := stderrBuf.String()
		if errMsg != "" {
			return nil, fmt.Errorf("worker failed to start:\n%s", errMsg)
		}
		return nil, fmt.Errorf("worker failed to start")
	}

	if err := waitForSocket(socketPath, SocketTimeout); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("worker socket not ready: %w", err)
	}

	return &Worker{
		SocketPath: socketPath,
		cmd:        cmd,
	}, nil
}

// limitedBuffer captures the last 4KB of output for error reporting. Thread-safe.
type limitedBuffer struct {
	mu   sync.Mutex
	buf  [4096]byte
	n    int
	full bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range p {
		b.buf[b.n%len(b.buf)] = c
		b.n++
		if !b.full && b.n >= len(b.buf) {
			b.full = true
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.n == 0 {
		return ""
	}
	if !b.full {
		return string(b.buf[:b.n])
	}
	start := b.n % len(b.buf)
	return string(b.buf[start:]) + string(b.buf[:start])
}

// Stop gracefully shuts down the worker (SIGTERM, then SIGKILL after 2s). Safe to call multiple times.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		if w.cmd != nil && w.cmd.Process != nil {
			w.cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan struct{})
			go func() { w.cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				w.cmd.Process.Kill()
				<-done
			}
		}
		os.Remove(w.SocketPath)
	})
}

// Pid returns the worker process ID, or 0 if not running.
func (w *Worker) Pid() int {
	if w.cmd != nil && w.cmd.Process != nil {
		return w.cmd.Process.Pid
	}
	return 0
}

// Wait blocks until the worker process exits.
func (w *Worker) Wait() error {
	if w.cmd == nil {
		return nil
	}
	return w.cmd.Wait()
}

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", path)
}
