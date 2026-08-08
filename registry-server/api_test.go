package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
)

func setupAPIRouter(t *testing.T) (*mux.Router, *FilesystemStorage, string) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-api-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	storage, err = NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	r := mux.NewRouter()

	// Registry protocol endpoints
	r.HandleFunc("/.well-known/terraform.json", wellKnownHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/versions", providerVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}", providerDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/versions", moduleVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/{version}/download", moduleDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/download", moduleLatestDownloadHandler).Methods("GET")
	r.HandleFunc("/download/{path:.*}", fileDownloadHandler).Methods("GET")
	r.HandleFunc("/health", healthHandler).Methods("GET")

	// Management API
	api := r.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/stats", registryStatsHandler).Methods("GET")
	api.HandleFunc("/providers", listProvidersHandler).Methods("GET")
	api.HandleFunc("/providers/{namespace}/{name}", getProviderHandler).Methods("GET")
	api.HandleFunc("/providers/{namespace}/{name}/{version}/{os}/{arch}", requireAuth(uploadProviderHandler)).Methods("POST")
	api.HandleFunc("/providers/{namespace}/{name}/{version}", requireAuth(deleteProviderVersionHandler)).Methods("DELETE")
	api.HandleFunc("/modules", listModulesHandler).Methods("GET")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}", getModuleHandler).Methods("GET")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}/{version}", requireAuth(uploadModuleHandler)).Methods("POST")
	api.HandleFunc("/modules/{namespace}/{name}/{provider}/{version}", requireAuth(deleteModuleVersionHandler)).Methods("DELETE")

	return r, storage.(*FilesystemStorage), tmpDir
}

func TestAPIStatsEmpty(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Error("expected success=true")
	}
}

