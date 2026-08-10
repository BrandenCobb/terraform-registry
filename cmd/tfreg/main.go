package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev"

var httpClient = &http.Client{Timeout: 10 * time.Minute}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "push":
		handlePush(args)
	case "pull":
		handlePull(args)
	case "list", "ls":
		handleList(args)
	case "bundle":
		handleBundle(args)
	case "delete", "rm":
		handleDelete(args)
	case "version":
		fmt.Printf("tfreg %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`tfreg - Terraform Registry CLI

Usage: tfreg <command> [options]

Commands:
  push      Upload a provider or module to the registry
  pull      Download a provider or module from the registry
  list      List providers or modules in the registry
  bundle    Create a distributable bundle from local files
  delete    Remove a provider or module version from the registry
  version   Show version

Global Options:
  --registry URL    Registry URL (or TFREG_REGISTRY env var)
  --api-key KEY     API key (or TFREG_API_KEY env var)

Run 'tfreg <command> --help' for command-specific help.`)
}

// --- PUSH ---

func handlePush(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tfreg push <provider|module> [options]")
		os.Exit(1)
	}

	switch args[0] {
	case "provider":
		pushProvider(args[1:])
	case "module":
		pushModule(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown type: %s (use 'provider' or 'module')\n", args[0])
		os.Exit(1)
	}
}

func pushProvider(args []string) {
	fs := newFlagSet("tfreg push provider")
	registry := fs.String("registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Registry URL")
	apiKey := fs.String("api-key", envOr("TFREG_API_KEY", ""), "API key")
	namespace := fs.String("namespace", "", "Provider namespace (e.g., hashicorp)")
	name := fs.String("name", "", "Provider name (e.g., aws)")
	ver := fs.String("version", "", "Provider version (e.g., 6.31.0)")
	file := fs.String("file", "", "Path to provider ZIP file")
	osName := fs.String("os", "linux", "Operating system")
	arch := fs.String("arch", "amd64", "Architecture")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *ver == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, --version, and --file are required")
		fs.Usage()
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/v1/providers/%s/%s/%s/%s/%s", *registry, *namespace, *name, *ver, *osName, *arch)
	fmt.Printf("Pushing provider %s/%s@%s (%s/%s)...\n", *namespace, *name, *ver, *osName, *arch)

	resp, err := uploadFile(url, *file, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	printResponse(resp)
}

func pushModule(args []string) {
	fs := newFlagSet("tfreg push module")
	registry := fs.String("registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Registry URL")
	apiKey := fs.String("api-key", envOr("TFREG_API_KEY", ""), "API key")
	namespace := fs.String("namespace", "", "Module namespace")
	name := fs.String("name", "", "Module name")
	provider := fs.String("provider", "", "Provider name")
	ver := fs.String("version", "", "Module version")
	file := fs.String("file", "", "Path to module tarball (.tar.gz)")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *provider == "" || *ver == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, --provider, --version, and --file are required")
		fs.Usage()
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/v1/modules/%s/%s/%s/%s", *registry, *namespace, *name, *provider, *ver)
	fmt.Printf("Pushing module %s/%s/%s@%s...\n", *namespace, *name, *provider, *ver)

	resp, err := uploadFile(url, *file, *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	printResponse(resp)
}

// --- PULL ---

func handlePull(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tfreg pull <provider|module> [options]")
		os.Exit(1)
	}

	switch args[0] {
	case "provider":
		pullProvider(args[1:])
	case "module":
		pullModule(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown type: %s\n", args[0])
		os.Exit(1)
	}
}

func pullProvider(args []string) {
	fs := newFlagSet("tfreg pull provider")
	registry := fs.String("registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Registry URL")
	namespace := fs.String("namespace", "", "Provider namespace")
	name := fs.String("name", "", "Provider name")
	ver := fs.String("version", "", "Provider version")
	osName := fs.String("os", "linux", "Operating system")
	arch := fs.String("arch", "amd64", "Architecture")
	output := fs.String("output", ".", "Output directory")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *ver == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, and --version are required")
		fs.Usage()
		os.Exit(1)
	}

	// Get download metadata
	metaURL := fmt.Sprintf("%s/v1/providers/%s/%s/%s/download/%s/%s", *registry, *namespace, *name, *ver, *osName, *arch)
	fmt.Printf("Fetching download info for %s/%s@%s (%s/%s)...\n", *namespace, *name, *ver, *osName, *arch)

	resp, err := httpClient.Get(metaURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error (%d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	var meta struct {
		Filename    string `json:"filename"`
		DownloadURL string `json:"download_url"`
		Shasum      string `json:"shasum"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	meta.Filename = filepath.Base(meta.Filename)
	if meta.Filename == "." || meta.Filename == "" {
		fmt.Fprintln(os.Stderr, "Error: registry returned an invalid filename")
		os.Exit(1)
	}
	if len(meta.Shasum) != 64 {
		fmt.Fprintln(os.Stderr, "Error: registry returned an invalid SHA256 checksum")
		os.Exit(1)
	}
	fmt.Printf("Downloading %s (sha256: %s...)...\n", meta.Filename, meta.Shasum[:16])

	dest := filepath.Join(*output, meta.Filename)
	if err := downloadFileVerified(meta.DownloadURL, dest, meta.Shasum); err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Saved to %s\n", dest)
}

