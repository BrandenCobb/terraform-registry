package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"log/slog"

	"github.com/gorilla/mux"
)

func setupTestEnv(t *testing.T) (*mux.Router, *Store, string) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "tfreg-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}

	testLogger := testLogger()
	s, err := NewStore(tmpDir, "http://localhost:8080", testLogger)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// Set globals
	store = s
	logger = testLogger
	metrics = NewMetrics()
	webhooks = NewWebhookManager("", testLogger)

	r := mux.NewRouter()
	r.HandleFunc("/.well-known/terraform.json", wellKnownHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/versions", providerVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}", providerDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/versions", moduleVersionsHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/{version}/download", moduleDownloadHandler).Methods("GET")
	r.HandleFunc("/v1/modules/{namespace}/{name}/{provider}/download", moduleLatestDownloadHandler).Methods("GET")
	r.HandleFunc("/{hostname}/{namespace}/{type}/index.json", networkMirrorIndexHandler).Methods("GET")
	r.HandleFunc("/{hostname}/{namespace}/{type}/{version}.json", networkMirrorVersionHandler).Methods("GET")
	r.HandleFunc("/download/{path:.*}", fileDownloadHandler).Methods("GET")
	r.HandleFunc("/health", healthHandler).Methods("GET")
	r.HandleFunc("/metrics", prometheusHandler(metrics)).Methods("GET")

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

	return r, s, tmpDir
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// --- Protocol Tests ---

func TestWellKnown(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/.well-known/terraform.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp WellKnown
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.ProvidersV1 != "/v1/providers/" || resp.ModulesV1 != "/v1/modules/" {
		t.Errorf("unexpected discovery: %+v", resp)
	}
}

func TestHealth(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestProviderVersions(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Seed data
	_ = s.AddProviderVersion("hashicorp", "aws", "6.31.0")
	meta := PlatformMeta{OS: "linux", Arch: "amd64", Filename: "terraform-provider-aws_6.31.0_linux_amd64.zip", Shasum: "abc123", Protocols: []string{"5.0"}}
	metaData, _ := json.Marshal(meta)
	_ = s.Put("providers/hashicorp/aws/6.31.0/linux_amd64.json", metaData)

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ProviderVersionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Versions) != 1 || resp.Versions[0].Version != "6.31.0" {
		t.Errorf("unexpected versions: %+v", resp.Versions)
	}
}

func TestProviderVersionsNotFound(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/v1/providers/nonexistent/provider/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestProviderDownload(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.AddProviderVersion("hashicorp", "aws", "6.31.0")
	meta := PlatformMeta{OS: "linux", Arch: "amd64", Filename: "terraform-provider-aws_6.31.0_linux_amd64.zip", Shasum: "abc123"}
	metaData, _ := json.Marshal(meta)
	_ = s.Put("providers/hashicorp/aws/6.31.0/linux_amd64.json", metaData)
	_ = s.Put("providers/hashicorp/aws/6.31.0/terraform-provider-aws_6.31.0_linux_amd64.zip", []byte("fake"))

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/6.31.0/download/linux/amd64", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp ProviderDownloadResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Shasum != "abc123" {
		t.Errorf("expected shasum abc123, got %s", resp.Shasum)
	}
	if resp.DownloadURL == "" {
		t.Error("expected download URL")
	}
}

func TestProviderDownloadNotFound(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/1.0.0/download/linux/amd64", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestModuleVersions(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.AddModuleVersion("example", "vpc", "aws", "1.0.0")

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp ModuleVersionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Modules) != 1 || resp.Modules[0].Version != "1.0.0" {
		t.Errorf("unexpected: %+v", resp.Modules)
	}
}

func TestModuleDownload(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.AddModuleVersion("example", "vpc", "aws", "1.0.0")
	_ = s.Put("modules/example/vpc/aws/1.0.0/module.tar.gz", []byte("fake tarball"))

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/1.0.0/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Terraform protocol: 204 with X-Terraform-Get
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if w.Header().Get("X-Terraform-Get") == "" {
		t.Error("expected X-Terraform-Get header")
	}
}

