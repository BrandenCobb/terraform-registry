# API Reference

This document describes the Terraform Registry API endpoints implemented by this server.

## Protocol Version

This registry implements the [Terraform Registry Protocol v1](https://www.terraform.io/internals/provider-registry-protocol) for both providers and modules.

## Base URL

All endpoints are relative to the registry base URL:
- Development: `http://localhost:5000`
- Production: `https://registry.example.com`

## Discovery

### Get Service Discovery

```
GET /.well-known/terraform.json
```

Returns the service discovery document indicating which protocol versions are supported.

**Response:**

```json
{
  "providers.v1": "/v1/providers/",
  "modules.v1": "/v1/modules/"
}
```

**Status Codes:**
- `200 OK` - Success

**Example:**

```bash
curl http://localhost:5000/.well-known/terraform.json
```

---

## Provider Endpoints

### List Provider Versions

```
GET /v1/providers/{namespace}/{type}/versions
```

List all available versions of a provider.

**Parameters:**
- `namespace` - Provider namespace (e.g., `hashicorp`)
- `type` - Provider type (e.g., `aws`)

**Response:**

```json
{
  "versions": [
    {
      "version": "6.31.0",
      "protocols": ["5.0"],
      "platforms": [
        {
          "os": "linux",
          "arch": "amd64"
        },
        {
          "os": "darwin",
          "arch": "amd64"
        }
      ]
    }
  ]
}
```

**Status Codes:**
- `200 OK` - Success (may return empty list)
- `404 Not Found` - Provider not found
- `500 Internal Server Error` - Server error

**Example:**

```bash
curl http://localhost:5000/v1/providers/hashicorp/aws/versions
```

### Get Provider Download URL

```
GET /v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}
```

Get download metadata for a specific provider version and platform.

**Parameters:**
- `namespace` - Provider namespace
- `type` - Provider type
- `version` - Provider version
- `os` - Operating system (`linux`, `darwin`, `windows`)
- `arch` - Architecture (`amd64`, `arm64`)

**Response:**

```json
{
  "protocols": ["5.0"],
  "os": "linux",
  "arch": "amd64",
  "filename": "terraform-provider-aws_v6.31.0_linux_amd64.zip",
  "download_url": "http://localhost:5000/download/providers/hashicorp/aws/6.31.0/terraform-provider-aws_v6.31.0_linux_amd64.zip",
  "shasum": "abc123def456...",
  "signing_keys": {
    "gpg_public_keys": []
  }
}
```

**Notes:**
- For S3 storage, `download_url` is a presigned S3 URL (expires in 15 minutes)
- For filesystem storage, `download_url` is a direct registry URL

**Status Codes:**
- `200 OK` - Success
- `404 Not Found` - Platform not found
- `500 Internal Server Error` - Server error

**Example:**

```bash
curl http://localhost:5000/v1/providers/hashicorp/aws/6.31.0/download/linux/amd64
```

---

## Module Endpoints

### List Module Versions

```
GET /v1/modules/{namespace}/{name}/{provider}/versions
```

List all available versions of a module.

**Parameters:**
- `namespace` - Module namespace (e.g., `example`)
- `name` - Module name (e.g., `vpc`)
- `provider` - Target provider (e.g., `aws`)

**Response:**

```json
{
  "modules": [
    {
      "version": "1.0.0"
    },
    {
      "version": "1.1.0"
    }
  ]
}
```

**Status Codes:**
- `200 OK` - Success (may return empty list)
- `404 Not Found` - Module not found
- `500 Internal Server Error` - Server error

**Example:**

```bash
curl http://localhost:5000/v1/modules/example/vpc/aws/versions
```

### Download Module Version

```
GET /v1/modules/{namespace}/{name}/{provider}/{version}/download
```

Download a specific module version. Returns a redirect to the download URL.

**Parameters:**
- `namespace` - Module namespace
- `name` - Module name
- `provider` - Target provider
- `version` - Module version

**Response:**

HTTP redirect (302) to download URL.

**Status Codes:**
- `302 Found` - Redirect to download URL
- `404 Not Found` - Module version not found

**Example:**

```bash
curl -L http://localhost:5000/v1/modules/example/vpc/aws/1.0.0/download
```

### Download Latest Module

```
GET /v1/modules/{namespace}/{name}/{provider}/download
```

Download the latest version of a module. Returns version metadata and download URL.

**Parameters:**
- `namespace` - Module namespace
- `name` - Module name
- `provider` - Target provider

**Response:**

```json
{
  "version": "1.1.0"
}
```

**Headers:**
- `X-Terraform-Get` - Download URL for the module

**Status Codes:**
- `200 OK` - Success
- `404 Not Found` - Module not found or no versions available

**Example:**

```bash
curl -i http://localhost:5000/v1/modules/example/vpc/aws/download
```

---

## File Download Endpoint

### Download File

```
GET /download/{path}
```

Direct file download endpoint for filesystem storage.

**Parameters:**
- `path` - Full path to the file (e.g., `providers/hashicorp/aws/6.31.0/...`)

**Response:**

Binary file content with appropriate `Content-Type` header.

**Status Codes:**
- `200 OK` - Success
- `404 Not Found` - File not found
- `501 Not Implemented` - When using S3 storage (presigned URLs used instead)

**Example:**

```bash
curl -O http://localhost:5000/download/providers/hashicorp/aws/6.31.0/terraform-provider-aws_v6.31.0_linux_amd64.zip
```

---

## Health Check

### Health Status

```
GET /health
```

Check if the registry is operational and storage is accessible.

**Response:**

```
OK
```

**Status Codes:**
- `200 OK` - Registry is healthy
- `503 Service Unavailable` - Storage not accessible

**Example:**

```bash
curl http://localhost:5000/health
```

---

## Error Responses

All error responses follow this format:

```
HTTP/1.1 <status_code> <status_text>
Content-Type: text/plain

<error_message>
```

**Common Status Codes:**
- `404 Not Found` - Resource not found
- `500 Internal Server Error` - Server error (check logs)
- `503 Service Unavailable` - Storage backend unavailable

---

## Storage Backend Behavior

### Filesystem Storage

- Downloads served directly from registry
- URLs: `http://registry/download/path/to/file`
- No expiration on URLs

### S3 Storage

- Downloads use presigned S3 URLs
- URLs expire after 15 minutes
- Direct S3 access (faster downloads)
- URLs format: `https://s3.region.amazonaws.com/bucket/...?signature=...`

---

## Rate Limiting

This registry does not implement rate limiting. For production deployments, add rate limiting at the reverse proxy or ingress level.

---

## Authentication

This registry does not implement authentication. For production deployments, add authentication at the reverse proxy or ingress level using:
- Basic Auth
- OAuth2
- Mutual TLS
- API Keys

---

## CORS

CORS is not configured by default. If you need browser access to the API, configure CORS headers at the reverse proxy level.

---

## Content Types

- Discovery: `application/json`
- Provider/Module metadata: `application/json`
- Provider binaries: `application/zip`
- Module archives: `application/gzip`

---

## API Versioning

The current API implements:
- Terraform Provider Protocol: `v1` (protocols 5.0)
- Terraform Module Protocol: `v1`

Future versions will be available at `/v2/` paths while maintaining `/v1/` for backwards compatibility.

---

## See Also

- [Configuration Guide](CONFIGURATION.md)
- [Deployment Guide](DEPLOYMENT.md)
- [Terraform Registry Protocol](https://www.terraform.io/internals/provider-registry-protocol)
