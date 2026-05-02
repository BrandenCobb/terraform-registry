#!/bin/bash
set -e

# Usage: ./upload-module.sh --storage STORAGE_TYPE --path STORAGE_PATH --namespace NAMESPACE --name NAME --provider PROVIDER --version VERSION --source SOURCE_DIR

STORAGE_TYPE="filesystem"
STORAGE_PATH=""
S3_BUCKET=""
NAMESPACE=""
NAME=""
PROVIDER=""
VERSION=""
SOURCE_DIR=""

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
      S3_BUCKET="$2"
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
    --provider)
      PROVIDER="$2"
      shift 2
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    --source)
      SOURCE_DIR="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

if [[ -z "$NAMESPACE" || -z "$NAME" || -z "$PROVIDER" || -z "$VERSION" || -z "$SOURCE_DIR" ]]; then
  echo "Usage: $0 --storage STORAGE_TYPE --namespace NAMESPACE --name NAME --provider PROVIDER --version VERSION --source SOURCE_DIR"
  echo ""
  echo "Options:"
  echo "  --storage TYPE      Storage type: filesystem or s3 (default: filesystem)"
  echo "  --path PATH         Filesystem storage path (for filesystem storage)"
  echo "  --bucket BUCKET     S3 bucket name (for s3 storage)"
  echo "  --namespace NS      Module namespace (e.g., hashicorp)"
  echo "  --name NAME         Module name (e.g., vpc)"
  echo "  --provider PROVIDER Provider name (e.g., aws)"
  echo "  --version VERSION   Module version (e.g., 1.0.0)"
  echo "  --source DIR        Source directory containing module files"
  echo ""
  echo "Example (filesystem):"
  echo "  $0 --storage filesystem --path ./data --namespace example --name vpc --provider aws --version 1.0.0 --source ./my-vpc-module"
  echo ""
  echo "Example (S3):"
  echo "  $0 --storage s3 --bucket my-registry --namespace example --name vpc --provider aws --version 1.0.0 --source ./my-vpc-module"
  exit 1
fi

if [[ ! -d "$SOURCE_DIR" ]]; then
  echo "Error: Source directory not found: $SOURCE_DIR"
  exit 1
fi

echo "Uploading module..."
echo "  Storage: $STORAGE_TYPE"
echo "  Module: $NAMESPACE/$NAME/$PROVIDER@$VERSION"
echo "  Source: $SOURCE_DIR"

# Create temporary directory
TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

# Create tarball
TARBALL="$TMPDIR/module.tar.gz"
echo "Creating tarball..."
tar -czf "$TARBALL" -C "$SOURCE_DIR" .

# Calculate SHA256
SHASUM=$(sha256sum "$TARBALL" | awk '{print $1}')
echo "  SHA256: $SHASUM"

# Upload based on storage type
if [[ "$STORAGE_TYPE" == "s3" ]]; then
  if [[ -z "$S3_BUCKET" ]]; then
    echo "Error: --bucket required for S3 storage"
    exit 1
  fi

  MODULE_PREFIX="modules/${NAMESPACE}/${NAME}/${PROVIDER}/${VERSION}"
  echo "Uploading to S3: s3://${S3_BUCKET}/${MODULE_PREFIX}/"

  # Upload tarball
  aws s3 cp "$TARBALL" "s3://${S3_BUCKET}/${MODULE_PREFIX}/module.tar.gz"

  # Update index.json
  INDEX_KEY="modules/${NAMESPACE}/${NAME}/${PROVIDER}/index.json"
  INDEX_FILE="$TMPDIR/index.json"

  # Download existing index or create new one
  if aws s3 cp "s3://${S3_BUCKET}/${INDEX_KEY}" "$INDEX_FILE" 2>/dev/null; then
    echo "  Found existing index"
    # Add version if not already present
    VERSIONS=$(jq -r '.versions[]' "$INDEX_FILE")
    if echo "$VERSIONS" | grep -q "^${VERSION}$"; then
      echo "  Version $VERSION already in index"
    else
      echo "  Adding version $VERSION to index"
      jq ".versions += [\"${VERSION}\"] | .versions |= sort_by(.) | .versions |= unique" "$INDEX_FILE" > "$INDEX_FILE.tmp"
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
  aws s3 cp "$INDEX_FILE" "s3://${S3_BUCKET}/${INDEX_KEY}"

else
  # Filesystem storage
  if [[ -z "$STORAGE_PATH" ]]; then
    echo "Error: --path required for filesystem storage"
    exit 1
  fi

  MODULE_DIR="${STORAGE_PATH}/modules/${NAMESPACE}/${NAME}/${PROVIDER}/${VERSION}"
  echo "Uploading to filesystem: ${MODULE_DIR}/"

  # Create directories
  mkdir -p "$MODULE_DIR"

  # Copy tarball
  cp "$TARBALL" "$MODULE_DIR/module.tar.gz"

  # Update index.json
  INDEX_FILE="${STORAGE_PATH}/modules/${NAMESPACE}/${NAME}/${PROVIDER}/index.json"

  if [[ -f "$INDEX_FILE" ]]; then
    echo "  Found existing index"
    # Add version if not already present
    VERSIONS=$(jq -r '.versions[]' "$INDEX_FILE")
    if echo "$VERSIONS" | grep -q "^${VERSION}$"; then
      echo "  Version $VERSION already in index"
    else
      echo "  Adding version $VERSION to index"
      jq ".versions += [\"${VERSION}\"] | .versions |= sort_by(.) | .versions |= unique" "$INDEX_FILE" > "$INDEX_FILE.tmp"
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
echo "Done! Module is now available:"
echo "  terraform {"
echo "    source = \"${NAMESPACE}/${NAME}/${PROVIDER}\""
echo "    version = \"${VERSION}\""
echo "  }"
