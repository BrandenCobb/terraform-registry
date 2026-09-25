package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"
)

const maxOCIManifestBytes = 4 << 20

var importArtifactNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func splitOCIImportReference(value string) (string, string, error) {
	ref, err := registry.ParseReference(strings.TrimSpace(value))
	if err != nil {
		return "", "", fmt.Errorf("invalid OCI reference: %w", err)
	}
	if ref.Reference == "" {
		return "", "", fmt.Errorf("OCI reference must include an explicit tag or digest")
	}
	if tagErr := ref.ValidateReferenceAsTag(); tagErr != nil {
		if digestErr := ref.ValidateReferenceAsDigest(); digestErr != nil {
			return "", "", fmt.Errorf("OCI reference must include a valid tag or digest")
		}
	}
	return ref.Registry + "/" + ref.Repository, ref.Reference, nil
}

type ociArtifactMetadata struct {
	Kind, Namespace, Name, Provider, Version, OS, Arch string
}

type ociImportOptions struct {
	Registry, APIKey, OCIReference, OCIUsername, OCIPassword string
	OCIPlainHTTP                                             bool
	MaxBytes                                                 int64
}

func handleImport(args []string) {
	fs := newFlagSet("tfreg import")
	opts := ociImportOptions{}
	fs.StringVar(&opts.Registry, "registry", envOr("TFREG_REGISTRY", "http://localhost:8080"), "Terraform registry URL")
	fs.StringVar(&opts.APIKey, "api-key", envOr("TFREG_API_KEY", ""), "Terraform registry API key")
	fs.StringVar(&opts.OCIReference, "oci-ref", "", "OCI/ECR artifact reference (full repository:tag or repository@digest)")
	fs.StringVar(&opts.OCIUsername, "oci-username", envOr("TFREG_OCI_USERNAME", ""), "OCI username (use AWS for ECR)")
	passwordStdin := fs.Bool("oci-password-stdin", false, "Read OCI password/token from stdin")
	fs.BoolVar(&opts.OCIPlainHTTP, "oci-plain-http", false, "Use HTTP for a local OCI registry")
	maxDownloadMB := fs.String("max-download-mb", "500", "Maximum OCI package size in MiB")
	fs.Parse(args)
	if opts.OCIReference == "" {
		fatalf("--oci-ref is required")
	}
	parsedMax, err := strconv.ParseInt(*maxDownloadMB, 10, 64)
	if err != nil || parsedMax <= 0 || parsedMax > 10240 {
		fatalf("--max-download-mb must be between 1 and 10240")
	}
	opts.MaxBytes = parsedMax << 20
	if *passwordStdin {
		opts.OCIPassword = readPasswordStdin()
	}
	metadata, err := importOCIArtifact(context.Background(), opts)
	if err != nil {
		fatalf("import OCI artifact: %v", err)
	}
	if metadata.Kind == "provider" {
		fmt.Printf("Imported provider %s/%s@%s (%s/%s)\n", metadata.Namespace, metadata.Name, metadata.Version, metadata.OS, metadata.Arch)
	} else {
		fmt.Printf("Imported module %s/%s/%s@%s\n", metadata.Namespace, metadata.Name, metadata.Provider, metadata.Version)
	}
}

func importOCIArtifact(ctx context.Context, opts ociImportOptions) (ociArtifactMetadata, error) {
	repository, tag, err := splitOCIImportReference(opts.OCIReference)
	if err != nil {
		return ociArtifactMetadata{}, err
	}
	repo, err := openOCIRepository(repository, opts.OCIUsername, opts.OCIPassword, opts.OCIPlainHTTP)
	if err != nil {
		return ociArtifactMetadata{}, err
	}
	manifest, err := repo.Resolve(ctx, tag)
	if err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("resolve OCI artifact: %w", err)
	}
	return importOCIArtifactFromTarget(ctx, repo, manifest, opts.Registry, opts.APIKey, opts.MaxBytes)
}