func pullModule(args []string) {
	fs := newFlagSet("tfreg pull module")
	registry := fs.String("registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Registry URL")
	namespace := fs.String("namespace", "", "Module namespace")
	name := fs.String("name", "", "Module name")
	provider := fs.String("provider", "", "Provider name")
	ver := fs.String("version", "", "Module version (latest if omitted)")
	output := fs.String("output", ".", "Output directory")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *provider == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, and --provider are required")
		fs.Usage()
		os.Exit(1)
	}

	var downloadURL string
	if *ver != "" {
		downloadURL = fmt.Sprintf("%s/v1/modules/%s/%s/%s/%s/download", *registry, *namespace, *name, *provider, *ver)
	} else {
		downloadURL = fmt.Sprintf("%s/v1/modules/%s/%s/%s/download", *registry, *namespace, *name, *provider)
	}

	fmt.Printf("Downloading module %s/%s/%s...\n", *namespace, *name, *provider)

	resp, err := httpClient.Get(downloadURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error (%d): %s\n", resp.StatusCode, string(body))
		os.Exit(1)
	}

	outFile := filepath.Join(*output, fmt.Sprintf("%s-%s-%s.tar.gz", *namespace, *name, *provider))
	if *ver != "" {
		outFile = filepath.Join(*output, fmt.Sprintf("%s-%s-%s-%s.tar.gz", *namespace, *name, *provider, *ver))
	}

	artifactURL := resp.Header.Get("X-Terraform-Get")
	if artifactURL == "" {
		fmt.Fprintln(os.Stderr, "Error: registry response omitted X-Terraform-Get")
		os.Exit(1)
	}
	if err := downloadFile(artifactURL, outFile); err != nil {
		fmt.Fprintf(os.Stderr, "Error downloading module: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Saved to %s\n", outFile)
}

// --- LIST ---

func handleList(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tfreg list <providers|modules>")
		os.Exit(1)
	}

	registry := envOr("TFREG_REGISTRY", "http://localhost:8080")
	apiKey := envOr("TFREG_API_KEY", "")

	// Parse --registry and --api-key from remaining args
	cleanArgs := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--registry" && i+1 < len(args) {
			registry = args[i+1]
			i++
		} else if args[i] == "--api-key" && i+1 < len(args) {
			apiKey = args[i+1]
			i++
		} else {
			cleanArgs = append(cleanArgs, args[i])
		}
	}

	if len(cleanArgs) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tfreg list <providers|modules>")
		os.Exit(1)
	}

	var endpoint string
	switch cleanArgs[0] {
	case "providers":
		endpoint = "/api/v1/providers"
	case "modules":
		endpoint = "/api/v1/modules"
	default:
		fmt.Fprintf(os.Stderr, "Unknown type: %s\n", cleanArgs[0])
		os.Exit(1)
	}

	url := registry + endpoint
	req, err := http.NewRequest(http.MethodGet, url, nil) // #nosec G704 -- registry origin is intentionally supplied by the CLI user.
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid registry URL: %v\n", err)
		os.Exit(1)
	}
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}

	resp, err := httpClient.Do(req) // #nosec G704 -- this CLI is a user-directed registry client.
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	var apiResp struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %s\n", string(body))
		os.Exit(1)
	}

	if !apiResp.Success {
		fmt.Fprintf(os.Stderr, "Error: %s\n", string(body))
		os.Exit(1)
	}

	switch cleanArgs[0] {
	case "providers":
		var providers []struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Versions  []struct {
				Version string `json:"version"`
			} `json:"versions"`
		}
		_ = json.Unmarshal(apiResp.Data, &providers)
		if len(providers) == 0 {
			fmt.Println("No providers registered.")
			return
		}
		fmt.Printf("%-25s %-10s %s\n", "PROVIDER", "VERSIONS", "LATEST")
		fmt.Println(strings.Repeat("-", 60))
		for _, p := range providers {
			latest := "-"
			if len(p.Versions) > 0 {
				latest = p.Versions[len(p.Versions)-1].Version
			}
			fmt.Printf("%-25s %-10d %s\n", p.Namespace+"/"+p.Name, len(p.Versions), latest)
		}

	case "modules":
		var modules []struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
			Provider  string `json:"provider"`
			Versions  []struct {
				Version string `json:"version"`
			} `json:"versions"`
		}
		_ = json.Unmarshal(apiResp.Data, &modules)
		if len(modules) == 0 {
			fmt.Println("No modules registered.")
			return
		}
		fmt.Printf("%-35s %-10s %s\n", "MODULE", "VERSIONS", "LATEST")
		fmt.Println(strings.Repeat("-", 60))
		for _, m := range modules {
			latest := "-"
			if len(m.Versions) > 0 {
				latest = m.Versions[len(m.Versions)-1].Version
			}
			fmt.Printf("%-35s %-10d %s\n", m.Namespace+"/"+m.Name+"/"+m.Provider, len(m.Versions), latest)
		}
	}
}

