// Tests in this file must NOT use t.Parallel() because they mutate
// package-level injectable vars and some use os.Chdir (process-global).
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasencarroll/fabric-server/internal/worker"
)

type stubServer struct {
	listenErr   error
	shutdownErr error
}

func (s *stubServer) ListenAndServe() error {
	if s.listenErr != nil {
		return s.listenErr
	}
	return http.ErrServerClosed
}

func (s *stubServer) Shutdown(ctx context.Context) error {
	return s.shutdownErr
}

func stubAll(t *testing.T) func() {
	origFindPython := FindPythonInDir
	origStartWorker := StartWorker
	origNewHTTP := NewHTTPServer

	origInitDB := InitDB

	FindPythonInDir = func(dir string) (string, error) { return "/usr/bin/python3", nil }
	InitDB = func(pythonPath, dbPath string) error { return nil }
	var stubListener net.Listener
	StartWorker = func(ctx context.Context, pythonPath, appDir, appRef, tmpDir string, env ...string) (*worker.Worker, error) {
		socketPath := filepath.Join(tmpDir, "worker-0.sock")
		l, _ := net.Listen("unix", socketPath)
		stubListener = l
		if l != nil {
			go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("stub"))
			}))
		}
		return &worker.Worker{SocketPath: socketPath}, nil
	}
	NewHTTPServer = func(addr string, handler http.Handler) HTTPServer {
		return &stubServer{}
	}

	return func() {
		if stubListener != nil {
			stubListener.Close()
		}
		FindPythonInDir = origFindPython
		InitDB = origInitDB
		StartWorker = origStartWorker
		NewHTTPServer = origNewHTTP
	}
}

// --- CLI dispatch ---

func TestRunNoArgs(t *testing.T)  { run([]string{}) }
func TestRunHelp(t *testing.T)    { run([]string{"help"}) }
func TestRunVersion(t *testing.T) { run([]string{"version"}) }

func TestRunUnknown(t *testing.T) {
	err := run([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command: %v", err)
	}
}

func TestRunDispatchServe(t *testing.T) {
	err := run([]string{"run"})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("expected usage: %v", err)
	}
}

// --- serve: validation ---

func TestServeNoApp(t *testing.T) {
	err := runRun([]string{})
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("expected usage: %v", err)
	}
}

func TestServeInvalidFlags(t *testing.T) {
	err := runRun([]string{"--bogus"})
	if err == nil || !strings.Contains(err.Error(), "invalid flags") {
		t.Fatalf("expected flags error: %v", err)
	}
}

func TestServeMissingPyFile(t *testing.T) {
	err := runRun([]string{"/nonexistent/app.py"})
	if err == nil || !strings.Contains(err.Error(), "app file not found") {
		t.Fatalf("expected not found: %v", err)
	}
}

func TestServePythonNotFound(t *testing.T) {
	restore := stubAll(t)
	defer restore()
	FindPythonInDir = func(dir string) (string, error) { return "", fmt.Errorf("python3 not found in PATH") }

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	err := runRun([]string{filepath.Join(tmpDir, "app.py")})
	if err == nil || !strings.Contains(err.Error(), "python3 not found") {
		t.Fatalf("expected python error: %v", err)
	}
}

func TestServeWorkerStartFails(t *testing.T) {
	restore := stubAll(t)
	defer restore()
	StartWorker = func(ctx context.Context, p, d, a, t string, env ...string) (*worker.Worker, error) {
		return nil, fmt.Errorf("worker failed")
	}

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	err := runRun([]string{filepath.Join(tmpDir, "app.py")})
	if err == nil || !strings.Contains(err.Error(), "start worker") {
		t.Fatalf("expected worker error: %v", err)
	}
}

func TestServeListenFails(t *testing.T) {
	restore := stubAll(t)
	defer restore()
	NewHTTPServer = func(addr string, handler http.Handler) HTTPServer {
		return &stubServer{listenErr: fmt.Errorf("address in use")}
	}

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	err := runRun([]string{filepath.Join(tmpDir, "app.py")})
	if err == nil || !strings.Contains(err.Error(), "serve") {
		t.Fatalf("expected serve error: %v", err)
	}
}

// --- serve: success ---

