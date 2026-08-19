# CLAUDE.md

Production-ready self-hosted Terraform Registry for air-gapped and private environments.

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
  webhooks.go             # Webhook notifications on publish/delete/deprecate
  scanning.go             # Scan records, policy, waivers, safe extraction/parsing
  scanner_manager.go      # Durable queue, workers, recovery, backfill, scheduling
  scanning_api.go         # Security overview/detail/report/rescan/waiver APIs
  crypto.go               # SHA256, GPG verification helpers
  ui.go                   # Embedded web UI (go:embed)
  ui/                     # Dashboard HTML/CSS/JS

cmd/tfreg/                # CLI tool (Go, zero deps)
  main.go                 # push/pull/bundle/list/delete
  archive.go              # zip/tar.gz helpers

Dockerfile                # Multi-stage: server + CLI
Dockerfile.scanner        # Scanner variant with pinned Trivy + Checkov
docker-compose.yml        # Quick start
docker-compose.scanning.yml # Scanner-enabled overlay
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
| `WEBHOOK_CONFIG` | (empty) | Path to webhook config JSON |
| `SCANNING_ENABLED` | `false` | Enable durable asynchronous artifact scanning |
| `SCAN_MODE` | `visibility` | `visibility`, `quarantine`, or `enforce` |
| `SCAN_WORKERS` | `1` | Concurrent scanner workers (1-16) |
| `SCAN_TIMEOUT` | `15m` | Per-artifact scanner timeout |
| `SCAN_STALE_AFTER` | `168h` | Age after which completed results become stale |
| `SCAN_INTERVAL` | `1h` | Scheduled stale/error rescan interval |
| `SCAN_DENY_SEVERITIES` | `critical,high` | Severities producing policy denial |
| `SCAN_OFFLINE` | `false` | Disable Trivy DB updates; preload/persist cache for air-gapped use |
| `TRIVY_PATH` / `CHECKOV_PATH` | `trivy` / `checkov` | Scanner executable paths in the scanner image |
| `TRIVY_CACHE_DIR` | (empty) | Persistent Trivy vulnerability database cache |

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
├── scans/                            # Digest-bound current/history/raw reports + waivers
├── trivy-cache/                      # Rebuildable vulnerability database cache
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
- `GET /api/v1/security/health` — Scanner enablement, mode, readiness, queue depth
- `GET /api/v1/security/summary` — Complete-inventory status/policy/severity aggregates for the command center
- `GET /api/v1/security/scans` — Public redacted security overview
- `GET /api/v1/security/scans/{digest}` — Authenticated findings/detail
- `GET /api/v1/security/scans/{digest}/history` — Authenticated history
- `GET /api/v1/security/scans/{digest}/reports/{scanID}` — Authenticated raw report
- `POST /api/v1/security/scans/{digest}/rescan` — Manual rescan (write)
- `POST /api/v1/security/scans/{digest}/waivers` — Expiring waiver (admin)
- `DELETE /api/v1/security/waivers/{waiverID}` — Revoke waiver (admin)

### Operations
- `GET /health` — Health check (JSON)
- `GET /metrics` — Prometheus/JSON metrics
- `GET /ui` — Web dashboard

## Production Deployment

Mount PVC at `STORAGE_PATH`. All writes are atomic (temp+rename).
File-level mutex prevents concurrent writes to same artifact.
Hourly GC cleans orphaned temp files. Graceful shutdown on SIGTERM/SIGINT.

Run exactly one registry process per filesystem volume. Scanning is optional and filesystem-backed: provider ZIPs use Trivy, module archives use Checkov, and artifacts are never executed. Begin upgrades in `SCAN_MODE=visibility`; `quarantine`/`enforce` fail closed for unknown, queued, scanning, errored, stale, or policy-denied digests. Persist the complete storage volume (including scan history and waivers) and the Trivy cache; scanner workspaces remain disposable under `/tmp`.

**Security/command-center notes (2026-08-12, v2.3.0):** `/ui` is now the enterprise command center, not a basic CRUD dashboard. It depends on `/api/v1/security/summary` for complete-inventory posture scoring and policy-blocked counts because `/security/scans?limit=100` is only a paginated overview. Keep security UI rendering DOM-only (no report HTML injection), and keep color paired with text labels for accessibility. Scanner Docker builds intentionally cross-compile the registry and Trivy binaries on the native BuildKit host platform to avoid slow QEMU compilation during multi-architecture releases.

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
tfreg delete provider --namespace hashicorp --name aws --version 6.31.0
```
