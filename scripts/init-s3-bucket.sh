#!/bin/bash
set -e

BUCKET_NAME="${1:-terraform-registry}"
REGION="${2:-us-west-1}"

echo "Initializing S3 bucket for Terraform registry..."
echo "  Bucket: $BUCKET_NAME"
echo "  Region: $REGION"

# Create bucket
echo "Creating S3 bucket..."
aws s3 mb "s3://${BUCKET_NAME}" --region "${REGION}" || echo "Bucket may already exist"

# Enable versioning
echo "Enabling versioning..."
aws s3api put-bucket-versioning \
  --bucket "${BUCKET_NAME}" \
  --versioning-configuration Status=Enabled

# Enable encryption
echo "Enabling server-side encryption..."
aws s3api put-bucket-encryption \
  --bucket "${BUCKET_NAME}" \
  --server-side-encryption-configuration '{
    "Rules": [
      {
        "ApplyServerSideEncryptionByDefault": {
          "SSEAlgorithm": "AES256"
        },
        "BucketKeyEnabled": true
      }
    ]
  }'

# Block public access
echo "Blocking public access..."
aws s3api put-public-access-block \
  --bucket "${BUCKET_NAME}" \
  --public-access-block-configuration \
    "BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true"

# Create bucket structure for providers and modules
echo "Creating bucket directory structure..."
echo "providers/" | aws s3 cp - "s3://${BUCKET_NAME}/providers/.keep" --content-type "text/plain" || true
echo "modules/" | aws s3 cp - "s3://${BUCKET_NAME}/modules/.keep" --content-type "text/plain" || true

# Create .well-known/terraform.json for protocol discovery
echo "Creating .well-known/terraform.json..."
cat > /tmp/terraform.json <<EOF
{
  "providers.v1": "/v1/providers/",
  "modules.v1": "/v1/modules/"
}
EOF

aws s3 cp /tmp/terraform.json "s3://${BUCKET_NAME}/.well-known/terraform.json" \
  --content-type "application/json"

rm /tmp/terraform.json

echo ""
echo "S3 bucket initialized successfully!"
echo ""
echo "Bucket structure:"
echo "  s3://${BUCKET_NAME}/"
echo "    ├── .well-known/terraform.json   (protocol discovery)"
echo "    ├── providers/                   (Terraform providers)"
echo "    └── modules/                     (Terraform modules)"
echo ""
echo "Next steps:"
echo "  1. Deploy registry server with S3 backend:"
echo "     docker run -d -p 8080:8080 \\"
echo "       -e STORAGE_TYPE=s3 \\"
echo "       -e S3_BUCKET=${BUCKET_NAME} \\"
echo "       -e AWS_REGION=${REGION} \\"
echo "       -e BASE_URL=http://localhost:8080 \\"
echo "       ghcr.io/brandencobb/terraform-registry:latest"
echo ""
echo "  2. Upload providers:"
echo "     curl -O https://raw.githubusercontent.com/BrandenCobb/terraform-registry/main/scripts/upload-provider.sh"
echo "     chmod +x upload-provider.sh"
echo "     ./upload-provider.sh --storage s3 --bucket ${BUCKET_NAME} --region ${REGION} --help"
echo ""
echo "  3. Upload modules:"
echo "     curl -O https://raw.githubusercontent.com/BrandenCobb/terraform-registry/main/scripts/upload-module.sh"
echo "     chmod +x upload-module.sh"
echo "     ./upload-module.sh --storage s3 --bucket ${BUCKET_NAME} --region ${REGION} --help"
echo ""
