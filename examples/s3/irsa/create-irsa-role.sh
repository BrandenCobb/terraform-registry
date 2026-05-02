#!/bin/bash
set -e

ACCOUNT_ID="${1:-ACCOUNT_ID}"
OIDC_ID="${2:-OIDC_ID}"
BUCKET_NAME="${3:-terraform-registry}"
ROLE_NAME="terraform-registry-s3-access"

echo "Creating IRSA role for Terraform provider registry..."
echo "Account ID: ${ACCOUNT_ID}"
echo "OIDC Provider ID: ${OIDC_ID}"
echo "Bucket: ${BUCKET_NAME}"

# Substitute variables in trust policy
cat trust-policy.json | \
  sed "s/ACCOUNT_ID/${ACCOUNT_ID}/g" | \
  sed "s/OIDC_ID/${OIDC_ID}/g" > trust-policy-filled.json

# Substitute bucket name in S3 policy
cat s3-policy.json | \
  sed "s/terraform-registry/${BUCKET_NAME}/g" > s3-policy-filled.json

# Create IAM role
echo "Creating IAM role: ${ROLE_NAME}"
aws iam create-role \
  --role-name "${ROLE_NAME}" \
  --assume-role-policy-document file://trust-policy-filled.json \
  --description "IRSA role for Terraform provider registry to access S3"

# Attach S3 policy
echo "Attaching S3 policy to role"
aws iam put-role-policy \
  --role-name "${ROLE_NAME}" \
  --policy-name "S3Access" \
  --policy-document file://s3-policy-filled.json

echo "IRSA role created successfully!"
echo "Role ARN: arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"
echo ""
echo "Update deploy/serviceaccount.yaml with this ARN:"
echo "  eks.amazonaws.com/role-arn: arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"

# Clean up temporary files
rm -f trust-policy-filled.json s3-policy-filled.json
