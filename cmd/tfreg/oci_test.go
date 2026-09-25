package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"
)

func TestPackOCIArtifactUsesTerraformMediaTypes(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "provider.zip")
	if err := os.WriteFile(artifact, []byte("provider package"), 0600); err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	desc, err := packOCIArtifact(context.Background(), store, artifact, "provider", map[string]string{
		"org.opencontainers.image.title": "terraform-provider-example_1.2.3_linux_amd64.zip",
		"io.terraform.namespace":         "acme",
		"io.terraform.name":              "example",
		"io.terraform.version":           "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := store.Fetch(context.Background(), desc)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ArtifactType != terraformOCIArtifactType {
		t.Fatalf("artifact type = %q", manifest.ArtifactType)
	}
	if len(manifest.Layers) != 1 || manifest.Layers[0].MediaType != terraformProviderLayerType {
		t.Fatalf("unexpected layers: %#v", manifest.Layers)
	}
	if manifest.Annotations["io.terraform.namespace"] != "acme" {
		t.Fatalf("annotations = %#v", manifest.Annotations)
	}
}

func TestSplitOCIReferenceRequiresTag(t *testing.T) {
	repo, tag, err := splitOCIReference("123456789012.dkr.ecr.us-east-1.amazonaws.com/terraform/providers/acme/example:1.2.3-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if repo != "123456789012.dkr.ecr.us-east-1.amazonaws.com/terraform/providers/acme/example" || tag != "1.2.3-linux-amd64" {
		t.Fatalf("repo=%q tag=%q", repo, tag)
	}
	if _, _, err := splitOCIReference("example.com/acme/provider"); err == nil {
		t.Fatal("expected an explicit tag to be required")
	}
}
