package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// --- Rate Limiter ---

// RateLimiter implements per-IP rate limiting using a token bucket.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int           // requests per window
	window   time.Duration // time window
	logger   *slog.Logger
}

type visitor struct {
	tokens   int
	lastSeen time.Time
}

// NewRateLimiter creates a rate limiter allowing `rate` requests per `window`.
func NewRateLimiter(rate int, window time.Duration, logger *slog.Logger) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
		logger:   logger,
	}
	// Cleanup stale entries every minute
	go func() {
		for {
			time.Sleep(time.Minute)
			rl.cleanup()
		}
	}()
	return rl
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, v := range rl.visitors {
		if time.Since(v.lastSeen) > rl.window*2 {
			delete(rl.visitors, ip)
		}
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		rl.visitors[ip] = &visitor{tokens: rl.rate - 1, lastSeen: time.Now()}
		return true
	}

	elapsed := time.Since(v.lastSeen)
	v.lastSeen = time.Now()

	// Refill tokens based on elapsed time
	refill := int(elapsed.Seconds() * float64(rl.rate) / rl.window.Seconds())
	v.tokens += refill
	if v.tokens > rl.rate {
		v.tokens = rl.rate
	}

	if v.tokens <= 0 {
		return false
	}
	v.tokens--
	return true
}

// Middleware returns rate limiting middleware.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = strings.Split(xff, ",")[0]
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			ip = xri
		}

		if !rl.allow(ip) {
			rl.logger.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
			http.Error(w, `{"success":false,"message":"Rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Audit Logger ---

// AuditLog writes structured audit entries for all management operations.
type AuditLog struct {
	logger   *slog.Logger
	file     *os.File
	mu       sync.Mutex
}

// NewAuditLog creates an audit logger that writes to the given file path.
// If path is empty, writes to stderr only.
func NewAuditLog(path string, logger *slog.Logger) (*AuditLog, error) {
	al := &AuditLog{logger: logger}
	if path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("open audit log: %w", err)
		}
		al.file = f
	}
	return al, nil
}

// Log writes an audit entry.
func (al *AuditLog) Log(event, keyName, method, path, remote string, status int, detail string) {
	entry := fmt.Sprintf(`{"time":%q,"event":%q,"key":%q,"method":%q,"path":%q,"remote":%q,"status":%d,"detail":%q}`,
		time.Now().UTC().Format(time.RFC3339), event, keyName, method, path, remote, status, detail)

	al.mu.Lock()
	defer al.mu.Unlock()

	al.logger.Info("audit",
		"event", event,
		"key", keyName,
		"method", method,
		"path", path,
		"remote", remote,
		"status", status,
	)

	if al.file != nil {
		_, _ = fmt.Fprintln(al.file, entry)
	}
}

// Close closes the audit log file.
func (al *AuditLog) Close() error {
	if al.file != nil {
		return al.file.Close()
	}
	return nil
}

// auditMiddleware logs all requests to the management API.
func auditMiddleware(al *AuditLog, ks *KeyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
				next.ServeHTTP(w, r)
				return
			}

			keyName := "anonymous"
			apiKey := extractAPIKey(r)
			if apiKey != "" {
				if ak := ks.Validate(apiKey); ak != nil {
					keyName = ak.Name
				}
			}

			rw := &responseWriter{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, r)

			al.Log(
				"api_request",
				keyName,
				r.Method,
				r.URL.Path,
				r.RemoteAddr,
				rw.status,
				fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// --- Upload Validator ---

// maxUploadSize is the configurable maximum upload size in bytes.
var maxUploadSize int64 = 500 * 1024 * 1024 // 500MB default

// SetMaxUploadSize sets the maximum upload size from environment.
func SetMaxUploadSize() {
	if s := os.Getenv("MAX_UPLOAD_MB"); s != "" {
		var mb int
		if _, err := fmt.Sscanf(s, "%d", &mb); err == nil && mb > 0 {
			maxUploadSize = int64(mb) * 1024 * 1024
		}
	}
}

// ValidateUpload checks that uploaded content is a valid zip or tar.gz.
func ValidateUpload(data []byte, expectedType string) error {
	if len(data) < 4 {
		return fmt.Errorf("file too small to be a valid archive")
	}

	switch expectedType {
	case "zip":
		// ZIP magic bytes: PK\x03\x04
		if data[0] != 'P' || data[1] != 'K' || data[2] != 3 || data[3] != 4 {
			return fmt.Errorf("not a valid ZIP file (missing PK magic bytes)")
		}
	case "tar.gz":
		// GZIP magic bytes: \x1f\x8b
		if data[0] != 0x1f || data[1] != 0x8b {
			return fmt.Errorf("not a valid GZIP file (missing gzip magic bytes)")
		}
	}
	return nil
}

// --- Protocol Helpers ---

// SetTerraformProtocolHeaders sets required headers for Terraform protocol responses.
func SetTerraformProtocolHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Terraform-Protocol-Version", "5.0")
	w.Header().Set("X-Terraform-Protocol-Versions", "5.0")
}

// Ensure io.Writer is satisfied
var _ io.Writer = (*responseWriter)(nil)
