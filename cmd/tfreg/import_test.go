package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
)

func TestValidImportVersionMatchesRegistrySemverRules(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.2.3", "1.2.3-alpha.1", "1.2.3+build.7", "1.2.3-alpha+build"} {
		if !validImportVersion(version) {
			t.Errorf("expected %q to be valid", version)
		}
	}
	for _, version := range []string{"", "v1.2.3", "release-1", "1.2", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3+", "1.2.3+build+again"} {
		if validImportVersion(version) {
			t.Errorf("expected %q to be invalid", version)
		}
	}
}

func TestSplitOCIImportReferenceAcceptsTagAndDigest(t *testing.T) {
	for _, reference := range []string{
		"123456789012.dkr.ecr.us-east-1.amazonaws.com/terraform/modules/acme/vpc/aws:1.0.0",
		"123456789012.dkr.ecr.us-east-1.amazonaws.com/terraform/modules/acme/vpc/aws@sha256:8c5d2fb4f4f6873ca8dd12b0b8f53d62b00cb4c38a8a269ff3696eefb77890c8",
	} {
		repository, resolvedReference, err := splitOCIImportReference(reference)
		if err != nil {
			t.Fatalf("split %q: %v", reference, err)
		}
		if repository == "" || resolvedReference == "" {
			t.Fatalf("repository=%q reference=%q", repository, resolvedReference)
		}
	}
	if _, _, err := splitOCIImportReference("example.com/repository"); err == nil {
		t.Fatal("expected missing tag or digest to fail")
	}
}