func TestModuleLatestDownload(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.AddModuleVersion("example", "vpc", "aws", "1.0.0")
	_ = s.AddModuleVersion("example", "vpc", "aws", "2.0.0")
	_ = s.Put("modules/example/vpc/aws/2.0.0/module.tar.gz", []byte("fake"))

	req := httptest.NewRequest("GET", "/v1/modules/example/vpc/aws/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Terraform-Get") == "" {
		t.Error("expected X-Terraform-Get header")
	}
}

func TestFileDownload(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.Put("test/file.zip", []byte("fake zip"))

	req := httptest.NewRequest("GET", "/download/test/file.zip", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/zip" {
		t.Errorf("expected application/zip, got %s", w.Header().Get("Content-Type"))
	}
}

func TestFileDownloadPathTraversal(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// gorilla/mux normalizes path traversal attempts
	req := httptest.NewRequest("GET", "/download/%2e%2e/%2e%2e/etc/passwd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should not serve /etc/passwd (either 403 or 404)
	if w.Code == http.StatusOK && w.Body.String() != "not found" {
		t.Errorf("path traversal should not succeed: got 200 with body %s", w.Body.String())
	}
}

func TestNetworkMirror(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.AddProviderVersion("hashicorp", "aws", "6.31.0")
	meta := PlatformMeta{OS: "linux", Arch: "amd64", Filename: "terraform-provider-aws_6.31.0_linux_amd64.zip", Shasum: "abc123"}
	metaData, _ := json.Marshal(meta)
	_ = s.Put("providers/hashicorp/aws/6.31.0/linux_amd64.json", metaData)
	_ = s.Put("providers/hashicorp/aws/6.31.0/terraform-provider-aws_6.31.0_linux_amd64.zip", []byte("fake"))

	// Network mirror index
	req := httptest.NewRequest("GET", "/registry.terraform.io/hashicorp/aws/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("index: expected 200, got %d", w.Code)
	}

	// Network mirror version
	req = httptest.NewRequest("GET", "/registry.terraform.io/hashicorp/aws/6.31.0.json", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("version: expected 200, got %d", w.Code)
	}
	var versionResp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &versionResp)
	archives, ok := versionResp["archives"].(map[string]interface{})
	if !ok {
		t.Fatal("expected archives in response")
	}
	if _, ok := archives["linux_amd64"]; !ok {
		t.Error("expected linux_amd64 in archives")
	}
}

// --- API Tests ---

func TestAPICRUDProvider(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload
	body := multipartBody(t, "file", "test.zip", fakeZipData())
	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", multipartContentType)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/api/v1/providers", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)

	// Get detail
	req = httptest.NewRequest("GET", "/api/v1/providers/hashicorp/aws", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)

	// Delete
	req = httptest.NewRequest("DELETE", "/api/v1/providers/hashicorp/aws/1.0.0", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)

	// Verify deleted
	req = httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/versions", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp ProviderVersionsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Versions) != 0 {
		t.Errorf("expected 0 versions after delete, got %d", len(resp.Versions))
	}
}

func TestAPICRUDModule(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Upload
	body := multipartBody(t, "file", "module.tar.gz", fakeGzipData())
	req := httptest.NewRequest("POST", "/api/v1/modules/example/vpc/aws/1.0.0", body)
	req.Header.Set("Content-Type", multipartContentType)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// List
	req = httptest.NewRequest("GET", "/api/v1/modules", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)

	// Delete
	req = httptest.NewRequest("DELETE", "/api/v1/modules/example/vpc/aws/1.0.0", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)
}

func TestAPIDeprecateProvider(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.AddProviderVersion("hashicorp", "aws", "1.0.0")
	_ = s.Put("providers/hashicorp/aws/1.0.0/linux_amd64.json", []byte(`{"os":"linux","arch":"amd64","filename":"test.zip","shasum":"abc"}`))
	_ = s.Put("providers/hashicorp/aws/1.0.0/test.zip", []byte("fake"))

	// Deprecate
	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/deprecate",
		jsonBody(map[string]string{"message": "Use 2.0.0 instead"}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)

	// Deprecated versions should be excluded from protocol responses
	req = httptest.NewRequest("GET", "/v1/providers/hashicorp/aws/versions", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp ProviderVersionsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Versions) != 0 {
		t.Errorf("deprecated version should be hidden, got %d versions", len(resp.Versions))
	}
}

