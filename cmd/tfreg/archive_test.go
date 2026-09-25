package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateTarGzExcludesVCSAndTerraformRuntimeFiles(t *testing.T) {
	source := t.TempDir()
	files := map[string]string{
		"main.tf":                  "resource {}",
		".git/config":              "secret remote",
		".terraform/plugin":        "binary",
		"terraform.tfstate":        "state secret",
		"terraform.tfstate.backup": "state secret",
		"crash.log":                "crash",
		"crash.123.log":            "crash",
		"nested/variables.tf":      "variable {}",
	}
	for name, content := range files {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	bundle := filepath.Join(t.TempDir(), "module.tar.gz")
	if err := createTarGz(source, bundle); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	entries := map[string]bool{}
	for {
		header, err := reader.Next()
		if err != nil {
			break
		}
		entries[header.Name] = true
	}
	for _, required := range []string{"main.tf", "nested/variables.tf"} {
		if !entries[required] {
			t.Errorf("expected %s in archive", required)
		}
	}
	for _, excluded := range []string{".git", ".git/config", ".terraform", ".terraform/plugin", "terraform.tfstate", "terraform.tfstate.backup", "crash.log", "crash.123.log"} {
		if entries[excluded] {
			t.Errorf("did not expect %s in archive", excluded)
		}
	}
}
