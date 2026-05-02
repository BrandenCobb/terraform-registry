# Filesystem Storage Example

This example shows how to run the registry with filesystem storage for local development.

## Setup

### 1. Start the Registry

```bash
docker run -d \
  --name terraform-registry \
  -p 5000:8080 \
  -v $(pwd)/registry-data:/var/lib/terraform-registry \
  -e STORAGE_TYPE=filesystem \
  -e BASE_URL=http://localhost:5000 \
  ghcr.io/brandencobb/terraform-registry:latest
```

### 2. Configure Terraform

Create `~/.terraformrc`:

```hcl
provider_installation {
  network_mirror {
    url = "http://localhost:5000"
  }
}
```

### 3. Upload a Provider

```bash
# Download upload script
curl -O https://raw.githubusercontent.com/BrandenCobb/terraform-registry/main/scripts/upload-provider.sh
chmod +x upload-provider.sh

# Upload provider
./upload-provider.sh \
  --storage filesystem \
  --path ./registry-data \
  --namespace hashicorp \
  --name random \
  --version 3.5.1 \
  --binary ./terraform-provider-random_v3.5.1_x5 \
  --os linux \
  --arch amd64
```

### 4. Use in Terraform

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

## Directory Structure

After uploading a provider, your `registry-data/` directory will look like:

```
registry-data/
├── providers/
│   └── hashicorp/
│       └── random/
│           ├── index.json
│           └── 3.5.1/
│               ├── linux_amd64.json
│               └── terraform-provider-random_v3.5.1_linux_amd64.zip
└── modules/
```

## Advantages

- ✅ Simple setup
- ✅ No cloud dependencies
- ✅ Easy to inspect registry contents
- ✅ Fast for local development

## Limitations

- ⚠️ Single host only (not suitable for multi-node)
- ⚠️ No built-in backups
- ⚠️ Manual volume management

## Clean Up

```bash
docker stop terraform-registry
docker rm terraform-registry
rm -rf registry-data
```
