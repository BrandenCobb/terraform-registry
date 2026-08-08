package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Permission levels for API keys
type Permission string

const (
	PermRead  Permission = "read"  // GET endpoints, terraform protocol
	PermWrite Permission = "write" // POST/PUT endpoints (upload)
	PermAdmin Permission = "admin" // DELETE, config changes, key management
)

// APIKey represents a registered API key.
type APIKey struct {
	Key         string     `json:"key"`                    // The actual key (hashed below for storage)
	Hash        string     `json:"hash"`                   // SHA256 hash for comparison
	Name        string     `json:"name"`                   // Human-readable name
	Permission  Permission `json:"permission"`             // read, write, or admin
	Enabled     bool       `json:"enabled"`                // Can be disabled without deletion
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	Description string     `json:"description,omitempty"`
}

// APIKeysConfig is the structure of the keys.json file.
type APIKeysConfig struct {
	Keys []APIKey `json:"keys"`
}

// KeyStore manages API keys with hot-reload on file change.
type KeyStore struct {
	path     string
	keys     map[string]*APIKey // hash -> key
	keysList []*APIKey
	mu       sync.RWMutex
	logger   *slog.Logger
	lastMod  time.Time
}

// NewKeyStore creates a key store from a JSON file. If the file doesn't exist,
// it creates one with a default admin key.
func NewKeyStore(path string, logger *slog.Logger) (*KeyStore, error) {
	ks := &KeyStore{
		path:   path,
		keys:   make(map[string]*APIKey),
		logger: logger,
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Generate default admin key
		defaultKey := generateRandomKey(32)
		if err := ks.createDefaultFile(path, defaultKey); err != nil {
			return nil, fmt.Errorf("create default keys file: %w", err)
		}
		logger.Info("Created default API key", "key", defaultKey, "permission", "admin")
		fmt.Fprintf(os.Stderr, "\n=== DEFAULT API KEY (save this!) ===\n%s\n====================================\n\n", defaultKey)
	}

	if err := ks.reload(); err != nil {
		return nil, fmt.Errorf("load keys: %w", err)
	}
	return ks, nil
}

// HasKeys returns true if any API keys are configured.
func (ks *KeyStore) HasKeys() bool {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return len(ks.keys) > 0
}

// Validate checks a key and returns the associated APIKey if valid.
func (ks *KeyStore) Validate(key string) *APIKey {
	if key == "" {
		return nil
	}

	// Hot-reload if file changed
	ks.maybeReload()

	ks.mu.RLock()
	defer ks.mu.RUnlock()

	hash := hashKey(key)
	for _, ak := range ks.keysList {
		if ak.Enabled && subtle.ConstantTimeCompare([]byte(ak.Hash), []byte(hash)) == 1 {
			return ak
		}
	}
	return nil
}

// HasPermission checks if a key has the required permission level.
func (ks *KeyStore) HasPermission(key string, required Permission) bool {
	ak := ks.Validate(key)
	if ak == nil {
		return !ks.HasKeys() // No keys configured = open access
	}
	switch required {
	case PermRead:
		return true // All authenticated keys have read
	case PermWrite:
		return ak.Permission == PermWrite || ak.Permission == PermAdmin
	case PermAdmin:
		return ak.Permission == PermAdmin
	}
	return false
}

// --- Auth Middleware ---

// AuthConfig holds auth configuration.
type AuthConfig struct {
	Enabled bool
	KeyStore *KeyStore
	Logger  *slog.Logger
}

// authMiddleware creates middleware that enforces RBAC on management endpoints.
// Terraform protocol endpoints (/.well-known, /v1/providers, /v1/modules, /download, /health)
// are always public so terraform init works without auth.
func authMiddleware(keyStore *KeyStore, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Public paths: Terraform protocol endpoints
			if isPublicPath(path) {
				next.ServeHTTP(w, r)
				return
			}

			// If no keys configured, allow all (open mode)
			if !keyStore.HasKeys() {
				next.ServeHTTP(w, r)
				return
			}

			// Extract API key
			apiKey := extractAPIKey(r)
			if apiKey == "" {
				httpJSONError(w, http.StatusUnauthorized, "API key required. Set X-API-Key header or api_key query parameter.")
				return
			}

			// Determine required permission
			required := requiredPermission(r)
			if !keyStore.HasPermission(apiKey, required) {
				ak := keyStore.Validate(apiKey)
				name := "unknown"
				if ak != nil {
					name = ak.Name
				}
				logger.Warn("permission denied",
					"key", name,
					"required", string(required),
					"method", r.Method,
					"path", path,
					"remote", r.RemoteAddr,
				)
				httpJSONError(w, http.StatusForbidden, fmt.Sprintf("Insufficient permissions. Required: %s", required))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isPublicPath(path string) bool {
	public := []string{
		"/.well-known/",
		"/v1/providers/",
		"/v1/modules/",
		"/download/",
		"/health",
		"/metrics",
		"/ui",
		"/api/v1/stats",
	}
	for _, p := range public {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	// GET on list/detail API endpoints is public for browsing
	if strings.HasPrefix(path, "/api/v1/") {
		return true // Read-only browsing is always public
	}
	return false
}

func requiredPermission(r *http.Request) Permission {
	switch r.Method {
	case "DELETE":
		return PermAdmin
	case "POST", "PUT", "PATCH":
		return PermWrite
	default:
		return PermRead
	}
}

func extractAPIKey(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	if key := r.URL.Query().Get("api_key"); key != "" {
		return key
	}
	// Also support Bearer token
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// --- File operations ---

func (ks *KeyStore) reload() error {
	data, err := os.ReadFile(ks.path)
	if err != nil {
		return err
	}

	var config APIKeysConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse keys file: %w", err)
	}

	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.keys = make(map[string]*APIKey)
	ks.keysList = make([]*APIKey, len(config.Keys))
	for i := range config.Keys {
		ks.keysList[i] = &config.Keys[i]
		ks.keys[config.Keys[i].Hash] = &config.Keys[i]
	}

	info, _ := os.Stat(ks.path)
	if info != nil {
		ks.lastMod = info.ModTime()
	}
	return nil
}

func (ks *KeyStore) maybeReload() {
	info, err := os.Stat(ks.path)
	if err != nil {
		return
	}
	ks.mu.RLock()
	needsReload := info.ModTime().After(ks.lastMod)
	ks.mu.RUnlock()

	if needsReload {
		if err := ks.reload(); err != nil {
			ks.logger.Error("failed to reload API keys", "error", err)
		}
	}
}

func (ks *KeyStore) createDefaultFile(path, defaultKey string) error {
	config := APIKeysConfig{
		Keys: []APIKey{
			{
				Key:        defaultKey,
				Hash:       hashKey(defaultKey),
				Name:       "default-admin",
				Permission: PermAdmin,
				Enabled:    true,
				CreatedAt:  time.Now(),
			},
		},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// --- Helpers ---

func hashKey(key string) string {
	h := sha256Sum([]byte(key))
	return fmt.Sprintf("%x", h)
}

// httpJSONError writes a JSON error response.
func httpJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"success":false,"message":%q}`, message)
}
