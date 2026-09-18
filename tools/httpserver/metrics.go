package main

import (
	"fmt"
	log "log/slog"
	"net/http"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/internal/logsafe"
)

// serverMetrics holds process-wide request counters exposed at /metrics
// in Prometheus text exposition format.
type serverMetrics struct {
	started      time.Time
	requests     atomic.Int64
	responses2xx atomic.Int64
	responses3xx atomic.Int64
	responses4xx atomic.Int64
	responses5xx atomic.Int64
	inFlight     atomic.Int64
}

var metrics = serverMetrics{started: time.Now()}

// statusRecorder captures the response status code. Flush is forwarded so
// SSE/streaming handlers that assert http.Flusher keep working.
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

func metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.requests.Add(1)
		metrics.inFlight.Add(1)
		defer metrics.inFlight.Add(-1)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)

		switch {
		case rec.status >= 500:
			metrics.responses5xx.Add(1)
		case rec.status >= 400:
			metrics.responses4xx.Add(1)
		case rec.status >= 300:
			metrics.responses3xx.Add(1)
		default:
			metrics.responses2xx.Add(1)
		}

		if rec.status >= 500 {
			log.Error("request failed", "method", r.Method, "path", logsafe.V(r.URL.Path), "status", rec.status, "duration_ms", elapsed.Milliseconds())
		} else {
			log.Debug("request", "method", r.Method, "path", logsafe.V(r.URL.Path), "status", rec.status, "duration_ms", elapsed.Milliseconds())
		}
	})
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP sop_build_info Build information.\n# TYPE sop_build_info gauge\nsop_build_info{version=%q} 1\n", sop.Version)
	fmt.Fprintf(w, "# HELP sop_uptime_seconds Seconds since the server started.\n# TYPE sop_uptime_seconds gauge\nsop_uptime_seconds %d\n", int64(time.Since(metrics.started).Seconds()))
	fmt.Fprintf(w, "# HELP sop_http_requests_total Total HTTP requests received.\n# TYPE sop_http_requests_total counter\nsop_http_requests_total %d\n", metrics.requests.Load())
	fmt.Fprintf(w, "# HELP sop_http_responses_total HTTP responses by status class.\n# TYPE sop_http_responses_total counter\n")
	fmt.Fprintf(w, "sop_http_responses_total{class=\"2xx\"} %d\n", metrics.responses2xx.Load())
	fmt.Fprintf(w, "sop_http_responses_total{class=\"3xx\"} %d\n", metrics.responses3xx.Load())
	fmt.Fprintf(w, "sop_http_responses_total{class=\"4xx\"} %d\n", metrics.responses4xx.Load())
	fmt.Fprintf(w, "sop_http_responses_total{class=\"5xx\"} %d\n", metrics.responses5xx.Load())
	fmt.Fprintf(w, "# HELP sop_http_requests_in_flight Requests currently being served.\n# TYPE sop_http_requests_in_flight gauge\nsop_http_requests_in_flight %d\n", metrics.inFlight.Load())
	fmt.Fprintf(w, "# HELP sop_goroutines Number of goroutines.\n# TYPE sop_goroutines gauge\nsop_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "# HELP sop_heap_alloc_bytes Bytes of allocated heap objects.\n# TYPE sop_heap_alloc_bytes gauge\nsop_heap_alloc_bytes %d\n", mem.HeapAlloc)
}
