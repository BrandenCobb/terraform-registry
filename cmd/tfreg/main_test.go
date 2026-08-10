package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifiedDownloadPreservesExistingFileOnChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("corrupt replacement"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "provider.zip")
	if err := os.WriteFile(dest, []byte("known-good"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := downloadFileVerified(server.URL, dest, fmt.Sprintf("%x", sha256.Sum256([]byte("expected")))); err == nil {
		t.Fatal("expected checksum mismatch")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "known-good" {
		t.Fatalf("existing destination was changed: %q", got)
	}
}

func TestVerifiedDownloadAtomicallyReplacesAfterChecksumSuccess(t *testing.T) {
	content := []byte("verified replacement")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "provider.zip")
	if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := downloadFileVerified(server.URL, dest, fmt.Sprintf("%x", sha256.Sum256(content))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("destination = %q, want %q", got, content)
	}
}