func unpackOCIArtifact(ctx context.Context, source oras.ReadOnlyTarget, manifestDescriptor ocispec.Descriptor, output io.Writer, maxBytes int64) (ociArtifactMetadata, error) {
	if manifestDescriptor.MediaType != ocispec.MediaTypeImageManifest {
		return ociArtifactMetadata{}, fmt.Errorf("unsupported OCI manifest media type %q", manifestDescriptor.MediaType)
	}
	manifestBody, err := fetchDescriptor(ctx, source, manifestDescriptor, maxOCIManifestBytes)
	if err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("fetch OCI manifest: %w", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBody, &manifest); err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("decode OCI manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 {
		return ociArtifactMetadata{}, fmt.Errorf("unsupported OCI schema version %d", manifest.SchemaVersion)
	}
	if manifest.ArtifactType != terraformOCIArtifactType {
		return ociArtifactMetadata{}, fmt.Errorf("unsupported OCI artifact type %q", manifest.ArtifactType)
	}
	if len(manifest.Layers) != 1 {
		return ociArtifactMetadata{}, fmt.Errorf("Terraform OCI artifact must contain exactly one layer")
	}

	metadata, err := metadataFromOCIManifest(manifest)
	if err != nil {
		return ociArtifactMetadata{}, err
	}
	layer := manifest.Layers[0]
	if err := layer.Digest.Validate(); err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("invalid OCI artifact digest: %w", err)
	}
	if layer.Size < 0 || layer.Size > maxBytes {
		return ociArtifactMetadata{}, fmt.Errorf("OCI artifact size %d exceeds limit %d", layer.Size, maxBytes)
	}
	reader, err := source.Fetch(ctx, layer)
	if err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("fetch OCI artifact layer: %w", err)
	}
	defer reader.Close()
	verifier := layer.Digest.Verifier()
	written, err := io.Copy(io.MultiWriter(output, verifier), io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("download OCI artifact layer: %w", err)
	}
	if written > maxBytes {
		return ociArtifactMetadata{}, fmt.Errorf("OCI artifact exceeds limit %d", maxBytes)
	}
	if written != layer.Size {
		return ociArtifactMetadata{}, fmt.Errorf("OCI artifact size mismatch: expected %d, received %d", layer.Size, written)
	}
	if !verifier.Verified() {
		return ociArtifactMetadata{}, fmt.Errorf("OCI artifact digest verification failed")
	}
	return metadata, nil
}

func fetchDescriptor(ctx context.Context, source oras.ReadOnlyTarget, descriptor ocispec.Descriptor, maxBytes int64) ([]byte, error) {
	if err := descriptor.Digest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid descriptor digest: %w", err)
	}
	if descriptor.Size < 0 || descriptor.Size > maxBytes {
		return nil, fmt.Errorf("descriptor size %d exceeds limit %d", descriptor.Size, maxBytes)
	}
	reader, err := source.Fetch(ctx, descriptor)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes || int64(len(body)) != descriptor.Size {
		return nil, fmt.Errorf("descriptor size mismatch or limit exceeded")
	}
	verifier := descriptor.Digest.Verifier()
	if _, err := verifier.Write(body); err != nil {
		return nil, err
	}
	if !verifier.Verified() {
		return nil, fmt.Errorf("descriptor digest verification failed")
	}
	return body, nil
}

func metadataFromOCIManifest(manifest ocispec.Manifest) (ociArtifactMetadata, error) {
	annotations := manifest.Annotations
	metadata := ociArtifactMetadata{
		Kind:      annotations["io.terraform.kind"],
		Namespace: annotations["io.terraform.namespace"],
		Name:      annotations["io.terraform.name"],
		Provider:  annotations["io.terraform.provider"],
		Version:   annotations["io.terraform.version"],
		OS:        annotations["io.terraform.os"],
		Arch:      annotations["io.terraform.arch"],
	}
	if len(manifest.Layers) != 1 {
		return ociArtifactMetadata{}, fmt.Errorf("Terraform OCI artifact must contain exactly one layer")
	}
	switch metadata.Kind {
	case "provider":
		if manifest.Layers[0].MediaType != terraformProviderLayerType {
			return ociArtifactMetadata{}, fmt.Errorf("provider OCI artifact has layer media type %q", manifest.Layers[0].MediaType)
		}
		if !validImportName(metadata.Namespace) || !validImportName(metadata.Name) || !validImportName(metadata.OS) || !validImportName(metadata.Arch) || !validImportVersion(metadata.Version) {
			return ociArtifactMetadata{}, fmt.Errorf("provider OCI artifact has missing or invalid identity annotations")
		}
	case "module":
		if manifest.Layers[0].MediaType != terraformModuleLayerType {
			return ociArtifactMetadata{}, fmt.Errorf("module OCI artifact has layer media type %q", manifest.Layers[0].MediaType)
		}
		if !validImportName(metadata.Namespace) || !validImportName(metadata.Name) || !validImportName(metadata.Provider) || !validImportVersion(metadata.Version) {
			return ociArtifactMetadata{}, fmt.Errorf("module OCI artifact has missing or invalid identity annotations")
		}
	default:
		return ociArtifactMetadata{}, fmt.Errorf("unsupported Terraform artifact kind %q", metadata.Kind)
	}
	return metadata, nil
}

