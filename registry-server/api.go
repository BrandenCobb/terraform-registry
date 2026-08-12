package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

var mutationMu sync.Mutex

// API response types
type APIResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

type ProviderInfo struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Versions  []ProviderVersion `json:"versions"`
}

type ModuleInfo struct {
	Namespace string          `json:"namespace"`
	Name      string          `json:"name"`
	Provider  string          `json:"provider"`
	Versions  []ModuleVersion `json:"versions"`
}

type RegistryStats struct {
	Providers        int `json:"providers"`
	ProviderVersions int `json:"provider_versions"`
	Modules          int `json:"modules"`
	ModuleVersions   int `json:"module_versions"`
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// --- Stats ---

func registryStatsHandler(w http.ResponseWriter, r *http.Request) {
	stats := RegistryStats{}

	_, nsPrefixes, _ := store.List("providers/", "/")
	for _, nsPrefix := range nsPrefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "providers/")
		_, namePrefixes, _ := store.List(nsPrefix, "/")
		for _, namePrefix := range namePrefixes {
			pName := strings.TrimPrefix(strings.TrimSuffix(namePrefix, "/"), nsPrefix)
			pName = strings.TrimSuffix(pName, "/")
			stats.Providers++
			versions, _ := store.ScanProviderVersions(ns, pName)
			stats.ProviderVersions += len(versions)
		}
	}

	_, moduleNSPrefixes, _ := store.List("modules/", "/")
	for _, nsPrefix := range moduleNSPrefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "modules/")
		_, namePrefixes, _ := store.List(nsPrefix, "/")
		for _, namePrefix := range namePrefixes {
			modName := strings.TrimPrefix(strings.TrimSuffix(namePrefix, "/"), nsPrefix)
			modName = strings.TrimSuffix(modName, "/")
			modPrefix := fmt.Sprintf("modules/%s/%s/", ns, modName)
			_, provPrefixes, _ := store.List(modPrefix, "/")
			for _, provPrefix := range provPrefixes {
				provName := strings.TrimPrefix(strings.TrimSuffix(provPrefix, "/"), modPrefix)
				provName = strings.TrimSuffix(provName, "/")
				stats.Modules++
				versions, _ := store.ScanModuleVersions(ns, modName, provName)
				stats.ModuleVersions += len(versions)
			}
		}
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: stats})
}

// --- Provider Management ---

func listProvidersHandler(w http.ResponseWriter, r *http.Request) {
	_, prefixes, err := store.List("providers/", "/")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	var providers []ProviderInfo
	for _, nsPrefix := range prefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "providers/")
		_, namePrefixes, _ := store.List(nsPrefix, "/")
		for _, namePrefix := range namePrefixes {
			pName := strings.TrimPrefix(strings.TrimSuffix(namePrefix, "/"), nsPrefix)
			pName = strings.TrimSuffix(pName, "/")
			versions, _ := store.ScanProviderVersions(ns, pName)
			var pVersions []ProviderVersion
			for _, v := range versions {
				platforms, _ := store.GetProviderPlatforms(ns, pName, v)
				pVersions = append(pVersions, ProviderVersion{Version: v, Protocols: []string{"5.0"}, Platforms: platforms})
			}
			providers = append(providers, ProviderInfo{Namespace: ns, Name: pName, Versions: pVersions})
		}
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: providers})
}

func getProviderHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace, name := vars["namespace"], vars["name"]

	versions, err := store.ScanProviderVersions(namespace, name)
	if err != nil || len(versions) == 0 {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Provider not found"})
		return
	}

	var pVersions []ProviderVersion
	for _, v := range versions {
		platforms, _ := store.GetProviderPlatforms(namespace, name, v)
		pVersions = append(pVersions, ProviderVersion{Version: v, Protocols: []string{"5.0"}, Platforms: platforms})
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: ProviderInfo{
		Namespace: namespace, Name: name, Versions: pVersions,
	}})
}

