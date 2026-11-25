// Command workload-api is a small HTTP service with a simulated workload,
// Kubernetes-style health/readiness probes and Prometheus metrics.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	// minWorkLatency is the lower bound of /work's simulated latency.
	minWorkLatency = 10 * time.Millisecond
	// shutdownDelay keeps serving after SIGTERM while /readyz reports 503, so
	// Kubernetes removes the pod from the Service endpoints before we stop
	// accepting connections.
	shutdownDelay = 5 * time.Second
	// drainTimeout bounds how long in-flight requests may take to finish.
	// shutdownDelay + drainTimeout stays below the default 30s grace period.
	drainTimeout = 10 * time.Second
)

// config holds the runtime settings, read from environment variables.
type config struct {
	Port           string
	WorkMaxLatency time.Duration
	WorkErrorRate  float64
	StartupDelay   time.Duration
}

// loadConfig reads the configuration using getenv (os.Getenv in production),
// applying defaults for unset variables.
func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		Port:           "8080",
		WorkMaxLatency: 500 * time.Millisecond,
		WorkErrorRate:  0.05,
	}
	if v := getenv("PORT"); v != "" {
		cfg.Port = v
	}
	if v := getenv("WORK_MAX_LATENCY_MS"); v != "" {
		ms, err := strconv.Atoi(v)
		if err != nil || ms < 0 {
			return config{}, fmt.Errorf("WORK_MAX_LATENCY_MS must be a non-negative integer, got %q", v)
		}
		cfg.WorkMaxLatency = time.Duration(ms) * time.Millisecond
	}
	if v := getenv("WORK_ERROR_RATE"); v != "" {
		rate, err := strconv.ParseFloat(v, 64)
		if err != nil || rate < 0 || rate > 1 {
			return config{}, fmt.Errorf("WORK_ERROR_RATE must be a number between 0 and 1, got %q", v)
		}
		cfg.WorkErrorRate = rate
	}
	if v := getenv("STARTUP_DELAY_SECONDS"); v != "" {
		s, err := strconv.Atoi(v)
		if err != nil || s < 0 {
			return config{}, fmt.Errorf("STARTUP_DELAY_SECONDS must be a non-negative integer, got %q", v)
		}
		cfg.StartupDelay = time.Duration(s) * time.Second
	}
	return cfg, nil
}

// metrics are the RED metrics exposed on /metrics.
type metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, path and status code.",
		}, []string{"method", "path", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by method and path.",
			Buckets: prometheus.DefBuckets, // 5ms..10s, covers /work's 10–500ms range
		}, []string{"method", "path"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// server holds the HTTP handlers and the readiness state.
type server struct {
	cfg      config
	ready    atomic.Bool
	metrics  *metrics
	reg      *prometheus.Registry
	hostname string
}

func newServer(cfg config) *server {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	hostname, _ := os.Hostname() // the pod name in Kubernetes
	return &server{cfg: cfg, metrics: newMetrics(reg), reg: reg, hostname: hostname}
}

// routes wires the endpoints. Each handler is instrumented with a fixed path
// label, so unknown URLs can't blow up the metrics' cardinality.
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", s.instrument("/", s.handleRoot))
	mux.Handle("GET /work", s.instrument("/work", s.handleWork))
	mux.Handle("GET /healthz", s.instrument("/healthz", s.handleHealthz))
	mux.Handle("GET /readyz", s.instrument("/readyz", s.handleReadyz))
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.reg, promhttp.HandlerOpts{}))
	return mux
}

// handleRoot is a fast, always-successful endpoint. The hostname shows which
// pod answered; the version shows which build is running.
func (s *server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message":  "hello from workload-api",
		"version":  version,
		"hostname": s.hostname,
	})
}

// handleWork simulates a backend call: random latency between minWorkLatency
// and WORK_MAX_LATENCY_MS, failing with 500 at WORK_ERROR_RATE.
func (s *server) handleWork(w http.ResponseWriter, r *http.Request) {
	latency := minWorkLatency
	if spread := s.cfg.WorkMaxLatency - minWorkLatency; spread > 0 {
		latency += rand.N(spread)
	}
	select {
	case <-time.After(latency):
	case <-r.Context().Done(): // client gave up; don't keep the goroutine busy
		return
	}
	if rand.Float64() < s.cfg.WorkErrorRate {
		http.Error(w, "simulated failure", http.StatusInternalServerError)
		return
	}
	_, _ = fmt.Fprintf(w, "work done in %s\n", latency.Round(time.Millisecond))
}

// handleHealthz is the liveness probe: it only proves the process can serve
// HTTP. It never checks dependencies, or an outage would restart every pod.
func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok\n"))
}

// handleReadyz is the readiness probe: 503 until initialization finishes and
// again during shutdown, so the pod only receives traffic when it can serve.
func (s *server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.ready.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write([]byte("ready\n"))
}

// instrument records request count and latency for a handler.
func (s *server) instrument(path string, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		h(rec, r)
		s.metrics.requests.WithLabelValues(r.Method, path, strconv.Itoa(rec.code)).Inc()
		s.metrics.duration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}

// statusRecorder captures the status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	if err := run(cfg); err != nil {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}
}

// run serves until SIGTERM/SIGINT, then shuts down gracefully:
// mark not-ready → wait for endpoints to update → drain in-flight requests.
func run(cfg config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	s := newServer(cfg)
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Simulated slow initialization (e.g. warming caches, loading config).
	go func() {
		select {
		case <-time.After(cfg.StartupDelay):
			s.ready.Store(true)
			slog.Info("initialization complete, ready for traffic")
		case <-ctx.Done():
		}
	}()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("listening", "port", cfg.Port, "version", version,
		"work_max_latency", cfg.WorkMaxLatency.String(), "work_error_rate", cfg.WorkErrorRate,
		"startup_delay", cfg.StartupDelay.String())

	select {
	case err := <-errCh:
		return err // ListenAndServe failed before any signal (e.g. port in use)
	case <-ctx.Done():
	}

	s.ready.Store(false)
	slog.Info("shutdown signal received, marked not ready", "delay", shutdownDelay.String())
	time.Sleep(shutdownDelay)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("draining connections: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	slog.Info("shutdown complete")
	return nil
}
