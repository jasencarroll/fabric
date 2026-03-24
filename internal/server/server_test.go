package server

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fb-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "w.sock")
}

func TestHandlerProxiesOverUnixSocket(t *testing.T) {
	socketPath := shortSocketPath(t)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("from unix socket"))
	})
	go http.Serve(listener, mux)

	handler := NewHandler("", socketPath, false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "from unix socket" {
		t.Fatalf("expected 'from unix socket', got: %s", string(body))
	}
}

func TestHandlerProxiesPost(t *testing.T) {
	socketPath := shortSocketPath(t)

	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(201)
		w.Write([]byte("created"))
	})
	go http.Serve(listener, mux)

	handler := NewHandler("", socketPath, false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/items", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

func TestHandlerServesStaticFiles(t *testing.T) {
	staticDir := t.TempDir()
	os.WriteFile(filepath.Join(staticDir, "style.css"), []byte("body{color:red}"), 0o644)

	handler := NewHandler(staticDir, "", false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/static/style.css")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "body{color:red}" {
		t.Fatalf("expected css, got: %s", string(body))
	}
}

func TestHandlerStaticAndProxy(t *testing.T) {
	socketPath := shortSocketPath(t)

	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("from worker"))
	})
	go http.Serve(listener, mux)

	staticDir := t.TempDir()
	os.WriteFile(filepath.Join(staticDir, "app.js"), []byte("console.log('hi')"), 0o644)

	handler := NewHandler(staticDir, socketPath, false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Static
	resp, _ := http.Get(ts.URL + "/static/app.js")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "console.log") {
		t.Fatalf("expected JS, got: %s", string(body))
	}

	// Proxy
	resp, _ = http.Get(ts.URL + "/")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "from worker" {
		t.Fatalf("expected worker response, got: %s", string(body))
	}
}

func TestHandlerNoSocketNoStatic(t *testing.T) {
	handler := NewHandler("", "", false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/anything")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandlerProdMode(t *testing.T) {
	handler := NewHandler("", "", true)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/test")
	resp.Body.Close()
	// Just verify it doesn't panic — log output goes to stderr
}

func TestHandlerDevMode(t *testing.T) {
	handler := NewHandler("", "", false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/test")
	resp.Body.Close()
}

func TestHandlerDevMode500(t *testing.T) {
	socketPath := shortSocketPath(t)
	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})
	go http.Serve(listener, mux)

	handler := NewHandler("", socketPath, false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/error")
	resp.Body.Close()
	// Just verify no panic — 500 should use red color
}

func TestHandlerDevMode400(t *testing.T) {
	socketPath := shortSocketPath(t)
	listener, _ := net.Listen("unix", socketPath)
	defer listener.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
	})
	go http.Serve(listener, mux)

	handler := NewHandler("", socketPath, false)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/bad")
	resp.Body.Close()
}

func TestStatusRecorder(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: 200}
	sr.WriteHeader(404)
	if sr.status != 404 {
		t.Fatalf("expected 404, got %d", sr.status)
	}
}
