# IRSA Setup for S3 Storage

This directory contains scripts and policies for setting up IAM Roles for Service Accounts (IRSA) to allow the registry to access S3 without static credentials.

## Files

- **create-irsa-role.sh** - Script to create IAM role and associate with Kubernetes ServiceAccount
- **s3-policy.json** - IAM policy granting S3 access
- **trust-policy.json** - Trust relationship allowing EKS to assume the role

## Prerequisites

- EKS cluster with OIDC provider enabled
- AWS CLI configured with admin permissions
- `eksctl` or `kubectl` installed
- S3 bucket already created

## Quick Setup

### Option 1: Using eksctl (Recommended)

```bash
# Set variables
export CLUSTER_NAME="my-cluster"
export REGION="us-east-1"
export BUCKET_NAME="terraform-registry"
export NAMESPACE="terraform-registry"

# Create IRSA in one command
eksctl create iamserviceaccount \
  --name terraform-registry \
  --namespace ${NAMESPACE} \
  --cluster ${CLUSTER_NAME} \
  --region ${REGION} \
  --attach-policy-arn arn:aws:iam::$(aws sts get-caller-identity --query Account --output text):policy/TerraformRegistryS3Access \
  --approve \
  --override-existing-serviceaccounts

# If policy doesn't exist, create it first
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

# Update s3-policy.json with your bucket name, then:
aws iam create-policy \
  --policy-name TerraformRegistryS3Access \
  --policy-document file://s3-policy.json
```

### Option 2: Using the Script

```bash
# Make script executable
chmod +x create-irsa-role.sh

# Run with your parameters
./create-irsa-role.sh \
  my-cluster \
  us-east-1 \
  terraform-registry \
  terraform-registry
```

## Manual Setup

### 1. Enable OIDC Provider (if not already enabled)

```bash
eksctl utils associate-iam-oidc-provider \
  --cluster ${CLUSTER_NAME} \
  --region ${REGION} \
  --approve
```

### 2. Create IAM Policy

Update `s3-policy.json` with your bucket name, then:

```bash
aws iam create-policy \
  --policy-name TerraformRegistryS3Access \
  --policy-document file://s3-policy.json
```

### 3. Create IAM Role

Get your OIDC provider:

```bash
OIDC_PROVIDER=$(aws eks describe-cluster \
  --name ${CLUSTER_NAME} \
  --region ${REGION} \
  --query "cluster.identity.oidc.issuer" \
  --output text | sed -e "s/^https:\/\///")
```

Update `trust-policy.json` with:
- Your account ID
- Your OIDC provider URL
- Your namespace

Create the role:

```bash
aws iam create-role \
  --role-name TerraformRegistryS3Role \
  --assume-role-policy-document file://trust-policy.json
```

### 4. Attach Policy to Role

```bash
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

aws iam attach-role-policy \
  --role-name TerraformRegistryS3Role \
  --policy-arn arn:aws:iam::${ACCOUNT_ID}:policy/TerraformRegistryS3Access
```

### 5. Create Kubernetes ServiceAccount

Create `serviceaccount.yaml` (or use the one in `../kubernetes/manifests/`):

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: terraform-registry
  namespace: terraform-registry
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::ACCOUNT_ID:role/TerraformRegistryS3Role
```

Apply:

```bash
kubectl apply -f serviceaccount.yaml
```

### 6. Update Deployment

Ensure your deployment uses the ServiceAccount:

```yaml
spec:
  template:
    spec:
      serviceAccountName: terraform-registry
      containers:
      - name: registry
        env:
        - name: STORAGE_TYPE
          value: "s3"
        - name: S3_BUCKET
          value: "terraform-registry"
        - name: AWS_REGION
          value: "us-east-1"
```

## Verification

### Test IAM Role

```bash
# Deploy a test pod
kubectl run aws-cli \
  --image=amazon/aws-cli:latest \
  --serviceaccount=terraform-registry \
  --namespace=terraform-registry \
  --rm -it --restart=Never \
  -- sts get-caller-identity

# Should show the IRSA role ARN
```

### Test S3 Access

```bash
kubectl run aws-cli \
  --image=amazon/aws-cli:latest \
  --serviceaccount=terraform-registry \
  --namespace=terraform-registry \
  --rm -it --restart=Never \
  -- s3 ls s3://terraform-registry/

# Should list bucket contents
```

### Check Registry Logs

```bash
kubectl logs -n terraform-registry -l app=terraform-registry

# Should see: "Using S3 storage: bucket=terraform-registry region=us-east-1"
```

## Troubleshooting

### Role Not Assumed

Check that:
1. OIDC provider is enabled on cluster
2. ServiceAccount has correct annotation
3. Pod is using the ServiceAccount
4. Trust policy allows the ServiceAccount

### Access Denied to S3

Check that:
1. S3 policy grants required permissions
2. Bucket name matches in policy and deployment
3. Region is correct

### Environment Variables

Verify AWS credentials are injected:

```bash
kubectl exec -n terraform-registry deployment/terraform-registry -- env | grep AWS

# Should see:
# AWS_ROLE_ARN=arn:aws:iam::ACCOUNT_ID:role/TerraformRegistryS3Role
# AWS_WEB_IDENTITY_TOKEN_FILE=/var/run/secrets/eks.amazonaws.com/serviceaccount/token
```

## Cleanup

```bash
# Delete ServiceAccount
kubectl delete serviceaccount terraform-registry -n terraform-registry

# Detach policy from role
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
aws iam detach-role-policy \
  --role-name TerraformRegistryS3Role \
  --policy-arn arn:aws:iam::${ACCOUNT_ID}:policy/TerraformRegistryS3Access

# Delete role
aws iam delete-role --role-name TerraformRegistryS3Role

# Delete policy
aws iam delete-policy \
  --policy-arn arn:aws:iam::${ACCOUNT_ID}:policy/TerraformRegistryS3Access
```

## Security Best Practices

1. **Least Privilege**: Only grant necessary S3 permissions
2. **Bucket Policy**: Add bucket policy to restrict access to IRSA role only
3. **Encryption**: Enable S3 bucket encryption
4. **Audit**: Enable CloudTrail for S3 API calls
5. **Rotation**: IRSA tokens rotate automatically (no manual rotation needed)

## See Also

- [AWS IRSA Documentation](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
- [S3 Example Documentation](../README.md)
- [Kubernetes Manifests](../../kubernetes/manifests/)
