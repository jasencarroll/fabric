package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"time"
)

// NewHandler creates an http.Handler that serves static files and proxies to a Unix socket.
func NewHandler(staticDir, socketPath string, prodMode bool) http.Handler {
	mux := http.NewServeMux()

	if staticDir != "" {
		fs := http.FileServer(http.Dir(staticDir))
		mux.Handle("/static/", http.StripPrefix("/static/", fs))
	}

	if socketPath != "" {
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 60 * time.Second,
		}
		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = "http"
				req.URL.Host = "fabric-worker"
				if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
					req.Header.Set("X-Forwarded-For", clientIP)
				}
				req.Header.Set("X-Forwarded-Host", req.Host)
				req.Header.Set("X-Forwarded-Proto", "http")
			},
			Transport: transport,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, proxyErr error) {
				if prodMode {
					errEntry := map[string]interface{}{
						"ts":    time.Now().UTC().Format(time.RFC3339),
						"level": "error",
						"msg":   "proxy error",
						"error": proxyErr.Error(),
						"path":  r.URL.Path,
					}
					line, _ := json.Marshal(errEntry)
					fmt.Fprintln(os.Stderr, string(line))
				} else {
					fmt.Fprintf(os.Stderr, "%s[fabric]%s \033[38;5;203mproxy error: %v\033[0m\n", dracPurple, dracReset, proxyErr)
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("worker unavailable"))
			},
		}
		mux.Handle("/", proxy)
	}

	var logFn func(http.Handler) http.Handler
	if prodMode {
		logFn = jsonLog
	} else {
		logFn = devLog
	}
	return logFn(mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func jsonLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		entry := map[string]interface{}{
			"ts":     start.UTC().Format(time.RFC3339),
			"level":  "info",
			"method": r.Method,
			"path":   r.URL.Path,
			"status": rec.status,
			"dur_ms": time.Since(start).Milliseconds(),
		}
		line, _ := json.Marshal(entry)
		fmt.Fprintln(os.Stderr, string(line))
	})
}

// Dracula background colors for Gin-style log blocks
const (
	// Foreground
	dracPurple = "\033[38;5;141m"
	dracReset  = "\033[0m"

	// Status code backgrounds (Dracula-inspired)
	bgGreen  = "\033[97;42m"  // 2xx — bright white on green
	bgWhite  = "\033[90;47m"  // 3xx — dark on white
	bgYellow = "\033[90;43m"  // 4xx — dark on yellow
	bgRed    = "\033[97;41m"  // 5xx — bright white on red

	// Method backgrounds (Dracula-inspired)
	bgBlue    = "\033[97;44m" // GET
	bgCyan    = "\033[97;46m" // POST
	bgMagenta = "\033[97;45m" // PUT/PATCH
	// bgRed reused for DELETE
)

func statusColor(code int) string {
	switch {
	case code >= 500:
		return bgRed
	case code >= 400:
		return bgYellow
	case code >= 300:
		return bgWhite
	default:
		return bgGreen
	}
}

func methodColor(method string) string {
	switch method {
	case "GET":
		return bgBlue
	case "POST":
		return bgCyan
	case "PUT", "PATCH":
		return bgMagenta
	case "DELETE":
		return bgRed
	default:
		return bgWhite
	}
}

func devLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)

		latency := time.Since(start)
		now := time.Now()

		// Truncate latency like Gin does
		switch {
		case latency > time.Minute:
			latency = latency.Truncate(time.Second)
		case latency > time.Second:
			latency = latency.Truncate(time.Millisecond)
		case latency > time.Millisecond:
			latency = latency.Truncate(time.Microsecond)
		}

		fmt.Fprintf(os.Stderr, "%s[fabric]%s %v |%s %3d %s| %13v | %s %s %s %s\n",
			dracPurple, dracReset,
			now.Format("2006/01/02 - 15:04:05"),
			statusColor(rec.status), rec.status, dracReset,
			latency,
			methodColor(r.Method), r.Method, dracReset,
			r.URL.Path,
		)
	})
}
