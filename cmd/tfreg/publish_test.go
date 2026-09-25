package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishModuleBundlesAndUploadsSource(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "main.tf"), []byte("variable \"name\" {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Errorf("missing API key")
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Errorf("parse upload: %v", err)
			http.Error(w, "bad upload", http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		body, _ := io.ReadAll(file)
		if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
			t.Errorf("upload is not gzip data")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"message":"uploaded"}`)
	}))
	defer server.Close()

	err := publishModuleArtifact(context.Background(), publishModuleOptions{
		Registry: server.URL, APIKey: "test-key", Namespace: "acme", Name: "vpc",
		Provider: "aws", Version: "1.2.3", Source: source,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/modules/acme/vpc/aws/1.2.3" {
		t.Fatalf("upload path = %q", gotPath)
	}
}

func TestPublishProviderBuildsBundlesAndUploadsSource(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module example.com/provider\n\ngo 1.23\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			t.Errorf("parse upload: %v", err)
			http.Error(w, "bad upload", http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("form file: %v", err)
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		magic := make([]byte, 4)
		if _, err := io.ReadFull(file, magic); err != nil || string(magic) != "PK\x03\x04" {
			t.Errorf("upload is not a ZIP: %q, %v", magic, err)
		}
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()

	err := publishProviderArtifact(context.Background(), publishProviderOptions{
		Registry: server.URL, Namespace: "acme", Name: "example", Version: "1.2.3",
		Source: source, OS: "linux", Arch: "amd64", SkipTests: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/providers/acme/example/1.2.3/linux/amd64" {
		t.Fatalf("upload path = %q", gotPath)
	}
}

func TestBuildProviderSourceRunsTestsAndCrossCompiles(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "go.mod"), []byte("module example.com/provider\n\ngo 1.23\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main_test.go"), []byte("package main\nimport \"testing\"\nfunc TestProvider(t *testing.T) {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "terraform-provider-example_v1.0.0")
	if err := buildProviderSource(context.Background(), source, output, "linux", "amd64", false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("built provider binary is empty")
	}
}

func TestArtifactAnnotationsAreComplete(t *testing.T) {
	annotations := artifactAnnotations("provider", "acme", "example", "", "1.2.3", "linux", "amd64", "bundle.zip")
	for _, key := range []string{"io.terraform.kind", "io.terraform.namespace", "io.terraform.name", "io.terraform.version", "io.terraform.os", "io.terraform.arch", "org.opencontainers.image.title"} {
		if strings.TrimSpace(annotations[key]) == "" {
			t.Fatalf("annotation %q missing: %#v", key, annotations)
		}
	}
}
