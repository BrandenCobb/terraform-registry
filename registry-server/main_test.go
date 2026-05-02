package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
)

func setupTestRouter(t *testing.T) (*mux.Router, *FilesystemStorage, string) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-integration-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	storage, err = NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	r := mux.NewRouter()
	r.HandleFunc("/.well-known/terraform.json", wellKnownHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/versions", providerVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}", providerDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/versions", moduleVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/{version}/download", moduleDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/download", moduleLatestDownloadHandler).Methods("GET")
	r.HandleFunc("/download/{path:.*}", fileDownloadHandler).Methods("GET")
	r.HandleFunc("/health", healthHandler).Methods("GET")

	return r, storage.(*FilesystemStorage), tmpDir
}

func TestWellKnownHandler(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/.well-known/terraform.json", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response WellKnown
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response.ProvidersV1 != "/v1/providers/" {
		t.Errorf("expected providers.v1 '/v1/providers/', got '%s'", response.ProvidersV1)
	}

	if response.ModulesV1 != "/v1/modules/" {
		t.Errorf("expected modules.v1 '/v1/modules/', got '%s'", response.ModulesV1)
	}
}

func TestHealthHandler(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if w.Body.String() != "OK" {
		t.Errorf("expected body 'OK', got '%s'", w.Body.String())
	}
}

