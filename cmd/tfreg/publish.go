package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type publishModuleOptions struct {
	Registry, APIKey, Namespace, Name, Provider, Version, Source string
	OCIReference, OCIUsername, OCIPassword                       string
	OCIPlainHTTP                                                 bool
}

type publishProviderOptions struct {
	Registry, APIKey, Namespace, Name, Version, Source string
	OS, Arch                                           string
	SkipTests                                          bool
	OCIReference, OCIUsername, OCIPassword             string
	OCIPlainHTTP                                       bool
}

func handlePublish(args []string) {
	if len(args) < 1 {
		fatalf("Usage: tfreg publish <provider|module> [options]")
	}
	switch args[0] {
	case "provider":
		publishProviderCommand(args[1:])
	case "module":
		publishModuleCommand(args[1:])
	default:
		fatalf("Unknown type: %s (use 'provider' or 'module')", args[0])
	}
}

func publishProviderCommand(args []string) {
	fs := newFlagSet("tfreg publish provider")
	opts := publishProviderOptions{}
	fs.StringVar(&opts.Registry, "registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Terraform registry URL")
	fs.StringVar(&opts.APIKey, "api-key", envOr("TFREG_API_KEY", ""), "Terraform registry API key")
	fs.StringVar(&opts.Namespace, "namespace", "", "Provider namespace")
	fs.StringVar(&opts.Name, "name", "", "Provider name")
	fs.StringVar(&opts.Version, "version", "", "Provider version")
	fs.StringVar(&opts.Source, "source", ".", "Provider Go source directory")
	fs.StringVar(&opts.OS, "os", "linux", "Target operating system")
	fs.StringVar(&opts.Arch, "arch", "amd64", "Target architecture")
	fs.BoolVar(&opts.SkipTests, "skip-tests", false, "Skip go test before building")
	fs.StringVar(&opts.OCIReference, "oci-ref", "", "Also push as OCI artifact (full repository:tag)")
	fs.StringVar(&opts.OCIUsername, "oci-username", envOr("TFREG_OCI_USERNAME", ""), "OCI username (use AWS for ECR)")
	passwordStdin := fs.Bool("oci-password-stdin", false, "Read OCI password/token from stdin")
	fs.BoolVar(&opts.OCIPlainHTTP, "oci-plain-http", false, "Use HTTP for a local OCI registry")
	fs.Parse(args)
	if opts.Namespace == "" || opts.Name == "" || opts.Version == "" {
		fatalf("--namespace, --name, and --version are required")
	}
	if *passwordStdin {
		opts.OCIPassword = readPasswordStdin()
	}
	if err := publishProviderArtifact(context.Background(), opts); err != nil {
		fatalf("publish provider: %v", err)
	}
	fmt.Printf("Published provider %s/%s@%s (%s/%s)\n", opts.Namespace, opts.Name, opts.Version, opts.OS, opts.Arch)
}

func publishModuleCommand(args []string) {
	fs := newFlagSet("tfreg publish module")
	opts := publishModuleOptions{}
	fs.StringVar(&opts.Registry, "registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Terraform registry URL")
	fs.StringVar(&opts.APIKey, "api-key", envOr("TFREG_API_KEY", ""), "Terraform registry API key")
	fs.StringVar(&opts.Namespace, "namespace", "", "Module namespace")
	fs.StringVar(&opts.Name, "name", "", "Module name")
	fs.StringVar(&opts.Provider, "provider", "", "Primary provider name")
	fs.StringVar(&opts.Version, "version", "", "Module version")
	fs.StringVar(&opts.Source, "source", ".", "Terraform module source directory")
	fs.StringVar(&opts.OCIReference, "oci-ref", "", "Also push as OCI artifact (full repository:tag)")
	fs.StringVar(&opts.OCIUsername, "oci-username", envOr("TFREG_OCI_USERNAME", ""), "OCI username (use AWS for ECR)")
	passwordStdin := fs.Bool("oci-password-stdin", false, "Read OCI password/token from stdin")
	fs.BoolVar(&opts.OCIPlainHTTP, "oci-plain-http", false, "Use HTTP for a local OCI registry")
	fs.Parse(args)
	if opts.Namespace == "" || opts.Name == "" || opts.Provider == "" || opts.Version == "" {
		fatalf("--namespace, --name, --provider, and --version are required")
	}
	if *passwordStdin {
		opts.OCIPassword = readPasswordStdin()
	}
	if err := publishModuleArtifact(context.Background(), opts); err != nil {
		fatalf("publish module: %v", err)
	}
	fmt.Printf("Published module %s/%s/%s@%s\n", opts.Namespace, opts.Name, opts.Provider, opts.Version)
}

