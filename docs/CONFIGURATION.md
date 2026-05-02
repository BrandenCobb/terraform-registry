# Configuration Guide

This document describes all configuration options for the Terraform Registry server.

## Environment Variables

The registry is configured entirely through environment variables. No configuration files are required.

### Required Variables

#### Storage Type

```bash
STORAGE_TYPE=<filesystem|s3>
```

Selects the storage backend for providers and modules.

- `filesystem` - Store artifacts on local disk (default)
- `s3` - Store artifacts in AWS S3 bucket

**Default:** `filesystem`

#### Base URL

```bash
BASE_URL=<url>
```

The public URL where the registry is accessible. Used to generate download URLs for filesystem storage.

**Required for:** Filesystem storage  
**Example:** `http://localhost:8080`, `https://registry.example.com`  
**Default:** `http://localhost:8080`

---

## Filesystem Storage Configuration

### Storage Path

```bash
STORAGE_PATH=</path/to/storage>
```

Base directory for storing providers and modules when using filesystem storage.

**Default:** `/var/lib/terraform-registry`

**Directory Structure:**
```
/var/lib/terraform-registry/
├── providers/
│   └── {namespace}/{name}/{version}/
│       ├── {os}_{arch}.json
│       └── terraform-provider-{name}_{version}_{os}_{arch}.zip
└── modules/
    └── {namespace}/{name}/{provider}/
        ├── versions.json
        └── {version}/module.tar.gz
```

**Volume Mount Example:**
```bash
docker run -v ./data:/var/lib/terraform-registry \
  -e STORAGE_TYPE=filesystem \
  -e BASE_URL=http://localhost:5000 \
  ghcr.io/brandencobb/terraform-registry:latest
```

---

## S3 Storage Configuration

### S3 Bucket

```bash
S3_BUCKET=<bucket-name>
```

Name of the S3 bucket for storing artifacts.

**Required for:** S3 storage  
**Example:** `terraform-registry`, `my-company-terraform-artifacts`

### AWS Region

```bash
AWS_REGION=<region>
```

AWS region where the S3 bucket is located.

**Default:** `us-west-1`  
**Example:** `us-east-1`, `us-gov-west-1`, `eu-west-1`

### AWS Credentials

The registry uses the AWS SDK for Go v2, which supports multiple credential sources:

1. **IRSA (IAM Roles for Service Accounts)** - Recommended for Kubernetes
   - Automatically configured via service account annotations
   - No environment variables needed

2. **Environment Variables**
   ```bash
   AWS_ACCESS_KEY_ID=<key-id>
   AWS_SECRET_ACCESS_KEY=<secret-key>
   AWS_SESSION_TOKEN=<token>  # Optional for temporary credentials
   ```

3. **Shared Credentials File** (`~/.aws/credentials`)

4. **IAM Instance Profile** - For EC2 instances

**Recommended:** Use IRSA for Kubernetes deployments. See [IRSA Setup Guide](../examples/s3/irsa/README.md).

### S3 Bucket Structure

```
s3://terraform-registry/
├── .well-known/terraform.json
├── providers/
│   └── {namespace}/{name}/{version}/
│       ├── {os}_{arch}.json
│       └── terraform-provider-{name}_{version}_{os}_{arch}.zip
└── modules/
    └── {namespace}/{name}/{provider}/
        ├── versions.json
        └── {version}/module.tar.gz
```

**Example:**
```bash
docker run \
  -e STORAGE_TYPE=s3 \
  -e S3_BUCKET=terraform-registry \
  -e AWS_REGION=us-east-1 \
  -e AWS_ACCESS_KEY_ID=AKIA... \
  -e AWS_SECRET_ACCESS_KEY=secret... \
  ghcr.io/brandencobb/terraform-registry:latest
```

---

## Server Configuration

### Port

```bash
PORT=<port>
```

HTTP server listen port.

**Default:** `8080`  
**Example:** `5000`, `8000`, `9090`

**Docker Port Mapping:**
```bash
docker run -p 5000:8080 -e PORT=8080 ...
```

The container listens on `PORT` (default 8080), and you map it to the host port of your choice.

---

## Logging

The registry logs to stdout/stderr. Log level and format are not configurable.

**Log Levels:**
- `INFO` - Startup, configuration, normal operations
- `ERROR` - Errors serving requests, storage issues

**Example Output:**
```
2024/01/15 10:00:00 Starting Terraform Registry Server
2024/01/15 10:00:00 Using filesystem storage: path=/var/lib/terraform-registry
2024/01/15 10:00:00 Server listening on :8080
```

