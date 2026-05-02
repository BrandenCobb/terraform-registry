# Deployment Guide

Complete guide to deploying the S3-backed Terraform provider registry.

## Prerequisites

- AWS account with permissions to create S3 buckets and IAM roles
- EKS cluster with IRSA (IAM Roles for Service Accounts) configured
- kubectl configured to access your cluster
- aws-cli v2 installed
- Docker for building the registry server image

## Step-by-Step Deployment

### 1. Initialize S3 Bucket

```bash
cd provider-scripts
./init-s3-bucket.sh terraform-registry us-west-1
```

This creates the S3 bucket with:
- Versioning enabled
- Encryption enabled (AES256)
- Public access blocked
- `.well-known/terraform.json` initialized

### 2. Create IRSA Role

Get your EKS cluster OIDC provider ID:

```bash
aws eks describe-cluster --name YOUR_CLUSTER_NAME --query "cluster.identity.oidc.issuer" --output text
# Example output: https://oidc.eks.us-west-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B716D3041E
# OIDC_ID is the part after /id/
```

Create the IRSA role:

```bash
cd examples/kubernetes/manifests/irsa
./create-irsa-role.sh YOUR_ACCOUNT_ID YOUR_OIDC_ID terraform-registry
```

Update `examples/kubernetes/manifests/serviceaccount.yaml` with the role ARN output by the script.

### 3. Build Registry Server Image

```bash
cd registry-server

# Build image
docker build -t YOUR_ECR_REGISTRY/terraform-registry:latest .

# Push to ECR
aws ecr get-login-password --region us-west-1 | docker login --username AWS --password-stdin YOUR_ECR_REGISTRY
docker push YOUR_ECR_REGISTRY/terraform-registry:latest
```

### 4. Update Deployment Manifests

Edit `examples/kubernetes/manifests/deployment.yaml`:
- Replace `ACCOUNT_ID` with your AWS account ID
- Update ECR registry URL if different

Edit `examples/kubernetes/manifests/serviceaccount.yaml`:
- Replace `ACCOUNT_ID` with your AWS account ID

Edit `examples/kubernetes/manifests/ingress.yaml` or `examples/kubernetes/manifests/istio-virtualservice.yaml`:
- Update hostname to match your DNS

### 5. Deploy to Kubernetes

```bash
cd deploy

# Create namespace and deploy all resources
kubectl apply -f namespace.yaml
kubectl apply -f serviceaccount.yaml
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml

# Choose one: Ingress or Istio VirtualService
kubectl apply -f ingress.yaml
# OR
kubectl apply -f istio-virtualservice.yaml
```

### 6. Verify Deployment

```bash
# Check pod status
kubectl get pods -n terraform-registry

# Check logs
kubectl logs -n terraform-registry -l app=terraform-registry

# Test health endpoint
kubectl port-forward -n terraform-registry svc/terraform-registry 8080:80
curl http://localhost:8080/health
curl http://localhost:8080/.well-known/terraform.json
```

### 7. Configure DNS

Create a DNS record pointing to your ingress/gateway:

```bash
# For NGINX ingress
kubectl get ingress -n terraform-registry terraform-registry

# For Istio
kubectl get svc -n istio-system istio-ingressgateway
```

Point `terraform-registry.internal.example.com` to the load balancer IP/hostname.

## Uploading Providers

### Option 1: Manual Upload

```bash
cd provider-scripts
./upload-provider.sh \
  --bucket terraform-registry \
  --namespace hashicorp \
  --name aws \
  --version 6.31.0 \
  --binary /path/to/terraform-provider-aws_v6.31.0_x5
```

### Option 2: CI/CD Integration

Copy `provider-scripts/gitlab-ci-example.yml` to your provider build repository and customize:

```yaml
variables:
  PROVIDER_NAMESPACE: "hashicorp"
  PROVIDER_NAME: "aws"
  PROVIDER_VERSION: "6.31.0"
  S3_BUCKET: "terraform-registry"
```