func uploadProviderHandler(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	mutationMu.Lock()
	defer mutationMu.Unlock()

	vars := mux.Vars(r)
	namespace, name, version := vars["namespace"], vars["name"], vars["version"]
	osName, arch := vars["os"], vars["arch"]

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+(1<<20))
	file, err := multipartUploadPart(r)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	defer func() { _ = file.Close() }()

	// Read first 4 bytes for magic byte validation
	peek := make([]byte, 4)
	n, _ := io.ReadFull(file, peek)
	if n < 4 {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "File too small"})
		return
	}

	// Validate zip magic bytes
	if err := ValidateUpload(peek, "zip"); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}

	// Store the artifact under a content-addressed immutable filename. Platform
	// metadata is atomically switched only after the artifact is durable.
	h := sha256.New()
	content := io.MultiReader(bytes.NewReader(peek), file)
	var filename string
	size, zipKey, artifactExisted, err := store.PutStreamImmutable(io.TeeReader(content, h), maxUploadSize, func(f *os.File, size int64) error {
		if err := validateProviderArchive(f, size, name, maxUploadSize); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		return nil
	}, func() string {
		shasum := fmt.Sprintf("%x", h.Sum(nil))
		filename = fmt.Sprintf("terraform-provider-%s_%s_%s_%s_%s.zip", name, version, osName, arch, shasum)
		return fmt.Sprintf("providers/%s/%s/%s/%s", namespace, name, version, filename)
	})
	if err != nil {
		if isUploadTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if errors.Is(err, ErrInvalidArchive) {
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Store failed: " + err.Error()})
		return
	}

	// Store platform metadata
	shasum := fmt.Sprintf("%x", h.Sum(nil))
	metaKey := fmt.Sprintf("providers/%s/%s/%s/%s_%s.json", namespace, name, version, osName, arch)
	previousMeta, previousMetaErr := store.Get(metaKey)
	platformMeta := PlatformMeta{OS: osName, Arch: arch, Filename: filename, Shasum: shasum, Protocols: []string{"5.0"}}
	metaData, _ := json.Marshal(platformMeta)
	if err := store.Put(metaKey, metaData); err != nil {
		if !artifactExisted {
			_ = store.Delete(zipKey)
		}
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Metadata store failed"})
		return
	}

	checksumsName, signatureName := providerChecksumsNames(name, version)
	versionPrefix := fmt.Sprintf("providers/%s/%s/%s/", namespace, name, version)
	checksumsKey, signatureKey := versionPrefix+checksumsName, versionPrefix+signatureName
	previousChecksums, previousChecksumsErr := store.Get(checksumsKey)
	previousSignature, previousSignatureErr := store.Get(signatureKey)
	restorePublication := func() {
		if previousMetaErr == nil {
			_ = store.Put(metaKey, previousMeta)
		} else {
			_ = store.Delete(metaKey)
		}
		if previousChecksumsErr == nil {
			_ = store.Put(checksumsKey, previousChecksums)
		} else {
			_ = store.Delete(checksumsKey)
		}
		if previousSignatureErr == nil {
			_ = store.Put(signatureKey, previousSignature)
		} else {
			_ = store.Delete(signatureKey)
		}
		if !artifactExisted {
			_ = store.Delete(zipKey)
		}
	}
	if err := rebuildProviderChecksums(namespace, name, version); err != nil {
		restorePublication()
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Checksum publication failed"})
		return
	}

	// Update index
	if err := store.AddProviderVersion(namespace, name, version); err != nil {
		restorePublication()
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Index update failed"})
		return
	}

	if previousMetaErr == nil {
		var oldMeta PlatformMeta
		if json.Unmarshal(previousMeta, &oldMeta) == nil && oldMeta.Filename != "" && oldMeta.Filename != filename {
			oldKey := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, name, version, oldMeta.Filename)
			_ = store.Delete(oldKey)
		}
	}
	if scanner != nil {
		if _, err := scanner.Enqueue(ScanJob{Digest: shasum, Kind: ArtifactProvider, ArtifactKey: zipKey, Namespace: namespace, Name: name, Version: version, Platform: osName + "/" + arch}, false); err != nil {
			logger.Error("provider security scan enqueue failed", "digest", shasum, "error", err)
		}
	}

	// Notify webhooks
	webhooks.Notify("publish", WebhookPayload{
		Kind: "provider", Namespace: namespace, Name: name, Version: version,
		Platform: osName + "/" + arch,
	})

	logger.Info("provider uploaded",
		"namespace", namespace, "name", name, "version", version,
		"platform", osName+"/"+arch, "size", size, "sha256", shasum[:16],
	)
	metrics.ProviderUploads.Add(1)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Provider %s/%s@%s (%s/%s) uploaded", namespace, name, version, osName, arch),
	})
}

func deleteProviderVersionHandler(w http.ResponseWriter, r *http.Request) {
	mutationMu.Lock()
	defer mutationMu.Unlock()

	vars := mux.Vars(r)
	namespace, name, version := vars["namespace"], vars["name"], vars["version"]

	prefix := fmt.Sprintf("providers/%s/%s/%s", namespace, name, version)
	rollback, commit, err := store.StageDeleteTree(prefix)
	if err != nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}
	if err := store.RemoveProviderVersion(namespace, name, version); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			logger.Error("provider delete rollback failed", "error", rollbackErr)
		}
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Index update failed"})
		return
	}
	if err := commit(); err != nil {
		logger.Error("provider delete cleanup failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Delete cleanup failed"})
		return
	}

	webhooks.Notify("delete", WebhookPayload{
		Kind: "provider", Namespace: namespace, Name: name, Version: version,
	})

	logger.Info("provider deleted", "namespace", namespace, "name", name, "version", version)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Provider %s/%s@%s deleted", namespace, name, version),
	})
}

