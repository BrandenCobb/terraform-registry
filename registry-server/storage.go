package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrUploadTooLarge = errors.New("upload exceeds maximum size")

// Store implements filesystem-backed storage for the Terraform registry.
// All writes are atomic (write-to-temp + rename). Reads use standard file I/O.
// A per-path mutex map prevents concurrent writes to the same artifact.
type Store struct {
	basePath string
	baseURL  string
	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	logger   *slog.Logger
}

// NewStore creates a new filesystem store. basePath is the root directory
// for all registry data. baseURL is the public URL used for download links.
func NewStore(basePath, baseURL string, logger *slog.Logger) (*Store, error) {
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	if err := os.MkdirAll(absBase, 0750); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	basePath, err = filepath.EvalSymlinks(absBase)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root symlinks: %w", err)
	}
	dirs := []string{
		filepath.Join(basePath, "providers"),
		filepath.Join(basePath, "modules"),
		filepath.Join(basePath, "keys"),
		filepath.Join(basePath, "tmp"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0750); err != nil {
			return nil, fmt.Errorf("create directory %s: %w", d, err)
		}
	}
	return &Store{
		basePath: basePath,
		baseURL:  strings.TrimRight(baseURL, "/"),
		locks:    make(map[string]*sync.Mutex),
		logger:   logger,
	}, nil
}

// getLock returns a per-key mutex for serializing concurrent writes.
func (s *Store) getLock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[key] == nil {
		s.locks[key] = &sync.Mutex{}
	}
	return s.locks[key]
}

// --- Core CRUD ---

// Get reads a file from storage.
func (s *Store) Get(key string) ([]byte, error) {
	path, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path) // #nosec G304 -- resolve confines the path to the storage root and rejects symlinks.
}

// Open opens an artifact for streaming reads.
func (s *Store) Open(key string) (*os.File, error) {
	path, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	return os.Open(path) // #nosec G304 -- resolve confines the path to the storage root and rejects symlinks.
}

// Put writes data atomically (write-to-temp, sync, rename).
func (s *Store) Put(key string, data []byte) error {
	lk := s.getLock(key)
	lk.Lock()
	defer lk.Unlock()
	return s.putUnlocked(key, data)
}

