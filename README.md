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
- Optional OCI 1.1 copies of provider/module packages for ECR and other OCI registries
- Prometheus metrics, JSON logs, audit logs, and rate limiting
- Linux, macOS, and Windows release binaries; multi-architecture container images

Planned reliability, scale, and identity work is tracked in the [project roadmap](ROADMAP.md).

## Deploy in five minutes

```bash
export REGISTRY_API_KEY="$(openssl rand -hex 32)"
export BASE_URL="http://localhost:5000"
docker compose up -d --build
curl -fsS http://localhost:5000/health
```

The named Docker volume is persistent and writable by the non-root container. Save `REGISTRY_API_KEY`; it is the initial admin credential. If it is omitted, the server generates a key once and prints it to container logs.

Open <http://localhost:5000/ui>.

### Run the current source without Compose

```bash
docker volume create terraform-registry-data
docker run -d --name terraform-registry \
  -p 5000:8080 \
  -v terraform-registry-data:/var/lib/terraform-registry \
  -e BASE_URL=http://localhost:5000 \
  -e REGISTRY_API_KEY="$REGISTRY_API_KEY" \
  terraform-registry:latest
```

Production deployments must set `BASE_URL` to the externally reachable HTTPS URL and terminate TLS at a reverse proxy or ingress. Run one server replica per filesystem volume.

## Install `tfreg`

Build the current CLI from this checkout:

```bash
make build
sudo install dist/tfreg /usr/local/bin/tfreg
tfreg version
```

Published releases also provide directly runnable binaries. After a release containing this feature is available, download its matching platform binary from the [releases page](https://github.com/BrandenCobb/terraform-registry/releases), mark it executable, and install it as `/usr/local/bin/tfreg`.

Set connection defaults:

```bash
export TFREG_REGISTRY=https://registry.example.com
export TFREG_API_KEY="$REGISTRY_API_KEY"
```

## Publish artifacts

`publish` is the normal source-to-registry workflow. It uses `TFREG_REGISTRY` and `TFREG_API_KEY`; `bundle` and `push` remain available for prebuilt packages.

### Provider: test, build, bundle, and push

From a cloned Go provider repository:

```bash
git clone https://github.com/acme/terraform-provider-example.git
cd terraform-provider-example
# modify and test the provider, then:
tfreg publish provider \
  --namespace acme --name example --version 1.2.3 \
  --source . --os linux --arch amd64
```

The command runs `go test ./...`, cross-compiles with `CGO_ENABLED=0`, creates the Terraform provider ZIP, uploads it, and removes temporary build files. Run it once per target platform. Use `--skip-tests` only when tests already ran in the same trusted pipeline.

### Module: bundle and push the repository

```bash
cd /path/to/terraform-module-vpc
tfreg publish module \
  --namespace acme --name vpc --provider aws --version 1.2.3 \
  --source .
```

The archive excludes `.git`, `.terraform`, Terraform state, and crash logs while retaining module source and lock files.

### Also publish an OCI artifact to ECR

Create the ECR repository once, then pipe the short-lived ECR token to `tfreg`:

```bash
AWS_ACCOUNT_ID=123456789012
AWS_REGION=us-east-1
ECR="$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"
aws ecr create-repository --repository-name terraform/providers/acme/example 2>/dev/null || true

aws ecr get-login-password --region "$AWS_REGION" | \
  tfreg publish provider \
    --namespace acme --name example --version 1.2.3 \
    --source . --os linux --arch amd64 \
    --oci-ref "$ECR/terraform/providers/acme/example:1.2.3-linux-amd64" \
    --oci-username AWS --oci-password-stdin
```

For a module, use the same flags with a module reference such as:

```bash
aws ecr get-login-password --region "$AWS_REGION" | \
  tfreg publish module \
    --namespace acme --name vpc --provider aws --version 1.2.3 --source . \
    --oci-ref "$ECR/terraform/modules/acme/vpc/aws:1.2.3" \
    --oci-username AWS --oci-password-stdin
```

If `--oci-username` and `--oci-password-stdin` are omitted, `tfreg` uses Docker's configured credential store. The OCI 1.1 manifest wraps the exact Terraform ZIP or tarball with Terraform media types and identity annotations. OCI is a portable copy/replication target; Terraform clients still consume artifacts through this service's Terraform protocol endpoints. Registry and OCI pushes are sequential rather than transactional, so retry the command after repairing either destination.

### Import an OCI/ECR artifact into the Terraform registry

`tfreg import` restores either artifact kind from its OCI metadata; namespace, name, version, provider, and platform flags are not repeated:

```bash
aws ecr get-login-password --region "$AWS_REGION" | \
  tfreg import \
    --oci-ref "$ECR/terraform/providers/acme/example:1.2.3-linux-amd64" \
    --oci-username AWS --oci-password-stdin
```

A digest-pinned reference is also accepted and is preferred for automation:

```bash
tfreg import \
  --oci-ref "$ECR/terraform/modules/acme/vpc/aws@sha256:<manifest-digest>"
```

Before upload, the CLI requires the Terraform OCI artifact type, exactly one matching provider ZIP or module tarball layer, complete safe identity annotations, an acceptable size, and matching manifest/package digests. It then sends the exact package through the normal registry upload endpoint. The server repeats package validation, writes atomically, and computes its SHA-256.

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

Protocol, mirror, artifact download, health, metrics, UI, and management overview `GET` routes are public. Management mutations require either:

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