func deprecateProviderHandler(w http.ResponseWriter, r *http.Request) {
	mutationMu.Lock()
	defer mutationMu.Unlock()

	vars := mux.Vars(r)
	namespace, name, version := vars["namespace"], vars["name"], vars["version"]
	idx, err := store.GetProviderIndex(namespace, name)
	if err != nil || !indexHasVersion(idx, version) {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}

	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := store.DeprecateProviderVersion(namespace, name, version, body.Message); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	webhooks.Notify("deprecate", WebhookPayload{
		Kind: "provider", Namespace: namespace, Name: name, Version: version,
		Data: map[string]string{"message": body.Message},
	})

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Provider %s/%s@%s deprecated", namespace, name, version),
	})
}

// --- Module Management ---

func listModulesHandler(w http.ResponseWriter, r *http.Request) {
	_, nsPrefixes, err := store.List("modules/", "/")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	var modules []ModuleInfo
	seen := make(map[string]bool)
	for _, nsPrefix := range nsPrefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "modules/")
		_, namePrefixes, _ := store.List(nsPrefix, "/")
		for _, namePrefix := range namePrefixes {
			modName := strings.TrimPrefix(strings.TrimSuffix(namePrefix, "/"), nsPrefix)
			modName = strings.TrimSuffix(modName, "/")
			modPrefix := fmt.Sprintf("modules/%s/%s/", ns, modName)
			_, provPrefixes, _ := store.List(modPrefix, "/")
			for _, provPrefix := range provPrefixes {
				provName := strings.TrimPrefix(strings.TrimSuffix(provPrefix, "/"), modPrefix)
				provName = strings.TrimSuffix(provName, "/")
				key := fmt.Sprintf("%s/%s/%s", ns, modName, provName)
				if seen[key] {
					continue
				}
				seen[key] = true
				versions, _ := store.ScanModuleVersions(ns, modName, provName)
				var mVersions []ModuleVersion
				for _, v := range versions {
					mVersions = append(mVersions, ModuleVersion{Version: v})
				}
				modules = append(modules, ModuleInfo{Namespace: ns, Name: modName, Provider: provName, Versions: mVersions})
			}
		}
	}

	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Namespace+modules[i].Name < modules[j].Namespace+modules[j].Name
	})

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: modules})
}

func getModuleHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace, name, provider := vars["namespace"], vars["name"], vars["provider"]

	versions, err := store.ScanModuleVersions(namespace, name, provider)
	if err != nil || len(versions) == 0 {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Module not found"})
		return
	}

	var mVersions []ModuleVersion
	for _, v := range versions {
		mVersions = append(mVersions, ModuleVersion{Version: v})
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: ModuleInfo{
		Namespace: namespace, Name: name, Provider: provider, Versions: mVersions,
	}})
}

func uploadModuleHandler(w http.ResponseWriter, r *http.Request) {
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	mutationMu.Lock()
	defer mutationMu.Unlock()

	vars := mux.Vars(r)
	namespace, name, provider, version := vars["namespace"], vars["name"], vars["provider"], vars["version"]

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+(1<<20))
	file, err := multipartUploadPart(r)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	defer func() { _ = file.Close() }()

	// Validate gzip magic bytes
	peek := make([]byte, 4)
	n, _ := io.ReadFull(file, peek)
	if n < 2 {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "File too small"})
		return
	}
	if err := ValidateUpload(peek, "tar.gz"); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
		return
	}

	prefix := fmt.Sprintf("modules/%s/%s/%s/%s", namespace, name, provider, version)
	content := io.MultiReader(bytes.NewReader(peek[:n]), file)
	h := sha256.New()
	var filename string
	size, key, artifactExisted, err := store.PutStreamImmutable(io.TeeReader(content, h), maxUploadSize, func(f *os.File, size int64) error {
		if err := validateModuleArchive(f, size, maxUploadSize); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		return nil
	}, func() string {
		shasum := fmt.Sprintf("%x", h.Sum(nil))
		filename = "module_" + shasum + ".tar.gz"
		return prefix + "/" + filename
	})
	if err != nil {
		if isUploadTooLarge(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, APIResponse{Success: false, Message: err.Error()})
			return
		}
		if errors.Is(err, ErrInvalidArchive) {
			writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Store failed: " + err.Error()})
		return
	}

	artifactMetaKey := prefix + "/artifact.json"
	previousMeta, previousMetaErr := store.Get(artifactMetaKey)
	artifactMeta, _ := json.Marshal(ModuleArtifactMeta{Filename: filename, SHA256: fmt.Sprintf("%x", h.Sum(nil))})
	if err := store.Put(artifactMetaKey, artifactMeta); err != nil {
		if !artifactExisted {
			_ = store.Delete(key)
		}
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Metadata store failed"})
		return
	}

	if err := store.AddModuleVersion(namespace, name, provider, version); err != nil {
		if previousMetaErr == nil {
			_ = store.Put(artifactMetaKey, previousMeta)
		} else {
			_ = store.Delete(artifactMetaKey)
		}
		if !artifactExisted {
			_ = store.Delete(key)
		}
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Index update failed"})
		return
	}
	if previousMetaErr == nil {
		var oldMeta ModuleArtifactMeta
		if json.Unmarshal(previousMeta, &oldMeta) == nil && oldMeta.Filename != "" && oldMeta.Filename != filename {
			_ = store.Delete(prefix + "/" + oldMeta.Filename)
		}
	}
	shasum := fmt.Sprintf("%x", h.Sum(nil))
	if scanner != nil {
		if _, err := scanner.Enqueue(ScanJob{Digest: shasum, Kind: ArtifactModule, ArtifactKey: key, Namespace: namespace, Name: name, Provider: provider, Version: version}, false); err != nil {
			logger.Error("module security scan enqueue failed", "digest", shasum, "error", err)
		}
	}

	webhooks.Notify("publish", WebhookPayload{
		Kind: "module", Namespace: namespace, Name: name, Provider: provider, Version: version,
	})

	logger.Info("module uploaded",
		"namespace", namespace, "name", name, "provider", provider,
		"version", version, "size", size,
	)
	metrics.ModuleUploads.Add(1)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Module %s/%s/%s@%s uploaded", namespace, name, provider, version),
	})
}

