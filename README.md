# Terraform Registry

A production-focused, self-hosted Terraform provider and module registry. It is a single Go server, an embedded web UI, and the `tfreg` CLI backed by a persistent filesystem volume.

> Unofficial community implementation; not affiliated with HashiCorp.

## What works

- Terraform provider registry protocol and provider network mirror protocol
- Terraform module registry protocol
- Provider/module upload, download, listing, deprecation, and deletion
- SHA-256 generation by the server and verification by `tfreg pull provider`
- RBAC API keys (`read`, `write`, `admin`) for management mutations
- Atomic writes, bounded streaming uploads/downloads, input validation, and traversal protection
- Embedded dashboard at `/ui`
- Optional enterprise artifact scanning: Trivy for provider ZIPs, Checkov for modules, durable history, policy quarantine, waivers, metrics, webhooks, and a security dashboard
- Prometheus metrics, JSON logs, audit logs, rate limiting, and signed webhooks
- Linux, macOS, and Windows release binaries; multi-architecture container images

## Deploy in five minutes

```bash
export REGISTRY_API_KEY="$(openssl rand -hex 32)"
export BASE_URL="http://localhost:5000"
docker compose up -d
curl -fsS http://localhost:5000/health
```

The named Docker volume is persistent and writable by the non-root container. Save `REGISTRY_API_KEY`; it is the initial admin credential. If it is omitted, the server generates a key once and prints it to container logs.

Open <http://localhost:5000/ui>.

### Enable artifact scanning

```bash
SCAN_MODE=visibility \
  docker compose -f docker-compose.yml -f docker-compose.scanning.yml up -d
curl -fsS http://localhost:5000/api/v1/security/health
```

Begin with `visibility`. Existing artifacts are discovered and queued during startup. After the backlog is clean and waivers are documented, switch to `quarantine` or `enforce`; blocking modes hide unknown, stale, errored, or denied artifacts consistently from Terraform protocol discovery and downloads. See [configuration](docs/CONFIGURATION.md#artifact-scanning).

### Pull the published image directly

```bash
docker volume create terraform-registry-data
docker run -d --name terraform-registry \
  -p 5000:8080 \
  -v terraform-registry-data:/var/lib/terraform-registry \
  -e BASE_URL=http://localhost:5000 \
  -e REGISTRY_API_KEY="$REGISTRY_API_KEY" \
  ghcr.io/brandencobb/terraform-registry:v2.1.0
```

Production deployments must set `BASE_URL` to the externally reachable HTTPS URL and terminate TLS at a reverse proxy or ingress. Run one server replica per filesystem volume.

## Install `tfreg`

Download a directly runnable binary from the [latest release](https://github.com/BrandenCobb/terraform-registry/releases/latest):

```bash
curl -fLO https://github.com/BrandenCobb/terraform-registry/releases/download/v2.1.0/tfreg-linux-amd64
chmod +x tfreg-linux-amd64
sudo install tfreg-linux-amd64 /usr/local/bin/tfreg

tfreg version
```

Set connection defaults:

```bash
export TFREG_REGISTRY=https://registry.example.com
export TFREG_API_KEY="$REGISTRY_API_KEY"
```

## Publish artifacts

### Provider

Bundle a provider executable, then upload the ZIP:

```bash
tfreg bundle provider \
  --namespace acme --name example --version 1.2.3 \
  --os linux --arch amd64 \
  --binary ./terraform-provider-example_v1.2.3

tfreg push provider \
  --namespace acme --name example --version 1.2.3 \
  --os linux --arch amd64 \
  --file ./terraform-provider-example_1.2.3_linux_amd64.zip
```

Repeat the upload for each OS/architecture. The server assigns a canonical filename and computes the checksum.

### Module

```bash
tfreg bundle module \
  --namespace acme --name vpc --provider aws --version 1.2.3 \
  --source ./modules/vpc

tfreg push module \
  --namespace acme --name vpc --provider aws --version 1.2.3 \
  --file ./acme-vpc-aws-1.2.3.tar.gz
```

### Browse, pull, and delete

```bash
tfreg list providers
tfreg list modules
tfreg pull provider --namespace acme --name example --version 1.2.3
tfreg pull module --namespace acme --name vpc --provider aws --version 1.2.3
tfreg delete provider --namespace acme --name example --version 1.2.3
```

## Configure Terraform

Use the service as a provider network mirror:

```hcl
# ~/.terraformrc
provider_installation {
  network_mirror {
    url     = "https://registry.example.com/"
    include = ["*/*"]
  }
  direct {
    exclude = ["*/*"]
  }
}
```

Module source addresses use the registry hostname:

```hcl
module "vpc" {
  source  = "registry.example.com/acme/vpc/aws"
  version = "1.2.3"
}
```

Private Terraform services require HTTPS outside local development.

## Authentication

Protocol, mirror, artifact download, health, metrics, UI, and management overview `GET` routes are public. Detailed scan findings/history/raw reports require a read-capable key. Management mutations require either:

```text
X-API-Key: <key>
Authorization: Bearer <key>
```

API keys are stored as SHA-256 hashes in `${STORAGE_PATH}/keys.json` and hot-reload when the file changes. Plaintext keys are never written by the current server. Permission hierarchy:

- `read`: authenticated reads (management reads are public by default)
- `write`: uploads and deprecation
- `admin`: write plus deletion and garbage collection

See [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md) for the file format and rotation procedure.

## Configuration

| Variable | Default | Purpose |
|---|---:|---|
| `STORAGE_PATH` | `/var/lib/terraform-registry` | Persistent filesystem root |
| `BASE_URL` | `http://localhost:8080` | Public absolute URL used in download metadata |
| `PORT` | `8080` | HTTP listen port |
| `API_KEYS_FILE` | `${STORAGE_PATH}/keys.json` | Hashed RBAC key configuration |
| `REGISTRY_API_KEY` | empty | Initial/deprecated single admin key bootstrap |
| `MAX_UPLOAD_MB` | `500` | Maximum artifact size |
| `RATE_LIMIT` | `100` | Per-client token-bucket capacity |
| `RATE_WINDOW` | `1m` | Token-bucket refill window |
| `TRUST_PROXY_HEADERS` | `false` | Trust `X-Forwarded-For`/`X-Real-IP` for rate limiting |
| `AUDIT_LOG` | empty | Optional append-only JSONL audit file |
| `LOG_LEVEL` | `info` | `info` or `debug` |
| `WEBHOOK_CONFIG` | empty | Webhook JSON configuration file |

Only enable `TRUST_PROXY_HEADERS` when the service is reachable exclusively through a trusted proxy that overwrites those headers.

## Operations

- `GET /health`: verifies the storage volume is writable
- `GET /metrics`: JSON by default; Prometheus text when `Accept: text/plain`
- `GET /ui`: embedded dashboard
- SIGTERM/SIGINT: graceful shutdown with a 30-second deadline

Back up the entire storage volume. Restore it as a unit while the server is stopped. Filesystem storage is single-writer; use one replica.

## Development

```bash
make check
make docker-build VERSION=dev
docker compose config
```

The CI pipeline runs race tests, vet, vulnerability/security scans, a blocking container scan, and real container/CLI integration tests.

Detailed references:

- [`docs/API.md`](docs/API.md)
- [`docs/CONFIGURATION.md`](docs/CONFIGURATION.md)
- [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md)
- [`SECURITY.md`](SECURITY.md)

## License

MIT — see [`LICENSE`](LICENSE).