func validImportName(value string) bool { return importArtifactNameRE.MatchString(value) }

// validImportVersion mirrors the server's strict SemVer parser so invalid OCI
// identity metadata is rejected before the package layer is downloaded.
func validImportVersion(value string) bool {
	if value == "" || strings.HasPrefix(value, "v") {
		return false
	}
	versionAndPre, build, hasBuild := strings.Cut(value, "+")
	if hasBuild && (strings.Contains(build, "+") || !validImportIdentifiers(build, false)) {
		return false
	}
	core, prerelease, hasPrerelease := strings.Cut(versionAndPre, "-")
	if hasPrerelease && !validImportIdentifiers(prerelease, true) {
		return false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

func validImportIdentifiers(value string, enforceNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		allNumeric := true
		for _, char := range identifier {
			if (char < '0' || char > '9') && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && char != '-' {
				return false
			}
			if char < '0' || char > '9' {
				allNumeric = false
			}
		}
		if enforceNumericLeadingZero && allNumeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func importUploadURL(registryURL string, metadata ociArtifactMetadata) (string, error) {
	base, err := url.Parse(strings.TrimRight(registryURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", fmt.Errorf("invalid Terraform registry URL")
	}
	var segments []string
	switch metadata.Kind {
	case "provider":
		if !validImportName(metadata.Namespace) || !validImportName(metadata.Name) || !validImportName(metadata.OS) || !validImportName(metadata.Arch) || !validImportVersion(metadata.Version) {
			return "", fmt.Errorf("provider identity is missing or invalid")
		}
		segments = []string{"api", "v1", "providers", metadata.Namespace, metadata.Name, metadata.Version, metadata.OS, metadata.Arch}
	case "module":
		if !validImportName(metadata.Namespace) || !validImportName(metadata.Name) || !validImportName(metadata.Provider) || !validImportVersion(metadata.Version) {
			return "", fmt.Errorf("module identity is missing or invalid")
		}
		segments = []string{"api", "v1", "modules", metadata.Namespace, metadata.Name, metadata.Provider, metadata.Version}
	default:
		return "", fmt.Errorf("unsupported Terraform artifact kind %q", metadata.Kind)
	}
	escaped := make([]string, len(segments))
	for index, segment := range segments {
		escaped[index] = url.PathEscape(segment)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.Join(escaped, "/")
	return base.String(), nil
}

func importOCIArtifactFromTarget(ctx context.Context, source oras.ReadOnlyTarget, manifestDescriptor ocispec.Descriptor, registryURL, apiKey string, maxBytes int64) (ociArtifactMetadata, error) {
	temporaryDirectory, err := os.MkdirTemp("", "tfreg-oci-import-*")
	if err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("create OCI import workspace: %w", err)
	}
	defer os.RemoveAll(temporaryDirectory)
	artifactFile, err := os.CreateTemp(temporaryDirectory, "artifact-*")
	if err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("create temporary artifact: %w", err)
	}
	artifactPath := artifactFile.Name()
	metadata, err := unpackOCIArtifact(ctx, source, manifestDescriptor, artifactFile, maxBytes)
	if err != nil {
		_ = artifactFile.Close()
		return ociArtifactMetadata{}, err
	}
	if err := artifactFile.Close(); err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("close temporary artifact: %w", err)
	}
	uploadURL, err := importUploadURL(registryURL, metadata)
	if err != nil {
		return ociArtifactMetadata{}, err
	}
	if err := uploadArtifact(uploadURL, artifactPath, apiKey); err != nil {
		return ociArtifactMetadata{}, fmt.Errorf("upload imported OCI artifact: %w", err)
	}
	return metadata, nil
}