func TestUnpackOCIArtifactValidatesAndExtractsProvider(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	packagePath := filepath.Join(t.TempDir(), "provider.zip")
	packageBody := []byte("provider package")
	if err := os.WriteFile(packagePath, packageBody, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := packOCIArtifact(ctx, store, packagePath, "provider", artifactAnnotations(
		"provider", "acme", "example", "", "1.2.3", "linux", "amd64", "provider.zip",
	))
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	metadata, err := unpackOCIArtifact(ctx, store, manifest, &output, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Kind != "provider" || metadata.Namespace != "acme" || metadata.Name != "example" || metadata.Version != "1.2.3" || metadata.OS != "linux" || metadata.Arch != "amd64" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if !bytes.Equal(output.Bytes(), packageBody) {
		t.Fatalf("extracted body = %q", output.Bytes())
	}
}

type corruptingTarget struct {
	oras.ReadOnlyTarget
	digest digest.Digest
	body   string
}

func (target corruptingTarget) Fetch(ctx context.Context, descriptor ocispec.Descriptor) (io.ReadCloser, error) {
	if descriptor.Digest == target.digest {
		return io.NopCloser(strings.NewReader(target.body)), nil
	}
	return target.ReadOnlyTarget.Fetch(ctx, descriptor)
}

func TestUnpackOCIArtifactRejectsMalformedLayerDigest(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	manifest := ocispec.Manifest{
		ArtifactType: terraformOCIArtifactType,
		Layers: []ocispec.Descriptor{{
			MediaType: terraformModuleLayerType,
			Digest:    "sha256:not-a-digest",
			Size:      10,
		}},
		Annotations: artifactAnnotations("module", "acme", "vpc", "aws", "1.0.0", "", "", "module.tar.gz"),
	}
	manifest.SchemaVersion = 2
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(body), Size: int64(len(body))}
	if err := store.Push(ctx, descriptor, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	_, err = unpackOCIArtifact(ctx, store, descriptor, io.Discard, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "invalid OCI artifact digest") {
		t.Fatalf("expected invalid layer digest error, got %v", err)
	}
}

func TestUnpackOCIArtifactRejectsLayerDigestMismatch(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	packageBody := []byte("provider package")
	packagePath := filepath.Join(t.TempDir(), "provider.zip")
	if err := os.WriteFile(packagePath, packageBody, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := packOCIArtifact(ctx, store, packagePath, "provider", artifactAnnotations(
		"provider", "acme", "example", "", "1.2.3", "linux", "amd64", "provider.zip",
	))
	if err != nil {
		t.Fatal(err)
	}
	corrupted := corruptingTarget{ReadOnlyTarget: store, digest: digest.FromBytes(packageBody), body: strings.Repeat("x", len(packageBody))}
	_, err = unpackOCIArtifact(ctx, corrupted, manifest, io.Discard, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "digest verification failed") {
		t.Fatalf("expected digest verification error, got %v", err)
	}
}

func TestUnpackOCIArtifactRejectsInvalidManifestBeforeDownload(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	manifest := ocispec.Manifest{
		Versioned:    ocispec.Manifest{}.Versioned,
		ArtifactType: "application/vnd.example.invalid",
		Annotations: map[string]string{
			"io.terraform.kind":      "module",
			"io.terraform.namespace": "acme",
			"io.terraform.name":      "vpc",
			"io.terraform.provider":  "aws",
			"io.terraform.version":   "1.0.0",
		},
	}
	manifest.SchemaVersion = 2
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	desc := ocispec.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: digest.FromBytes(body), Size: int64(len(body))}
	if err := store.Push(ctx, desc, strings.NewReader(string(body))); err != nil {
		t.Fatal(err)
	}
	_, err = unpackOCIArtifact(ctx, store, desc, io.Discard, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "artifact type") {
		t.Fatalf("expected artifact type error, got %v", err)
	}
}

func TestUnpackOCIArtifactRejectsOversizedLayer(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	packagePath := filepath.Join(t.TempDir(), "module.tar.gz")
	if err := os.WriteFile(packagePath, []byte("package larger than limit"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := packOCIArtifact(ctx, store, packagePath, "module", artifactAnnotations(
		"module", "acme", "vpc", "aws", "1.0.0", "", "", "module.tar.gz",
	))
	if err != nil {
		t.Fatal(err)
	}
	_, err = unpackOCIArtifact(ctx, store, manifest, io.Discard, 4)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestImportOCIArtifactUploadsExactPackage(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	packagePath := filepath.Join(t.TempDir(), "module.tar.gz")
	packageBody := []byte("exact module package")
	if err := os.WriteFile(packagePath, packageBody, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := packOCIArtifact(ctx, store, packagePath, "module", artifactAnnotations(
		"module", "acme", "vpc", "aws", "1.0.0", "", "", "module.tar.gz",
	))
	if err != nil {
		t.Fatal(err)
	}
	var gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Header.Get("X-API-Key") != "import-key" {
			t.Errorf("missing API key")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			http.Error(w, "bad upload", http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("read uploaded file: %v", err)
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()
		gotBody, _ = io.ReadAll(file)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	metadata, err := importOCIArtifactFromTarget(ctx, store, manifest, server.URL, "import-key", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Kind != "module" || gotPath != "/api/v1/modules/acme/vpc/aws/1.0.0" {
		t.Fatalf("metadata=%#v path=%q", metadata, gotPath)
	}
	if string(gotBody) != string(packageBody) {
		t.Fatalf("uploaded body = %q", gotBody)
	}
}

func TestOCIImportUploadURLUsesArtifactIdentity(t *testing.T) {
	providerURL, err := importUploadURL("https://registry.example", ociArtifactMetadata{
		Kind: "provider", Namespace: "acme", Name: "example", Version: "1.2.3", OS: "linux", Arch: "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if providerURL != "https://registry.example/api/v1/providers/acme/example/1.2.3/linux/amd64" {
		t.Fatalf("provider URL = %q", providerURL)
	}
	moduleURL, err := importUploadURL("https://registry.example/", ociArtifactMetadata{
		Kind: "module", Namespace: "acme", Name: "vpc", Provider: "aws", Version: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if moduleURL != "https://registry.example/api/v1/modules/acme/vpc/aws/1.0.0" {
		t.Fatalf("module URL = %q", moduleURL)
	}
	if _, err := importUploadURL("https://registry.example", ociArtifactMetadata{Kind: "provider", Namespace: "../bad"}); err == nil {
		t.Fatal("expected unsafe or incomplete identity to be rejected")
	}
	if _, err := importUploadURL("file://registry.example", ociArtifactMetadata{Kind: "module", Namespace: "acme", Name: "vpc", Provider: "aws", Version: "1.0.0"}); err == nil {
		t.Fatal("expected non-HTTP registry URL to be rejected")
	}
	if _, err := importUploadURL("https://user:secret@registry.example", ociArtifactMetadata{Kind: "module", Namespace: "acme", Name: "vpc", Provider: "aws", Version: "1.0.0"}); err == nil {
		t.Fatal("expected registry URL credentials to be rejected")
	}
}
