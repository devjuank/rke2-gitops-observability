package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    config
		wantErr bool
	}{
		{
			name: "defaults",
			env:  map[string]string{},
			want: config{Port: "8080", WorkMaxLatency: 500 * time.Millisecond, WorkErrorRate: 0.05},
		},
		{
			name: "all set",
			env: map[string]string{
				"PORT": "9090", "WORK_MAX_LATENCY_MS": "200",
				"WORK_ERROR_RATE": "0.2", "STARTUP_DELAY_SECONDS": "10",
			},
			want: config{Port: "9090", WorkMaxLatency: 200 * time.Millisecond, WorkErrorRate: 0.2, StartupDelay: 10 * time.Second},
		},
		{name: "error rate above 1", env: map[string]string{"WORK_ERROR_RATE": "1.5"}, wantErr: true},
		{name: "error rate not a number", env: map[string]string{"WORK_ERROR_RATE": "high"}, wantErr: true},
		{name: "negative latency", env: map[string]string{"WORK_MAX_LATENCY_MS": "-1"}, wantErr: true},
		{name: "startup delay not an integer", env: map[string]string{"STARTUP_DELAY_SECONDS": "1.5"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadConfig(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHandlers(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		ready     bool
		errorRate float64
		wantCode  int
		wantBody  string
	}{
		{name: "root", path: "/", ready: true, wantCode: http.StatusOK, wantBody: "hello from workload-api"},
		{name: "liveness ok even when not ready", path: "/healthz", ready: false, wantCode: http.StatusOK},
		{name: "readiness before init", path: "/readyz", ready: false, wantCode: http.StatusServiceUnavailable},
		{name: "readiness after init", path: "/readyz", ready: true, wantCode: http.StatusOK},
		{name: "work never fails at rate 0", path: "/work", ready: true, errorRate: 0, wantCode: http.StatusOK, wantBody: "work done"},
		{name: "work always fails at rate 1", path: "/work", ready: true, errorRate: 1, wantCode: http.StatusInternalServerError},
		{name: "unknown path", path: "/nope", ready: true, wantCode: http.StatusNotFound},
		{name: "metrics endpoint", path: "/metrics", ready: true, wantCode: http.StatusOK, wantBody: "go_goroutines"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(config{WorkMaxLatency: minWorkLatency, WorkErrorRate: tt.errorRate})
			s.ready.Store(tt.ready)

			rec := httptest.NewRecorder()
			s.routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if rec.Code != tt.wantCode {
				t.Errorf("code = %d, want %d", rec.Code, tt.wantCode)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestMetricsAreRecorded(t *testing.T) {
	s := newServer(config{WorkMaxLatency: minWorkLatency, WorkErrorRate: 1})
	h := s.routes()
	for range 3 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/work", nil))
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := testutil.ToFloat64(s.metrics.requests.WithLabelValues("GET", "/work", "500")); got != 3 {
		t.Errorf(`http_requests_total{path="/work",code="500"} = %v, want 3`, got)
	}
	if got := testutil.ToFloat64(s.metrics.requests.WithLabelValues("GET", "/", "200")); got != 1 {
		t.Errorf(`http_requests_total{path="/",code="200"} = %v, want 1`, got)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, name := range []string{"http_requests_total", "http_request_duration_seconds_bucket"} {
		if !strings.Contains(rec.Body.String(), name) {
			t.Errorf("/metrics does not expose %s", name)
		}
	}
}
