package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

var (
	store    *Store
	keyStore *KeyStore
	auditLog *AuditLog
	metrics  *RegistryMetrics
	webhooks *WebhookManager
	logger   *slog.Logger
	signer   *RegistrySigner
)

var version = "dev"

// Terraform Protocol Types
type WellKnown struct {
	ProvidersV1 string `json:"providers.v1"`
	ModulesV1   string `json:"modules.v1"`
}

type ProviderVersionsResponse struct {
	Versions []ProviderVersion `json:"versions"`
}

type ProviderVersion struct {
	Version   string         `json:"version"`
	Protocols []string       `json:"protocols"`
	Platforms []PlatformMeta `json:"platforms"`
}

type ProviderDownloadResponse struct {
	Protocols           []string    `json:"protocols"`
	OS                  string      `json:"os"`
	Arch                string      `json:"arch"`
	Filename            string      `json:"filename"`
	DownloadURL         string      `json:"download_url"`
	ShasumsURL          string      `json:"shasums_url,omitempty"`
	ShasumsSignatureURL string      `json:"shasums_signature_url,omitempty"`
	Shasum              string      `json:"shasum"`
	SigningKeys         SigningKeys `json:"signing_keys"`
}

type SigningKeys struct {
	GPGPublicKeys []GPGPublicKey `json:"gpg_public_keys"`
}

type GPGPublicKey struct {
	KeyID      string `json:"key_id"`
	ASCIIArmor string `json:"ascii_armor,omitempty"`
}

type ModuleVersionsResponse struct {
	Modules []ModuleVersionsEntry `json:"modules"`
}

type ModuleVersionsEntry struct {
	Source   string          `json:"source"`
	Versions []ModuleVersion `json:"versions"`
}

type ModuleVersion struct {
	Version string `json:"version"`
}

