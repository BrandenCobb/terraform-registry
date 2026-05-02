#!/bin/bash
set -e

# Usage: ./upload-provider.sh --storage STORAGE_TYPE [--bucket BUCKET | --path PATH] --namespace NAMESPACE --name NAME --version VERSION --binary BINARY_PATH [--os OS] [--arch ARCH]

STORAGE_TYPE="filesystem"
STORAGE_PATH=""
BUCKET=""
NAMESPACE=""
NAME=""
VERSION=""
BINARY=""
OS="linux"
ARCH="amd64"

while [[ $# -gt 0 ]]; do
  case $1 in
    --storage)
      STORAGE_TYPE="$2"
      shift 2
      ;;
    --path)
      STORAGE_PATH="$2"
      shift 2
      ;;
    --bucket)
      BUCKET="$2"
      shift 2
      ;;
    --namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    --name)
      NAME="$2"
      shift 2
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    --binary)
      BINARY="$2"
      shift 2
      ;;
    --os)
      OS="$2"
      shift 2
      ;;
    --arch)
      ARCH="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

if [[ -z "$NAMESPACE" || -z "$NAME" || -z "$VERSION" || -z "$BINARY" ]]; then
  echo "Usage: $0 --storage STORAGE_TYPE [--bucket BUCKET | --path PATH] --namespace NAMESPACE --name NAME --version VERSION --binary BINARY_PATH [--os OS] [--arch ARCH]"
  echo ""
  echo "Example (S3):"
  echo "  $0 --storage s3 --bucket terraform-registry --namespace hashicorp --name aws --version 6.31.0 --binary terraform-provider-aws_v6.31.0_x5"
  echo ""
  echo "Example (Filesystem):"
  echo "  $0 --storage filesystem --path ./data --namespace hashicorp --name aws --version 6.31.0 --binary terraform-provider-aws_v6.31.0_x5"
  exit 1
fi

if [[ "$STORAGE_TYPE" == "s3" && -z "$BUCKET" ]]; then
  echo "Error: --bucket required for S3 storage"
  exit 1
fi

if [[ "$STORAGE_TYPE" == "filesystem" && -z "$STORAGE_PATH" ]]; then
  echo "Error: --path required for filesystem storage"
  exit 1
fi

if [[ ! -f "$BINARY" ]]; then
  echo "Error: Binary file not found: $BINARY"
  exit 1
fi

echo "Uploading provider..."
echo "  Storage: $STORAGE_TYPE"
if [[ "$STORAGE_TYPE" == "s3" ]]; then
  echo "  Bucket: $BUCKET"
else
  echo "  Path: $STORAGE_PATH"
fi
echo "  Provider: $NAMESPACE/$NAME@$VERSION"
echo "  Platform: $OS/$ARCH"
echo "  Binary: $BINARY"

# Create temporary directory
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

FILENAME="terraform-provider-${NAME}_${VERSION}_${OS}_${ARCH}.zip"
PLATFORM="${OS}_${ARCH}"

# Zip the provider binary
echo "Creating zip archive..."
cp "$BINARY" "$TMPDIR/terraform-provider-${NAME}_v${VERSION}_x5"
cd "$TMPDIR"
zip "$FILENAME" "terraform-provider-${NAME}_v${VERSION}_x5"

# Calculate SHA256
echo "Calculating SHA256 checksum..."
SHASUM=$(sha256sum "$FILENAME" | awk '{print $1}')
echo "  SHA256: $SHASUM"

# Create metadata JSON
cat > "${PLATFORM}.json" <<EOF
{
  "filename": "$FILENAME",
  "shasum": "$SHASUM"
}
EOF

if [[ "$STORAGE_TYPE" == "s3" ]]; then
  # Upload files to S3
  S3_PREFIX="providers/${NAMESPACE}/${NAME}/${VERSION}"
  echo "Uploading to s3://${BUCKET}/${S3_PREFIX}/"

  aws s3 cp "$FILENAME" "s3://${BUCKET}/${S3_PREFIX}/$FILENAME"
  aws s3 cp "${PLATFORM}.json" "s3://${BUCKET}/${S3_PREFIX}/${PLATFORM}.json"

  echo "Provider uploaded successfully!"

  # Update index.json
  echo "Updating provider index..."
  INDEX_KEY="providers/${NAMESPACE}/${NAME}/index.json"
  INDEX_FILE="$TMPDIR/index.json"

  # Download existing index or create new one
  if aws s3 cp "s3://${BUCKET}/${INDEX_KEY}" "$INDEX_FILE" 2>/dev/null; then
    echo "  Found existing index"
    # Add version if not already present
    VERSIONS=$(jq -r '.versions[]' "$INDEX_FILE")
    if echo "$VERSIONS" | grep -q "^${VERSION}$"; then
      echo "  Version $VERSION already in index"
    else
      echo "  Adding version $VERSION to index"
      jq ".versions += [\"${VERSION}\"] | .versions |= sort" "$INDEX_FILE" > "$INDEX_FILE.tmp"
      mv "$INDEX_FILE.tmp" "$INDEX_FILE"
    fi
  else
    echo "  Creating new index"
    cat > "$INDEX_FILE" <<EOF
{
  "versions": ["$VERSION"]
}
EOF
  fi

  # Upload updated index
  aws s3 cp "$INDEX_FILE" "s3://${BUCKET}/${INDEX_KEY}"

else
  # Filesystem storage
  PROVIDER_DIR="${STORAGE_PATH}/providers/${NAMESPACE}/${NAME}/${VERSION}"
  echo "Uploading to filesystem: ${PROVIDER_DIR}/"

  # Create directories
  mkdir -p "$PROVIDER_DIR"

  # Copy files
  cp "$FILENAME" "$PROVIDER_DIR/$FILENAME"
  cp "${PLATFORM}.json" "$PROVIDER_DIR/${PLATFORM}.json"

  echo "Provider uploaded successfully!"

  # Update index.json
  echo "Updating provider index..."
  INDEX_FILE="${STORAGE_PATH}/providers/${NAMESPACE}/${NAME}/index.json"

  if [[ -f "$INDEX_FILE" ]]; then
    echo "  Found existing index"
    # Add version if not already present
    VERSIONS=$(jq -r '.versions[]' "$INDEX_FILE")
    if echo "$VERSIONS" | grep -q "^${VERSION}$"; then
      echo "  Version $VERSION already in index"
    else
      echo "  Adding version $VERSION to index"
      jq ".versions += [\"${VERSION}\"] | .versions |= sort" "$INDEX_FILE" > "$INDEX_FILE.tmp"
      mv "$INDEX_FILE.tmp" "$INDEX_FILE"
    fi
  else
    echo "  Creating new index"
    mkdir -p "$(dirname "$INDEX_FILE")"
    cat > "$INDEX_FILE" <<EOF
{
  "versions": ["$VERSION"]
}
EOF
  fi
fi

echo ""
if [[ "$STORAGE_TYPE" == "s3" ]]; then
  echo "Done! Provider is now available at: s3://${BUCKET}/providers/${NAMESPACE}/${NAME}/${VERSION}/"
else
  echo "Done! Provider is now available at: ${STORAGE_PATH}/providers/${NAMESPACE}/${NAME}/${VERSION}/"
fi
echo ""
echo "Test with terraform:"
echo "  terraform {"
echo "    required_providers {"
echo "      ${NAME} = {"
echo "        source  = \"${NAMESPACE}/${NAME}\""
echo "        version = \"${VERSION}\""
echo "      }"
echo "    }"
echo "  }"
