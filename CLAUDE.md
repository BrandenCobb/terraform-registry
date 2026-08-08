# CLAUDE.md

Project-specific guidance for the self-hosted Terraform Registry.

## Project Overview

A full-featured self-hosted Terraform registry for versioned providers and modules. Supports both S3 and filesystem storage backends, container-deployable with Docker Compose, includes a web UI for browsing/uploading, a management API, and a CLI tool (`tfreg`) for push/pull/bundle/list/delete operations.

## Architecture

- **Registry Server**: Go HTTP server implementing Terraform's provider and module registry protocol
- **Management API**: REST API for CRUD operations on providers and modules (`/api/v1/`)
- **Web UI**: Embedded SPA dashboard for browsing and uploading (`/ui`)
- **CLI Tool** (`tfreg`): Unified command-line tool for push/pull/bundle/list/delete
- **Storage**: Pluggable backend — S3 (production) or filesystem (local/dev)
- **Auth**: Optional API key auth via `REGISTRY_API_KEY` env var
- **Deployment**: Docker Compose (standalone) or Kubernetes (EKS with IRSA)

## Key Files

### Server (registry-server/)
- [main.go](registry-server/main.go): HTTP router, Terraform protocol handlers
- [storage.go](registry-server/storage.go): Storage interface (S3 + filesystem)
- [api.go](registry-server/api.go): Management API handlers (upload/delete/list/stats)
- [auth.go](registry-server/auth.go): API key authentication middleware
- [ui.go](registry-server/ui.go): Embedded web UI (Go embed)
- [ui/](registry-server/ui/): Web UI assets (HTML, CSS, JS)

### CLI (cmd/tfreg/)
- [main.go](cmd/tfreg/main.go): CLI entry point with all commands
- [archive.go](cmd/tfreg/archive.go): Zip and tar.gz creation helpers

### Infrastructure
- [Dockerfile](Dockerfile): Multi-stage build (server + CLI)
- [docker-compose.yml](docker-compose.yml): Quick start compose
- [Caddyfile](Caddyfile): HTTPS reverse proxy config
- [scripts/](scripts/): Legacy bash upload scripts (prefer `tfreg` CLI)

## Common Commands

### Build and Run
```bash
# Build server
cd registry-server && go build -o terraform-registry .

# Build CLI
cd cmd/tfreg && go build -o tfreg .

# Run locally
cd registry-server && STORAGE_TYPE=filesystem STORAGE_PATH=./data BASE_URL=http://localhost:8080 ./terraform-registry

# Docker
docker-compose up -d
```

### Run Tests
```bash
cd registry-server && SKIP_STORAGE_INIT=true go test -v ./...
```

### CLI Usage
```bash
# Set defaults
export TFREG_REGISTRY=http://localhost:8080
export TFREG_API_KEY=your-key  # optional

# Push artifacts
tfreg push provider --namespace hashicorp --name aws --version 6.31.0 --file provider.zip
tfreg push module --namespace example --name vpc --provider aws --version 1.0.0 --file module.tar.gz

# Pull artifacts
tfreg pull provider --namespace hashicorp --name aws --version 6.31.0
tfreg pull module --namespace example --name vpc --provider aws --version 1.0.0

# List registry contents
tfreg list providers
tfreg list modules

# Bundle local files for later upload
tfreg bundle provider --namespace hashicorp --name aws --version 6.31.0 --binary ./terraform-provider-aws
tfreg bundle module --namespace example --name vpc --provider aws --version 1.0.0 --source ./my-module/

# Delete versions
tfreg delete provider --namespace hashicorp --name aws --version 6.31.0
tfreg delete module --namespace example --name vpc --provider aws --version 1.0.0
```

### Management API
```bash
# List
curl http://localhost:8080/api/v1/providers
curl http://localhost:8080/api/v1/modules
curl http://localhost:8080/api/v1/stats

# Upload (requires API key if REGISTRY_API_KEY is set)
curl -X POST http://localhost:8080/api/v1/providers/hashicorp/aws/1.0.0/linux/amd64 \
  -H "X-API-Key: your-key" -F "file=@provider.zip"

curl -X POST http://localhost:8080/api/v1/modules/example/vpc/aws/1.0.0 \
  -H "X-API-Key: your-key" -F "file=@module.tar.gz"

# Delete
curl -X DELETE http://localhost:8080/api/v1/providers/hashicorp/aws/1.0.0 -H "X-API-Key: your-key"
```

## Storage Structure

```
providers/{namespace}/{name}/index.json          # Version list
providers/{namespace}/{name}/{version}/
  {os}_{arch}.json                                # Platform metadata
  terraform-provider-{name}_{version}_{os}_{arch}.zip

modules/{namespace}/{name}/{provider}/index.json  # Version list
modules/{namespace}/{name}/{provider}/{version}/
  module.tar.gz
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `STORAGE_TYPE` | `filesystem` | `filesystem` or `s3` |
| `STORAGE_PATH` | `/var/lib/terraform-registry` | Local storage path |
| `BASE_URL` | `http://localhost:8080` | Public URL for download links |
| `PORT` | `8080` | Server port |
| `S3_BUCKET` | `terraform-registry` | S3 bucket name (when STORAGE_TYPE=s3) |
| `AWS_REGION` | `us-gov-west-1` | AWS region |
| `REGISTRY_API_KEY` | (empty) | API key for management endpoints (empty = no auth) |

## Terraform Protocol

The registry implements:
1. `/.well-known/terraform.json` — Protocol discovery
2. `/v1/providers/{ns}/{type}/versions` — List provider versions
3. `/v1/providers/{ns}/{type}/{ver}/download/{os}/{arch}` — Download metadata
4. `/{hostname}/{ns}/{type}/index.json` — Network mirror index
5. `/{hostname}/{ns}/{type}/{ver}.json` — Network mirror version
6. `/v1/modules/{ns}/{name}/{provider}/versions` — List module versions
7. `/v1/modules/{ns}/{name}/{provider}/{ver}/download` — Download module

## Integration with adv12-deployer

Configure `.terraformrc` in deployer to use network_mirror:
```hcl
provider_installation {
  network_mirror {
    url = "https://registry.internal.example.com/"
    include = ["*/*"]
  }
}
```