type ModuleArtifactMeta struct {
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

func moduleArtifactKey(namespace, name, provider, version string) (string, error) {
	prefix := fmt.Sprintf("modules/%s/%s/%s/%s", namespace, name, provider, version)
	data, err := store.Get(prefix + "/artifact.json")
	if err == nil {
		var meta ModuleArtifactMeta
		if json.Unmarshal(data, &meta) != nil || !moduleFileRE.MatchString(meta.Filename) {
			return "", fmt.Errorf("invalid module artifact metadata")
		}
		key := prefix + "/" + meta.Filename
		if !store.Exists(key) {
			return "", os.ErrNotExist
		}
		return key, nil
	}
	legacy := prefix + "/module.tar.gz"
	if store.Exists(legacy) {
		return legacy, nil
	}
	return "", os.ErrNotExist
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("terraform-registry %s\n", version)
		return
	}
	// Structured logging
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	// Config from environment
	basePath := envOrDefault("STORAGE_PATH", "/var/lib/terraform-registry")
	baseURL := envOrDefault("BASE_URL", "http://localhost:8080")
	port := envOrDefault("PORT", "8080")
	keysFile := envOrDefault("API_KEYS_FILE", filepath_join(basePath, "keys.json"))
	auditPath := envOrDefault("AUDIT_LOG", "")
	rateLimit := envOrDefault("RATE_LIMIT", "100")
	rateWindow := envOrDefault("RATE_WINDOW", "1m")

	// Initialize upload size limit
	SetMaxUploadSize()

	// Initialize storage
	var err error
	store, err = NewStore(basePath, baseURL, logger)
	if err != nil {
		logger.Error("failed to initialize storage", "error", err)
		os.Exit(1)
	}
	signingKeyFile := envOrDefault("SIGNING_KEY_FILE", filepath_join(basePath, "signing-key.asc"))
	signer, err = NewRegistrySigner(signingKeyFile)
	if err != nil {
		logger.Error("failed to initialize artifact signer", "error", err)
		os.Exit(1)
	}
	if err := rebuildAllProviderChecksums(); err != nil {
		logger.Warn("some legacy provider checksums could not be rebuilt", "error", err)
	}

	// Initialize API keys
	keyStore, err = NewKeyStore(keysFile, logger)
	if err != nil {
		logger.Error("failed to initialize API keys", "error", err)
		os.Exit(1)
	}

	// Initialize audit log
	auditLog, err = NewAuditLog(auditPath, logger)
	if err != nil {
		logger.Error("failed to initialize audit log", "error", err)
		os.Exit(1)
	}
	defer func() { _ = auditLog.Close() }()

	// Initialize metrics
	metrics = NewMetrics()

	// Initialize webhooks
	webhooks = NewWebhookManager(loadWebhookConfigPath(), logger)

	// Initialize rate limiter
	var rl *RateLimiter
	var rlRate int
	var rlWindow time.Duration
	if _, err := fmt.Sscanf(rateLimit, "%d", &rlRate); err != nil || rlRate <= 0 {
		rlRate = 100
	}
	if rlWindow, err = time.ParseDuration(rateWindow); err != nil {
		rlWindow = time.Minute
	}
	rl = NewRateLimiter(rlRate, rlWindow, logger)

	// Build router
	r := mux.NewRouter()

	// Middleware chain (order matters)
	r.Use(metricsMiddleware(metrics))
	r.Use(securityHeadersMiddleware)
	r.Use(rl.Middleware)
	r.Use(auditMiddleware(auditLog, keyStore))
	r.Use(authMiddleware(keyStore, logger))
	r.Use(routeValidationMiddleware)

	// Terraform protocol endpoints (always public)
	r.HandleFunc("/.well-known/terraform.json", wellKnownHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/versions", providerVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}", providerDownloadHandler).Methods("GET")
	r.HandleFunc("/{hostname}/{namespace}/{type}/index.json", networkMirrorIndexHandler).Methods("GET")
	r.HandleFunc("/{hostname}/{namespace}/{type}/{version}.json", networkMirrorVersionHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/versions", moduleVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/{version}/download", moduleDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/download", moduleLatestDownloadHandler).Methods("GET")
	r.HandleFunc("/download/{path:.*}", fileDownloadHandler).Methods("GET")

	// Health and metrics
	r.HandleFunc("/health", healthHandler).Methods("GET")
	r.HandleFunc("/metrics", prometheusHandler(metrics)).Methods("GET")

	// Management API
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/stats", registryStatsHandler).Methods("GET")
	api.HandleFunc("/providers", listProvidersHandler).Methods("GET")
	api.HandleFunc("/providers/{namespace}/{name}", getProviderHandler).Methods("GET")
	api.HandleFunc("/providers/{namespace}/{name}/{version}/{os}/{arch}", uploadProviderHandler).Methods("POST")
	api.HandleFunc("/providers/{namespace}/{name}/{version}", deleteProviderVersionHandler).Methods("DELETE")
	api.HandleFunc("/providers/{namespace}/{name}/{version}/deprecate", deprecateProviderHandler).Methods("POST")
	api.HandleFunc("/modules", listModulesHandler).Methods("GET")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}", getModuleHandler).Methods("GET")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}/{version}", uploadModuleHandler).Methods("POST")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}/{version}", deleteModuleVersionHandler).Methods("DELETE")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}/{version}/deprecate", deprecateModuleHandler).Methods("POST")
	api.HandleFunc("/gc", gcHandler).Methods("POST")

	// Web UI
	r.PathPrefix("/ui").HandlerFunc(uiHandler)
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusFound)
	})

	// Graceful shutdown
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      5 * time.Minute, // Long for large uploads
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Start periodic GC
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			n, err := store.GarbageCollect()
			if err != nil {
				logger.Error("GC failed", "error", err)
			} else if n > 0 {
				logger.Info("GC completed", "files_removed", n)
			}
		}
	}()

	// Channel to receive errors from ListenAndServe
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting terraform registry",
			"port", port,
			"storage", basePath,
			"url", baseURL,
			"rate_limit", fmt.Sprintf("%d/%s", rlRate, rateWindow),
		)
		errCh <- srv.ListenAndServe()
	}()

	// Wait for interrupt or error
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	case sig := <-quit:
		logger.Info("shutting down", "signal", sig.String())
	}

	// Graceful shutdown with 30s deadline
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("forced shutdown", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
}

// --- Protocol Handlers ---

func wellKnownHandler(w http.ResponseWriter, r *http.Request) {
	SetTerraformProtocolHeaders(w)
	_ = json.NewEncoder(w).Encode(WellKnown{
		ProvidersV1: "/v1/providers/",
		ModulesV1:   "/v1/modules/",
	})
}

func providerVersionsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	providerType := vars["type"]

	logger.Debug("provider versions", "namespace", namespace, "type", providerType)
	metrics.ProviderDownloads.Add(1)

	idx, err := store.GetProviderIndex(namespace, providerType)
	if err != nil {
		http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
		return
	}

	var versions []ProviderVersion
	for _, v := range idx.Versions {
		// Skip deprecated versions
		metaKey := fmt.Sprintf("providers/%s/%s/%s/metadata.json", namespace, providerType, v)
		if meta, err := store.GetVersionMetadata(metaKey); err == nil && meta.Deprecated {
			continue
		}

		platforms, _ := store.GetProviderPlatforms(namespace, providerType, v)
		published := platforms[:0]
		for _, platform := range platforms {
			key := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, providerType, v, platform.Filename)
			if store.Exists(key) {
				published = append(published, platform)
			}
		}
		if len(published) == 0 {
			continue
		}
		versions = append(versions, ProviderVersion{
			Version:   v,
			Protocols: []string{"5.0"},
			Platforms: published,
		})
	}

	if len(versions) == 0 {
		SetTerraformProtocolHeaders(w)
		http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
		return
	}

	SetTerraformProtocolHeaders(w)
	_ = json.NewEncoder(w).Encode(ProviderVersionsResponse{Versions: versions})
}

func providerDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	providerType := vars["type"]
	version := vars["version"]
	osName := vars["os"]
	arch := vars["arch"]

	logger.Debug("provider download", "namespace", namespace, "type", providerType,
		"version", version, "os", osName, "arch", arch)
	metrics.ProviderDownloads.Add(1)

	metaKey := fmt.Sprintf("providers/%s/%s/%s/%s_%s.json", namespace, providerType, version, osName, arch)
	metaData, err := store.Get(metaKey)
	if err != nil {
		http.Error(w, `{"error":"platform not found"}`, http.StatusNotFound)
		return
	}

	var meta PlatformMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		http.Error(w, `{"error":"invalid metadata"}`, http.StatusInternalServerError)
		return
	}

	zipKey := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, providerType, version, meta.Filename)
	if !store.Exists(zipKey) {
		http.Error(w, `{"error":"artifact not found"}`, http.StatusNotFound)
		return
	}

	checksumsName, signatureName := providerChecksumsNames(providerType, version)
	versionPrefix := fmt.Sprintf("providers/%s/%s/%s/", namespace, providerType, version)
	checksumsKey, signatureKey := versionPrefix+checksumsName, versionPrefix+signatureName
	if signer == nil || !store.Exists(checksumsKey) || !store.Exists(signatureKey) {
		http.Error(w, `{"error":"provider integrity metadata unavailable"}`, http.StatusInternalServerError)
		return
	}
	signingKeys := SigningKeys{GPGPublicKeys: []GPGPublicKey{{
		KeyID: signer.KeyID, ASCIIArmor: signer.PublicArmor,
	}}}

	SetTerraformProtocolHeaders(w)
	_ = json.NewEncoder(w).Encode(ProviderDownloadResponse{
		Protocols:           []string{"5.0"},
		OS:                  osName,
		Arch:                arch,
		Filename:            meta.Filename,
		DownloadURL:         store.DownloadURL(zipKey),
		ShasumsURL:          store.DownloadURL(checksumsKey),
		ShasumsSignatureURL: store.DownloadURL(signatureKey),
		Shasum:              meta.Shasum,
		SigningKeys:         signingKeys,
	})
}

func networkMirrorIndexHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	providerType := vars["type"]

	idx, err := store.GetProviderIndex(namespace, providerType)
	if err != nil {
		http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
		return
	}

	versionMap := make(map[string]struct{})
	for _, v := range idx.Versions {
		metaKey := fmt.Sprintf("providers/%s/%s/%s/metadata.json", namespace, providerType, v)
		if meta, err := store.GetVersionMetadata(metaKey); err == nil && meta.Deprecated {
			continue
		}
		versionMap[v] = struct{}{}
	}
	if len(versionMap) == 0 {
		http.Error(w, `{"error":"provider not found"}`, http.StatusNotFound)
		return
	}

	SetTerraformProtocolHeaders(w)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"versions": versionMap})
}

func networkMirrorVersionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	providerType := vars["type"]
	version := vars["version"]

	idx, err := store.GetProviderIndex(namespace, providerType)
	if err != nil || !indexHasVersion(idx, version) {
		http.Error(w, `{"error":"version not found"}`, http.StatusNotFound)
		return
	}
	versionMetaKey := fmt.Sprintf("providers/%s/%s/%s/metadata.json", namespace, providerType, version)
	if meta, err := store.GetVersionMetadata(versionMetaKey); err == nil && meta.Deprecated {
		http.Error(w, `{"error":"version not found"}`, http.StatusNotFound)
		return
	}

	platforms, err := store.GetProviderPlatforms(namespace, providerType, version)
	if err != nil {
		http.Error(w, `{"error":"version not found"}`, http.StatusNotFound)
		return
	}

	archives := make(map[string]map[string]interface{})
	for _, p := range platforms {
		platformKey := p.OS + "_" + p.Arch
		metaKey := fmt.Sprintf("providers/%s/%s/%s/%s_%s.json", namespace, providerType, version, p.OS, p.Arch)
		metaData, err := store.Get(metaKey)
		if err != nil {
			continue
		}
		var meta PlatformMeta
		if err := json.Unmarshal(metaData, &meta); err != nil {
			continue
		}

		zipKey := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, providerType, version, meta.Filename)
		if !store.Exists(zipKey) {
			continue
		}
		archives[platformKey] = map[string]interface{}{
			"url":    store.DownloadURL(zipKey),
			"hashes": []string{fmt.Sprintf("zh:%s", meta.Shasum)},
		}
	}
	if len(archives) == 0 {
		http.Error(w, `{"error":"version not found"}`, http.StatusNotFound)
		return
	}

	SetTerraformProtocolHeaders(w)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"archives": archives})
}

func moduleVersionsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]

	logger.Debug("module versions", "namespace", namespace, "name", name, "provider", provider)

	idx, err := store.GetModuleIndex(namespace, name, provider)
	if err != nil {
		http.Error(w, `{"error":"module not found"}`, http.StatusNotFound)
		return
	}

	var modules []ModuleVersion
	for _, v := range idx.Versions {
		metaKey := fmt.Sprintf("modules/%s/%s/%s/%s/metadata.json", namespace, name, provider, v)
		if meta, err := store.GetVersionMetadata(metaKey); err == nil && meta.Deprecated {
			continue
		}
		if _, err := moduleArtifactKey(namespace, name, provider, v); err != nil {
			continue
		}
		modules = append(modules, ModuleVersion{Version: v})
	}

	if len(modules) == 0 {
		SetTerraformProtocolHeaders(w)
		http.Error(w, `{"error":"module not found"}`, http.StatusNotFound)
		return
	}

	SetTerraformProtocolHeaders(w)
	_ = json.NewEncoder(w).Encode(ModuleVersionsResponse{Modules: []ModuleVersionsEntry{{
		Source: namespace + "/" + name + "/" + provider, Versions: modules,
	}}})
}

func moduleDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]
	version := vars["version"]

	logger.Debug("module download", "namespace", namespace, "name", name,
		"provider", provider, "version", version)
	metrics.ModuleDownloads.Add(1)

	key, err := moduleArtifactKey(namespace, name, provider, version)
	if err != nil {
		http.Error(w, `{"error":"module not found"}`, http.StatusNotFound)
		return
	}

	// Per Terraform protocol: return 204 with X-Terraform-Get header
	downloadURL := store.DownloadURL(key)
	w.Header().Set("X-Terraform-Get", downloadURL)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNoContent)
}

func moduleLatestDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]

	logger.Debug("module latest download", "namespace", namespace, "name", name, "provider", provider)

	idx, err := store.GetModuleIndex(namespace, name, provider)
	if err != nil || len(idx.Versions) == 0 {
		http.Error(w, `{"error":"no versions available"}`, http.StatusNotFound)
		return
	}

	latestVersion := ""
	latestKey := ""
	for i := len(idx.Versions) - 1; i >= 0; i-- {
		candidate := idx.Versions[i]
		metaKey := fmt.Sprintf("modules/%s/%s/%s/%s/metadata.json", namespace, name, provider, candidate)
		if meta, err := store.GetVersionMetadata(metaKey); err == nil && meta.Deprecated {
			continue
		}
		downloadKey, err := moduleArtifactKey(namespace, name, provider, candidate)
		if err == nil {
			latestVersion = candidate
			latestKey = downloadKey
			break
		}
	}
	if latestVersion == "" {
		http.Error(w, `{"error":"no versions available"}`, http.StatusNotFound)
		return
	}
	downloadURL := store.DownloadURL(latestKey)

	w.Header().Set("X-Terraform-Get", downloadURL)
	w.WriteHeader(http.StatusNoContent)
}

func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	// Only published artifacts are downloadable. Never expose indexes,
	// metadata, key files, audit logs, or temporary storage objects.
	if !isPublicArtifactPath(path) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	f, err := store.Open(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if strings.HasSuffix(path, ".tar.gz") {
		w.Header().Set("Content-Type", "application/gzip")
	} else if strings.HasSuffix(path, ".zip") {
		w.Header().Set("Content-Type", "application/zip")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if err := store.HealthCheck(); err != nil {
		http.Error(w, fmt.Sprintf("unhealthy: %v", err), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"healthy"}`))
}

// --- Helpers ---

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func filepath_join(parts ...string) string {
	return strings.Join(parts, "/")
}