func TestServeSuccess(t *testing.T) {
	restore := stubAll(t)
	defer restore()

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	err := runRun([]string{filepath.Join(tmpDir, "app.py")})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestServeWithProd(t *testing.T) {
	restore := stubAll(t)
	defer restore()

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	err := runRun([]string{"--prod", filepath.Join(tmpDir, "app.py")})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestServeWithStatic(t *testing.T) {
	restore := stubAll(t)
	defer restore()

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(tmpDir, "static"), 0o755)

	err := runRun([]string{"--static", filepath.Join(tmpDir, "static"), filepath.Join(tmpDir, "app.py")})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestServeModuleRef(t *testing.T) {
	restore := stubAll(t)
	defer restore()

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	err := runRun([]string{"app:create_app"})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

func TestServeAppInCurrentDir(t *testing.T) {
	restore := stubAll(t)
	defer restore()

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	err := runRun([]string{"app.py"})
	if err != nil {
		t.Fatalf("expected nil: %v", err)
	}
}

// --- resolveApp ---

func TestResolveAppPy(t *testing.T) {
	tmpDir := t.TempDir()
	appPath := filepath.Join(tmpDir, "app.py")
	os.WriteFile(appPath, []byte("x"), 0o644)

	dir, mod, err := resolveApp(appPath)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if mod != "app" {
		t.Fatalf("expected 'app', got %s", mod)
	}
	if dir != tmpDir {
		t.Fatalf("expected %s, got %s", tmpDir, dir)
	}
}

func TestResolveAppModule(t *testing.T) {
	dir, mod, err := resolveApp("myapp:create_app")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if mod != "myapp:create_app" {
		t.Fatalf("expected 'myapp:create_app', got %s", mod)
	}
	cwd, _ := os.Getwd()
	if dir != cwd {
		t.Fatalf("expected cwd, got %s", dir)
	}
}

func TestResolveAppMissingFile(t *testing.T) {
	_, _, err := resolveApp("/nonexistent/app.py")
	if err == nil || !strings.Contains(err.Error(), "app file not found") {
		t.Fatalf("expected not found: %v", err)
	}
}

// --- findPython ---

func TestDefaultFindPythonSuccess(t *testing.T) {
	path, err := defaultFindPythonInDir("")
	if err != nil {
		t.Skipf("python not available: %v", err)
	}
	if path == "" {
		t.Fatalf("expected path")
	}
}

func TestDefaultFindPythonFallbackToPython(t *testing.T) {
	origLookPath := ExecLookPath
	defer func() { ExecLookPath = origLookPath }()
	ExecLookPath = func(file string) (string, error) {
		if file == "python3" {
			return "", fmt.Errorf("not found")
		}
		if file == "python" {
			return "/usr/bin/python", nil
		}
		return "", fmt.Errorf("not found")
	}

	path, err := defaultFindPythonInDir("")
	if err != nil {
		t.Fatalf("expected fallback: %v", err)
	}
	if path != "/usr/bin/python" {
		t.Fatalf("expected /usr/bin/python, got %s", path)
	}
}

func TestDefaultFindPythonNotFound(t *testing.T) {
	origLookPath := ExecLookPath
	defer func() { ExecLookPath = origLookPath }()
	ExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}

	_, err := defaultFindPythonInDir("")
	if err == nil || !strings.Contains(err.Error(), "python3 not found") {
		t.Fatalf("expected not found: %v", err)
	}
}

func TestServeTmpDirFails(t *testing.T) {
	restore := stubAll(t)
	defer restore()

	// Stub MkdirTemp to fail by making it a directory we can't create
	origMkdirTemp := MkdirTemp
	MkdirTemp = func(dir, pattern string) (string, error) {
		return "", fmt.Errorf("disk full")
	}
	defer func() { MkdirTemp = origMkdirTemp }()

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	err := runRun([]string{filepath.Join(tmpDir, "app.py")})
	if err == nil || !strings.Contains(err.Error(), "create temp dir") {
		t.Fatalf("expected temp dir error: %v", err)
	}
}

// --- main subprocess ---

func TestMainFunction(t *testing.T) {
	if os.Getenv("TEST_MAIN") == "1" {
		os.Args = []string{"fabric", "version"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainFunction")
	cmd.Env = append(os.Environ(), "TEST_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("main failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fabric 0.0.1") {
		t.Fatalf("expected version: %s", out)
	}
}

func TestMainFunctionError(t *testing.T) {
	if os.Getenv("TEST_MAIN_ERR") == "1" {
		os.Args = []string{"fabric", "bogus"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainFunctionError")
	cmd.Env = append(os.Environ(), "TEST_MAIN_ERR=1")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected error exit")
	}
}

func TestDefaultInitDBSuccess(t *testing.T) {
	python, err := defaultFindPythonInDir("")
	if err != nil {
		t.Skipf("no python: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if err := defaultInitDB(python, dbPath); err != nil {
		t.Fatalf("expected nil: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db not created: %v", err)
	}
}

func TestDefaultInitDBFails(t *testing.T) {
	err := defaultInitDB("/nonexistent/python", "/tmp/test.db")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestServeInitDBFails(t *testing.T) {
	restore := stubAll(t)
	defer restore()
	InitDB = func(pythonPath, dbPath string) error {
		return fmt.Errorf("db init failed")
	}

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "app.py"), []byte("x"), 0o644)

	err := runRun([]string{filepath.Join(tmpDir, "app.py")})
	if err == nil || !strings.Contains(err.Error(), "init db") {
		t.Fatalf("expected init db error: %v", err)
	}
}

func TestPrintHelp(t *testing.T) { printHelp() }
