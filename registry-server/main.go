package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gorilla/mux"
)

var (
	storage Storage
)

// Terraform Protocol Types
type WellKnown struct {
	ProvidersV1 string `json:"providers.v1"`
	ModulesV1   string `json:"modules.v1"`
}

// Provider Types
type ProviderVersionsResponse struct {
	Versions []ProviderVersion `json:"versions"`
}

type ProviderVersion struct {
	Version   string             `json:"version"`
	Protocols []string           `json:"protocols"`
	Platforms []ProviderPlatform `json:"platforms"`
}

type ProviderPlatform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
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
	ASCIIArmor string `json:"ascii_armor"`
}

// Module Types
type ModuleVersionsResponse struct {
	Modules []ModuleVersion `json:"modules"`
}

type ModuleVersion struct {
	Version string `json:"version"`
}

type ModuleDownloadResponse struct {
	Source string `json:"source,omitempty"` // For download endpoint
}

type ModuleLatestResponse struct {
	Version string     `json:"version"`
	Root    ModuleRoot `json:"root,omitempty"`
}

type ModuleRoot struct {
	Dependencies []string `json:"dependencies,omitempty"`
	Providers    []string `json:"providers,omitempty"`
}

// Index Types
type Index struct {
	Versions []string `json:"versions"`
}

type PlatformMetadata struct {
	Filename string `json:"filename"`
	Shasum   string `json:"shasum"`
}

func init() {
	// Skip storage initialization during tests
	if os.Getenv("SKIP_STORAGE_INIT") == "true" {
		return
	}

	storageType := os.Getenv("STORAGE_TYPE")
	if storageType == "" {
		storageType = "filesystem" // Default to filesystem
	}

	log.Printf("Initializing %s storage...", storageType)

	if storageType == "s3" {
		initS3Storage()
	} else {
		initFilesystemStorage()
	}
}

func initS3Storage() {
	bucketName := os.Getenv("S3_BUCKET")
	if bucketName == "" {
		bucketName = "terraform-registry"
	}

	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-gov-west-1"
	}

	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(region))
	if err != nil {
		log.Fatalf("unable to load SDK config, %v", err)
	}

	s3Client := s3.NewFromConfig(cfg)
	storage = NewS3Storage(s3Client, bucketName)
	log.Printf("Using S3 storage: bucket=%s region=%s", bucketName, region)
}

func initFilesystemStorage() {
	basePath := os.Getenv("STORAGE_PATH")
	if basePath == "" {
		basePath = "/var/lib/terraform-registry"
	}

	baseURL := os.Getenv("BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}

	var err error
	storage, err = NewFilesystemStorage(basePath, baseURL)
	if err != nil {
		log.Fatalf("failed to initialize filesystem storage: %v", err)
	}
	log.Printf("Using filesystem storage: path=%s url=%s", basePath, baseURL)
}

func main() {
	r := mux.NewRouter()

	// Discovery endpoint
	r.HandleFunc("/.well-known/terraform.json", wellKnownHandler).Methods("GET")

	// Provider endpoints (registry protocol)
	r.HandleFunc("/v1/providers/{namespace}/{type}/versions", providerVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}", providerDownloadHandler).Methods("GET")

	// Network mirror endpoints (Terraform CLI format)
	r.HandleFunc("/{hostname}/{namespace}/{type}/index.json", networkMirrorIndexHandler).Methods("GET")
	r.HandleFunc("/{hostname}/{namespace}/{type}/{version}.json", networkMirrorVersionHandler).Methods("GET")

	// Module endpoints
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/versions", moduleVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/{version}/download", moduleDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/download", moduleLatestDownloadHandler).Methods("GET")

	// File download endpoint (for filesystem storage)
	r.HandleFunc("/download/{path:.*}", fileDownloadHandler).Methods("GET")

	// Health check
	r.HandleFunc("/health", healthHandler).Methods("GET")

	// Management API endpoints
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/stats", registryStatsHandler).Methods("GET")

	// Provider management
	api.HandleFunc("/providers", listProvidersHandler).Methods("GET")
	api.HandleFunc("/providers/{namespace}/{name}", getProviderHandler).Methods("GET")
	api.HandleFunc("/providers/{namespace}/{name}/{version}/{os}/{arch}", requireAuth(uploadProviderHandler)).Methods("POST")
	api.HandleFunc("/providers/{namespace}/{name}/{version}", requireAuth(deleteProviderVersionHandler)).Methods("DELETE")

	// Module management
	api.HandleFunc("/modules", listModulesHandler).Methods("GET")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}", getModuleHandler).Methods("GET")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}/{version}", requireAuth(uploadModuleHandler)).Methods("POST")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}/{version}", requireAuth(deleteModuleVersionHandler)).Methods("DELETE")

	// Web UI
	r.PathPrefix("/ui").HandlerFunc(uiHandler)
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusFound)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting Terraform registry on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}

func wellKnownHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(WellKnown{
		ProvidersV1: "/v1/providers/",
		ModulesV1:   "/v1/modules/",
	})
}

// Provider Handlers
func providerVersionsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	providerType := vars["type"]

	log.Printf("Provider versions request: %s/%s", namespace, providerType)

	// Try to read index.json
	key := fmt.Sprintf("providers/%s/%s/index.json", namespace, providerType)
	obj, err := storage.GetObject(key)
	if err != nil {
		log.Printf("Error fetching provider index: %v", err)
		// Scan storage for versions
		versions, err := scanProviderVersions(namespace, providerType)
		if err != nil {
			http.Error(w, fmt.Sprintf("Provider not found: %v", err), http.StatusNotFound)
			return
		}

		response := ProviderVersionsResponse{Versions: versions}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	var index Index
	if err := json.Unmarshal(obj, &index); err != nil {
		http.Error(w, "Invalid index format", http.StatusInternalServerError)
		return
	}

	// Build response with version details
	var versions []ProviderVersion
	for _, v := range index.Versions {
		platforms, err := getProviderPlatforms(namespace, providerType, v)
		if err != nil {
			log.Printf("Error getting platforms for %s/%s@%s: %v", namespace, providerType, v, err)
			continue
		}

		versions = append(versions, ProviderVersion{
			Version:   v,
			Protocols: []string{"5.0"},
			Platforms: platforms,
		})
	}

	response := ProviderVersionsResponse{Versions: versions}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func providerDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	providerType := vars["type"]
	version := vars["version"]
	osArch := fmt.Sprintf("%s_%s", vars["os"], vars["arch"])

	log.Printf("Provider download request: %s/%s@%s (%s)", namespace, providerType, version, osArch)

	// Get platform metadata
	metaKey := fmt.Sprintf("providers/%s/%s/%s/%s.json", namespace, providerType, version, osArch)
	metaObj, err := storage.GetObject(metaKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("Platform not found: %v", err), http.StatusNotFound)
		return
	}

	var metadata PlatformMetadata
	if err := json.Unmarshal(metaObj, &metadata); err != nil {
		http.Error(w, "Invalid metadata format", http.StatusInternalServerError)
		return
	}

	// Generate download URL
	zipKey := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, providerType, version, metadata.Filename)
	downloadURL, err := storage.GenerateDownloadURL(zipKey)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error generating download URL: %v", err), http.StatusInternalServerError)
		return
	}

	response := ProviderDownloadResponse{
		Protocols:   []string{"5.0"},
		OS:          vars["os"],
		Arch:        vars["arch"],
		Filename:    metadata.Filename,
		DownloadURL: downloadURL,
		Shasum:      metadata.Shasum,
		SigningKeys: SigningKeys{
			GPGPublicKeys: []GPGPublicKey{},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// Network Mirror Handlers (Terraform CLI network_mirror format)
func networkMirrorIndexHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	// hostname is ignored (e.g., registry.terraform.io)
	namespace := vars["namespace"]
	providerType := vars["type"]

	log.Printf("Network mirror index request: %s/%s", namespace, providerType)

	// Get versions using existing logic
	key := fmt.Sprintf("providers/%s/%s/index.json", namespace, providerType)
	obj, err := storage.GetObject(key)

	var versions []string
	if err != nil {
		// Fallback to scanning
		providerVersions, err := scanProviderVersions(namespace, providerType)
		if err != nil {
			http.Error(w, "Provider not found", http.StatusNotFound)
			return
		}
		for _, pv := range providerVersions {
			versions = append(versions, pv.Version)
		}
	} else {
		var index struct {
			Versions []string `json:"versions"`
		}
		if err := json.Unmarshal(obj, &index); err != nil {
			http.Error(w, "Invalid index format", http.StatusInternalServerError)
			return
		}
		versions = index.Versions
	}

	// Network mirror index format: {"versions":{"1.0.0":{},"2.0.0":{}}}
	versionMap := make(map[string]struct{})
	for _, v := range versions {
		versionMap[v] = struct{}{}
	}

	response := map[string]interface{}{
		"versions": versionMap,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func networkMirrorVersionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	providerType := vars["type"]
	version := vars["version"]

	log.Printf("Network mirror version request: %s/%s@%s", namespace, providerType, version)

	// Get all platforms for this version
	platforms, err := getProviderPlatforms(namespace, providerType, version)
	if err != nil {
		http.Error(w, "Version not found", http.StatusNotFound)
		return
	}

	// Network mirror version format: {"archives":{"linux_amd64":{"url":"...","hashes":["h1:..."]}}}
	archives := make(map[string]map[string]interface{})

	for _, platform := range platforms {
		platformKey := fmt.Sprintf("%s_%s", platform.OS, platform.Arch)
		metadataKey := fmt.Sprintf("providers/%s/%s/%s/%s_%s.json", namespace, providerType, version, platform.OS, platform.Arch)

		obj, err := storage.GetObject(metadataKey)
		if err != nil {
			continue
		}

		var metadata struct {
			Filename string `json:"filename"`
			Shasum   string `json:"shasum"`
		}
		if err := json.Unmarshal(obj, &metadata); err != nil {
			continue
		}

		zipKey := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, providerType, version, metadata.Filename)
		downloadURL, err := storage.GenerateDownloadURL(zipKey)
		if err != nil {
			continue
		}

		archives[platformKey] = map[string]interface{}{
			"url":    downloadURL,
			"hashes": []string{fmt.Sprintf("zh:%s", metadata.Shasum)},
		}
	}

	response := map[string]interface{}{
		"archives": archives,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// Module Handlers
func moduleVersionsHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]

	log.Printf("Module versions request: %s/%s/%s", namespace, name, provider)

	// Read index.json
	key := fmt.Sprintf("modules/%s/%s/%s/index.json", namespace, name, provider)
	obj, err := storage.GetObject(key)
	if err != nil {
		log.Printf("Error fetching module index: %v", err)
		// Scan storage for versions
		versions, err := scanModuleVersions(namespace, name, provider)
		if err != nil {
			http.Error(w, fmt.Sprintf("Module not found: %v", err), http.StatusNotFound)
			return
		}

		response := ModuleVersionsResponse{Modules: versions}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	var index Index
	if err := json.Unmarshal(obj, &index); err != nil {
		http.Error(w, "Invalid index format", http.StatusInternalServerError)
		return
	}

	var modules []ModuleVersion
	for _, v := range index.Versions {
		modules = append(modules, ModuleVersion{Version: v})
	}

	response := ModuleVersionsResponse{Modules: modules}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func moduleDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]
	version := vars["version"]

	log.Printf("Module download request: %s/%s/%s@%s", namespace, name, provider, version)

	// Generate download URL for module tarball
	key := fmt.Sprintf("modules/%s/%s/%s/%s/module.tar.gz", namespace, name, provider, version)
	downloadURL, err := storage.GenerateDownloadURL(key)
	if err != nil {
		http.Error(w, fmt.Sprintf("Module not found: %v", err), http.StatusNotFound)
		return
	}

	// Terraform expects an HTTP redirect to the download URL
	http.Redirect(w, r, downloadURL, http.StatusFound)
}

func moduleLatestDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]

	log.Printf("Module latest download request: %s/%s/%s", namespace, name, provider)

	// Get latest version from index
	key := fmt.Sprintf("modules/%s/%s/%s/index.json", namespace, name, provider)
	obj, err := storage.GetObject(key)
	if err != nil {
		http.Error(w, "Module not found", http.StatusNotFound)
		return
	}

	var index Index
	if err := json.Unmarshal(obj, &index); err != nil {
		http.Error(w, "Invalid index format", http.StatusInternalServerError)
		return
	}

	if len(index.Versions) == 0 {
		http.Error(w, "No versions available", http.StatusNotFound)
		return
	}

	// Get latest version (last in sorted list)
	latestVersion := index.Versions[len(index.Versions)-1]

	// Generate download URL
	downloadKey := fmt.Sprintf("modules/%s/%s/%s/%s/module.tar.gz", namespace, name, provider, latestVersion)
	downloadURL, err := storage.GenerateDownloadURL(downloadKey)
	if err != nil {
		http.Error(w, "Module not found", http.StatusNotFound)
		return
	}

	// Return version info
	w.Header().Set("X-Terraform-Get", downloadURL)
	response := ModuleLatestResponse{
		Version: latestVersion,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// File download handler for filesystem storage
func fileDownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	path := vars["path"]

	log.Printf("File download request: %s", path)

	// Only works with filesystem storage
	fsStorage, ok := storage.(*FilesystemStorage)
	if !ok {
		http.Error(w, "Direct file download not available", http.StatusNotImplemented)
		return
	}

	// Set appropriate headers
	if strings.HasSuffix(path, ".tar.gz") {
		w.Header().Set("Content-Type", "application/gzip")
	} else if strings.HasSuffix(path, ".zip") {
		w.Header().Set("Content-Type", "application/zip")
	}

	if err := fsStorage.ServeFile(w, path); err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if err := storage.HealthCheck(); err != nil {
		http.Error(w, fmt.Sprintf("Storage not accessible: %v", err), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

// Helper functions
func scanProviderVersions(namespace, providerType string) ([]ProviderVersion, error) {
	prefix := fmt.Sprintf("providers/%s/%s/", namespace, providerType)
	_, prefixes, err := storage.ListObjects(prefix, "/")
	if err != nil {
		return nil, err
	}

	versionMap := make(map[string]bool)
	for _, p := range prefixes {
		parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
		if len(parts) >= 4 {
			version := parts[3]
			versionMap[version] = true
		}
	}

	var versions []ProviderVersion
	for v := range versionMap {
		platforms, err := getProviderPlatforms(namespace, providerType, v)
		if err != nil {
			continue
		}
		versions = append(versions, ProviderVersion{
			Version:   v,
			Protocols: []string{"5.0"},
			Platforms: platforms,
		})
	}

	return versions, nil
}

func getProviderPlatforms(namespace, providerType, version string) ([]ProviderPlatform, error) {
	prefix := fmt.Sprintf("providers/%s/%s/%s/", namespace, providerType, version)
	objects, _, err := storage.ListObjects(prefix, "")
	if err != nil {
		return nil, err
	}

	platformMap := make(map[string]bool)
	for _, obj := range objects {
		if strings.HasSuffix(obj, ".json") && !strings.HasSuffix(obj, "index.json") {
			parts := strings.Split(obj, "/")
			if len(parts) >= 5 {
				filename := parts[4]
				osArch := strings.TrimSuffix(filename, ".json")
				platformMap[osArch] = true
			}
		}
	}

	var platforms []ProviderPlatform
	for osArch := range platformMap {
		parts := strings.Split(osArch, "_")
		if len(parts) == 2 {
			platforms = append(platforms, ProviderPlatform{
				OS:   parts[0],
				Arch: parts[1],
			})
		}
	}

	sort.Slice(platforms, func(i, j int) bool {
		return platforms[i].OS+platforms[i].Arch < platforms[j].OS+platforms[j].Arch
	})

	return platforms, nil
}

func scanModuleVersions(namespace, name, provider string) ([]ModuleVersion, error) {
	prefix := fmt.Sprintf("modules/%s/%s/%s/", namespace, name, provider)
	_, prefixes, err := storage.ListObjects(prefix, "/")
	if err != nil {
		return nil, err
	}

	var versions []ModuleVersion
	for _, p := range prefixes {
		parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
		if len(parts) >= 5 {
			version := parts[4]
			versions = append(versions, ModuleVersion{Version: version})
		}
	}

	return versions, nil
}