// --- BUNDLE ---

func handleBundle(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tfreg bundle <provider|module> [options]")
		os.Exit(1)
	}

	switch args[0] {
	case "provider":
		bundleProvider(args[1:])
	case "module":
		bundleModule(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown type: %s\n", args[0])
		os.Exit(1)
	}
}

func bundleProvider(args []string) {
	fs := newFlagSet("tfreg bundle provider")
	namespace := fs.String("namespace", "", "Provider namespace")
	name := fs.String("name", "", "Provider name")
	ver := fs.String("version", "", "Provider version")
	binary := fs.String("binary", "", "Path to provider binary")
	osName := fs.String("os", "linux", "Operating system")
	arch := fs.String("arch", "amd64", "Architecture")
	output := fs.String("output", ".", "Output directory")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *ver == "" || *binary == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, --version, and --binary are required")
		fs.Usage()
		os.Exit(1)
	}

	if _, err := os.Stat(*binary); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: binary not found: %s\n", *binary)
		os.Exit(1)
	}

	filename := fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", *name, *ver, *osName, *arch)
	outPath := filepath.Join(*output, filename)

	fmt.Printf("Bundling provider %s/%s@%s (%s/%s)...\n", *namespace, *name, *ver, *osName, *arch)

	if err := createZip(*binary, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating zip: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created: %s\n", outPath)
}

func bundleModule(args []string) {
	fs := newFlagSet("tfreg bundle module")
	namespace := fs.String("namespace", "", "Module namespace")
	name := fs.String("name", "", "Module name")
	provider := fs.String("provider", "", "Provider name")
	ver := fs.String("version", "", "Module version")
	source := fs.String("source", "", "Source directory containing module files")
	output := fs.String("output", ".", "Output directory")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *provider == "" || *ver == "" || *source == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, --provider, --version, and --source are required")
		fs.Usage()
		os.Exit(1)
	}

	if _, err := os.Stat(*source); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: source directory not found: %s\n", *source)
		os.Exit(1)
	}

	outPath := filepath.Join(*output, fmt.Sprintf("%s-%s-%s-%s.tar.gz", *namespace, *name, *provider, *ver))

	fmt.Printf("Bundling module %s/%s/%s@%s...\n", *namespace, *name, *provider, *ver)

	if err := createTarGz(*source, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating tarball: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created: %s\n", outPath)
}

// --- DELETE ---

func handleDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: tfreg delete <provider|module> [options]")
		os.Exit(1)
	}

	switch args[0] {
	case "provider":
		deleteProvider(args[1:])
	case "module":
		deleteModule(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown type: %s\n", args[0])
		os.Exit(1)
	}
}