func (s *Store) putUnlocked(key string, data []byte) error {
	path, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Write to temp file in same directory (same filesystem for atomic rename)
	tmp := filepath.Join(filepath.Dir(path), fmt.Sprintf(".tmp-%s", filepath.Base(path)))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304 -- path is confined by resolve above.
	if err != nil {
		return fmt.Errorf("open temp: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// PutStream writes data from a reader atomically, with a max size limit.
func (s *Store) PutStream(key string, r io.Reader, maxSize int64) (int64, error) {
	return s.PutStreamValidated(key, r, maxSize, nil)
}

// PutStreamValidated writes to a unique temporary file, validates it before
// publication, and atomically replaces the destination only after validation.
func (s *Store) PutStreamValidated(key string, r io.Reader, maxSize int64, validate func(*os.File, int64) error) (int64, error) {
	lk := s.getLock(key)
	lk.Lock()
	defer lk.Unlock()

	path, err := s.resolve(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}

	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return 0, fmt.Errorf("open temp: %w", err)
	}
	tmp := f.Name()
	published := false
	defer func() {
		_ = f.Close()
		if !published {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0640); err != nil {
		return 0, fmt.Errorf("chmod temp: %w", err)
	}

	limited := io.LimitedReader{R: r, N: maxSize + 1}
	n, err := io.Copy(f, &limited)
	if err != nil {
		return 0, fmt.Errorf("copy: %w", err)
	}
	if n > maxSize {
		return 0, fmt.Errorf("%w of %d bytes", ErrUploadTooLarge, maxSize)
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("sync: %w", err)
	}
	if validate != nil {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, fmt.Errorf("seek temp: %w", err)
		}
		if err := validate(f, n); err != nil {
			return 0, err
		}
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, fmt.Errorf("rename: %w", err)
	}
	published = true
	return n, nil
}

// PutStreamImmutable validates a stream before publishing it at a
// content-derived key. Existing immutable objects are preserved.
func (s *Store) PutStreamImmutable(r io.Reader, maxSize int64, validate func(*os.File, int64) error, key func() string) (int64, string, bool, error) {
	f, err := os.CreateTemp(s.basePath, ".tmp-upload-*")
	if err != nil {
		return 0, "", false, fmt.Errorf("open temp: %w", err)
	}
	tmp := f.Name()
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()
	if err := f.Chmod(0640); err != nil {
		return 0, "", false, fmt.Errorf("chmod temp: %w", err)
	}
	limited := io.LimitedReader{R: r, N: maxSize + 1}
	n, err := io.Copy(f, &limited)
	if err != nil {
		return 0, "", false, fmt.Errorf("copy: %w", err)
	}
	if n > maxSize {
		return 0, "", false, fmt.Errorf("%w of %d bytes", ErrUploadTooLarge, maxSize)
	}
	if err := f.Sync(); err != nil {
		return 0, "", false, fmt.Errorf("sync: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, "", false, fmt.Errorf("seek temp: %w", err)
	}
	if validate != nil {
		if err := validate(f, n); err != nil {
			return 0, "", false, err
		}
	}
	destinationKey := key()
	destination, err := s.resolve(destinationKey)
	if err != nil {
		return 0, "", false, err
	}
	lk := s.getLock(destinationKey)
	lk.Lock()
	defer lk.Unlock()
	if _, err := os.Stat(destination); err == nil {
		return n, destinationKey, true, nil
	} else if !os.IsNotExist(err) {
		return 0, "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0750); err != nil {
		return 0, "", false, fmt.Errorf("mkdir: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, "", false, fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, destination); err != nil {
		return 0, "", false, fmt.Errorf("rename: %w", err)
	}
	return n, destinationKey, false, nil
}

// StageDeleteTree atomically hides a version directory and returns rollback and
// commit functions. It is intended for serialized management mutations.
func (s *Store) StageDeleteTree(key string) (func() error, func() error, error) {
	path, err := s.resolve(strings.TrimSuffix(key, "/"))
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, nil, err
	}
	marker, err := os.CreateTemp(filepath.Dir(path), ".trash-"+filepath.Base(path)+"-*")
	if err != nil {
		return nil, nil, err
	}
	trash := marker.Name()
	if err := marker.Close(); err != nil {
		_ = os.Remove(trash)
		return nil, nil, err
	}
	if err := os.Remove(trash); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(path, trash); err != nil {
		return nil, nil, err
	}
	rollback := func() error { return os.Rename(trash, path) }
	commit := func() error { return os.RemoveAll(trash) }
	return rollback, commit, nil
}

// Delete removes a file. Cleans up empty parent directories up to basePath.
func (s *Store) Delete(key string) error {
	lk := s.getLock(key)
	lk.Lock()
	defer lk.Unlock()

	path, err := s.resolve(key)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	// Clean empty parent dirs
	dir := filepath.Dir(path)
	for dir != s.basePath {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil || len(entries) > 0 {
			break
		}
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
	return nil
}

// List returns objects and subdirectories under a prefix.
// If delimiter is non-empty, returns immediate subdirectories as prefixes.
// If delimiter is empty, returns all files recursively.
func (s *Store) List(prefix, delimiter string) (objects []string, prefixes []string, err error) {
	searchPath, err := s.resolve(prefix)
	if err != nil {
		return nil, nil, err
	}

	if delimiter != "" {
		entries, err := os.ReadDir(searchPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, nil
			}
			return nil, nil, err
		}
		for _, e := range entries {
			rel := filepath.ToSlash(filepath.Join(prefix, e.Name()))
			if e.IsDir() {
				prefixes = append(prefixes, rel+"/")
			} else {
				objects = append(objects, rel)
			}
		}
		return objects, prefixes, nil
	}

	err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(s.basePath, path)
			objects = append(objects, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	return objects, prefixes, nil
}

// Exists checks if a key exists in storage.
func (s *Store) Exists(key string) bool {
	path, err := s.resolve(key)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// DownloadURL returns the public download URL for a key.
func (s *Store) DownloadURL(key string) string {
	return fmt.Sprintf("%s/download/%s", s.baseURL, key)
}

// HealthCheck verifies storage is accessible.
func (s *Store) HealthCheck() error {
	f, err := os.CreateTemp(s.basePath, ".health-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if err := f.Close(); err != nil {
		return err
	}
	return os.Remove(name)
}

// BasePath returns the storage root.
func (s *Store) BasePath() string {
	return s.basePath
}

// --- Index Management ---

// Index represents a version index file.
type Index struct {
	Versions []string          `json:"versions"`
	Metadata map[string]string `json:"metadata,omitempty"` // version -> description
}

// VersionMetadata stores per-version metadata.
type VersionMetadata struct {
	Version     string            `json:"version"`
	Description string            `json:"description,omitempty"`
	Deprecated  bool              `json:"deprecated,omitempty"`
	Deprecation string            `json:"deprecation_message,omitempty"`
	GPGKeyID    string            `json:"gpg_key_id,omitempty"`
	GPGKeyArmor string            `json:"gpg_key_armor,omitempty"`
	Platforms   []PlatformMeta    `json:"platforms,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// PlatformMeta stores per-platform artifact metadata.
type PlatformMeta struct {
	OS        string   `json:"os"`
	Arch      string   `json:"arch"`
	Filename  string   `json:"filename"`
	Shasum    string   `json:"shasum"`
	Protocols []string `json:"protocols,omitempty"`
}

// GetProviderIndex reads the provider version index.
func (s *Store) GetProviderIndex(namespace, name string) (*Index, error) {
	return s.getIndex(fmt.Sprintf("providers/%s/%s/index.json", namespace, name))
}

// GetModuleIndex reads the module version index.
func (s *Store) GetModuleIndex(namespace, name, provider string) (*Index, error) {
	return s.getIndex(fmt.Sprintf("modules/%s/%s/%s/index.json", namespace, name, provider))
}

// AddProviderVersion adds a version to the provider index.
func (s *Store) AddProviderVersion(namespace, name, version string) error {
	key := fmt.Sprintf("providers/%s/%s/index.json", namespace, name)
	return s.addVersionToIndex(key, version)
}

// AddModuleVersion adds a version to the module index.
func (s *Store) AddModuleVersion(namespace, name, provider, version string) error {
	key := fmt.Sprintf("modules/%s/%s/%s/index.json", namespace, name, provider)
	return s.addVersionToIndex(key, version)
}

// RemoveProviderVersion removes a version from the provider index.
func (s *Store) RemoveProviderVersion(namespace, name, version string) error {
	key := fmt.Sprintf("providers/%s/%s/index.json", namespace, name)
	return s.removeVersionFromIndex(key, version)
}

// RemoveModuleVersion removes a version from the module index.
func (s *Store) RemoveModuleVersion(namespace, name, provider, version string) error {
	key := fmt.Sprintf("modules/%s/%s/%s/index.json", namespace, name, provider)
	return s.removeVersionFromIndex(key, version)
}

// DeprecateProviderVersion marks a provider version as deprecated.
func (s *Store) DeprecateProviderVersion(namespace, name, version, message string) error {
	key := fmt.Sprintf("providers/%s/%s/%s/metadata.json", namespace, name, version)
	return s.setDeprecation(key, version, message)
}

// DeprecateModuleVersion marks a module version as deprecated.
func (s *Store) DeprecateModuleVersion(namespace, name, provider, version, message string) error {
	key := fmt.Sprintf("modules/%s/%s/%s/%s/metadata.json", namespace, name, provider, version)
	return s.setDeprecation(key, version, message)
}

// ScanProviderVersions scans the filesystem for provider versions.
func (s *Store) ScanProviderVersions(namespace, name string) ([]string, error) {
	prefix := fmt.Sprintf("providers/%s/%s/", namespace, name)
	_, prefixes, err := s.List(prefix, "/")
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var versions []string
	for _, p := range prefixes {
		parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
		if len(parts) >= 4 {
			v := parts[3]
			if !seen[v] && v != "index.json" {
				seen[v] = true
				versions = append(versions, v)
			}
		}
	}
	sortSemanticVersions(versions)
	return versions, nil
}

// ScanModuleVersions scans the filesystem for module versions.
func (s *Store) ScanModuleVersions(namespace, name, provider string) ([]string, error) {
	prefix := fmt.Sprintf("modules/%s/%s/%s/", namespace, name, provider)
	_, prefixes, err := s.List(prefix, "/")
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, p := range prefixes {
		parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
		if len(parts) >= 5 {
			versions = append(versions, parts[4])
		}
	}
	sortSemanticVersions(versions)
	return versions, nil
}

// GetProviderPlatforms returns available platforms for a provider version.
func (s *Store) GetProviderPlatforms(namespace, name, version string) ([]PlatformMeta, error) {
	prefix := fmt.Sprintf("providers/%s/%s/%s/", namespace, name, version)
	objects, _, err := s.List(prefix, "")
	if err != nil {
		return nil, err
	}

	var platforms []PlatformMeta
	seen := make(map[string]bool)
	for _, obj := range objects {
		if strings.HasSuffix(obj, ".json") && !strings.HasSuffix(obj, "index.json") && !strings.HasSuffix(obj, "metadata.json") {
			data, err := s.Get(obj)
			if err != nil {
				continue
			}
			var pm PlatformMeta
			if err := json.Unmarshal(data, &pm); err != nil {
				continue
			}
			key := pm.OS + "/" + pm.Arch
			if !seen[key] {
				seen[key] = true
				platforms = append(platforms, pm)
			}
		}
	}
	return platforms, nil
}

// GetVersionMetadata reads per-version metadata.
func (s *Store) GetVersionMetadata(key string) (*VersionMetadata, error) {
	data, err := s.Get(key)
	if err != nil {
		return nil, err
	}
	var meta VersionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// GarbageCollect removes temp files older than the given path's mtime.
func (s *Store) GarbageCollect() (int, error) {
	removed := 0
	cutoff := time.Now().Add(-time.Hour)
	err := filepath.Walk(s.basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasPrefix(info.Name(), ".tmp-") && info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) { // #nosec G122 -- Walk does not follow symlinks and storage has one trusted writer.
				return err
			}
			removed++
		}
		return nil
	})
	return removed, err
}

// --- Internal helpers ---

func (s *Store) getIndex(key string) (*Index, error) {
	data, err := s.Get(key)
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{Versions: []string{}}, nil
		}
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("corrupt index %s: %w", key, err)
	}
	if idx.Versions == nil {
		idx.Versions = []string{}
	}
	sortSemanticVersions(idx.Versions)
	return &idx, nil
}

func (s *Store) addVersionToIndex(key, version string) error {
	lk := s.getLock(key)
	lk.Lock()
	defer lk.Unlock()

	idx, err := s.getIndex(key)
	if err != nil {
		return err
	}
	for _, v := range idx.Versions {
		if v == version {
			return nil // already present
		}
	}
	idx.Versions = append(idx.Versions, version)
	sortSemanticVersions(idx.Versions)

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return s.putUnlocked(key, data)
}

func (s *Store) removeVersionFromIndex(key, version string) error {
	lk := s.getLock(key)
	lk.Lock()
	defer lk.Unlock()

	idx, err := s.getIndex(key)
	if err != nil {
		return err
	}
	var filtered []string
	for _, v := range idx.Versions {
		if v != version {
			filtered = append(filtered, v)
		}
	}
	idx.Versions = filtered

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return s.putUnlocked(key, data)
}

// resolve confines every storage key to basePath.
func (s *Store) resolve(key string) (string, error) {
	if key == "" || key == "." {
		return s.basePath, nil
	}
	if filepath.IsAbs(key) || strings.Contains(key, "\\") {
		return "", fmt.Errorf("invalid storage key")
	}
	clean := filepath.Clean(filepath.FromSlash(key))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("storage key escapes root")
	}
	path := filepath.Join(s.basePath, clean)
	rel, err := filepath.Rel(s.basePath, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("storage key escapes root")
	}
	if err := s.rejectSymlinkEscape(path); err != nil {
		return "", err
	}
	return path, nil
}

// rejectSymlinkEscape resolves the nearest existing ancestor so both reads and
// writes are confined even when an in-root path component is a symlink.
func (s *Store) rejectSymlinkEscape(path string) error {
	candidate := path
	for {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err == nil {
			rel, relErr := filepath.Rel(s.basePath, resolved)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("storage path escapes root through symlink")
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("resolve storage path: %w", err)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return fmt.Errorf("cannot resolve storage path")
		}
		candidate = parent
	}
}

func (s *Store) setDeprecation(key, version, message string) error {
	lk := s.getLock(key)
	lk.Lock()
	defer lk.Unlock()

	var meta VersionMetadata
	data, err := s.Get(key)
	if err == nil {
		_ = json.Unmarshal(data, &meta)
	}
	meta.Version = version
	meta.Deprecated = true
	meta.Deprecation = message

	newData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return s.putUnlocked(key, newData)
}

// Unused logger reference to avoid compile error
var _ = (*slog.Logger)(nil)
