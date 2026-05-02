package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemStorage(t *testing.T) {
	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "terraform-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Initialize filesystem storage
	storage, err := NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	t.Run("PutObject and GetObject", func(t *testing.T) {
		key := "test/object.txt"
		data := []byte("test data")

		// Put object
		err := storage.PutObject(key, data)
		if err != nil {
			t.Fatalf("PutObject failed: %v", err)
		}

		// Get object
		retrieved, err := storage.GetObject(key)
		if err != nil {
			t.Fatalf("GetObject failed: %v", err)
		}

		// Verify data
		if string(retrieved) != string(data) {
			t.Errorf("expected %q, got %q", string(data), string(retrieved))
		}

		// Verify file exists
		path := filepath.Join(tmpDir, key)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("file not created at %s", path)
		}
	})

	t.Run("ListObjects", func(t *testing.T) {
		// Create test structure
		files := []string{
			"providers/hashicorp/aws/1.0.0/linux_amd64.json",
			"providers/hashicorp/aws/1.0.0/provider.zip",
			"providers/hashicorp/aws/2.0.0/linux_amd64.json",
			"modules/example/vpc/aws/1.0.0/module.tar.gz",
		}

		for _, file := range files {
			if err := storage.PutObject(file, []byte("test")); err != nil {
				t.Fatalf("failed to create test file %s: %v", file, err)
			}
		}

		// Test listing with delimiter
		_, prefixes, err := storage.ListObjects("providers/hashicorp/aws/", "/")
		if err != nil {
			t.Fatalf("ListObjects failed: %v", err)
		}

		if len(prefixes) != 2 {
			t.Errorf("expected 2 prefixes, got %d", len(prefixes))
		}

		// Test listing without delimiter (recursive)
		objects, _, err := storage.ListObjects("providers/hashicorp/aws/", "")
		if err != nil {
			t.Fatalf("ListObjects failed: %v", err)
		}

		if len(objects) != 3 {
			t.Errorf("expected 3 objects, got %d", len(objects))
		}
	})

	t.Run("GenerateDownloadURL", func(t *testing.T) {
		key := "test/download.zip"
		url, err := storage.GenerateDownloadURL(key)
		if err != nil {
			t.Fatalf("GenerateDownloadURL failed: %v", err)
		}

		expectedURL := "http://localhost:8080/download/test/download.zip"
		if url != expectedURL {
			t.Errorf("expected URL %q, got %q", expectedURL, url)
		}
	})

	t.Run("HealthCheck", func(t *testing.T) {
		err := storage.HealthCheck()
		if err != nil {
			t.Errorf("HealthCheck failed: %v", err)
		}
	})

	t.Run("GetObject non-existent", func(t *testing.T) {
		_, err := storage.GetObject("non/existent/file.txt")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})
}

func TestFilesystemStorageNestedDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage, err := NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	// Test deeply nested path
	key := "a/b/c/d/e/f/file.txt"
	data := []byte("nested data")

	err = storage.PutObject(key, data)
	if err != nil {
		t.Fatalf("PutObject failed for nested path: %v", err)
	}

	retrieved, err := storage.GetObject(key)
	if err != nil {
		t.Fatalf("GetObject failed for nested path: %v", err)
	}

	if string(retrieved) != string(data) {
		t.Errorf("data mismatch for nested path")
	}
}

func TestFilesystemStoragePermissions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage, err := NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	key := "test/permissions.txt"
	err = storage.PutObject(key, []byte("test"))
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	// Check file permissions
	path := filepath.Join(tmpDir, key)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	expectedMode := os.FileMode(0644)
	if info.Mode().Perm() != expectedMode {
		t.Errorf("expected permissions %v, got %v", expectedMode, info.Mode().Perm())
	}

	// Check directory permissions
	dir := filepath.Dir(path)
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir failed: %v", err)
	}

	expectedDirMode := os.FileMode(0755)
	if dirInfo.Mode().Perm() != expectedDirMode {
		t.Errorf("expected dir permissions %v, got %v", expectedDirMode, dirInfo.Mode().Perm())
	}
}

func TestFilesystemStorageServeFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage, err := NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	key := "test/serve.txt"
	data := []byte("serve test data")
	err = storage.PutObject(key, data)
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	// Create a buffer to write to
	var buf []byte
	writer := &mockWriter{buf: &buf}

	err = storage.ServeFile(writer, key)
	if err != nil {
		t.Fatalf("ServeFile failed: %v", err)
	}

	if string(buf) != string(data) {
		t.Errorf("expected %q, got %q", string(data), string(buf))
	}
}

func TestFilesystemStorageServeFileNotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "terraform-registry-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	storage, err := NewFilesystemStorage(tmpDir, "http://localhost:8080")
	if err != nil {
		t.Fatalf("failed to create filesystem storage: %v", err)
	}

	var buf []byte
	writer := &mockWriter{buf: &buf}

	err = storage.ServeFile(writer, "nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// mockWriter implements io.Writer for testing
type mockWriter struct {
	buf *[]byte
}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	*m.buf = append(*m.buf, p...)
	return len(p), nil
}
