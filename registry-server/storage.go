package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Storage interface abstracts S3 and filesystem storage
type Storage interface {
	GetObject(key string) ([]byte, error)
	PutObject(key string, data []byte) error
	DeleteObject(key string) error
	ListObjects(prefix string, delimiter string) ([]string, []string, error) // Returns objects and common prefixes
	GenerateDownloadURL(key string) (string, error)
	HealthCheck() error
}

// S3Storage implements Storage using AWS S3
type S3Storage struct {
	client     *s3.Client
	bucketName string
}

func NewS3Storage(client *s3.Client, bucket string) *S3Storage {
	return &S3Storage{
		client:     client,
		bucketName: bucket,
	}
}

func (s *S3Storage) GetObject(key string) ([]byte, error) {
	result, err := s.client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = result.Body.Close() }()

	data, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (s *S3Storage) PutObject(key string, data []byte) error {
	_, err := s.client.PutObject(context.TODO(), &s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
		Body:   strings.NewReader(string(data)),
	})
	return err
}

func (s *S3Storage) DeleteObject(key string) error {
	_, err := s.client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	return err
}

func (s *S3Storage) ListObjects(prefix string, delimiter string) ([]string, []string, error) {
	result, err := s.client.ListObjectsV2(context.TODO(), &s3.ListObjectsV2Input{
		Bucket:    aws.String(s.bucketName),
		Prefix:    aws.String(prefix),
		Delimiter: aws.String(delimiter),
	})
	if err != nil {
		return nil, nil, err
	}

	var objects []string
	for _, obj := range result.Contents {
		objects = append(objects, *obj.Key)
	}

	var prefixes []string
	for _, prefix := range result.CommonPrefixes {
		prefixes = append(prefixes, *prefix.Prefix)
	}

	return objects, prefixes, nil
}

func (s *S3Storage) GenerateDownloadURL(key string) (string, error) {
	presignClient := s3.NewPresignClient(s.client)
	presignResult, err := presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return "", err
	}
	return presignResult.URL, nil
}

func (s *S3Storage) HealthCheck() error {
	_, err := s.client.HeadBucket(context.TODO(), &s3.HeadBucketInput{
		Bucket: aws.String(s.bucketName),
	})
	return err
}

// FilesystemStorage implements Storage using local filesystem
type FilesystemStorage struct {
	basePath string
	baseURL  string // Base URL for serving files
}

func NewFilesystemStorage(basePath, baseURL string) (*FilesystemStorage, error) {
	// Ensure base path exists
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base path: %w", err)
	}

	return &FilesystemStorage{
		basePath: basePath,
		baseURL:  baseURL,
	}, nil
}

func (f *FilesystemStorage) GetObject(key string) ([]byte, error) {
	path := filepath.Join(f.basePath, key)
	return os.ReadFile(path)
}

func (f *FilesystemStorage) PutObject(key string, data []byte) error {
	path := filepath.Join(f.basePath, key)

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func (f *FilesystemStorage) DeleteObject(key string) error {
	path := filepath.Join(f.basePath, key)
	err := os.Remove(path)
	if err != nil && os.IsNotExist(err) {
		return nil // already gone
	}
	// Clean up empty parent directories up to basePath
	dir := filepath.Dir(path)
	for dir != f.basePath {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil || len(entries) > 0 {
			break
		}
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
	return err
}

func (f *FilesystemStorage) ListObjects(prefix string, delimiter string) ([]string, []string, error) {
	searchPath := filepath.Join(f.basePath, prefix)

	var objects []string
	var prefixes []string

	// If delimiter is set, only list immediate children
	if delimiter != "" {
		entries, err := os.ReadDir(searchPath)
		if err != nil {
			if os.IsNotExist(err) {
				return objects, prefixes, nil
			}
			return nil, nil, err
		}

		for _, entry := range entries {
			relPath := filepath.Join(prefix, entry.Name())
			if entry.IsDir() {
				prefixes = append(prefixes, relPath+"/")
			} else {
				objects = append(objects, relPath)
			}
		}

		return objects, prefixes, nil
	}

	// No delimiter - recursive listing
	err := filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, _ := filepath.Rel(f.basePath, path)
			objects = append(objects, filepath.ToSlash(relPath))
		}
		return nil
	})

	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}

	return objects, prefixes, nil
}

func (f *FilesystemStorage) GenerateDownloadURL(key string) (string, error) {
	// For filesystem storage, return a direct URL
	return fmt.Sprintf("%s/download/%s", f.baseURL, key), nil
}

func (f *FilesystemStorage) HealthCheck() error {
	// Check if base path is accessible
	_, err := os.Stat(f.basePath)
	return err
}

// ServeFile serves a file from filesystem storage (for direct downloads)
func (f *FilesystemStorage) ServeFile(w io.Writer, key string) error {
	path := filepath.Join(f.basePath, key)
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	_, err = io.Copy(w, file)
	return err
}