func deleteModuleVersionHandler(w http.ResponseWriter, r *http.Request) {
	mutationMu.Lock()
	defer mutationMu.Unlock()

	vars := mux.Vars(r)
	namespace, name, provider, version := vars["namespace"], vars["name"], vars["provider"], vars["version"]

	prefix := fmt.Sprintf("modules/%s/%s/%s/%s", namespace, name, provider, version)
	rollback, commit, err := store.StageDeleteTree(prefix)
	if err != nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}
	if err := store.RemoveModuleVersion(namespace, name, provider, version); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			logger.Error("module delete rollback failed", "error", rollbackErr)
		}
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Index update failed"})
		return
	}
	if err := commit(); err != nil {
		logger.Error("module delete cleanup failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Delete cleanup failed"})
		return
	}

	webhooks.Notify("delete", WebhookPayload{
		Kind: "module", Namespace: namespace, Name: name, Provider: provider, Version: version,
	})

	logger.Info("module deleted",
		"namespace", namespace, "name", name, "provider", provider, "version", version,
	)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Module %s/%s/%s@%s deleted", namespace, name, provider, version),
	})
}

func deprecateModuleHandler(w http.ResponseWriter, r *http.Request) {
	mutationMu.Lock()
	defer mutationMu.Unlock()

	vars := mux.Vars(r)
	namespace, name, provider, version := vars["namespace"], vars["name"], vars["provider"], vars["version"]
	idx, err := store.GetModuleIndex(namespace, name, provider)
	if err != nil || !indexHasVersion(idx, version) {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}

	var body struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	if err := store.DeprecateModuleVersion(namespace, name, provider, version, body.Message); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	webhooks.Notify("deprecate", WebhookPayload{
		Kind: "module", Namespace: namespace, Name: name, Provider: provider, Version: version,
		Data: map[string]string{"message": body.Message},
	})

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Module %s/%s/%s@%s deprecated", namespace, name, provider, version),
	})
}

func gcHandler(w http.ResponseWriter, r *http.Request) {
	mutationMu.Lock()
	defer mutationMu.Unlock()

	n, err := store.GarbageCollect()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "GC failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Garbage collection complete, %d files removed", n),
	})
}

func multipartUploadPart(r *http.Request) (io.ReadCloser, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("invalid multipart request: %w", err)
	}
	part, err := mr.NextPart()
	if err != nil {
		return nil, fmt.Errorf("missing 'file' field: %w", err)
	}
	if part.FormName() != "file" || part.FileName() == "" {
		_ = part.Close()
		return nil, fmt.Errorf("the first multipart part must be a file named 'file'")
	}
	return part, nil
}

func isUploadTooLarge(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr) || errors.Is(err, ErrUploadTooLarge)
}

func writeUploadError(w http.ResponseWriter, err error) {
	if isUploadTooLarge(err) {
		writeJSON(w, http.StatusRequestEntityTooLarge, APIResponse{Success: false, Message: "Upload exceeds configured maximum size"})
		return
	}
	writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: err.Error()})
}

func indexHasVersion(idx *Index, version string) bool {
	if idx == nil {
		return false
	}
	for _, v := range idx.Versions {
		if v == version {
			return true
		}
	}
	return false
}
