# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.1.0] - 2026-08-09

### Added
- Strict SemVer ordering, streaming upload/download paths, archive validation, provider checksum signing, security headers, Prometheus metrics, and hardened management authentication.
- Production-ready `tfreg` push, pull, bundle, list, deprecate, delete, and garbage-collection workflows.
- Maintained single-replica Docker Compose and Kubernetes deployment examples.

### Changed
- Standardized on filesystem-only storage with one server replica per persistent volume.
- Management mutations now require hashed API-key authorization; query-string credentials are no longer accepted.
- Provider and module artifacts are published atomically under immutable content-addressed names.
- Docker, CI, and release builds use Go 1.26.5 and non-root containers.

### Fixed
- Corrected Terraform provider and module protocol response shapes, status codes, prerelease ordering, deprecation filtering, and provider binary signatures.
- Confined storage operations against traversal and symlink escapes and bounded both compressed and expanded upload sizes.
- Made CLI downloads checksum-verified and atomically replace destination files.
- Replaced webhook signature construction with HMAC-SHA256 and made proxy-header trust opt-in.

## [1.0.0] - 2026-05-01

### Added
- **Core Features**
  - Unified registry server supporting both Terraform providers and modules
  - Full Terraform Registry Protocol v1 implementation
  - Pluggable storage backends: filesystem and S3
  - Health check endpoint (`/health`)
  - Protocol discovery endpoint (`/.well-known/terraform.json`)

- **Provider Support**
  - Provider versions listing (`/v1/providers/{namespace}/{type}/versions`)
  - Provider download metadata with presigned URLs
  - Multi-platform support (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64)
  - Automatic platform detection and metadata generation

- **Module Support**
  - Module versions listing (`/v1/modules/{namespace}/{name}/{provider}/versions`)
  - Module download with redirect support
  - Latest version endpoint for modules
  - Tar.gz module archive support

- **Storage Backends**
  - Filesystem storage with volume mount support
  - S3 storage with presigned download URLs
  - IRSA (IAM Roles for Service Accounts) support for keyless AWS authentication
  - Automatic index.json generation and updates

- **Deployment**
  - Docker containerization (single ~20MB image)
  - Docker Compose configuration with examples
  - Kubernetes manifests (Deployment, Service, Ingress)
  - Istio VirtualService examples
  - Multi-platform Docker images (amd64, arm64)

- **Scripts & Tools**
  - Provider upload script (`scripts/upload-provider.sh`)
  - Module upload script (`scripts/upload-module.sh`)
  - S3 bucket initialization script (`scripts/init-s3-bucket.sh`)
  - Comprehensive Makefile with 20+ targets

- **Documentation**
  - Professional README with quick start guide
  - Quick Start Guide (`docs/QUICKSTART.md`)
  - Deployment Guide (`docs/DEPLOYMENT.md`)
  - Provider Build Guide (`docs/PROVIDER_BUILD.md`)
  - Contributing guidelines (`CONTRIBUTING.md`)
  - Security policy (`SECURITY.md`)
  - Code of Conduct (`CODE_OF_CONDUCT.md`)

- **Testing**
  - 30 unit tests with 72% code coverage
  - 10 integration tests with end-to-end HTTP testing
  - Test coverage reporting (`make test-coverage`)
  - Integration test suite (`tests/integration/run-tests.sh`)

- **CI/CD**
  - GitHub Actions workflow for CI (lint, test, build)
  - GitHub Actions workflow for releases (multi-platform builds)
  - Automated Docker image building and publishing
  - Container image pushed to ghcr.io
  - Dependabot configuration for automated dependency updates
  - Issue templates (bug report, feature request)
  - Pull request template

### Security
- Non-root container execution (runs as `nobody` user, UID 65534)
- Read-only root filesystem support
- No embedded credentials or secrets
- IRSA support for keyless AWS authentication
- S3 bucket encryption and versioning support
- Public access blocking on S3 buckets
- SHA256 checksums for all provider binaries
- Security vulnerability scanning with govulncheck

### Technical Details
- Written in Go 1.26
- Uses Gorilla Mux for routing
- AWS SDK v2 for S3 operations
- Minimal dependencies for security
- CGO disabled for static binaries
- Multi-stage Docker builds for small images

[Unreleased]: https://github.com/BrandenCobb/terraform-registry/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/BrandenCobb/terraform-registry/releases/tag/v1.0.0
