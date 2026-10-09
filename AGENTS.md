# AGENTS.md

Production-ready self-hosted Terraform Registry for air-gapped and private environments.

## Scope guardrails — read before making changes

This registry is intentionally small, lightweight, and portable. Before adding a feature, check this list:

**In scope:** Terraform protocol serving, filesystem storage, `tfreg` CLI (push/pull/bundle/import/list/delete), OCI import/export for ECR promotion pipelines, RBAC API keys, OIDC (Keycloak / e-ICAM), metrics, audit logs, air-gapped operability.

**Out of scope — handled by Advana/WDP:** Runtime artifact scanning (Trivy/Checkov), quarantine/waiver policies, security dashboards, promotion channel enforcement.

**Out of scope — removed:** Webhooks (no use case in target deployment).

**Never add:** Mandatory external service calls, telemetry, auto-update checks, Docker socket exposure, database dependencies.

If a research task or issue proposes something in the out-of-scope list, close it with a reference to Advana/WDP or note it as not applicable. Do not re-introduce removed subsystems.

## Architecture

Filesystem-only storage (PVC-mountable), zero cloud dependencies. Single Go binary + CLI tool.

```
registry-server/          # Server (Go, gorilla/mux)
  main.go                 # Router, protocol handlers, graceful shutdown
  storage.go              # Filesystem storage with atomic writes
  api.go                  # Management API (CRUD, deprecation, GC)
  auth.go                 # RBAC with per-user API keys (JSON file)
  middleware.go            # Rate limiter, audit log, upload validation
  metrics.go              # Prometheus + JSON metrics
  crypto.go               # SHA256, GPG verification helpers
  ui.go                   # Embedded web UI (go:embed)
  ui/                     # Dashboard HTML/CSS/JS

cmd/tfreg/                # CLI tool (Go, zero deps)
  main.go                 # push/pull/bundle/import/list/delete
  archive.go              # zip/tar.gz helpers

Dockerfile                # Multi-stage: server + CLI
docker-compose.yml        # Quick start
```

## Build & Run

```bash
# Server
cd registry-server && go build -o terraform-registry .

# CLI
cd cmd/tfreg && go build -o tfreg .

# Run
STORAGE_PATH=./data BASE_URL=http://localhost:8080 PORT=8080 ./terraform-registry

# Tests
cd registry-server && go test -v ./...

# Docker
docker-compose up -d
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `STORAGE_PATH` | `/var/lib/terraform-registry` | Filesystem storage root (mount PVC here) |
| `BASE_URL` | `http://localhost:8080` | Public URL for download links |
| `PORT` | `8080` | Listen port |
| `API_KEYS_FILE` | `{STORAGE_PATH}/keys.json` | API key file (auto-generated on first run) |
| `REGISTRY_API_KEY` | (empty) | Legacy single-key auth (deprecated, use keys.json) |
| `AUDIT_LOG` | (empty) | Path to audit log file |
| `RATE_LIMIT` | `100` | Requests per window |
| `RATE_WINDOW` | `1m` | Rate limit window duration |
| `MAX_UPLOAD_MB` | `500` | Maximum upload size in MB |
| `LOG_LEVEL` | `info` | `info` or `debug` |

## API Keys (RBAC)

First run auto-generates `keys.json` with a default admin key printed to stderr.
Permissions: `read` (GET), `write` (POST/uploads), `admin` (DELETE, config).
Terraform protocol endpoints are always public (no auth needed for `terraform init`).

## Storage Layout

```
{STORAGE_PATH}/
├── providers/{ns}/{name}/
│   ├── index.json                    # Version list
│   └── {version}/
│       ├── {os}_{arch}.json          # Platform metadata + shasum
│       ├── metadata.json             # Version metadata, deprecation, GPG key
│       └── terraform-provider-{name}_{version}_{os}_{arch}.zip
├── modules/{ns}/{name}/{provider}/
│   ├── index.json
│   └── {version}/
│       ├── module.tar.gz
│       └── metadata.json
├── keys.json                         # API keys
└── tmp/                              # Temp uploads (GC'd hourly)
```

## Endpoints

### Terraform Protocol (always public)
- `/.well-known/terraform.json` — Discovery
- `/v1/providers/{ns}/{type}/versions` — Provider versions
- `/v1/providers/{ns}/{type}/{ver}/download/{os}/{arch}` — Download metadata
- `/{hostname}/{ns}/{type}/index.json` — Network mirror
- `/v1/modules/{ns}/{name}/{provider}/versions` — Module versions
- `/v1/modules/{ns}/{name}/{provider}/{ver}/download` — Module download (204 + X-Terraform-Get)

### Management API (auth required for mutations)
- `GET /api/v1/stats` — Registry statistics
- `GET /api/v1/providers` — List providers
- `POST /api/v1/providers/{ns}/{name}/{ver}/{os}/{arch}` — Upload provider
- `DELETE /api/v1/providers/{ns}/{name}/{ver}` — Delete version (admin)
- `POST /api/v1/providers/{ns}/{name}/{ver}/deprecate` — Deprecate version
- `GET /api/v1/modules` — List modules
- `POST /api/v1/modules/{ns}/{name}/{provider}/{ver}` — Upload module
- `DELETE /api/v1/modules/{ns}/{name}/{provider}/{ver}` — Delete (admin)
- `POST /api/v1/modules/{ns}/{name}/{provider}/{ver}/deprecate` — Deprecate
- `POST /api/v1/gc` — Trigger garbage collection (admin)

### Operations
- `GET /health` — Health check (JSON)
- `GET /metrics` — Prometheus/JSON metrics
- `GET /ui` — Web dashboard

## Production Deployment

Mount PVC at `STORAGE_PATH`. All writes are atomic (temp+rename).
File-level mutex prevents concurrent writes to same artifact.
Hourly GC cleans orphaned temp files. Graceful shutdown on SIGTERM/SIGINT.

Run exactly one registry process per filesystem volume. Persist the complete storage volume. TLS is supplied by a reverse proxy or ingress.

```yaml
# Kubernetes PVC
volumes:
  - name: registry-data
    persistentVolumeClaim:
      claimName: terraform-registry-data
volumeMounts:
  - mountPath: /var/lib/terraform-registry
    name: registry-data
```

## CLI (tfreg)

```bash
export TFREG_REGISTRY=https://registry.internal.example.com
tfreg push provider --namespace hashicorp --name aws --version 6.31.0 --file provider.zip
tfreg push module --namespace example --name vpc --provider aws --version 1.0.0 --file module.tar.gz
tfreg list providers
tfreg pull provider --namespace hashicorp --name aws --version 6.31.0
tfreg bundle provider --namespace hashicorp --name aws --version 6.31.0 --binary ./terraform-provider-aws
tfreg import --oci-ref "$ECR/terraform/providers/acme/example:1.2.3-linux-amd64" --oci-username AWS --oci-password-stdin
tfreg delete provider --namespace hashicorp --name aws --version 6.31.0
```

## Air-Gapped Deployment

The registry has no mandatory external service calls. For air-gapped environments:
1. Build the container image in an internet-connected environment
2. Transfer the image and any provider/module ZIPs via approved media
3. `docker load` the image, start the registry, and use `tfreg push` or `tfreg import` to populate artifacts
4. Configure Terraform clients to use the internal registry as a network mirror (see README)