func TestProviderVersionsHandler(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create test provider structure
	namespace := "hashicorp"
	providerType := "aws"
	version := "6.31.0"

	// Create index.json
	index := Index{Versions: []string{version}}
	indexData, _ := json.Marshal(index)
	indexKey := filepath.Join("providers", namespace, providerType, "index.json")
	if err := fs.PutObject(indexKey, indexData); err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	// Create platform metadata
	platformMeta := PlatformMetadata{
		Filename: "terraform-provider-aws_v6.31.0_linux_amd64.zip",
		Shasum:   "abc123",
	}
	metaData, _ := json.Marshal(platformMeta)
	metaKey := filepath.Join("providers", namespace, providerType, version, "linux_amd64.json")
	if err := fs.PutObject(metaKey, metaData); err != nil {
		t.Fatalf("failed to create metadata: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response ProviderVersionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(response.Versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(response.Versions))
	}

	if response.Versions[0].Version != version {
		t.Errorf("expected version %s, got %s", version, response.Versions[0].Version)
	}

	if len(response.Versions[0].Platforms) != 1 {
		t.Errorf("expected 1 platform, got %d", len(response.Versions[0].Platforms))
	}
}

func TestProviderVersionsHandlerNotFound(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/v1/providers/nonexistent/provider/versions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// When no index exists, scanProviderVersions returns empty list with 200
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response ProviderVersionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(response.Versions) != 0 {
		t.Errorf("expected 0 versions for non-existent provider, got %d", len(response.Versions))
	}
}

func TestProviderDownloadHandler(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	namespace := "hashicorp"
	providerType := "aws"
	version := "6.31.0"
	osArch := "linux_amd64"

	// Create platform metadata
	platformMeta := PlatformMetadata{
		Filename: "terraform-provider-aws_v6.31.0_linux_amd64.zip",
		Shasum:   "abc123def456",
	}
	metaData, _ := json.Marshal(platformMeta)
	metaKey := filepath.Join("providers", namespace, providerType, version, osArch+".json")
	if err := fs.PutObject(metaKey, metaData); err != nil {
		t.Fatalf("failed to create metadata: %v", err)
	}

	// Create dummy zip file
	zipKey := filepath.Join("providers", namespace, providerType, version, platformMeta.Filename)
	if err := fs.PutObject(zipKey, []byte("fake zip data")); err != nil {
		t.Fatalf("failed to create zip: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/6.31.0/download/linux/amd64", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response ProviderDownloadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response.Filename != platformMeta.Filename {
		t.Errorf("expected filename %s, got %s", platformMeta.Filename, response.Filename)
	}

	if response.Shasum != platformMeta.Shasum {
		t.Errorf("expected shasum %s, got %s", platformMeta.Shasum, response.Shasum)
	}

	if response.OS != "linux" {
		t.Errorf("expected OS 'linux', got '%s'", response.OS)
	}

	if response.Arch != "amd64" {
		t.Errorf("expected Arch 'amd64', got '%s'", response.Arch)
	}
}

func TestModuleVersionsHandler(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	namespace := "example"
	name := "vpc"
	provider := "aws"
	version := "1.0.0"

	// Create index.json
	index := Index{Versions: []string{version}}
	indexData, _ := json.Marshal(index)
	indexKey := filepath.Join("modules", namespace, name, provider, "index.json")
	if err := fs.PutObject(indexKey, indexData); err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/versions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response ModuleVersionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(response.Modules) != 1 {
		t.Errorf("expected 1 module version, got %d", len(response.Modules))
	}

	if response.Modules[0].Version != version {
		t.Errorf("expected version %s, got %s", version, response.Modules[0].Version)
	}
}

func TestModuleDownloadHandler(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	namespace := "example"
	name := "vpc"
	provider := "aws"
	version := "1.0.0"

	// Create module tarball
	moduleKey := filepath.Join("modules", namespace, name, provider, version, "module.tar.gz")
	if err := fs.PutObject(moduleKey, []byte("fake tarball")); err != nil {
		t.Fatalf("failed to create module: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/1.0.0/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should redirect with 302
	if w.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	expectedPrefix := "http://localhost:8080/download/"
	if len(location) == 0 || location[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("expected Location to start with %s, got %s", expectedPrefix, location)
	}
}

func TestModuleLatestDownloadHandler(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	namespace := "example"
	name := "vpc"
	provider := "aws"
	versions := []string{"1.0.0", "1.1.0", "2.0.0"}

	// Create index.json
	index := Index{Versions: versions}
	indexData, _ := json.Marshal(index)
	indexKey := filepath.Join("modules", namespace, name, provider, "index.json")
	if err := fs.PutObject(indexKey, indexData); err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	// Create module tarball for latest version
	moduleKey := filepath.Join("modules", namespace, name, provider, "2.0.0", "module.tar.gz")
	if err := fs.PutObject(moduleKey, []byte("fake tarball")); err != nil {
		t.Fatalf("failed to create module: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response ModuleLatestResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response.Version != "2.0.0" {
		t.Errorf("expected latest version '2.0.0', got '%s'", response.Version)
	}

	xTerraformGet := w.Header().Get("X-Terraform-Get")
	if len(xTerraformGet) == 0 {
		t.Error("expected X-Terraform-Get header to be set")
	}
}

func TestFileDownloadHandler(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create test file
	fileKey := "test/file.zip"
	fileData := []byte("test zip data")
	if err := fs.PutObject(fileKey, fileData); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	req := httptest.NewRequest("GET", "/download/test/file.zip", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "application/zip" {
		t.Errorf("expected Content-Type 'application/zip', got '%s'", w.Header().Get("Content-Type"))
	}

	if w.Body.String() != string(fileData) {
		t.Errorf("file content mismatch")
	}
}

func TestScanProviderVersions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-scan-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err = NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	namespace := "hashicorp"
	providerType := "aws"

	// Create multiple versions
	versions := []string{"6.30.0", "6.31.0"}
	for _, version := range versions {
		platformMeta := PlatformMetadata{
			Filename: "terraform-provider-aws_v" + version + "_linux_amd64.zip",
			Shasum:   "abc123",
		}
		metaData, _ := json.Marshal(platformMeta)
		metaKey := filepath.Join("providers", namespace, providerType, version, "linux_amd64.json")
		fs := storage.(*FilesystemStorage)
		if err := fs.PutObject(metaKey, metaData); err != nil {
			t.Fatalf("failed to create metadata: %v", err)
		}
	}

	result, err := scanProviderVersions(namespace, providerType)
	if err != nil {
		t.Fatalf("scanProviderVersions failed: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 versions, got %d", len(result))
	}
}

func TestGetProviderPlatforms(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-platforms-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err = NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	namespace := "hashicorp"
	providerType := "aws"
	version := "6.31.0"

	// Create multiple platforms
	platforms := []string{"linux_amd64", "linux_arm64", "darwin_amd64"}
	fs := storage.(*FilesystemStorage)
	for _, platform := range platforms {
		platformMeta := PlatformMetadata{
			Filename: "terraform-provider-aws_v" + version + "_" + platform + ".zip",
			Shasum:   "abc123",
		}
		metaData, _ := json.Marshal(platformMeta)
		metaKey := filepath.Join("providers", namespace, providerType, version, platform+".json")
		if err := fs.PutObject(metaKey, metaData); err != nil {
			t.Fatalf("failed to create metadata: %v", err)
		}
	}

	result, err := getProviderPlatforms(namespace, providerType, version)
	if err != nil {
		t.Fatalf("getProviderPlatforms failed: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 platforms, got %d", len(result))
	}
}

func TestScanModuleVersions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-modules-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err = NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	namespace := "example"
	name := "vpc"
	provider := "aws"
	versions := []string{"1.0.0", "1.1.0"}

	fs := storage.(*FilesystemStorage)
	for _, version := range versions {
		moduleKey := filepath.Join("modules", namespace, name, provider, version, "module.tar.gz")
		if err := fs.PutObject(moduleKey, []byte("fake tarball")); err != nil {
			t.Fatalf("failed to create module: %v", err)
		}
	}

	result, err := scanModuleVersions(namespace, name, provider)
	if err != nil {
		t.Fatalf("scanModuleVersions failed: %v", err)
	}

	if len(result) != 2 {
		t.Errorf("expected 2 versions, got %d", len(result))
	}
}

func TestProviderDownloadHandlerNotFound(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/1.0.0/download/linux/amd64", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestProviderDownloadHandlerInvalidMetadata(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create invalid JSON metadata
	metaKey := filepath.Join("providers", "hashicorp", "aws", "1.0.0", "linux_amd64.json")
	if err := fs.PutObject(metaKey, []byte("invalid json")); err != nil {
		t.Fatalf("failed to create metadata: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/1.0.0/download/linux/amd64", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestModuleVersionsHandlerNotFound(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/v1/modules/nonexistent/module/aws/versions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response ModuleVersionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(response.Modules) != 0 {
		t.Errorf("expected 0 modules for non-existent module, got %d", len(response.Modules))
	}
}

func TestModuleVersionsHandlerInvalidIndex(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create invalid JSON index
	indexKey := filepath.Join("modules", "example", "vpc", "aws", "index.json")
	if err := fs.PutObject(indexKey, []byte("invalid json")); err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/versions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestModuleDownloadHandlerNotFound(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/1.0.0/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Module download generates a redirect URL even if file doesn't exist
	// The actual 404 happens when following the redirect
	if w.Code != http.StatusFound {
		t.Errorf("expected status 302, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if len(location) == 0 {
		t.Error("expected Location header to be set")
	}
}

func TestModuleLatestDownloadHandlerNotFound(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/v1/modules/nonexistent/module/aws/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestModuleLatestDownloadHandlerInvalidIndex(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create invalid JSON index
	indexKey := filepath.Join("modules", "example", "vpc", "aws", "index.json")
	if err := fs.PutObject(indexKey, []byte("invalid json")); err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestModuleLatestDownloadHandlerNoVersions(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create empty index
	index := Index{Versions: []string{}}
	indexData, _ := json.Marshal(index)
	indexKey := filepath.Join("modules", "example", "vpc", "aws", "index.json")
	if err := fs.PutObject(indexKey, indexData); err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/download", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestFileDownloadHandlerNotFound(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/download/nonexistent/file.zip", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestFileDownloadHandlerTarGz(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create test file
	fileKey := "test/file.tar.gz"
	fileData := []byte("test tarball data")
	if err := fs.PutObject(fileKey, fileData); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	req := httptest.NewRequest("GET", "/download/test/file.tar.gz", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "application/gzip" {
		t.Errorf("expected Content-Type 'application/gzip', got '%s'", w.Header().Get("Content-Type"))
	}
}

func TestProviderVersionsHandlerInvalidIndex(t *testing.T) {
	router, fs, tmpDir := setupTestRouter(t)
	defer os.RemoveAll(tmpDir)

	// Create invalid JSON index
	indexKey := filepath.Join("providers", "hashicorp", "aws", "index.json")
	if err := fs.PutObject(indexKey, []byte("invalid json")); err != nil {
		t.Fatalf("failed to create index: %v", err)
	}

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestHealthHandlerStorageFailure(t *testing.T) {
	router, _, tmpDir := setupTestRouter(t)

	// Remove temp dir to cause storage failure
	os.RemoveAll(tmpDir)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}
}
