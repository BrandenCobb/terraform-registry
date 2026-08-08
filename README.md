# Terraform Registry

<p align="center">
  <img src="https://img.shields.io/badge/terraform-1.5+-5C4EE5?logo=terraform" alt="Terraform 1.5+">
  <img src="https://img.shields.io/badge/go-1.26+-00ADD8?logo=go" alt="Go 1.26+">
  <img src="https://img.shields.io/badge/docker-ready-2496ED?logo=docker" alt="Docker Ready">
  <img src="https://img.shields.io/badge/license-MIT-green" alt="MIT License">
</p>

A production-ready, self-hosted Terraform registry for **providers** and **modules**. Filesystem-only storage (PVC-mountable), zero cloud dependencies. Built for air-gapped and private environments.

> **⚠️ Disclaimer:** This is an unofficial, community-maintained implementation of the Terraform Registry Protocol. This project is not affiliated with, endorsed by, or sponsored by HashiCorp, Inc. Terraform® is a registered trademark of HashiCorp, Inc.

## Features

- **Providers & Modules** — Host both in a single registry
- **Filesystem Storage** — PVC-mountable, atomic writes, no cloud dependencies
- **Web Dashboard** — Built-in UI at `/ui` for browsing, uploading, and managing artifacts
- **CLI Tool** (`tfreg`) — Push, pull, bundle, list, and delete from the command line
- **RBAC API Keys** — Per-user keys with read/write/admin permissions
- **Metrics** — Prometheus-compatible endpoint at `/metrics`
- **Webhooks** — Notify external systems on publish, delete, and deprecate events
- **Version Deprecation** — Mark versions as deprecated (hidden from `terraform init`)
- **GPG Signing** — GPG key management for provider artifact verification
- **Rate Limiting** — Per-IP rate limiting with configurable thresholds
- **Audit Logging** — Structured JSON audit trail for all management operations
- **Air-Gap Ready** — No external calls, works fully offline

## Quick Start

### Docker Compose

```bash
git clone https://github.com/BrandenCobb/terraform-registry.git
cd terraform-registry
docker-compose up -d
```

The registry is available at `http://localhost:5000` with the web UI at `http://localhost:5000/ui`.

### Docker

```bash
docker run -d \
  --name terraform-registry \
  -p 5000:8080 \
  -v $(pwd)/data:/var/lib/terraform-registry \
  ghcr.io/brandencobb/terraform-registry:latest
```

### First Run

On first start, a default admin API key is printed to stderr:

```
=== DEFAULT API KEY (save this!) ===
6cc82f43c88710568120e4a3ff62c34b99a0d4bedd90ce4ae2bb91b0820894bd
====================================
```

Save this key — it's the only time it's shown.

## Upload Artifacts

### Using the CLI (`tfreg`)

```bash
# Download tfreg from releases or build it
cd cmd/tfreg && go build -o tfreg .

# Set registry URL
export TFREG_REGISTRY=http://localhost:5000
export TFREG_API_KEY=<your-api-key>

# Push a provider
tfreg push provider \
  --namespace hashicorp \
  --name aws \
  --version 6.31.0 \
  --file terraform-provider-aws_v6.31.0_x5

# Push a module
tfreg push module \
  --namespace example \
  --name vpc \
  --provider aws \
  --version 1.0.0 \
  --file module.tar.gz

# List contents
tfreg list providers
tfreg list modules

# Pull artifacts
tfreg pull provider --namespace hashicorp --name aws --version 6.31.0
tfreg pull module --namespace example --name vpc --provider aws --version 1.0.0

# Bundle local files for later upload
tfreg bundle provider --namespace hashicorp --name aws --version 6.31.0 \
  --binary ./terraform-provider-aws

# Delete a version
tfreg delete provider --namespace hashicorp --name aws --version 6.31.0
```

### Using the API

```bash
# Upload a provider (multipart form)
curl -X POST http://localhost:5000/api/v1/providers/hashicorp/aws/6.31.0/linux/amd64 \
  -H "X-API-Key: <your-api-key>" \
  -F "file=@terraform-provider-aws.zip"

# Upload a module
curl -X POST http://localhost:5000/api/v1/modules/example/vpc/aws/1.0.0 \
  -H "X-API-Key: <your-api-key>" \
  -F "file=@module.tar.gz"

# List providers
curl http://localhost:5000/api/v1/providers

# List modules
curl http://localhost:5000/api/v1/modules

# Registry stats
curl http://localhost:5000/api/v1/stats
```

## Configure Terraform

### Provider Network Mirror

```hcl
# ~/.terraformrc
provider_installation {
  network_mirror {
    url = "https://registry.example.com/"  # or http:// for local dev
    include = ["*/*"]
  }
}
```

