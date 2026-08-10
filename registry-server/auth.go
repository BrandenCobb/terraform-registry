package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
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
	Key         string     `json:"key,omitempty"` // Legacy input only; cleared before persistence
	Hash        string     `json:"hash"`          // SHA256 hash for comparison
	Name        string     `json:"name"`          // Human-readable name
	Permission  Permission `json:"permission"`    // read, write, or admin
	Enabled     bool       `json:"enabled"`       // Can be disabled without deletion
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
		defaultKey := os.Getenv("REGISTRY_API_KEY")
		generated := defaultKey == ""
		if generated {
			defaultKey = generateRandomKey(32)
		}
		if err := ks.createDefaultFile(path, defaultKey); err != nil {
			return nil, fmt.Errorf("create default keys file: %w", err)
		}
		if generated {
			logger.Info("created default admin API key")
			fmt.Fprintf(os.Stderr, "\n=== DEFAULT API KEY (save this!) ===\n%s\n====================================\n\n", defaultKey)
		} else {
			logger.Warn("REGISTRY_API_KEY is deprecated; migrate to API_KEYS_FILE")
		}
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
		return false
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
	Enabled  bool
	KeyStore *KeyStore
	Logger   *slog.Logger
}

// authMiddleware creates middleware that enforces RBAC on management endpoints.
// Terraform protocol endpoints (/.well-known, /v1/providers, /v1/modules, /download, /health)
// are always public so terraform init works without auth.
func authMiddleware(keyStore *KeyStore, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path

			// Only management API mutations require authentication. Protocol,
			// mirror, health, metrics, UI, and management reads are public.
			if !strings.HasPrefix(path, "/api/v1/") || r.Method == http.MethodGet || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}

			// Extract API key
			apiKey := extractAPIKey(r)
			if apiKey == "" {
				if metrics != nil {
					metrics.AuthFailures.Add(1)
				}
				httpJSONError(w, http.StatusUnauthorized, "API key required. Set X-API-Key or Authorization: Bearer header.")
				return
			}

			// Determine required permission
			required := requiredPermission(r)
			if !keyStore.HasPermission(apiKey, required) {
				if metrics != nil {
					metrics.AuthFailures.Add(1)
				}
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

func requiredPermission(r *http.Request) Permission {
	if r.URL.Path == "/api/v1/gc" {
		return PermAdmin
	}
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
	if len(config.Keys) == 0 {
		return fmt.Errorf("keys file must contain at least one API key")
	}
	seen := make(map[string]struct{}, len(config.Keys))
	enabled := 0
	migrated := false
	for i := range config.Keys {
		if config.Keys[i].Key != "" {
			if config.Keys[i].Hash == "" {
				config.Keys[i].Hash = hashKey(config.Keys[i].Key)
			}
			config.Keys[i].Key = ""
			migrated = true
		}
		config.Keys[i].Hash = strings.ToLower(strings.TrimSpace(config.Keys[i].Hash))
		decoded, err := hex.DecodeString(config.Keys[i].Hash)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("key %q has an invalid SHA-256 hash", config.Keys[i].Name)
		}
		switch config.Keys[i].Permission {
		case PermRead, PermWrite, PermAdmin:
		default:
			return fmt.Errorf("key %q has invalid permission %q", config.Keys[i].Name, config.Keys[i].Permission)
		}
		if _, exists := seen[config.Keys[i].Hash]; exists {
			return fmt.Errorf("duplicate API key hash for %q", config.Keys[i].Name)
		}
		seen[config.Keys[i].Hash] = struct{}{}
		if config.Keys[i].Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		return fmt.Errorf("keys file must contain at least one enabled API key")
	}
	if migrated {
		if err := writeKeysConfigAtomic(ks.path, config); err != nil {
			return fmt.Errorf("remove legacy plaintext keys: %w", err)
		}
		ks.logger.Warn("migrated legacy plaintext API keys to SHA-256 hashes")
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
				Hash:       hashKey(defaultKey),
				Name:       "default-admin",
				Permission: PermAdmin,
				Enabled:    true,
				CreatedAt:  time.Now(),
			},
		},
	}
	return writeKeysConfigAtomic(path, config)
}

func writeKeysConfigAtomic(path string, config APIKeysConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".keys-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
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
