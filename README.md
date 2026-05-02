# Terraform Registry

<p align="center">
  <img src="https://img.shields.io/badge/terraform-1.5+-5C4EE5?logo=terraform" alt="Terraform 1.5+">
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8?logo=go" alt="Go 1.26+">
  <img src="https://img.shields.io/badge/docker-ready-2496ED?logo=docker" alt="Docker Ready">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="MIT License">
</p>

A self-hosted Terraform registry supporting both **providers** and **modules** with pluggable storage backends. Like Docker Registry, but for Terraform artifacts.

> **⚠️ Disclaimer:** This is an unofficial, community-maintained implementation of the Terraform Registry Protocol. This project is not affiliated with, endorsed by, or sponsored by HashiCorp, Inc. Terraform® is a registered trademark of HashiCorp, Inc.

## Features

✨ **Dual Support** - Host both providers and modules in a single registry  
🔌 **Pluggable Storage** - Choose filesystem (volume mounts) or S3 backend  
🐳 **Docker-like** - Simple volume mounts: `-v ./data:/var/lib/terraform-registry`  
📦 **Single Container** - One lightweight container serves everything  
🔒 **Enterprise Ready** - IRSA support, health checks, and production-ready  
🚀 **Protocol Complete** - Full Terraform Registry Protocol v1 implementation

## Quick Start

### 1. Run the Registry

```bash
# Using Docker
docker run -d \
  --name terraform-registry \
  -p 5000:8080 \
  -v $(pwd)/data:/var/lib/terraform-registry \
  ghcr.io/brandencobb/terraform-registry:latest

# Using Docker Compose
docker-compose up -d
```

### 2. Upload Artifacts

```bash
# Upload a provider
./scripts/upload-provider.sh \
  --storage filesystem \
  --path ./data \
  --namespace hashicorp \
  --name aws \
  --version 6.31.0 \
  --binary ./terraform-provider-aws_v6.31.0_x5

# Upload a module
./scripts/upload-module.sh \
  --storage filesystem \
  --path ./data \
  --namespace example \
  --name vpc \
  --provider aws \
  --version 1.0.0 \
  --source ./my-vpc-module/
```

### 3. Configure Terraform

```hcl
# ~/.terraformrc
provider_installation {
  network_mirror {
    url = "http://localhost:5000/"
    include = ["*/*"]
  }
}
```

### 4. Use in Your Code

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.31.0"
    }
  }
}

module "vpc" {
  source  = "example/vpc/aws"
  version = "1.0.0"
  
  cidr = "10.0.0.0/16"
}
```

## Documentation

- [Quick Start Guide](docs/QUICKSTART.md)
- [Deployment Guide](docs/DEPLOYMENT.md) - Kubernetes, Docker, AWS
- [Provider Build Guide](docs/PROVIDER_BUILD.md) - Building custom providers
- [API Reference](docs/API.md) - REST API documentation
- [Configuration](docs/CONFIGURATION.md) - Environment variables and options

## Storage Backends

### Filesystem Storage (Default)

Perfect for local development and single-server deployments:

```bash
docker run -d -p 5000:8080 \
  -v ./providers:/var/lib/terraform-registry/providers \
  -v ./modules:/var/lib/terraform-registry/modules \
  -e STORAGE_TYPE=filesystem \
  ghcr.io/brandencobb/terraform-registry:latest
```

### S3 Storage

Ideal for production Kubernetes deployments with IRSA:

```bash
docker run -d -p 5000:8080 \
  -e STORAGE_TYPE=s3 \
  -e S3_BUCKET=terraform-registry \
  -e AWS_REGION=us-west-1 \
  ghcr.io/brandencobb/terraform-registry:latest
```

## Deployment

### Docker

```bash
docker-compose up -d
```

### Kubernetes

```bash
kubectl apply -f examples/kubernetes/manifests/
```

See [Deployment Guide](docs/DEPLOYMENT.md) for detailed instructions.

## Building

```bash
# Build registry server
make build

# Build and push Docker image
make docker-build docker-push

# Run tests
make test

# Run all checks
make lint test
```

## API Endpoints

### Providers

- `GET /.well-known/terraform.json` - Service discovery
- `GET /v1/providers/{namespace}/{type}/versions` - List provider versions
- `GET /v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}` - Download provider

### Modules

- `GET /v1/modules/{namespace}/{name}/{provider}/versions` - List module versions
- `GET /v1/modules/{namespace}/{name}/{provider}/{version}/download` - Download specific version
- `GET /v1/modules/{namespace}/{name}/{provider}/download` - Download latest version

### Health

- `GET /health` - Health check endpoint

See [API Reference](docs/API.md) for complete documentation.

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `STORAGE_TYPE` | `filesystem` | Storage backend: `filesystem` or `s3` |
| `STORAGE_PATH` | `/var/lib/terraform-registry` | Base path for filesystem storage |
| `BASE_URL` | `http://localhost:8080` | Base URL for file downloads |
| `S3_BUCKET` | - | S3 bucket name (required for S3 storage) |
| `AWS_REGION` | `us-west-1` | AWS region for S3 storage |
| `PORT` | `8080` | HTTP server port |

## Use Cases

- 🏠 **Local Development** - Mount volumes for fast iteration
- 🔒 **Air-Gapped Environments** - Pre-populate and transfer
- 🏢 **Enterprise** - S3 backend with IRSA for multi-cluster
- 🚀 **CI/CD** - Share custom providers and modules across pipelines
- 🌍 **Multi-Tenant** - Separate S3 buckets per tenant/team

## Comparison

| Feature | Terraform Registry | Terraform Cloud | Artifactory Pro | Harbor |
|---------|-------------------|-----------------|-----------------|--------|
| Providers | ✅ | ✅ | ✅ | ❌ |
| Modules | ✅ | ✅ | ❌ | ❌ |
| Self-Hosted | ✅ | Enterprise only | ✅ | ✅ |
| Filesystem | ✅ | ❌ | ❌ | ✅ |
| S3 Backend | ✅ | ❌ | ✅ | ❌ |
| IRSA | ✅ | ❌ | ❌ | ❌ |
| License | MIT | Proprietary | Commercial | Apache 2.0 |

## Examples

Check out the [examples/](examples/) directory for:

- Kubernetes deployments with PVC and IRSA
- Docker Compose configurations
- Sample Terraform configurations
- Upload script examples
- Provider build examples

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

### Development

```bash
# Clone repository
git clone https://github.com/BrandenCobb/terraform-registry.git
cd terraform-registry

# Install dependencies
cd registry-server
go mod download

# Run tests
go test ./...

# Build
go build -o terraform-registry main.go storage.go

# Run locally
./terraform-registry
```

## Security

- Runs as non-root user (`nobody`)
- Read-only root filesystem support
- IRSA for keyless AWS authentication
- No embedded credentials
- TLS termination via ingress/reverse proxy

See [SECURITY.md](SECURITY.md) for security policy.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Acknowledgments

- Inspired by [Docker Registry](https://github.com/distribution/distribution)
- Built for [Terraform](https://www.terraform.io/) by HashiCorp
- Protocol documentation from [Terraform Registry Protocol](https://www.terraform.io/docs/internals/provider-registry-protocol.html)

## Support

- 📖 [Documentation](docs/)
- 🐛 [Issue Tracker](https://github.com/BrandenCobb/terraform-registry/issues)
- 💬 [Discussions](https://github.com/BrandenCobb/terraform-registry/discussions)

---