func deleteProvider(args []string) {
	fs := newFlagSet("tfreg delete provider")
	registry := fs.String("registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Registry URL")
	apiKey := fs.String("api-key", envOr("TFREG_API_KEY", ""), "API key")
	namespace := fs.String("namespace", "", "Provider namespace")
	name := fs.String("name", "", "Provider name")
	ver := fs.String("version", "", "Provider version")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *ver == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, and --version are required")
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/v1/providers/%s/%s/%s", *registry, *namespace, *name, *ver)
	fmt.Printf("Deleting provider %s/%s@%s...\n", *namespace, *name, *ver)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid registry URL: %v\n", err)
		os.Exit(1)
	}
	if *apiKey != "" {
		req.Header.Set("X-API-Key", *apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	printResponse(resp)
}

func deleteModule(args []string) {
	fs := newFlagSet("tfreg delete module")
	registry := fs.String("registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Registry URL")
	apiKey := fs.String("api-key", envOr("TFREG_API_KEY", ""), "API key")
	namespace := fs.String("namespace", "", "Module namespace")
	name := fs.String("name", "", "Module name")
	provider := fs.String("provider", "", "Provider name")
	ver := fs.String("version", "", "Module version")
	fs.Parse(args)

	if *namespace == "" || *name == "" || *provider == "" || *ver == "" {
		fmt.Fprintln(os.Stderr, "Error: --namespace, --name, --provider, and --version are required")
		os.Exit(1)
	}

	url := fmt.Sprintf("%s/api/v1/modules/%s/%s/%s/%s", *registry, *namespace, *name, *provider, *ver)
	fmt.Printf("Deleting module %s/%s/%s@%s...\n", *namespace, *name, *provider, *ver)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: invalid registry URL: %v\n", err)
		os.Exit(1)
	}
	if *apiKey != "" {
		req.Header.Set("X-API-Key", *apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }()

	printResponse(resp)
}

// --- Helpers ---

func uploadFile(url, filePath, apiKey string) (*http.Response, error) {
	file, err := os.Open(filePath) // #nosec G304 -- upload path is explicitly selected by the CLI user.
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	contentType := writer.FormDataContentType()
	go func() {
		defer func() { _ = file.Close() }()
		part, err := writer.CreateFormFile("file", filepath.Base(filePath))
		if err == nil {
			_, err = io.Copy(part, file)
		}
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		_ = pw.CloseWithError(err)
	}()

	req, err := http.NewRequest(http.MethodPost, url, pr)
	if err != nil {
		_ = pr.Close()
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	return httpClient.Do(req)
}

func downloadFile(url, destPath string) error {
	return downloadFileWithChecksum(url, destPath, "")
}

func downloadFileVerified(url, destPath, expectedSHA256 string) error {
	if len(expectedSHA256) != 64 {
		return fmt.Errorf("invalid expected SHA256 checksum")
	}
	return downloadFileWithChecksum(url, destPath, expectedSHA256)
}

func downloadFileWithChecksum(url, destPath, expectedSHA256 string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(destPath), ".tfreg-download-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	var writer io.Writer = f
	var hashWriter hash.Hash
	if expectedSHA256 != "" {
		hashWriter = sha256.New()
		writer = io.MultiWriter(f, hashWriter)
	}
	if _, err := io.Copy(writer, resp.Body); err != nil {
		return err
	}
	if hashWriter != nil {
		actual := fmt.Sprintf("%x", hashWriter.Sum(nil))
		if !strings.EqualFold(actual, expectedSHA256) {
			return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expectedSHA256, actual)
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, destPath); err != nil {
		return err
	}
	ok = true
	return nil
}

func printResponse(resp *http.Response) {
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "Error (%d): %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		os.Exit(1)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Println(string(body))
		return
	}

	if msg, ok := result["message"].(string); ok {
		if success, ok := result["success"].(bool); ok && success {
			fmt.Printf("✓ %s\n", msg)
		} else {
			fmt.Fprintf(os.Stderr, "✗ %s\n", msg)
			os.Exit(1)
		}
	} else {
		fmt.Println(string(body))
	}
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func newFlagSet(name string) *flagSet {
	return &flagSet{name: name}
}

// Simple flag set wrapper
type flagSet struct {
	name  string
	flags []flag
	rest  []string
}

type flag struct {
	name  string
	value *string
	def   string
	desc  string
}

func (fs *flagSet) String(name, def, desc string) *string {
	v := new(string)
	*v = def
	fs.flags = append(fs.flags, flag{name: name, value: v, def: def, desc: desc})
	return v
}

func (fs *flagSet) Parse(args []string) {
	remaining := []string{}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			key := strings.TrimPrefix(args[i], "--")
			if key == "help" {
				fs.Usage()
				os.Exit(0)
			}
			found := false
			for j := range fs.flags {
				if fs.flags[j].name == key {
					found = true
					if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
						fmt.Fprintf(os.Stderr, "Error: --%s requires a value\n", key)
						fs.Usage()
						os.Exit(2)
					}
					*fs.flags[j].value = args[i+1]
					i++
					break
				}
			}
			if !found {
				fmt.Fprintf(os.Stderr, "Error: unknown option --%s\n", key)
				fs.Usage()
				os.Exit(2)
			}
		} else {
			remaining = append(remaining, args[i])
		}
	}
	if len(remaining) > 0 {
		fmt.Fprintf(os.Stderr, "Error: unexpected argument %q\n", remaining[0])
		fs.Usage()
		os.Exit(2)
	}
	fs.rest = remaining
}

func (fs *flagSet) Usage() {
	fmt.Fprintf(os.Stderr, "\nUsage: %s [options]\n\nOptions:\n", fs.name)
	for _, f := range fs.flags {
		fmt.Fprintf(os.Stderr, "  --%-12s %s (default: %s)\n", f.name, f.desc, f.def)
	}
	fmt.Fprintln(os.Stderr)
}