func publishProviderArtifact(ctx context.Context, opts publishProviderOptions) error {
	tmp, err := os.MkdirTemp("", "tfreg-provider-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	binaryName := fmt.Sprintf("terraform-provider-%s_v%s", opts.Name, opts.Version)
	binaryPath := filepath.Join(tmp, binaryName)
	if err := buildProviderSource(ctx, opts.Source, binaryPath, opts.OS, opts.Arch, opts.SkipTests); err != nil {
		return err
	}
	bundleName := fmt.Sprintf("terraform-provider-%s_%s_%s_%s.zip", opts.Name, opts.Version, opts.OS, opts.Arch)
	bundlePath := filepath.Join(tmp, bundleName)
	if err := createZip(binaryPath, bundlePath); err != nil {
		return fmt.Errorf("bundle provider: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/providers/%s/%s/%s/%s/%s", strings.TrimRight(opts.Registry, "/"), opts.Namespace, opts.Name, opts.Version, opts.OS, opts.Arch)
	if err := uploadArtifact(url, bundlePath, opts.APIKey); err != nil {
		return err
	}
	if opts.OCIReference != "" {
		_, err = pushOCIArtifact(ctx, ociPushOptions{Reference: opts.OCIReference, Artifact: bundlePath, Kind: "provider", Username: opts.OCIUsername, Password: opts.OCIPassword, PlainHTTP: opts.OCIPlainHTTP, Annotations: artifactAnnotations("provider", opts.Namespace, opts.Name, "", opts.Version, opts.OS, opts.Arch, bundleName)})
		if err != nil {
			return fmt.Errorf("terraform registry upload succeeded, but OCI push failed: %w", err)
		}
	}
	return nil
}

func publishModuleArtifact(ctx context.Context, opts publishModuleOptions) error {
	tmp, err := os.MkdirTemp("", "tfreg-module-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	bundleName := fmt.Sprintf("%s-%s-%s-%s.tar.gz", opts.Namespace, opts.Name, opts.Provider, opts.Version)
	bundlePath := filepath.Join(tmp, bundleName)
	if err := createTarGz(opts.Source, bundlePath); err != nil {
		return fmt.Errorf("bundle module: %w", err)
	}
	url := fmt.Sprintf("%s/api/v1/modules/%s/%s/%s/%s", strings.TrimRight(opts.Registry, "/"), opts.Namespace, opts.Name, opts.Provider, opts.Version)
	if err := uploadArtifact(url, bundlePath, opts.APIKey); err != nil {
		return err
	}
	if opts.OCIReference != "" {
		_, err = pushOCIArtifact(ctx, ociPushOptions{Reference: opts.OCIReference, Artifact: bundlePath, Kind: "module", Username: opts.OCIUsername, Password: opts.OCIPassword, PlainHTTP: opts.OCIPlainHTTP, Annotations: artifactAnnotations("module", opts.Namespace, opts.Name, opts.Provider, opts.Version, "", "", bundleName)})
		if err != nil {
			return fmt.Errorf("terraform registry upload succeeded, but OCI push failed: %w", err)
		}
	}
	return nil
}

func buildProviderSource(ctx context.Context, source, output, osName, arch string, skipTests bool) error {
	if !skipTests {
		cmd := exec.CommandContext(ctx, "go", "test", "./...") // #nosec G204 -- fixed executable and arguments.
		cmd.Dir = source
		cmd.Env = filteredEnvironment("GOOS", "GOARCH", "CGO_ENABLED")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("provider tests failed: %w", err)
		}
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", output, ".") // #nosec G204 -- fixed executable and arguments.
	cmd.Dir = source
	cmd.Env = append(filteredEnvironment("GOOS", "GOARCH", "CGO_ENABLED"), "GOOS="+osName, "GOARCH="+arch, "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build provider: %w", err)
	}
	return nil
}

func filteredEnvironment(keys ...string) []string {
	blocked := make(map[string]bool, len(keys))
	for _, key := range keys {
		blocked[key] = true
	}
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			result = append(result, entry)
		}
	}
	return result
}

func uploadArtifact(url, path, apiKey string) error {
	resp, err := uploadFile(url, path, apiKey)
	if err != nil {
		return fmt.Errorf("upload artifact: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read upload response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("registry returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func artifactAnnotations(kind, namespace, name, provider, version, osName, arch, title string) map[string]string {
	values := map[string]string{
		"org.opencontainers.image.title": title,
		"io.terraform.kind":              kind,
		"io.terraform.namespace":         namespace,
		"io.terraform.name":              name,
		"io.terraform.version":           version,
	}
	if provider != "" {
		values["io.terraform.provider"] = provider
	}
	if osName != "" {
		values["io.terraform.os"] = osName
	}
	if arch != "" {
		values["io.terraform.arch"] = arch
	}
	return values
}

func readPasswordStdin() string {
	value, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<10))
	if err != nil {
		fatalf("read OCI password: %v", err)
	}
	password := strings.TrimSpace(string(value))
	if password == "" {
		fatalf("OCI password from stdin is empty")
	}
	return password
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
