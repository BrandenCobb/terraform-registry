package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
)

const (
	terraformOCIArtifactType   = "application/vnd.terraform.registry.artifact.v1"
	terraformProviderLayerType = "application/vnd.terraform.provider.package.v1+zip"
	terraformModuleLayerType   = "application/vnd.terraform.module.package.v1+tar+gzip"
)

type ociPushOptions struct {
	Reference   string
	Artifact    string
	Kind        string
	Annotations map[string]string
	Username    string
	Password    string
	PlainHTTP   bool
}

func splitOCIReference(value string) (string, string, error) {
	ref, err := registry.ParseReference(strings.TrimSpace(value))
	if err != nil {
		return "", "", fmt.Errorf("invalid OCI reference: %w", err)
	}
	if err := ref.ValidateReferenceAsTag(); err != nil || ref.Reference == "" {
		return "", "", fmt.Errorf("OCI reference must include an explicit tag")
	}
	return ref.Registry + "/" + ref.Repository, ref.Reference, nil
}

func packOCIArtifact(ctx context.Context, target oras.Target, artifactPath, kind string, annotations map[string]string) (ocispec.Descriptor, error) {
	mediaType := terraformModuleLayerType
	if kind == "provider" {
		mediaType = terraformProviderLayerType
	} else if kind != "module" {
		return ocispec.Descriptor{}, fmt.Errorf("unsupported Terraform artifact kind %q", kind)
	}
	file, err := os.Open(artifactPath) // #nosec G304 -- artifact path is explicitly selected by the CLI user.
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("stat artifact: %w", err)
	}
	artifactDigest, err := digest.FromReader(file)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("hash artifact: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("rewind artifact: %w", err)
	}
	layer := ocispec.Descriptor{
		MediaType: mediaType,
		Digest:    artifactDigest,
		Size:      info.Size(),
		Annotations: map[string]string{
			ocispec.AnnotationTitle: info.Name(),
		},
	}
	if err := target.Push(ctx, layer, file); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("stage OCI layer: %w", err)
	}
	manifestAnnotations := make(map[string]string, len(annotations)+1)
	for key, value := range annotations {
		manifestAnnotations[key] = value
	}
	if _, ok := manifestAnnotations[ocispec.AnnotationCreated]; !ok {
		manifestAnnotations[ocispec.AnnotationCreated] = time.Now().UTC().Format(time.RFC3339)
	}
	return oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, terraformOCIArtifactType, oras.PackManifestOptions{
		Layers:              []ocispec.Descriptor{layer},
		ManifestAnnotations: manifestAnnotations,
	})
}

func pushOCIArtifact(ctx context.Context, opts ociPushOptions) (ocispec.Descriptor, error) {
	repository, tag, err := splitOCIReference(opts.Reference)
	if err != nil {
		return ocispec.Descriptor{}, err
	}
	repo, err := remote.NewRepository(repository)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("open OCI repository: %w", err)
	}
	repo.PlainHTTP = opts.PlainHTTP
	credentialFunc := auth.CredentialFunc(nil)
	if opts.Username != "" || opts.Password != "" {
		credentialFunc = auth.StaticCredential(repo.Reference.Registry, auth.Credential{
			Username: opts.Username,
			Password: opts.Password,
		})
	} else {
		credentialStore, storeErr := credentials.NewStoreFromDocker(credentials.StoreOptions{})
		if storeErr != nil {
			return ocispec.Descriptor{}, fmt.Errorf("open Docker credential store: %w", storeErr)
		}
		credentialFunc = credentials.Credential(credentialStore)
	}
	repo.Client = &auth.Client{
		Client:     retry.DefaultClient,
		Cache:      auth.NewCache(),
		Credential: credentialFunc,
	}
	manifest, err := packOCIArtifact(ctx, repo, opts.Artifact, opts.Kind, opts.Annotations)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("push OCI content: %w", err)
	}
	if err := repo.Tag(ctx, manifest, tag); err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("tag OCI artifact: %w", err)
	}
	return manifest, nil
}
