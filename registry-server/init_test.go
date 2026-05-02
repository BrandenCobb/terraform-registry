package main

import (
	"os"
	"testing"
)

func TestInitFunctions(t *testing.T) {
	// Save original storage
	originalStorage := storage

	t.Run("initFilesystemStorage", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "terraform-registry-init-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		os.Setenv("STORAGE_PATH", tmpDir)
		os.Setenv("BASE_URL", "http://test:9000")
		defer func() {
			os.Unsetenv("STORAGE_PATH")
			os.Unsetenv("BASE_URL")
		}()

		initFilesystemStorage()

		if storage == nil {
			t.Error("storage should be initialized")
		}

		fsStorage, ok := storage.(*FilesystemStorage)
		if !ok {
			t.Error("storage should be FilesystemStorage")
		}

		if fsStorage.basePath != tmpDir {
			t.Errorf("expected basePath %s, got %s", tmpDir, fsStorage.basePath)
		}

		if fsStorage.baseURL != "http://test:9000" {
			t.Errorf("expected baseURL 'http://test:9000', got '%s'", fsStorage.baseURL)
		}
	})

	t.Run("initFilesystemStorage with defaults", func(t *testing.T) {
		// Use temp dir to avoid permission issues
		tmpDir, err := os.MkdirTemp("", "terraform-registry-init-default-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tmpDir)

		os.Setenv("STORAGE_PATH", tmpDir)
		defer os.Unsetenv("STORAGE_PATH")

		initFilesystemStorage()

		if storage == nil {
			t.Error("storage should be initialized")
		}

		fsStorage, ok := storage.(*FilesystemStorage)
		if !ok {
			t.Error("storage should be FilesystemStorage")
		}

		if fsStorage.baseURL != "http://localhost:8080" {
			t.Errorf("expected default baseURL 'http://localhost:8080', got '%s'", fsStorage.baseURL)
		}
	})

	// Restore original storage
	storage = originalStorage
}

func TestNewS3Storage(t *testing.T) {
	// Just test the constructor, not actual S3 operations
	s3Storage := NewS3Storage(nil, "test-bucket")

	if s3Storage == nil {
		t.Error("NewS3Storage should not return nil")
		return
	}

	if s3Storage.bucketName != "test-bucket" {
		t.Errorf("expected bucket 'test-bucket', got '%s'", s3Storage.bucketName)
	}
}