## Client Configuration

### Network Mirror (Recommended)

Create or update `~/.terraformrc`:

```hcl
provider_installation {
  network_mirror {
    url = "https://terraform-registry.internal.example.com/"
    include = ["*/*"]
  }
}
```

### Filesystem Mirror (Airgap)

If you need airgap support, pre-populate the filesystem mirror:

```bash
# Download providers from registry to local cache
mkdir -p ~/.terraform.d/plugin-cache
terraform providers mirror ~/.terraform.d/plugin-cache
```

Update `~/.terraformrc`:

```hcl
provider_installation {
  filesystem_mirror {
    path    = "/home/deployer/.terraform.d/plugin-cache"
    include = ["*/*"]
  }
}
```

## Testing

Create a test Terraform configuration:

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.31.0"
    }
  }
}

provider "aws" {
  region = "us-west-1"
}
```

Run:

```bash
terraform init
# Should download providers from your registry
```

Check logs:

```bash
kubectl logs -n terraform-registry -l app=terraform-registry --tail=50
```

## Troubleshooting

### Provider not found

Check S3 bucket structure:

```bash
aws s3 ls s3://terraform-registry/ --recursive
```

Expected structure:
```
hashicorp/aws/index.json
hashicorp/aws/6.31.0/linux_amd64.json
hashicorp/aws/6.31.0/terraform-provider-aws_6.31.0_linux_amd64.zip
```

### IRSA not working

Verify service account annotation:

```bash
kubectl get sa -n terraform-registry terraform-registry -o yaml
```

Check pod has AWS credentials:

```bash
kubectl exec -n terraform-registry -it deployment/terraform-registry -- env | grep AWS
```

Should see:
- `AWS_ROLE_ARN`
- `AWS_WEB_IDENTITY_TOKEN_FILE`

### Presigned URL errors

Verify IRSA role has `s3:GetObject` permission:

```bash
aws iam get-role-policy --role-name terraform-registry-s3-access --policy-name S3Access
```

## Scaling

### Horizontal Scaling

Increase replicas in `examples/kubernetes/manifests/deployment.yaml`:

```yaml
spec:
  replicas: 5  # Increase as needed
```

Apply:

```bash
kubectl apply -f examples/kubernetes/manifests/deployment.yaml
```

### Caching

Add a caching layer (optional):

```yaml
# examples/kubernetes/manifests/nginx-cache.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nginx-cache-config
  namespace: terraform-registry
data:
  nginx.conf: |
    proxy_cache_path /var/cache/nginx levels=1:2 keys_zone=provider_cache:10m max_size=1g inactive=24h;
    
    server {
      location / {
        proxy_pass http://terraform-registry;
        proxy_cache provider_cache;
        proxy_cache_valid 200 24h;
      }
    }
```

## Monitoring

### Prometheus Metrics

Add Prometheus annotations to deployment:

```yaml
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
    prometheus.io/path: "/metrics"
```

### CloudWatch Logs

Enable CloudWatch logging for the registry pods using Fluent Bit or similar.

## Backup and Disaster Recovery

S3 bucket has versioning enabled. To restore a previous version:

```bash
aws s3api list-object-versions --bucket terraform-registry --prefix hashicorp/aws/
aws s3api get-object --bucket terraform-registry --key hashicorp/aws/6.31.0/... --version-id VERSION_ID output.zip
```

## Security Considerations

1. **TLS**: Ensure ingress uses valid TLS certificates
2. **Network Policies**: Restrict access to registry pods
3. **S3 Bucket Policies**: Lock down bucket to only IRSA role
4. **Audit Logging**: Enable S3 access logging and CloudTrail
5. **Vulnerability Scanning**: Scan provider binaries before upload

## Next Steps

- Set up automated provider builds in CI/CD
- Configure monitoring and alerting
- Document provider update procedures
- Create runbooks for common operations
