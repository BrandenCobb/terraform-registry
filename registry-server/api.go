package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// listProvidersHandler returns all registered providers
func listProvidersHandler(w http.ResponseWriter, r *http.Request) {
	_, prefixes, err := storage.ListObjects("providers/", "/")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	var providers []ProviderInfo
	for _, nsPrefix := range prefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "providers/")
		_, namePrefixes, err := storage.ListObjects(nsPrefix, "/")
		if err != nil {
			continue
		}
		for _, namePrefix := range namePrefixes {
			providerName := strings.TrimSuffix(namePrefix, "/")
			providerName = strings.TrimPrefix(providerName, "providers/"+ns+"/")
			providerName = strings.TrimSuffix(providerName, "/")
			versions, _ := scanProviderVersions(ns, providerName)
			providers = append(providers, ProviderInfo{
				Namespace: ns,
				Name:      providerName,
				Versions:  versions,
			})
		}
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: providers})
}

// getProviderHandler returns details for a specific provider
func getProviderHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]

	versions, err := scanProviderVersions(namespace, name)
	if err != nil || len(versions) == 0 {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Provider not found"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: ProviderInfo{
		Namespace: namespace,
		Name:      name,
		Versions:  versions,
	}})
}

// uploadProviderHandler handles provider binary upload via multipart form
func uploadProviderHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	version := vars["version"]
	osName := vars["os"]
	arch := vars["arch"]

	if err := r.ParseMultipartForm(500 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid multipart form: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Missing 'file' field in form"})
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to read file: " + err.Error()})
		return
	}

	filename := header.Filename
	if filename == "" || !strings.HasSuffix(filename, ".zip") {
		filename = fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", name, version, osName, arch)
	}

	// Store the binary
	zipKey := fmt.Sprintf("providers/%s/%s/%s/%s", namespace, name, version, filename)
	if err := storage.PutObject(zipKey, data); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to store file: " + err.Error()})
		return
	}

	// Compute checksum
	shasum := fmt.Sprintf("%x", sha256.Sum256(data))

	// Store platform metadata
	metaKey := fmt.Sprintf("providers/%s/%s/%s/%s_%s.json", namespace, name, version, osName, arch)
	meta := PlatformMetadata{Filename: filename, Shasum: shasum}
	metaData, _ := json.Marshal(meta)
	if err := storage.PutObject(metaKey, metaData); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to store metadata: " + err.Error()})
		return
	}

	// Update index
	if err := addVersionToIndex(fmt.Sprintf("providers/%s/%s/index.json", namespace, name), version); err != nil {
		log.Printf("Warning: failed to update provider index: %v", err)
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Provider %s/%s@%s (%s/%s) uploaded", namespace, name, version, osName, arch),
	})
}

// deleteProviderVersionHandler removes a specific provider version
func deleteProviderVersionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	version := vars["version"]

	prefix := fmt.Sprintf("providers/%s/%s/%s/", namespace, name, version)
	objects, _, err := storage.ListObjects(prefix, "")
	if err != nil || len(objects) == 0 {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}

	for _, obj := range objects {
		if err := storage.DeleteObject(obj); err != nil {
			log.Printf("Warning: failed to delete %s: %v", obj, err)
		}
	}

	if err := removeVersionFromIndex(fmt.Sprintf("providers/%s/%s/index.json", namespace, name), version); err != nil {
		log.Printf("Warning: failed to update index: %v", err)
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Provider %s/%s@%s deleted", namespace, name, version),
	})
}

// listModulesHandler returns all registered modules
func listModulesHandler(w http.ResponseWriter, r *http.Request) {
	_, nsPrefixes, err := storage.ListObjects("modules/", "/")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: err.Error()})
		return
	}

	var modules []ModuleInfo
	seen := make(map[string]bool)

	for _, nsPrefix := range nsPrefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "modules/")
		_, namePrefixes, _ := storage.ListObjects(nsPrefix, "/")
		for _, namePrefix := range namePrefixes {
			modName := strings.TrimPrefix(strings.TrimSuffix(namePrefix, "/"), "modules/"+ns+"/")
			modPrefix := fmt.Sprintf("modules/%s/%s/", ns, modName)
			_, provPrefixes, _ := storage.ListObjects(modPrefix, "/")
			for _, provPrefix := range provPrefixes {
				provName := strings.TrimPrefix(strings.TrimSuffix(provPrefix, "/"), modPrefix)
				key := fmt.Sprintf("%s/%s/%s", ns, modName, provName)
				if seen[key] {
					continue
				}
				seen[key] = true
				versions, _ := scanModuleVersions(ns, modName, provName)
				modules = append(modules, ModuleInfo{
					Namespace: ns,
					Name:      modName,
					Provider:  provName,
					Versions:  versions,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: modules})
}

// getModuleHandler returns details for a specific module
func getModuleHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]

	versions, err := scanModuleVersions(namespace, name, provider)
	if err != nil {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Module not found"})
		return
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: ModuleInfo{
		Namespace: namespace,
		Name:      name,
		Provider:  provider,
		Versions:  versions,
	}})
}

