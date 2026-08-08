package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

// RegistryMetrics tracks key operational metrics.
// Uses atomic operations for lock-free concurrent access.
type RegistryMetrics struct {
	// Counters
	RequestsTotal      atomic.Int64
	RequestsOK         atomic.Int64
	RequestsErr        atomic.Int64
	ProviderUploads    atomic.Int64
	ProviderDownloads  atomic.Int64
	ModuleUploads      atomic.Int64
	ModuleDownloads    atomic.Int64
	AuthFailures       atomic.Int64
	RateLimitHits      atomic.Int64

	// Gauges (set directly)
	startTime time.Time
}

// NewMetrics creates a new metrics instance.
func NewMetrics() *RegistryMetrics {
	return &RegistryMetrics{
		startTime: time.Now(),
	}
}

// Snapshot returns a point-in-time metrics snapshot.
type MetricsSnapshot struct {
	Uptime             string `json:"uptime"`
	UptimeSeconds      int64  `json:"uptime_seconds"`
	RequestsTotal      int64  `json:"requests_total"`
	RequestsOK         int64  `json:"requests_ok"`
	RequestsErr        int64  `json:"requests_err"`
	ProviderUploads    int64  `json:"provider_uploads"`
	ProviderDownloads  int64  `json:"provider_downloads"`
	ModuleUploads      int64  `json:"module_uploads"`
	ModuleDownloads    int64  `json:"module_downloads"`
	AuthFailures       int64  `json:"auth_failures"`
	RateLimitHits      int64  `json:"rate_limit_hits"`
}

// Snapshot returns current metrics.
func (m *RegistryMetrics) Snapshot() MetricsSnapshot {
	uptime := time.Since(m.startTime)
	return MetricsSnapshot{
		Uptime:             uptime.Round(time.Second).String(),
		UptimeSeconds:      int64(uptime.Seconds()),
		RequestsTotal:      m.RequestsTotal.Load(),
		RequestsOK:         m.RequestsOK.Load(),
		RequestsErr:        m.RequestsErr.Load(),
		ProviderUploads:    m.ProviderUploads.Load(),
		ProviderDownloads:  m.ProviderDownloads.Load(),
		ModuleUploads:      m.ModuleUploads.Load(),
		ModuleDownloads:    m.ModuleDownloads.Load(),
		AuthFailures:       m.AuthFailures.Load(),
		RateLimitHits:      m.RateLimitHits.Load(),
	}
}

// metricsHandler serves /metrics as JSON (compatible with Prometheus json_exporter).
func metricsHandler(metrics *RegistryMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := metrics.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}
}

// metricsMiddleware wraps a handler to track request counts.
func metricsMiddleware(metrics *RegistryMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			metrics.RequestsTotal.Add(1)
			rw := &metricsResponseWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)
			if rw.status < 400 {
				metrics.RequestsOK.Add(1)
			} else {
				metrics.RequestsErr.Add(1)
			}
		})
	}
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rw *metricsResponseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *metricsResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.wroteHeader = true
	}
	return rw.ResponseWriter.Write(b)
}

// PrometheusFormat returns metrics in Prometheus text exposition format.
// This is more compatible with Prometheus scraping than JSON.
func (m *RegistryMetrics) PrometheusFormat() string {
	snap := m.Snapshot()
	return fmt.Sprintf(`# HELP terraform_registry_requests_total Total HTTP requests
# TYPE terraform_registry_requests_total counter
terraform_registry_requests_total %d
# HELP terraform_registry_requests_ok Successful HTTP requests
# TYPE terraform_registry_requests_ok counter
terraform_registry_requests_ok %d
# HELP terraform_registry_requests_err Error HTTP requests
# TYPE terraform_registry_requests_err counter
terraform_registry_requests_err %d
# HELP terraform_registry_provider_uploads Total provider uploads
# TYPE terraform_registry_provider_uploads counter
terraform_registry_provider_uploads %d
# HELP terraform_registry_provider_downloads Total provider downloads
# TYPE terraform_registry_provider_downloads counter
terraform_registry_provider_downloads %d
# HELP terraform_registry_module_uploads Total module uploads
# TYPE terraform_registry_module_uploads counter
terraform_registry_module_uploads %d
# HELP terraform_registry_module_downloads Total module downloads
# TYPE terraform_registry_module_downloads counter
terraform_registry_module_downloads %d
# HELP terraform_registry_auth_failures Total authentication failures
# TYPE terraform_registry_auth_failures counter
terraform_registry_auth_failures %d
# HELP terraform_registry_rate_limit_hits Total rate limit hits
# TYPE terraform_registry_rate_limit_hits counter
terraform_registry_rate_limit_hits %d
# HELP terraform_registry_uptime_seconds Uptime in seconds
# TYPE terraform_registry_uptime_seconds gauge
terraform_registry_uptime_seconds %d
`,
		snap.RequestsTotal,
		snap.RequestsOK,
		snap.RequestsErr,
		snap.ProviderUploads,
		snap.ProviderDownloads,
		snap.ModuleUploads,
		snap.ModuleDownloads,
		snap.AuthFailures,
		snap.RateLimitHits,
		snap.UptimeSeconds,
	)
}

// prometheusHandler serves /metrics in Prometheus text format when Accept header requests it.
func prometheusHandler(metrics *RegistryMetrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if contains(accept, "text/plain") || contains(accept, "openmetrics") {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = w.Write([]byte(metrics.PrometheusFormat()))
		} else {
			metricsHandler(metrics)(w, r)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