func TestAPIDeprecateModule(t *testing.T) {
	r, s, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	_ = s.AddModuleVersion("example", "vpc", "aws", "1.0.0")

	req := httptest.NewRequest("POST", "/api/v1/modules/example/vpc/aws/1.0.0/deprecate",
		jsonBody(map[string]string{"message": "Use 2.0.0"}))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)
}

func TestAPIStats(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestAPIGC(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("POST", "/api/v1/gc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assertJSONSuccess(t, w, true)
}

func TestAPIGetProviderNotFound(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/api/v1/providers/nonexistent/provider", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var snap MetricsSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("parse metrics: %v", err)
	}
	// Note: test router doesn't use middleware so counters may be 0
	_ = snap.RequestsTotal
}

func TestUploadValidationRejectsBadZip(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	body := multipartBody(t, "file", "test.zip", []byte("not a zip file"))
	req := httptest.NewRequest("POST", "/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64", body)
	req.Header.Set("Content-Type", multipartContentType)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid zip, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUploadValidationRejectsBadGzip(t *testing.T) {
	r, _, tmpDir := setupTestEnv(t)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	body := multipartBody(t, "file", "module.tar.gz", []byte("not gzip"))
	req := httptest.NewRequest("POST", "/api/v1/modules/example/vpc/aws/1.0.0", body)
	req.Header.Set("Content-Type", multipartContentType)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid gzip, got %d: %s", w.Code, w.Body.String())
	}
}

// --- Storage Tests ---

func TestStorageAtomicWrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tfreg-store-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s, err := NewStore(tmpDir, "http://localhost:8080", testLogger())
	if err != nil {
		t.Fatal(err)
	}

	key := "test/atomic.txt"
	data := []byte("atomic write test")
	if err := s.Put(key, data); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, err := s.Get(key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("mismatch: %q != %q", got, data)
	}

	// No temp files should remain
	entries, _ := os.ReadDir(tmpDir + "/test")
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestStorageDeleteCleanup(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tfreg-store-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s, err := NewStore(tmpDir, "http://localhost:8080", testLogger())
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Put("a/b/c/d.txt", []byte("nested"))
	if err := s.Delete("a/b/c/d.txt"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Empty dirs should be cleaned
	if _, err := os.Stat(tmpDir + "/a/b/c"); !os.IsNotExist(err) {
		t.Error("expected empty dir cleanup")
	}
}

func TestStorageIndexManagement(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tfreg-store-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s, err := NewStore(tmpDir, "http://localhost:8080", testLogger())
	if err != nil {
		t.Fatal(err)
	}

	// Add versions
	_ = s.AddProviderVersion("hashicorp", "aws", "1.0.0")
	_ = s.AddProviderVersion("hashicorp", "aws", "2.0.0")
	_ = s.AddProviderVersion("hashicorp", "aws", "1.0.0") // duplicate

	idx, err := s.GetProviderIndex("hashicorp", "aws")
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(idx.Versions))
	}

	// Remove version
	_ = s.RemoveProviderVersion("hashicorp", "aws", "1.0.0")
	idx, _ = s.GetProviderIndex("hashicorp", "aws")
	if len(idx.Versions) != 1 || idx.Versions[0] != "2.0.0" {
		t.Errorf("expected [2.0.0], got %v", idx.Versions)
	}
}

func TestStorageExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tfreg-store-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s, _ := NewStore(tmpDir, "http://localhost:8080", testLogger())

	if s.Exists("nonexistent") {
		t.Error("should not exist")
	}

	_ = s.Put("exists.txt", []byte("yes"))
	if !s.Exists("exists.txt") {
		t.Error("should exist")
	}
}

func TestStorageDownloadURL(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tfreg-store-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	s, _ := NewStore(tmpDir, "https://registry.example.com", testLogger())
	url := s.DownloadURL("providers/hashicorp/aws/1.0.0/test.zip")
	expected := "https://registry.example.com/download/providers/hashicorp/aws/1.0.0/test.zip"
	if url != expected {
		t.Errorf("expected %s, got %s", expected, url)
	}
}
