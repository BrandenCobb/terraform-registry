# Quick Start Guide

Get up and running with Terraform Registry in 5 minutes.

## Prerequisites

- Docker and Docker Compose installed
- `curl` or `wget`
- `jq` for JSON parsing (optional)
- Terraform 1.5+ (for testing)
- **For Terraform integration:** mkcert for HTTPS (see [HTTPS Setup](HTTPS.md))

## Option 1: Docker (Recommended)

### 1. Start the Registry

```bash
docker run -d \
  --name terraform-registry \
  -p 5000:8080 \
  -v $(pwd)/registry-data:/var/lib/terraform-registry \
  ghcr.io/brandencobb/terraform-registry:latest
```

### 2. Verify It's Running

```bash
curl http://localhost:5000/health
# Should return: OK

curl http://localhost:5000/.well-known/terraform.json
# Should return JSON with providers_v1 and modules_v1
```

### 3. Upload a Provider

Create a test provider binary:
```bash
mkdir -p test-files
echo "test provider" > test-files/terraform-provider-test_v1.0.0_x5
chmod +x test-files/terraform-provider-test_v1.0.0_x5
```

Upload it:
```bash
./scripts/upload-provider.sh \
  --storage filesystem \
  --path ./registry-data \
  --namespace example \
  --name test \
  --version 1.0.0 \
  --binary test-files/terraform-provider-test_v1.0.0_x5
```

### 4. Upload a Module

Create a test module:
```bash
mkdir -p test-module
cat > test-module/main.tf <<EOF
variable "name" {
  description = "Resource name"
  type        = string
}

output "name" {
  value = var.name
}
EOF
```

Upload it:
```bash
./scripts/upload-module.sh \
  --storage filesystem \
  --path ./registry-data \
  --namespace example \
  --name test-module \
  --provider aws \
  --version 1.0.0 \
  --source test-module/
```

### 5. Enable HTTPS (Required for Terraform)

**⚠️ Important:** Terraform requires HTTPS for network mirrors. See the [HTTPS Setup Guide](HTTPS.md) for complete instructions.

Quick HTTPS setup:
```bash
# Install mkcert (one-time setup)
wget https://github.com/FiloSottile/mkcert/releases/download/v1.4.4/mkcert-v1.4.4-linux-amd64
chmod +x mkcert-v1.4.4-linux-amd64
sudo mv mkcert-v1.4.4-linux-amd64 /usr/local/bin/mkcert
mkcert -install

# Generate certificates
mkcert registry.local localhost 127.0.0.1

# Add to /etc/hosts
echo "127.0.0.1 registry.local" | sudo tee -a /etc/hosts

# Restart with HTTPS proxy
docker-compose down
docker-compose up -d  # Starts registry + Caddy proxy
```

### 6. Configure Terraform

Create `~/.terraformrc`:
```hcl
provider_installation {
  network_mirror {
    url = "https://registry.local/"  # HTTPS required
    include = ["*/*"]
  }
}
```

### 7. Test with Terraform

Create `test.tf`:
```hcl
terraform {
  required_providers {
    test = {
      source  = "example/test"
      version = "1.0.0"
    }
  }
}

module "example" {
  source  = "example/test-module/aws"
  version = "1.0.0"

  name = "test"
}
```

Run Terraform:
```bash
terraform init
# Should download from your local registry!
```

## Option 2: Docker Compose

### 1. Create `docker-compose.yml`

```yaml
version: '3.8'

services:
  terraform-registry:
    image: ghcr.io/brandencobb/terraform-registry:latest
    ports:
      - "5000:8080"
    volumes:
      - ./registry-data:/var/lib/terraform-registry
    environment:
      - STORAGE_TYPE=filesystem
      - BASE_URL=http://localhost:5000
    restart: unless-stopped
```

### 2. Start

```bash
docker-compose up -d
```

### 3. Follow steps 2-6 from Option 1

## Option 3: Build from Source

### 1. Clone Repository

```bash
git clone https://github.com/BrandenCobb/terraform-registry.git
cd terraform-registry
```

### 2. Build

```bash
make build
```

### 3. Run

```bash
make run
```

Registry starts on `http://localhost:8080`

### 4. Follow steps 2-6 from Option 1 (use port 8080)

## Next Steps

### Production Deployment

- [Kubernetes Deployment](DEPLOYMENT.md#kubernetes)
- [AWS S3 Backend](DEPLOYMENT.md#s3-storage)
- [Security Configuration](../SECURITY.md)

### Building Providers

- [Provider Build Guide](PROVIDER_BUILD.md)
- [CI/CD Integration](PROVIDER_BUILD.md#cicd-pipeline)

### Configuration

- [Environment Variables](CONFIGURATION.md)
- [Storage Backends](CONFIGURATION.md#storage)

## Troubleshooting

### Registry Not Starting

Check logs:
```bash
docker logs terraform-registry
```

### Provider Not Found

Verify upload:
```bash
ls -R registry-data/providers/
```

Check registry:
```bash
curl http://localhost:5000/v1/providers/example/test/versions
```

### Permission Errors

Fix data directory permissions:
```bash
sudo chown -R $USER:$USER registry-data/
chmod -R 755 registry-data/
```

### Terraform Init Fails

Check `.terraformrc` configuration:
```bash
cat ~/.terraformrc
```

Enable debug logging:
```bash
TF_LOG=DEBUG terraform init
```

## Common Use Cases

### Local Development

Perfect for testing custom providers before publishing:

```bash
# Build your provider
cd my-terraform-provider
go build -o terraform-provider-myprovider

# Upload to local registry
../terraform-registry/scripts/upload-provider.sh \
  --storage filesystem \
  --path ../registry-data \
  --namespace myorg \
  --name myprovider \
  --version 0.1.0-dev \
  --binary terraform-provider-myprovider

# Test immediately in Terraform
cd ../my-terraform-project
terraform init
```

### Sharing Modules

Share modules across your team:

```bash
# Developer A: Publish module
./scripts/upload-module.sh \
  --storage filesystem \
  --path /shared/registry \
  --namespace myteam \
  --name networking \
  --provider aws \
  --version 2.0.0 \
  --source ./modules/networking

# Developer B: Use module
# In terraform code:
module "network" {
  source  = "myteam/networking/aws"
  version = "2.0.0"
}
```

### Air-Gapped Environments

Pre-populate registry, then transfer:

```bash
# On internet-connected machine
docker run -v ./registry-data:/var/lib/terraform-registry terraform-registry:latest
# Upload all providers and modules
./populate-registry.sh

# Transfer registry-data/ to air-gapped environment
tar czf registry-data.tar.gz registry-data/
# ... transfer to air-gapped network ...

# On air-gapped machine
tar xzf registry-data.tar.gz
docker run -v ./registry-data:/var/lib/terraform-registry terraform-registry:latest
```

## Getting Help

- 📖 [Full Documentation](../README.md)
- 🐛 [Report Issues](https://github.com/BrandenCobb/terraform-registry/issues)
- 💬 [Discussions](https://github.com/BrandenCobb/terraform-registry/discussions)