// uploadModuleHandler handles module tarball upload via multipart form
func uploadModuleHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]
	version := vars["version"]

	if err := r.ParseMultipartForm(500 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Invalid multipart form: " + err.Error()})
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Success: false, Message: "Missing 'file' field in form"})
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to read file: " + err.Error()})
		return
	}

	key := fmt.Sprintf("modules/%s/%s/%s/%s/module.tar.gz", namespace, name, provider, version)
	if err := storage.PutObject(key, data); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Success: false, Message: "Failed to store module: " + err.Error()})
		return
	}

	if err := addVersionToIndex(fmt.Sprintf("modules/%s/%s/%s/index.json", namespace, name, provider), version); err != nil {
		log.Printf("Warning: failed to update module index: %v", err)
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Module %s/%s/%s@%s uploaded", namespace, name, provider, version),
	})
}

// deleteModuleVersionHandler removes a specific module version
func deleteModuleVersionHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	namespace := vars["namespace"]
	name := vars["name"]
	provider := vars["provider"]
	version := vars["version"]

	prefix := fmt.Sprintf("modules/%s/%s/%s/%s/", namespace, name, provider, version)
	objects, _, err := storage.ListObjects(prefix, "")
	if err != nil || len(objects) == 0 {
		writeJSON(w, http.StatusNotFound, APIResponse{Success: false, Message: "Version not found"})
		return
	}

	for _, obj := range objects {
		if err := storage.DeleteObject(obj); err != nil {
			log.Printf("Warning: failed to delete %s: %v", obj, err)
		}
	}

	indexKey := fmt.Sprintf("modules/%s/%s/%s/index.json", namespace, name, provider)
	if err := removeVersionFromIndex(indexKey, version); err != nil {
		log.Printf("Warning: failed to update index: %v", err)
	}

	writeJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Message: fmt.Sprintf("Module %s/%s/%s@%s deleted", namespace, name, provider, version),
	})
}

// registryStatsHandler returns summary statistics
func registryStatsHandler(w http.ResponseWriter, r *http.Request) {
	stats := RegistryStats{}

	_, providerNSPrefixes, _ := storage.ListObjects("providers/", "/")
	for _, nsPrefix := range providerNSPrefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "providers/")
		_, namePrefixes, _ := storage.ListObjects(nsPrefix, "/")
		for _, namePrefix := range namePrefixes {
			pName := strings.TrimPrefix(strings.TrimSuffix(namePrefix, "/"), nsPrefix)
			pName = strings.TrimSuffix(pName, "/")
			stats.Providers++
			versions, _ := scanProviderVersions(ns, pName)
			stats.ProviderVersions += len(versions)
		}
	}

	_, moduleNSPrefixes, _ := storage.ListObjects("modules/", "/")
	for _, nsPrefix := range moduleNSPrefixes {
		ns := strings.TrimPrefix(strings.TrimSuffix(nsPrefix, "/"), "modules/")
		_, namePrefixes, _ := storage.ListObjects(nsPrefix, "/")
		for _, namePrefix := range namePrefixes {
			modName := strings.TrimPrefix(strings.TrimSuffix(namePrefix, "/"), nsPrefix)
			modName = strings.TrimSuffix(modName, "/")
			modPrefix := fmt.Sprintf("modules/%s/%s/", ns, modName)
			_, provPrefixes, _ := storage.ListObjects(modPrefix, "/")
			for _, provPrefix := range provPrefixes {
				provName := strings.TrimPrefix(strings.TrimSuffix(provPrefix, "/"), modPrefix)
				provName = strings.TrimSuffix(provName, "/")
				stats.Modules++
				versions, _ := scanModuleVersions(ns, modName, provName)
				stats.ModuleVersions += len(versions)
			}
		}
	}

	writeJSON(w, http.StatusOK, APIResponse{Success: true, Data: stats})
}

// Index management helpers

func addVersionToIndex(key, version string) error {
	var index Index

	data, err := storage.GetObject(key)
	if err != nil {
		index = Index{Versions: []string{version}}
	} else {
		if err := json.Unmarshal(data, &index); err != nil {
			return err
		}
		for _, v := range index.Versions {
			if v == version {
				return nil // already present
			}
		}
		index.Versions = append(index.Versions, version)
		sort.Strings(index.Versions)
	}

	newData, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return storage.PutObject(key, newData)
}

func removeVersionFromIndex(key, version string) error {
	data, err := storage.GetObject(key)
	if err != nil {
		return err
	}

	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}

	var newVersions []string
	for _, v := range index.Versions {
		if v != version {
			newVersions = append(newVersions, v)
		}
	}
	index.Versions = newVersions

	newData, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return storage.PutObject(key, newData)
}