func TestAPIUploadProvider(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.zip")
	_, _ = part.Write([]byte("fake provider binary"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success, got: %s", resp.Message)
	}
}

func TestAPIListProvidersAfterUpload(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload a provider first
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.zip")
	_, _ = part.Write([]byte("fake"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload failed: %s", w.Body.String())
	}

	// List providers
	req = httptest.NewRequest("GET", "/api/v1/providers", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	data, _ := json.Marshal(resp.Data)
	var providers []ProviderInfo
	if err := json.Unmarshal(data, &providers); err != nil {
		t.Fatalf("failed to parse providers: %v", err)
	}

	if len(providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(providers))
	}

	if providers[0].Namespace != "hashicorp" || providers[0].Name != "aws" {
		t.Errorf("unexpected provider: %s/%s", providers[0].Namespace, providers[0].Name)
	}
}

func TestAPIDeleteProvider(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.zip")
	_, _ = part.Write([]byte("fake"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Delete
	req = httptest.NewRequest("DELETE", "/api/v1/providers/hashicorp/aws/1.0.0", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify deleted via registry protocol
	req = httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/versions", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var versionsResp ProviderVersionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &versionsResp); err != nil {
		t.Fatalf("failed to parse versions: %v", err)
	}

	if len(versionsResp.Versions) != 0 {
		t.Errorf("expected 0 versions after delete, got %d", len(versionsResp.Versions))
	}
}

func TestAPIUploadModule(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "module.tar.gz")
	_, _ = part.Write([]byte("fake module tarball"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/modules/example/vpc/aws/1.0.0", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success, got: %s", resp.Message)
	}
}

func TestAPIDeleteModule(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "module.tar.gz")
	_, _ = part.Write([]byte("fake"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/modules/example/vpc/aws/1.0.0", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Delete
	req = httptest.NewRequest("DELETE", "/api/v1/modules/example/vpc/aws/1.0.0", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPIGetProvider(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.zip")
	_, _ = part.Write([]byte("fake"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Get
	req = httptest.NewRequest("GET", "/api/v1/providers/hashicorp/aws", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	data, _ := json.Marshal(resp.Data)
	var info ProviderInfo
	json.Unmarshal(data, &info)

	if info.Namespace != "hashicorp" || info.Name != "aws" {
		t.Errorf("unexpected provider: %s/%s", info.Namespace, info.Name)
	}

	if len(info.Versions) != 1 {
		t.Errorf("expected 1 version, got %d", len(info.Versions))
	}
}

func TestAPIGetProviderNotFound(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/api/v1/providers/nonexistent/provider", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestAPIDeleteProviderNotFound(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("DELETE", "/api/v1/providers/hashicorp/aws/9.9.9", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestAPIAuthRequired(t *testing.T) {
	// Set API key
	_ = os.Setenv("REGISTRY_API_KEY", "test-secret-key")
	defer func() { _ = os.Unsetenv("REGISTRY_API_KEY") }()

	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload without key should fail
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.zip")
	_, _ = part.Write([]byte("fake"))
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without key, got %d", w.Code)
	}

	// Upload with correct key should succeed
	body = &bytes.Buffer{}
	writer = multipart.NewWriter(body)
	part, _ = writer.CreateFormFile("file", "test.zip")
	_, _ = part.Write([]byte("fake"))
	_ = writer.Close()

	req = httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-API-Key", "test-secret-key")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 with correct key, got %d: %s", w.Code, w.Body.String())
	}

	// Public endpoints should still work without key
	req = httptest.NewRequest("GET", "/.well-known/terraform.json", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for well-known, got %d", w.Code)
	}

	// Health should still work
	req = httptest.NewRequest("GET", "/health", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for health, got %d", w.Code)
	}
}

func TestAPIStatsAfterUploads(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload a provider and module
	for _, upload := range []struct{ url, content string }{
		{"/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", "provider"},
		{"/api/v1/providers/hashicorp/aws/2.0.0/linux/amd64", "provider"},
		{"/api/v1/modules/example/vpc/aws/1.0.0", "module"},
	} {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("file", "test.zip")
		_, _ = part.Write([]byte(upload.content))
		_ = writer.Close()

		req := httptest.NewRequest("POST", upload.url, body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("upload %s failed: %s", upload.url, w.Body.String())
		}
	}

	// Check stats
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	data, _ := json.Marshal(resp.Data)
	var stats RegistryStats
	json.Unmarshal(data, &stats)

	if stats.Providers != 1 {
		t.Errorf("expected 1 provider, got %d", stats.Providers)
	}
	if stats.ProviderVersions != 2 {
		t.Errorf("expected 2 provider versions, got %d", stats.ProviderVersions)
	}
	if stats.Modules != 1 {
		t.Errorf("expected 1 module, got %d", stats.Modules)
	}
	if stats.ModuleVersions != 1 {
		t.Errorf("expected 1 module version, got %d", stats.ModuleVersions)
	}
}

func TestUIEndpoints(t *testing.T) {
	r, _, tmpDir := setupAPIRouter(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	r.PathPrefix("/ui").HandlerFunc(uiHandler)

	req := httptest.NewRequest("GET", "/ui", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for /ui, got %d", w.Code)
	}

	if contentType := w.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Errorf("expected text/html, got %s", contentType)
	}

	// Check that UI contains expected elements
	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("Terraform Registry")) {
		t.Error("UI missing title")
	}
}

func TestFilesystemDeleteObjectCleansEmptyDirs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-delete-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage, err := NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	// Create nested file
	key := "providers/hashicorp/aws/1.0.0/linux_amd64.json"
	if err := storage.PutObject(key, []byte(`{}`)); err != nil {
		t.Fatalf("failed to create file: %v", err)
	}

	// Verify directory exists
	dir := filepath.Join(tmpDir, "providers/hashicorp/aws/1.0.0")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("expected directory to exist")
	}

	// Delete
	if err := storage.DeleteObject(key); err != nil {
		t.Fatalf("failed to delete: %v", err)
	}

	// Verify empty directories were cleaned up
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("expected empty version directory to be removed")
	}

	parentDir := filepath.Join(tmpDir, "providers/hashicorp/aws")
	if _, err := os.Stat(parentDir); !os.IsNotExist(err) {
		t.Error("expected empty provider directory to be removed")
	}
}

// needed for mux import
var _ = mux.NewRouter
