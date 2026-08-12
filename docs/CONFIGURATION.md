# Configuration

The server is configured entirely through environment variables.

| Variable | Default | Description |
|---|---|---|
| `STORAGE_PATH` | `/var/lib/terraform-registry` | Persistent filesystem root |
| `BASE_URL` | `http://localhost:8080` | Externally reachable absolute URL used in artifact links |
| `PORT` | `8080` | HTTP listen port |
| `API_KEYS_FILE` | `${STORAGE_PATH}/keys.json` | RBAC key file |
| `REGISTRY_API_KEY` | empty | Deprecated bootstrap admin key used only when creating a missing key file |
| `AUDIT_LOG` | empty | Optional audit JSONL path |
| `RATE_LIMIT` | `100` | Per-client token capacity |
| `RATE_WINDOW` | `1m` | Token refill window (Go duration) |
| `MAX_UPLOAD_MB` | `500` | Maximum artifact upload size |
| `LOG_LEVEL` | `info` | `info` or `debug` |
| `WEBHOOK_CONFIG` | empty | Webhook configuration file |
| `TRUST_PROXY_HEADERS` | `false` | Trust proxy client-IP headers |
| `SCANNING_ENABLED` | `false` | Enable durable asynchronous artifact scanning |
| `SCAN_MODE` | `visibility` | `visibility`, `quarantine`, or `enforce` |
| `SCAN_WORKERS` | `1` | Concurrent scanner workers (1-16) |
| `SCAN_TIMEOUT` | `15m` | Per-artifact scanner timeout |
| `SCAN_STALE_AFTER` | `168h` | Age after which completed results become stale |
| `SCAN_INTERVAL` | `1h` | Scheduled stale/error rescan interval |
| `SCAN_MAX_REPORT_MB` | `10` | Maximum retained raw scanner JSON (1-100 MiB) |
| `SCAN_DENY_SEVERITIES` | `critical,high` | Severities producing policy denial |
| `SCAN_OFFLINE` | `false` | Disable Trivy database updates and use offline mode |
| `TRIVY_PATH` | `trivy` | Trivy executable path |
| `CHECKOV_PATH` | `checkov` | Checkov executable path |
| `TRIVY_CACHE_DIR` | empty | Persistent Trivy vulnerability database cache |

`BASE_URL` must not contain a trailing path intended for some other application. In production, use the public HTTPS origin, for example `https://registry.example.com`.

## API keys

If `API_KEYS_FILE` does not exist, startup creates one admin key. When `REGISTRY_API_KEY` is set, its hash becomes that initial key. Otherwise a random key is printed once to stderr.

```json
{
  "keys": [
    {
      "hash": "<lowercase SHA-256 of the plaintext key>",
      "name": "ci-publisher",
      "permission": "write",
      "enabled": true,
      "created_at": "2026-08-09T00:00:00Z",
      "description": "CI artifact publisher"
    }
  ]
}
```

Generate a key and hash:

```bash
KEY="$(openssl rand -hex 32)"
printf '%s' "$KEY" | sha256sum
```

Valid permissions are `read`, `write`, and `admin`. Replace the file atomically (`write temporary file`, then `mv`) to rotate keys; the server hot-reloads it after the modification time changes. Never put plaintext keys in this file.

Send credentials with `X-API-Key` or `Authorization: Bearer`. Query-string credentials are intentionally rejected because URLs are commonly logged.

## Webhooks

```json
{
  "webhooks": [
    {
      "url": "https://hooks.example.com/terraform-registry",
      "secret": "replace-me",
      "events": ["publish", "delete", "deprecate"],
      "enabled": true
    }
  ]
}
```

Events may contain `publish`, `delete`, `deprecate`, or `*`. Payloads are POSTed as JSON. When a secret is set, `X-Registry-Signature` contains `sha256=<hex HMAC-SHA256(body)>`. Deliveries time out after 10 seconds and are asynchronous; failed deliveries are logged but not retried.

## Reverse proxies

The server uses `RemoteAddr` for rate limiting by default. Set `TRUST_PROXY_HEADERS=true` only if untrusted clients cannot reach the service directly and the proxy overwrites `X-Forwarded-For` and `X-Real-IP`.

Example Caddy configuration:

```caddy
registry.example.com {
  reverse_proxy terraform-registry:8080
}
```

## Storage

Storage is filesystem-only. Mount a persistent volume at `STORAGE_PATH`, ensure UID/GID 65534 can write it, and run exactly one replica per volume. Back up the complete directory, including `keys.json`, provider/module indexes, metadata, artifacts, `scans/`, waivers, and the Trivy cache. Restore the complete volume as one consistency unit.

## Artifact scanning

Use the scanner-enabled image and Compose overlay:

```bash
docker compose -f docker-compose.yml -f docker-compose.scanning.yml up -d
```

`visibility` records and displays results without changing Terraform protocol visibility. `quarantine` and `enforce` fail closed: unknown, queued, running, errored, stale, or policy-denied artifacts are omitted from protocol/mirror discovery and direct downloads until allowed or covered by an active waiver. Start upgrades in `visibility`, allow startup backfill to complete, then deliberately enable a blocking mode.

The scanner image runs Trivy and Checkov as UID/GID 65534 in disposable `/tmp` workspaces. It never executes provider binaries and does not require a Docker socket. Give `/tmp` enough tmpfs capacity for the maximum expanded artifact and make `TRIVY_CACHE_DIR` persistent. For air-gapped operation, preload the Trivy database cache, set `SCAN_OFFLINE=true`, and prevent scanner egress at the platform network-policy layer.
