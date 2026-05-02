# Docker Compose Example

This example shows how to run the registry using Docker Compose with both filesystem and S3 storage options.

## Quick Start

### Filesystem Storage (Default)

```bash
# Start registry on port 5000
docker-compose up -d registry-filesystem

# Verify it's running
curl http://localhost:5000/health
```

### S3 Storage

```bash
# Set AWS credentials
export AWS_ACCESS_KEY_ID="your-key"
export AWS_SECRET_ACCESS_KEY="your-secret"

# Start registry on port 5001
docker-compose --profile s3 up -d registry-s3

# Verify it's running
curl http://localhost:5001/health
```

## Configuration

The `docker-compose.yml` defines two services:

### registry-filesystem
- **Port**: 5000
- **Storage**: Local volume (`registry-data`)
- **Use case**: Development, testing, single-host deployments
- **Starts**: Automatically with `docker-compose up`

### registry-s3
- **Port**: 5001
- **Storage**: AWS S3
- **Use case**: Production, multi-host deployments
- **Starts**: Only with `--profile s3` flag
- **Requires**: AWS credentials in environment

## Environment Variables

### Common
- `STORAGE_TYPE`: `filesystem` or `s3`
- `BASE_URL`: Public URL of the registry
- `PORT`: Internal port (default: 8080)

### S3-specific
- `S3_BUCKET`: S3 bucket name
- `AWS_REGION`: AWS region
- `AWS_ACCESS_KEY_ID`: AWS access key (or use IAM role)
- `AWS_SECRET_ACCESS_KEY`: AWS secret key (or use IAM role)
- `AWS_SESSION_TOKEN`: Optional session token for temporary credentials

## Usage Examples

### Upload Provider

```bash
# Download upload script
curl -O https://raw.githubusercontent.com/BrandenCobb/terraform-registry/main/scripts/upload-provider.sh
chmod +x upload-provider.sh

# Upload to filesystem registry
./upload-provider.sh \
  --storage filesystem \
  --path ./registry-data/_data \
  --namespace hashicorp \
  --name random \
  --version 3.5.1 \
  --binary ./terraform-provider-random_v3.5.1_linux_amd64 \
  --os linux \
  --arch amd64

# Upload to S3 registry
./upload-provider.sh \
  --storage s3 \
  --bucket terraform-registry \
  --region us-east-1 \
  --namespace hashicorp \
  --name random \
  --version 3.5.1 \
  --binary ./terraform-provider-random_v3.5.1_linux_amd64 \
  --os linux \
  --arch amd64
```

### Configure Terraform

Create `~/.terraformrc`:

```hcl
# For filesystem registry (port 5000)
provider_installation {
  network_mirror {
    url = "http://localhost:5000"
  }
}

# OR for S3 registry (port 5001)
provider_installation {
  network_mirror {
    url = "http://localhost:5001"
  }
}
```

### Test with Terraform

Create `main.tf`:

```hcl
terraform {
  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "3.5.1"
    }
  }
}

resource "random_string" "example" {
  length  = 16
  special = false
}

output "random_string" {
  value = random_string.example.result
}
```

Run:

```bash
terraform init
terraform apply
```

## Production Configuration

For production, create a `.env` file:

```bash
# .env
S3_BUCKET=my-terraform-registry
AWS_REGION=us-east-1
BASE_URL=https://registry.example.com
```

Update `docker-compose.yml` to use `.env`:

```yaml
services:
  registry-s3:
    env_file:
      - .env
```

## Monitoring

### View Logs

```bash
# Filesystem registry
docker-compose logs -f registry-filesystem

# S3 registry
docker-compose logs -f registry-s3
```

### Health Check

```bash
# Filesystem
curl http://localhost:5000/health

# S3
curl http://localhost:5001/health
```

### Discovery Endpoint

```bash
# Filesystem
curl http://localhost:5000/.well-known/terraform.json

# S3
curl http://localhost:5001/.well-known/terraform.json
```

## Running Both Services

You can run both registries simultaneously for testing:

```bash
# Start both
docker-compose --profile s3 up -d

# Filesystem registry: http://localhost:5000
# S3 registry: http://localhost:5001
```

## Backup (Filesystem)

Backup the volume data:

```bash
# Create backup
docker run --rm \
  -v terraform-registry_registry-data:/data \
  -v $(pwd):/backup \
  alpine tar czf /backup/registry-backup.tar.gz -C /data .

# Restore backup
docker run --rm \
  -v terraform-registry_registry-data:/data \
  -v $(pwd):/backup \
  alpine tar xzf /backup/registry-backup.tar.gz -C /data
```

## Scaling

### Filesystem Storage
Single instance only (volume is not shared)

### S3 Storage
Scale horizontally:

```bash
docker-compose --profile s3 up -d --scale registry-s3=3
```

> **Note**: You'll need a load balancer (nginx, traefik) to distribute traffic across instances.

## Clean Up

```bash
# Stop and remove containers
docker-compose down

# Remove volumes (WARNING: deletes all data)
docker-compose down -v

# Remove images
docker-compose down --rmi all
```

## Troubleshooting

### Registry won't start

Check logs:
```bash
docker-compose logs registry-filesystem
```

### Can't connect to S3

Verify credentials:
```bash
docker-compose exec registry-s3 env | grep AWS
```

### Provider not found

Check registry contents:
```bash
# Filesystem
docker-compose exec registry-filesystem ls -la /var/lib/terraform-registry/providers/

# S3
aws s3 ls s3://terraform-registry/providers/ --recursive
```