**Viewing Logs:**
```bash
# Docker
docker logs terraform-registry

# Kubernetes
kubectl logs -n terraform-registry deployment/terraform-registry -f
```

---

## Health Check

The registry provides a health check endpoint for monitoring.

**Endpoint:** `GET /health`

**Responses:**
- `200 OK` - Registry is healthy and storage is accessible
- `503 Service Unavailable` - Storage backend is not accessible

**Configuration:**

Docker:
```yaml
healthcheck:
  test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/health"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 10s
```

Kubernetes:
```yaml
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 10
  periodSeconds: 30
readinessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
```

---

## Configuration Examples

### Local Development (Filesystem)

```bash
docker run -d \
  --name terraform-registry \
  -p 5000:8080 \
  -v $(pwd)/data:/var/lib/terraform-registry \
  -e STORAGE_TYPE=filesystem \
  -e BASE_URL=http://localhost:5000 \
  -e PORT=8080 \
  ghcr.io/brandencobb/terraform-registry:latest
```

### Production (S3 with IRSA)

Kubernetes deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: terraform-registry
  namespace: terraform-registry
spec:
  replicas: 2
  selector:
    matchLabels:
      app: terraform-registry
  template:
    metadata:
      labels:
        app: terraform-registry
    spec:
      serviceAccountName: terraform-registry  # With IRSA annotation
      containers:
      - name: registry
        image: ghcr.io/brandencobb/terraform-registry:latest
        ports:
        - containerPort: 8080
        env:
        - name: STORAGE_TYPE
          value: "s3"
        - name: S3_BUCKET
          value: "terraform-registry"
        - name: AWS_REGION
          value: "us-east-1"
        - name: BASE_URL
          value: "https://registry.example.com"
        - name: PORT
          value: "8080"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
```

### Production (S3 with Static Credentials)

```bash
docker run -d \
  --name terraform-registry \
  -p 80:8080 \
  -e STORAGE_TYPE=s3 \
  -e S3_BUCKET=terraform-registry \
  -e AWS_REGION=us-east-1 \
  -e AWS_ACCESS_KEY_ID=AKIA... \
  -e AWS_SECRET_ACCESS_KEY=secret... \
  -e BASE_URL=https://registry.example.com \
  -e PORT=8080 \
  --restart unless-stopped \
  ghcr.io/brandencobb/terraform-registry:latest
```

### Air-Gapped Environment

```bash
# 1. Pre-populate filesystem storage on a connected machine
docker run -v ./data:/var/lib/terraform-registry \
  -e STORAGE_TYPE=filesystem \
  ghcr.io/brandencobb/terraform-registry:latest

# Upload providers and modules
./scripts/upload-provider.sh --storage filesystem --path ./data ...
./scripts/upload-module.sh --storage filesystem --path ./data ...

# 2. Transfer ./data directory to air-gapped environment

# 3. Run registry in air-gapped environment
docker run -d -p 5000:8080 \
  -v ./data:/var/lib/terraform-registry \
  -e STORAGE_TYPE=filesystem \
  -e BASE_URL=http://registry.local:5000 \
  ghcr.io/brandencobb/terraform-registry:latest
```

---

## Configuration Validation

The registry validates configuration on startup and will exit with an error if:

- `STORAGE_TYPE=s3` but `S3_BUCKET` is not set
- `STORAGE_PATH` is not readable/writable (filesystem mode)
- AWS credentials are invalid (S3 mode)
- `PORT` is not a valid number

**Example Error:**
```
2024/01/15 10:00:00 Error: S3_BUCKET environment variable is required when using S3 storage
```

---

## Environment Variable Summary

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `STORAGE_TYPE` | No | `filesystem` | Storage backend (`filesystem` or `s3`) |
| `STORAGE_PATH` | No | `/var/lib/terraform-registry` | Base path for filesystem storage |
| `BASE_URL` | No | `http://localhost:8080` | Public URL for the registry |
| `S3_BUCKET` | S3 only | - | S3 bucket name |
| `AWS_REGION` | No | `us-west-1` | AWS region for S3 |
| `AWS_ACCESS_KEY_ID` | No | - | AWS access key (use IRSA instead) |
| `AWS_SECRET_ACCESS_KEY` | No | - | AWS secret key (use IRSA instead) |
| `AWS_SESSION_TOKEN` | No | - | AWS session token (temporary creds) |
| `PORT` | No | `8080` | HTTP server listen port |

---

## See Also

- [API Reference](API.md)
- [Deployment Guide](DEPLOYMENT.md)
- [Quick Start Guide](QUICKSTART.md)
- [S3 IRSA Setup](../examples/s3/irsa/README.md)