### Use in Terraform Code

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
}
```

## API Endpoints

### Terraform Protocol (always public — no auth required)

| Endpoint | Description |
|----------|-------------|
| `GET /.well-known/terraform.json` | Protocol discovery |
| `GET /v1/providers/{ns}/{type}/versions` | List provider versions |
| `GET /v1/providers/{ns}/{type}/{ver}/download/{os}/{arch}` | Provider download metadata |
| `GET /v1/modules/{ns}/{name}/{provider}/versions` | List module versions |
| `GET /v1/modules/{ns}/{name}/{provider}/{ver}/download` | Download module version |
| `GET /v1/modules/{ns}/{name}/{provider}/download` | Download latest module |

### Management API (auth required for mutations)

| Endpoint | Method | Permission | Description |
|----------|--------|------------|-------------|
| `/api/v1/stats` | GET | — | Registry statistics |
| `/api/v1/providers` | GET | — | List all providers |
| `/api/v1/providers/{ns}/{name}` | GET | — | Provider details |
| `/api/v1/providers/{ns}/{name}/{ver}/{os}/{arch}` | POST | write | Upload provider |
| `/api/v1/providers/{ns}/{name}/{ver}` | DELETE | admin | Delete version |
| `/api/v1/providers/{ns}/{name}/{ver}/deprecate` | POST | write | Deprecate version |
| `/api/v1/modules` | GET | — | List all modules |
| `/api/v1/modules/{ns}/{name}/{provider}` | GET | — | Module details |
| `/api/v1/modules/{ns}/{name}/{provider}/{ver}` | POST | write | Upload module |
| `/api/v1/modules/{ns}/{name}/{provider}/{ver}` | DELETE | admin | Delete version |
| `/api/v1/modules/{ns}/{name}/{provider}/{ver}/deprecate` | POST | write | Deprecate version |
| `/api/v1/gc` | POST | admin | Trigger garbage collection |

### Operations

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Health check (JSON) |
| `GET /metrics` | Prometheus/JSON metrics |
| `GET /ui` | Web dashboard |

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `STORAGE_PATH` | `/var/lib/terraform-registry` | Filesystem storage root (mount PVC here) |
| `BASE_URL` | `http://localhost:8080` | Public URL for download links |
| `PORT` | `8080` | HTTP listen port |
| `API_KEYS_FILE` | `{STORAGE_PATH}/keys.json` | API key file (auto-generated on first run) |
| `AUDIT_LOG` | — | Path to audit log file |
| `RATE_LIMIT` | `100` | Requests per window |
| `RATE_WINDOW` | `1m` | Rate limit window duration |
| `MAX_UPLOAD_MB` | `500` | Maximum upload size in MB |
| `LOG_LEVEL` | `info` | Log level: `info` or `debug` |
| `WEBHOOK_CONFIG` | — | Path to webhook configuration JSON |

## API Keys & RBAC

On first run, a `keys.json` file is auto-generated with a default admin key. Permissions:

| Level | Capabilities |
|-------|-------------|
| `read` | All GET endpoints, Terraform protocol |
| `write` | `read` + POST (upload artifacts, deprecate versions) |
| `admin` | `write` + DELETE, garbage collection, key management |

Terraform protocol endpoints are always public — `terraform init` works without authentication.

API keys can be passed via:
- `X-API-Key` header
- `Authorization: Bearer <key>` header
- `?api_key=<key>` query parameter

The `keys.json` file hot-reloads on change — no restart needed.

## Webhooks

Create a webhook config file and set `WEBHOOK_CONFIG` to its path:

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/registry",
      "secret": "optional-hmac-secret",
      "events": ["publish", "delete", "deprecate"],
      "enabled": true
    }
  ]
}
```

Webhook payloads include event type, artifact kind, namespace, name, version, and timestamp.

## Deployment

### Kubernetes with PVC

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: terraform-registry
spec:
  replicas: 1
  selector:
    matchLabels:
      app: terraform-registry
  template:
    metadata:
      labels:
        app: terraform-registry
    spec:
      containers:
        - name: registry
          image: ghcr.io/brandencobb/terraform-registry:latest
          ports:
            - containerPort: 8080
          env:
            - name: BASE_URL
              value: "https://registry.internal.example.com"
            - name: API_KEYS_FILE
              value: "/var/lib/terraform-registry/keys.json"
          volumeMounts:
            - mountPath: /var/lib/terraform-registry
              name: registry-data
          livenessProbe:
            httpGet:
              path: /health
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /health
              port: 8080
      volumes:
        - name: registry-data
          persistentVolumeClaim:
            claimName: terraform-registry-data
```

### HTTPS with Reverse Proxy

The registry serves HTTP. For HTTPS, place it behind a reverse proxy (Caddy, nginx, Traefik):

```
# Caddyfile
registry.example.com {
    reverse_proxy terraform-registry:8080
}
```

## Building from Source

```bash
# Build server
cd registry-server && go build -o terraform-registry .

# Build CLI
cd cmd/tfreg && go build -o tfreg .

# Run tests
cd registry-server && go test ./...

# Docker
docker build -t terraform-registry .
```

## Security

- Runs as non-root user (`nobody`)
- Atomic writes (temp file + rename)
- Per-file mutex prevents concurrent corruption
- Magic byte validation rejects non-archive uploads
- Path traversal protection on download endpoint
- RBAC API keys with per-user permissions
- Structured audit logging
- Graceful shutdown on SIGTERM/SIGINT
- No embedded credentials or external calls

## License

This project is licensed under the MIT License — see [LICENSE](LICENSE) for details.

## Acknowledgments

- Inspired by [Docker Registry](https://github.com/distribution/distribution)
- Built for [Terraform](https://www.terraform.io/) by HashiCorp
- Protocol documentation from [Terraform Registry Protocol](https://developer.hashicorp.com/terraform/internals/provider-registry-protocol)
