# Provider Build Guide

How to build and publish Terraform providers to the S3-backed registry.

## Overview

Each provider should have its own build repository that:
1. Clones the upstream provider source
2. Applies CVE fixes and dependency updates
3. Builds the provider binary
4. Publishes to S3 via the registry upload script

## Repository Structure

```
terraform-provider-aws-fips/
├── .gitlab-ci.yml          # CI/CD pipeline
├── build.sh                # Build script
├── patches/                # Optional patches
│   └── cve-fixes.patch
├── versions.yaml           # Provider and dependency versions
└── README.md
```

## Example: AWS Provider

### versions.yaml

```yaml
provider:
  name: aws
  namespace: hashicorp
  version: 6.31.0
  source: https://github.com/hashicorp/terraform-provider-aws.git

dependencies:
  - name: github.com/cloudflare/circl
    version: v1.6.3
    reason: CVE-2024-XXXXX fix
  - name: google.golang.org/grpc
    version: v1.79.3
    reason: CVE-2024-YYYYY fix
  - name: go.opentelemetry.io/otel
    version: v1.43.0
    reason: CVE-2024-ZZZZZ fix
```

### build.sh

```bash
#!/bin/bash
set -e

PROVIDER_VERSION="6.31.0"
PROVIDER_NAME="aws"

echo "Building terraform-provider-${PROVIDER_NAME} v${PROVIDER_VERSION}"

# Clone provider
git clone --depth 1 --branch v${PROVIDER_VERSION} \
  https://github.com/hashicorp/terraform-provider-${PROVIDER_NAME}.git

cd terraform-provider-${PROVIDER_NAME}

# Apply CVE fixes
go get github.com/cloudflare/circl@v1.6.3
go get google.golang.org/grpc@v1.79.3
go get go.opentelemetry.io/otel@v1.43.0

# AWS-specific fixes
go get github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream@v1.7.8
go get github.com/aws/aws-sdk-go-v2/service/bedrockagentcore@v1.15.2
go get github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime@v1.51.8
go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime@v1.50.4
go get github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs@v1.65.0
go get github.com/aws/aws-sdk-go-v2/service/iotsitewise@v1.52.19
go get github.com/aws/aws-sdk-go-v2/service/kinesis@v1.43.5
go get github.com/aws/aws-sdk-go-v2/service/lambda@v1.88.5
go get github.com/aws/aws-sdk-go-v2/service/lexruntimev2@v1.35.15
go get github.com/aws/aws-sdk-go-v2/service/s3@v1.97.3
go get github.com/aws/aws-sdk-go-v2/service/sagemakerruntime@v1.39.6
go get github.com/aws/aws-sdk-go-v2/service/transcribestreaming@v1.34.5

go mod tidy

# Build
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build \
  -ldflags="-X 'github.com/hashicorp/terraform-provider-aws/internal/provider.version=${PROVIDER_VERSION}' \
            -X 'github.com/hashicorp/terraform-provider-aws/version.ProviderVersion=${PROVIDER_VERSION}'" \
  -o ../terraform-provider-${PROVIDER_NAME}_v${PROVIDER_VERSION}_x5

cd ..
ls -lh terraform-provider-${PROVIDER_NAME}_v${PROVIDER_VERSION}_x5
```

### .gitlab-ci.yml

```yaml
variables:
  PROVIDER_NAMESPACE: "hashicorp"
  PROVIDER_NAME: "aws"
  PROVIDER_VERSION: "6.31.0"
  S3_BUCKET: "terraform-registry"

stages:
  - build
  - scan
  - publish

build:
  stage: build
  image: golang:1.26-alpine
  before_script:
    - apk add --no-cache git make bash
  script:
    - chmod +x build.sh
    - ./build.sh
  artifacts:
    paths:
      - terraform-provider-${PROVIDER_NAME}_v${PROVIDER_VERSION}_x5
    expire_in: 1 day

security-scan:
  stage: scan
  image: aquasec/trivy:latest
  dependencies:
    - build
  script:
    - trivy fs --severity HIGH,CRITICAL terraform-provider-${PROVIDER_NAME}_v${PROVIDER_VERSION}_x5
  allow_failure: true

publish:
  stage: publish
  image: 
    name: amazon/aws-cli:latest
    entrypoint: [""]
  dependencies:
    - build
  before_script:
    - yum install -y jq zip curl
  script:
    # Download upload script
    - curl -o upload-provider.sh https://raw.githubusercontent.com/BrandenCobb/terraform-registry/main/scripts/upload-provider.sh
    - chmod +x upload-provider.sh
    
    # Upload to S3
    - |
      ./upload-provider.sh \
        --bucket ${S3_BUCKET} \
        --namespace ${PROVIDER_NAMESPACE} \
        --name ${PROVIDER_NAME} \
        --version ${PROVIDER_VERSION} \
        --binary terraform-provider-${PROVIDER_NAME}_v${PROVIDER_VERSION}_x5 \
        --os linux \
        --arch amd64
  only:
    - main
    - tags
```

## Multi-Platform Builds

Build for multiple OS/architectures:

```yaml
build-multi:
  stage: build
  image: golang:1.26-alpine
  parallel:
    matrix:
      - GOOS: linux
        GOARCH: amd64
      - GOOS: linux
        GOARCH: arm64
      - GOOS: darwin
        GOARCH: amd64
      - GOOS: darwin
        GOARCH: arm64
  script:
    - apk add --no-cache git make bash
    - export GOOS GOARCH
    - ./build.sh
    - mv terraform-provider-${PROVIDER_NAME}_v${PROVIDER_VERSION}_x5 \
         terraform-provider-${PROVIDER_NAME}_v${PROVIDER_VERSION}_${GOOS}_${GOARCH}
  artifacts:
    name: "provider-${GOOS}-${GOARCH}"
    paths:
      - terraform-provider-*_v*_*_*
    expire_in: 1 day

publish-multi:
  stage: publish
  dependencies:
    - build-multi
  script:
    - |
      for binary in terraform-provider-*_v*_*_*; do
        # Extract OS and ARCH from filename
        PLATFORM=$(echo $binary | sed 's/.*_v[0-9.]*_\(.*\)/\1/')
        OS=$(echo $PLATFORM | cut -d_ -f1)
        ARCH=$(echo $PLATFORM | cut -d_ -f2)
        
        ./upload-provider.sh \
          --bucket ${S3_BUCKET} \
          --namespace ${PROVIDER_NAMESPACE} \
          --name ${PROVIDER_NAME} \
          --version ${PROVIDER_VERSION} \
          --binary $binary \
          --os $OS \
          --arch $ARCH
      done
```

## Provider List

Recommended providers to build:

### Core Providers
- `hashicorp/aws` (6.31.0)
- `hashicorp/kubernetes` (3.0.1)
- `gavinbunney/kubectl` (1.19.0)
- `hashicorp/local` (2.6.2)
- `hashicorp/null` (3.2.4)
- `hashicorp/random` (3.8.1)
- `hashicorp/time` (0.13.1)
- `hashicorp/tls` (4.2.1)

### Cloud Providers
- `databricks/databricks` (1.105.0)

### Specialized Providers
- `keycloak/keycloak` (5.6.0)
- `cyrilgdn/postgresql` (1.26.0)
- `hashicorp/vault` (4.6.2)

## Version Management

Track provider versions in a central manifest:

```yaml
# provider-manifest.yaml
providers:
  - namespace: hashicorp
    name: aws
    version: 6.31.0
    repository: https://gitlab.internal/terraform-providers/terraform-provider-aws-fips
    
  - namespace: hashicorp
    name: kubernetes
    version: 3.0.1
    repository: https://gitlab.internal/terraform-providers/terraform-provider-kubernetes-fips
    
  - namespace: databricks
    name: databricks
    version: 1.105.0
    repository: https://gitlab.internal/terraform-providers/terraform-provider-databricks-fips
```

## Testing Providers

Before publishing, test the provider binary:

```bash
# Create test directory
mkdir test-provider && cd test-provider

# Create test configuration
cat > main.tf <<EOF
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.31.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

data "aws_caller_identity" "current" {}

output "account_id" {
  value = data.aws_caller_identity.current.account_id
}
EOF

# Set up local filesystem mirror
mkdir -p .terraform/providers/registry.terraform.io/hashicorp/aws/6.31.0/linux_amd64
cp ../terraform-provider-aws_v6.31.0_x5 \
   .terraform/providers/registry.terraform.io/hashicorp/aws/6.31.0/linux_amd64/
chmod +x .terraform/providers/registry.terraform.io/hashicorp/aws/6.31.0/linux_amd64/terraform-provider-aws_v6.31.0_x5

# Test
terraform init
terraform plan
```

## CI/CD Best Practices

1. **Version Pinning**: Always pin exact versions in `versions.yaml`
2. **CVE Scanning**: Run Trivy or similar on built binaries
3. **Automated Updates**: Use Renovate or Dependabot to track upstream versions
4. **Build Caching**: Cache Go modules between builds
5. **Artifact Storage**: Keep build artifacts for troubleshooting
6. **Provenance**: Sign artifacts with GPG (optional)

## Troubleshooting

### Build fails with missing dependencies

```bash
# Check go.mod in provider repo
go mod graph | grep <dependency>

# Verify version availability
go get <dependency>@<version>
```

### CVE in transitive dependency

```bash
# Find dependency chain
go mod why <dependency>

# Try updating the parent dependency
go get <parent-dependency>@latest
go mod tidy
```

### Provider binary too large

```bash
# Strip debug symbols
go build -ldflags="-s -w" -o provider

# Compress with upx (optional)
upx --best provider
```

## Automation

Create a meta-repository that builds all providers:

```yaml
# build-all-providers.yml
workflow:
  rules:
    - if: '$CI_PIPELINE_SOURCE == "schedule"'

include:
  - project: terraform-providers/terraform-provider-aws-fips
    file: .gitlab-ci.yml
  - project: terraform-providers/terraform-provider-kubernetes-fips
    file: .gitlab-ci.yml
  - project: terraform-providers/terraform-provider-databricks-fips
    file: .gitlab-ci.yml
```

Schedule weekly builds to catch new CVEs early.
