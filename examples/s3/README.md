# S3 Storage Example

This example shows how to run the registry with S3 storage for production deployments.

## Prerequisites

- AWS account with S3 access
- AWS CLI configured
- IAM permissions to create S3 buckets

## Setup

### 1. Create S3 Bucket

```bash
# Download initialization script
curl -O https://raw.githubusercontent.com/BrandenCobb/terraform-registry/main/scripts/init-s3-bucket.sh
chmod +x init-s3-bucket.sh

# Initialize bucket
./init-s3-bucket.sh terraform-registry us-east-1
```

This creates a bucket with:
- Versioning enabled
- Server-side encryption (AES256)
- Public access blocked
- Proper directory structure

### 2. Start the Registry

**With AWS credentials:**

```bash
docker run -d \
  --name terraform-registry \
  -p 5000:8080 \
  -e STORAGE_TYPE=s3 \
  -e S3_BUCKET=terraform-registry \
  -e AWS_REGION=us-east-1 \
  -e BASE_URL=https://registry.example.com \
  -e AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID} \
  -e AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY} \
  ghcr.io/brandencobb/terraform-registry:latest
```

**With IAM role (recommended):**

```bash
# On EC2 instance or ECS with IAM role
docker run -d \
  --name terraform-registry \
  -p 5000:8080 \
  -e STORAGE_TYPE=s3 \
  -e S3_BUCKET=terraform-registry \
  -e AWS_REGION=us-east-1 \
  -e BASE_URL=https://registry.example.com \
  ghcr.io/brandencobb/terraform-registry:latest
```

### 3. Configure Terraform

Create `~/.terraformrc`:

```hcl
provider_installation {
  network_mirror {
    url = "https://registry.example.com"
  }
}
```

### 4. Upload a Provider

```bash
# Download upload script
curl -O https://raw.githubusercontent.com/BrandenCobb/terraform-registry/main/scripts/upload-provider.sh
chmod +x upload-provider.sh

# Upload provider
./upload-provider.sh \
  --storage s3 \
  --bucket terraform-registry \
  --region us-east-1 \
  --namespace hashicorp \
  --name aws \
  --version 6.31.0 \
  --binary ./terraform-provider-aws_v6.31.0_linux_amd64 \
  --os linux \
  --arch amd64
```

### 5. Use in Terraform

Create `main.tf`:

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
  region = "us-east-1"
}

data "aws_caller_identity" "current" {}

output "account_id" {
  value = data.aws_caller_identity.current.account_id
}
```

Run:

```bash
terraform init
terraform apply
```

## IAM Policy

Create an IAM policy for the registry:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:ListBucket"
      ],
      "Resource": [
        "arn:aws:s3:::terraform-registry",
        "arn:aws:s3:::terraform-registry/*"
      ]
    }
  ]
}
```

## S3 Bucket Structure

```
s3://terraform-registry/
├── .well-known/terraform.json
├── providers/
│   └── hashicorp/
│       └── aws/
│           ├── index.json
│           └── 6.31.0/
│               ├── linux_amd64.json
│               └── terraform-provider-aws_v6.31.0_linux_amd64.zip
└── modules/
```

## Advantages

- ✅ Highly available and durable
- ✅ Automatic backups with versioning
- ✅ Scales to multiple registry instances
- ✅ No local storage management
- ✅ Presigned URLs for secure downloads

## Cost Optimization

- Use S3 Intelligent-Tiering for automatic cost optimization
- Enable S3 lifecycle policies to archive old versions
- Use S3 Transfer Acceleration for faster uploads (if needed)

## Monitoring

Monitor these S3 metrics:
- `NumberOfObjects` - Total artifacts stored
- `BucketSizeBytes` - Storage usage
- `AllRequests` - Traffic volume
- `4xxErrors`, `5xxErrors` - Error rates

## Clean Up

```bash
# Stop registry
docker stop terraform-registry
docker rm terraform-registry

# Delete S3 bucket (WARNING: deletes all data)
aws s3 rb s3://terraform-registry --force
```
