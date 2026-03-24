package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jasencarroll/fabric-server/internal/server"
	"github.com/jasencarroll/fabric-server/internal/watcher"
	"github.com/jasencarroll/fabric-server/internal/worker"
)

const version = "0.0.1"

// StartWorker is injectable for testing.
var StartWorker func(ctx context.Context, pythonPath, appDir, appRef, tmpDir string, env ...string) (*worker.Worker, error) = worker.Start


// MkdirTemp is injectable for testing.
var MkdirTemp = os.MkdirTemp

// HTTPServer abstracts http.Server for testing.
type HTTPServer interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

// NewHTTPServer is injectable for testing.
var NewHTTPServer = func(addr string, handler http.Handler) HTTPServer {
	return &http.Server{Addr: addr, Handler: handler}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}
	switch args[0] {
	case "run":
		return runRun(args[1:])
	case "version":
		fmt.Printf("fabric %s\n", version)
		return nil
	case "help":
		printHelp()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	port := fs.Int("port", 3000, "port to listen on")
	staticDir := fs.String("static", "", "serve static files from directory")
	prod := fs.Bool("prod", false, "production mode")
	dbPath := fs.String("db", "./fabric.db", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("invalid flags: %v", err)
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("usage: fabric run <app.py>")
	}
	appRef := remaining[0]

	appDir, appModule, err := resolveApp(appRef)
	if err != nil {
		return err
	}

	pythonPath, err := FindPythonInDir(appDir)
	if err != nil {
		return err
	}

	// Ensure SQLite WAL mode
	absDB, err := filepath.Abs(*dbPath)
	if err != nil {
		return fmt.Errorf("resolve db path: %v", err)
	}
	if err := InitDB(pythonPath, absDB); err != nil {
		return fmt.Errorf("init db: %v", err)
	}

	tmpDir, err := MkdirTemp("", "fabric-")
	if err != nil {
		return fmt.Errorf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	env := []string{fmt.Sprintf("FABRIC_DB=%s", absDB)}
	w, err := StartWorker(ctx, pythonPath, appDir, appModule, tmpDir, env...)
	if err != nil {
		return fmt.Errorf("start worker: %v", err)
	}

	var mu sync.Mutex
	defer func() { mu.Lock(); w.Stop(); mu.Unlock() }()

	handler := server.NewHandler(*staticDir, w.SocketPath, *prod)
	addr := fmt.Sprintf(":%d", *port)
	srv := NewHTTPServer(addr, handler)

	shutdown := func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
	}

	// Worker crash auto-restart
	restartWorker := func(reason string) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "\033[38;5;141m[fabric]\033[0m \033[38;5;228m%s\033[0m — restarting worker\n", reason)
		w.Stop()
		newW, restartErr := StartWorker(ctx, pythonPath, appDir, appModule, tmpDir, env...)
		if restartErr != nil {
			fmt.Fprintf(os.Stderr, "\033[38;5;141m[fabric]\033[0m \033[38;5;203mrestart failed: %v — shutting down\033[0m\n", restartErr)
			go shutdown()
			return
		}
		w = newW
	}

	// Crash monitor goroutine
	go func() {
		var crashes []time.Time
		for {
			mu.Lock()
			currentW := w
			mu.Unlock()
			if currentW.Wait() != nil {
				if ctx.Err() != nil {
					return
				}
				// Check if another restart already happened (e.g., watcher)
				mu.Lock()
				if w != currentW {
					mu.Unlock()
					crashes = nil // reset crash counter — this wasn't a real crash
					continue
				}
				mu.Unlock()

				now := time.Now()
				crashes = append(crashes, now)
				recent := crashes[:0]
				for _, t := range crashes {
					if now.Sub(t) < 5*time.Second {
						recent = append(recent, t)
					}
				}
				crashes = recent
				if len(crashes) >= 3 {
					fmt.Fprintf(os.Stderr, "\033[38;5;141m[fabric]\033[0m \033[38;5;203mcrash loop detected — exiting\033[0m\n")
					go shutdown()
					return
				}
				restartWorker("worker crashed")
				if ctx.Err() != nil {
					return // shutdown triggered by failed restart
				}
			} else {
				return
			}
		}
	}()

	if !*prod {
		purple := "\033[38;5;141m"
		green := "\033[38;5;84m"
		cyan := "\033[38;5;117m"
		bold := "\033[1m"
		reset := "\033[0m"
		fmt.Fprintf(os.Stderr, "\n%s[fabric]%s %s%sfabric v%s%s\n", purple, reset, bold, green, version, reset)
		fmt.Fprintf(os.Stderr, "%s[fabric]%s %shttp://localhost%s%s\n", purple, reset, cyan, addr, reset)
		fmt.Fprintf(os.Stderr, "%s[fabric]%s watching %s for changes\n\n", purple, reset, filepath.Base(appRef))
		go watcher.Watch(ctx, appDir, []string{".py", ".html"}, func(path string) {
			restartWorker(fmt.Sprintf("changed %s", filepath.Base(path)))
		})
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			shutdown()
		case <-ctx.Done():
			// context cancelled by crash loop or other path — goroutine exits
		}
		signal.Stop(sigCh)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve: %v", err)
	}
	return nil
}

func resolveApp(appRef string) (dir string, module string, err error) {
	if strings.HasSuffix(appRef, ".py") {
		if _, err := os.Stat(appRef); err != nil {
			return "", "", fmt.Errorf("app file not found: %s", appRef)
		}
		absPath, err := filepath.Abs(appRef)
		if err != nil {
			return "", "", fmt.Errorf("resolve app path: %w", err)
		}
		dir = filepath.Dir(absPath)
		module = strings.TrimSuffix(filepath.Base(appRef), ".py")
	} else {
		dir, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("resolve working directory: %w", err)
		}
		module = appRef
	}
	return dir, module, nil
}

func printHelp() {
	fmt.Print(`Usage:
  fabric <command>

Commands:
  fabric run <app.py>           run a WSGI app
  fabric version               print version
  fabric help                  show this message

Flags (run):
  --port <port>                port to listen on (default 3000)
  --static <dir>               serve static files at /static/
  --prod                       production mode (JSON logs, no watcher)
  --db <path>                  SQLite database path (default ./fabric.db)

Note: single worker, one request at a time. For dev and internal tools.

`)
}

// ExecLookPath is injectable for testing.
var ExecLookPath = exec.LookPath

// InitDB is injectable for testing.
var InitDB = defaultInitDB

func defaultInitDB(pythonPath, dbPath string) error {
	cmd := exec.Command(pythonPath, "-c",
		fmt.Sprintf("import sqlite3; db=sqlite3.connect(%q); db.execute('PRAGMA journal_mode=WAL'); db.close()", dbPath))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite init: %s\n%w", string(output), err)
	}
	return nil
}

// FindPythonInDir looks for a venv python in the given directory first.
var FindPythonInDir = defaultFindPythonInDir

func defaultFindPythonInDir(appDir string) (string, error) {
	// Check for .venv in the app directory first
	venvPython := filepath.Join(appDir, ".venv", "bin", "python3")
	if _, err := os.Stat(venvPython); err == nil {
		return venvPython, nil
	}
	venvPython = filepath.Join(appDir, ".venv", "bin", "python")
	if _, err := os.Stat(venvPython); err == nil {
		return venvPython, nil
	}
	// Fall back to system python
	if path, err := ExecLookPath("python3"); err == nil {
		return path, nil
	}
	if path, err := ExecLookPath("python"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("python3 not found in PATH")
}

