# HTTP API

All responses are JSON unless the endpoint returns an artifact or the Terraform protocol requires `204 No Content`.

## Authentication

Terraform protocol, network mirror, downloads, UI, health, metrics, and management overview reads are public. Detailed security findings, history, and raw reports require any authenticated key. Management mutations require:

```http
X-API-Key: <key>
```

or:

```http
Authorization: Bearer <key>
```

`write` permits POST uploads/deprecation. `admin` additionally permits DELETE and garbage collection.

## Service discovery

### `GET /.well-known/terraform.json`

```json
{"providers.v1":"/v1/providers/","modules.v1":"/v1/modules/"}
```

## Provider registry protocol

- `GET /v1/providers/{namespace}/{type}/versions`
- `GET /v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}`

The download response includes `filename`, `download_url`, and a SHA-256 `shasum`.

## Provider network mirror protocol

- `GET /{hostname}/{namespace}/{type}/index.json`
- `GET /{hostname}/{namespace}/{type}/{version}.json`

The version response contains `archives` keyed by `os_arch`, with download URLs and `zh:<sha256>` hashes.

## Module registry protocol

### `GET /v1/modules/{namespace}/{name}/{provider}/versions`

```json
{
  "modules": [
    {
      "source": "acme/vpc/aws",
      "versions": [{"version":"1.2.3"}]
    }
  ]
}
```

### Download

- `GET /v1/modules/{namespace}/{name}/{provider}/{version}/download`
- `GET /v1/modules/{namespace}/{name}/{provider}/download`

Success is `204 No Content` with `X-Terraform-Get` pointing to the module archive.

## Management API

| Method | Path | Permission | Description |
|---|---|---|---|
| GET | `/api/v1/stats` | public | Counts |
| GET | `/api/v1/providers` | public | List providers |
| GET | `/api/v1/providers/{namespace}/{name}` | public | Provider detail |
| POST | `/api/v1/providers/{namespace}/{name}/{version}/{os}/{arch}` | write | Multipart upload (`file`) |
| POST | `/api/v1/providers/{namespace}/{name}/{version}/deprecate` | write | Deprecate existing version |
| DELETE | `/api/v1/providers/{namespace}/{name}/{version}` | admin | Delete version |
| GET | `/api/v1/modules` | public | List modules |
| GET | `/api/v1/modules/{namespace}/{name}/{provider}` | public | Module detail |
| POST | `/api/v1/modules/{namespace}/{name}/{provider}/{version}` | write | Multipart upload (`file`) |
| POST | `/api/v1/modules/{namespace}/{name}/{provider}/{version}/deprecate` | write | Deprecate existing version |
| DELETE | `/api/v1/modules/{namespace}/{name}/{provider}/{version}` | admin | Delete version |
| POST | `/api/v1/gc` | admin | Remove stale temporary files |
| GET | `/api/v1/security/health` | public | Scanner enablement, mode, readiness, queue depth |
| GET | `/api/v1/security/summary` | public | Complete-inventory status, policy, blocking, and severity aggregates |
| GET | `/api/v1/security/scans` | public | Paginated status-only security overview |
| GET | `/api/v1/security/scans/{digest}` | read | Current detail, findings, policy explanation, active waivers |
| GET | `/api/v1/security/scans/{digest}/history` | read | Digest-bound scan history |
| GET | `/api/v1/security/scans/{digest}/reports/{scanID}` | read | Original machine-readable scanner report |
| POST | `/api/v1/security/scans/{digest}/rescan` | write | Queue a manual rescan |
| POST | `/api/v1/security/scans/{digest}/waivers` | admin | Create a time-bounded waiver |
| DELETE | `/api/v1/security/waivers/{waiverID}` | admin | Revoke a waiver |

Upload example:

```bash
curl --fail-with-body -X POST \
  -H "X-API-Key: $TFREG_API_KEY" \
  -F 'file=@provider.zip' \
  https://registry.example.com/api/v1/providers/acme/example/1.2.3/linux/amd64
```

Success envelope:

```json
{"success":true,"message":"...","data":{}}
```

Error envelope:

```json
{"success":false,"message":"..."}
```

Common statuses: `400` invalid input/archive, `401` missing key, `403` invalid key or permission, `404` missing artifact/version, `413` request too large, `429` rate limited, and `500` storage failure.

## Operations

- `GET /health`: `200` only when the storage root is writable
- `GET /metrics`: JSON by default; Prometheus text with `Accept: text/plain`
- `GET /ui`: dashboard
- `GET /download/{storage-path}`: streamed artifact with range support
