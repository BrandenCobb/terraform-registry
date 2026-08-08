package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/gorilla/mux"
)

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
	vars := mux.Vars(r)
	namespace, name, version := vars["namespace"], vars["name"], vars["version"]
	osName, arch := vars["os"], vars["arch"]

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid form: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Missing 'file' field"})
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

	// Reset and read full content
	if seeker, ok := file.(io.Seeker); ok {
		_, _ = seeker.Seek(0, io.SeekStart)
	}

	filename := header.Filename
	if filename == "" || !strings.HasSuffix(filename, ".zip") {
		filename = fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", name, version, osName, arch)
	}

	// Store binary
	zipKey := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, name, version, filename)
	data, _ := io.ReadAll(file)
	if int64(len(data)) > maxUploadSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, APIResponse{
			Success: false,
			Message: fmt.Sprintf("File exceeds maximum size of %d MB", maxUploadSize/(1024*1024)),
		})
		return
	}

	if err := store.Put(zipKey, data); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Store failed: " + err.Error()})
		return
	}

	// Store platform metadata
	shasum := sha256Hex(data)
	metaKey := fmt.Sprintf("providers/%s/%s/%s/%s_%s.json", namespace, name, version, osName, arch)
	platformMeta := PlatformMeta{OS: osName, Arch: arch, Filename: filename, Shasum: shasum, Protocols: []string{"5.0"}}
	metaData, _ := json.Marshal(platformMeta)
	if err := store.Put(metaKey, metaData); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Metadata store failed"})
		return
	}

	// Update index
	if err := store.AddProviderVersion(namespace, name, version); err != nil {
		logger.Warn("failed to update provider index", "error", err)
	}

	// Notify webhooks
	webhooks.Notify("publish", WebhookPayload{
		Kind: "provider", Namespace: namespace, Name: name, Version: version,
		Platform: osName + "/" + arch,
	})

	logger.Info("provider uploaded",
		"namespace", namespace, "name", name, "version", version,
		"platform", osName+"/"+arch, "size", len(data), "sha256", shasum[:16],
	)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Provider %s/%s@%s (%s/%s) uploaded", namespace, name, version, osName, arch),
	})
}

func deleteProviderVersionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace, name, version := vars["namespace"], vars["name"], vars["version"]

	prefix := fmt.Sprintf("providers/%s/%s/%s/", namespace, name, version)
	objects, _, err := store.List(prefix, "")
	if err != nil || len(objects) == 0 {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}

	for _, obj := range objects {
		if err := store.Delete(obj); err != nil {
			logger.Warn("failed to delete", "key", obj, "error", err)
		}
	}

	_ = store.RemoveProviderVersion(namespace, name, version)

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
	vars := mux.Vars(r)
	namespace, name, version := vars["namespace"], vars["name"], vars["version"]

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
	vars := mux.Vars(r)
	namespace, name, provider, version := vars["namespace"], vars["name"], vars["provider"], vars["version"]

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid form: " + err.Error()})
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Missing 'file' field"})
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

	if seeker, ok := file.(io.Seeker); ok {
		_, _ = seeker.Seek(0, io.SeekStart)
	}

	key := fmt.Sprintf("modules/%s/%s/%s/%s/module.tar.gz", namespace, name, provider, version)
	data, _ := io.ReadAll(file)
	if int64(len(data)) > maxUploadSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, APIResponse{
			Success: false,
			Message: fmt.Sprintf("File exceeds maximum size of %d MB", maxUploadSize/(1024*1024)),
		})
		return
	}

	if err := store.Put(key, data); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Store failed: " + err.Error()})
		return
	}

	if err := store.AddModuleVersion(namespace, name, provider, version); err != nil {
		logger.Warn("failed to update module index", "error", err)
	}

	webhooks.Notify("publish", WebhookPayload{
		Kind: "module", Namespace: namespace, Name: name, Provider: provider, Version: version,
	})

	logger.Info("module uploaded",
		"namespace", namespace, "name", name, "provider", provider,
		"version", version, "size", len(data),
	)

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Module %s/%s/%s@%s uploaded", namespace, name, provider, version),
	})
}

func deleteModuleVersionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace, name, provider, version := vars["namespace"], vars["name"], vars["provider"], vars["version"]

	prefix := fmt.Sprintf("modules/%s/%s/%s/%s/", namespace, name, provider, version)
	objects, _, err := store.List(prefix, "")
	if err != nil || len(objects) == 0 {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}

	for _, obj := range objects {
		_ = store.Delete(obj)
	}

	_ = store.RemoveModuleVersion(namespace, name, provider, version)

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
	vars := mux.Vars(r)
	namespace, name, provider, version := vars["namespace"], vars["name"], vars["provider"], vars["version"]

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
