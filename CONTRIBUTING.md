# Contributing to Terraform Registry

Thank you for your interest in contributing to Terraform Registry! This document provides guidelines and instructions for contributing.

## Code of Conduct

This project adheres to a code of conduct. By participating, you are expected to uphold this code. Please report unacceptable behavior to the project maintainers.

## How Can I Contribute?

### Reporting Bugs

Before creating bug reports, please check the [issue tracker](https://github.com/BrandenCobb/terraform-registry/issues) to avoid duplicates. When you create a bug report, include as many details as possible:

- **Use a clear and descriptive title**
- **Describe the exact steps to reproduce the problem**
- **Provide specific examples** (config files, commands, etc.)
- **Describe the behavior you observed and expected**
- **Include logs and error messages**
- **Note your environment** (OS, Docker version, Kubernetes version, etc.)

### Suggesting Enhancements

Enhancement suggestions are tracked as GitHub issues. When creating an enhancement suggestion, include:

- **Use a clear and descriptive title**
- **Provide a detailed description of the suggested enhancement**
- **Explain why this enhancement would be useful**
- **List examples of how it would be used**

### Pull Requests

1. Fork the repository and create your branch from `main`
2. Make your changes and add tests if applicable
3. Ensure the test suite passes
4. Make sure your code follows the existing style
5. Write clear, concise commit messages
6. Open a pull request with a clear title and description

## Development Setup

### Prerequisites

- Go 1.26 or higher
- Docker
- Make
- kubectl (for Kubernetes testing)

### Local Development

```bash
# Clone your fork
git clone https://github.com/BrandenCobb/terraform-registry.git
cd terraform-registry

# Install dependencies
cd registry-server
go mod download

# Build
go build -o terraform-registry main.go storage.go

# Run tests
go test -v ./...

# Run locally
export STORAGE_TYPE=filesystem
export STORAGE_PATH=/tmp/terraform-registry
./terraform-registry
```

### Running with Docker

```bash
# Build Docker image
docker build -t terraform-registry:dev -f registry-server/Dockerfile registry-server/

# Run
docker run -d -p 5000:8080 \
  -v $(pwd)/test-data:/var/lib/terraform-registry \
  terraform-registry:dev
```

## Project Structure

```
terraform-registry/
├── registry-server/      # Go server implementation
│   ├── main.go           # Main server with unified protocol
│   ├── storage.go        # Storage abstraction layer
│   ├── Dockerfile        # Production Dockerfile
│   └── go.mod            # Go dependencies
├── scripts/              # Upload and management scripts
│   ├── upload-provider.sh
│   ├── upload-module.sh
│   └── init-s3-bucket.sh
├── examples/kubernetes/manifests/               # Kubernetes manifests
│   ├── deployment.yaml
│   ├── service.yaml
│   └── irsa/            # IRSA configuration
├── docs/                # Documentation
├── examples/            # Example configurations
├── tests/              # Integration tests
└── .github/            # GitHub Actions workflows
```

## Coding Standards

### Go Code

- Follow standard Go formatting (`gofmt`)
- Use meaningful variable and function names
- Add comments for exported functions and complex logic
- Keep functions focused and concise
- Handle errors explicitly
- Write unit tests for new functionality

### Shell Scripts

- Use `#!/bin/bash` shebang
- Add `set -e` for error handling
- Include usage documentation
- Use meaningful variable names
- Add comments for complex logic

### Docker

- Use multi-stage builds
- Run as non-root user
- Include health checks
- Minimize layers
- Use specific base image tags

## Testing

### Unit Tests

```bash
cd registry-server
go test -v ./...
```

### Integration Tests

```bash
# Start registry
docker-compose up -d

# Run integration tests
./tests/integration/run-tests.sh

# Cleanup
docker-compose down
```

### Manual Testing

```bash
# Upload a test provider
./scripts/upload-provider.sh \
  --storage filesystem \
  --path ./test-data \
  --namespace test \
  --name example \
  --version 1.0.0 \
  --binary ./test-files/provider

# Test with Terraform
cd tests/terraform
terraform init
terraform plan
```

## Documentation

- Update relevant documentation for any changes
- Add examples for new features
- Keep API documentation up to date
- Update CHANGELOG.md for user-facing changes

## Commit Messages

- Use the present tense ("Add feature" not "Added feature")
- Use the imperative mood ("Move cursor to..." not "Moves cursor to...")
- Limit the first line to 72 characters
- Reference issues and pull requests after the first line

Example:
```
Add module versioning support

- Implement version comparison logic
- Add tests for semver parsing
- Update documentation

Fixes #123
```

## Release Process

Releases are managed by project maintainers:

1. Update CHANGELOG.md
2. Update version in relevant files
3. Create and push a git tag (`v1.2.3`)
4. GitHub Actions automatically builds and publishes
5. Create GitHub release with changelog

## Questions?

- Open an issue for questions about contributing
- Join discussions in GitHub Discussions
- Reach out to maintainers for guidance

## Attribution

This CONTRIBUTING.md is adapted from open-source contribution guidelines.

Thank you for contributing! 🎉
