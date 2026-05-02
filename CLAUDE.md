# CLAUDE.md

Project-specific guidance for the S3-backed Terraform provider registry.

## Project Overview

A self-hosted Terraform provider registry backed by S3, designed for AWS GovCloud environments. Providers are built separately, stored in S3, and served via a lightweight Go HTTP server running in EKS with IRSA for secure S3 access.

## Architecture

- **Registry Server**: Go application implementing Terraform's provider protocol
- **Storage**: S3 bucket with versioning and encryption
- **Deployment**: Kubernetes (EKS) with IRSA for S3 access
- **Client**: Terraform uses network_mirror to fetch providers from registry

## Key Files

- [registry-server/main.go](registry-server/main.go): Go server implementing provider protocol
- [provider-scripts/upload-provider.sh](provider-scripts/upload-provider.sh): Script to upload providers to S3
- [examples/kubernetes/manifests/](examples/kubernetes/manifests/): Kubernetes manifests for EKS deployment
- [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md): Full deployment guide
- [docs/PROVIDER_BUILD.md](docs/PROVIDER_BUILD.md): Guide for building providers

## Common Tasks

### Build and Deploy Registry Server

```bash
make build
make push
make deploy
```

### Initialize S3 Bucket

```bash
make init-s3 S3_BUCKET=terraform-registry
```

### Upload a Provider

```bash
cd provider-scripts
./upload-provider.sh \
  --bucket terraform-registry \
  --namespace hashicorp \
  --name aws \
  --version 6.31.0 \
  --binary /path/to/terraform-provider-aws_v6.31.0_x5
```

### Test Registry

```bash
kubectl port-forward -n terraform-registry svc/terraform-registry 8080:80
curl http://localhost:8080/.well-known/terraform.json
curl http://localhost:8080/v1/providers/hashicorp/aws/versions
```

## S3 Bucket Structure

```
s3://terraform-registry/
├── .well-known/terraform.json          # Protocol discovery
├── {namespace}/{name}/
│   ├── index.json                      # Version list
│   └── {version}/
│       ├── {os}_{arch}.json            # Platform metadata (filename + shasum)
│       └── terraform-provider-{name}_{version}_{os}_{arch}.zip
```

## Terraform Provider Protocol

The registry implements these endpoints:

1. `/.well-known/terraform.json` - Discovery endpoint
2. `/v1/providers/{namespace}/{type}/versions` - List available versions
3. `/v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}` - Download metadata with presigned S3 URL

## IRSA Configuration

The registry uses IRSA (IAM Roles for Service Accounts) to access S3 without static credentials:

1. EKS OIDC provider trusts the service account
2. Service account has annotation with IAM role ARN
3. IAM role has policy allowing S3 access
4. Pods automatically get temporary credentials via AWS SDK

## Development

### Run Locally

```bash
cd registry-server
export S3_BUCKET=terraform-registry
export AWS_REGION=us-west-1
go run main.go
```

Access at http://localhost:8080

### Add New Provider

1. Create provider build repo (see [docs/PROVIDER_BUILD.md](docs/PROVIDER_BUILD.md))
2. Build provider binary with CVE fixes
3. Upload to S3 using `upload-provider.sh`
4. Provider is immediately available via registry

## Security

- S3 bucket has versioning, encryption, and public access blocked
- Registry uses IRSA (no static credentials)
- Providers validated with SHA256 checksums
- Optional: GPG signing for provider artifacts
- Network policies restrict registry pod access

## Troubleshooting

### Provider not found

Check S3 structure:
```bash
aws s3 ls s3://terraform-registry/{namespace}/{name}/ --recursive
```

### IRSA not working

Verify service account:
```bash
kubectl get sa -n terraform-registry terraform-registry -o yaml
kubectl describe pod -n terraform-registry -l app=terraform-registry
```

Check for AWS environment variables in pod:
```bash
kubectl exec -n terraform-registry -it deployment/terraform-registry -- env | grep AWS
```

### Presigned URL errors

Verify IAM role policy allows `s3:GetObject`:
```bash
aws iam get-role-policy --role-name terraform-registry-s3-access --policy-name S3Access
```

## Integration with adv12-deployer

This registry is designed to work with the `adv12-deployer` container image. Instead of baking providers into the deployer image, they can be pulled at runtime:

1. Build providers separately (faster, parallel builds)
2. Upload to S3 registry
3. Configure `.terraformrc` in deployer to use network_mirror
4. Terraform pulls providers on-demand during `terraform init`

Benefits:
- Smaller deployer image (~500MB vs 2GB+)
- Faster deployer builds (5-10 min vs 60 min)
- Provider versions can be updated without rebuilding deployer
- Different projects can use different provider versions

## Related Projects

- [adv12-deployer](../adv12-deployer): Container image using these providers
- Provider build repos: Separate repos for each provider with CI/CD
