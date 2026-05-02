#!/bin/bash
set -eo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test configuration
REGISTRY_PORT=18080
REGISTRY_URL="http://localhost:${REGISTRY_PORT}"
STORAGE_PATH="/tmp/terraform-registry-integration-test"
BINARY="dist/terraform-registry"
PID_FILE="/tmp/terraform-registry-test.pid"

# Counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Cleanup function
cleanup() {
    echo -e "${YELLOW}Cleaning up...${NC}"
    if [ -f "$PID_FILE" ]; then
        kill $(cat "$PID_FILE") 2>/dev/null || true
        rm "$PID_FILE"
    fi
    rm -rf "$STORAGE_PATH"
}

trap cleanup EXIT

# Helper functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_test() {
    echo -e "${YELLOW}[TEST]${NC} $1"
}

pass() {
    echo -e "${GREEN}✓ PASS${NC}"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

fail() {
    echo -e "${RED}✗ FAIL${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

run_test() {
    TESTS_RUN=$((TESTS_RUN + 1))
    log_test "$1"
}

# Check if binary exists
if [ ! -f "$BINARY" ]; then
    log_error "Binary not found at $BINARY. Run 'make build' first."
    exit 1
fi

# Start registry server
log_info "Starting registry server on port $REGISTRY_PORT..."
rm -rf "$STORAGE_PATH"
mkdir -p "$STORAGE_PATH"

STORAGE_TYPE=filesystem \
STORAGE_PATH="$STORAGE_PATH" \
BASE_URL="$REGISTRY_URL" \
PORT="$REGISTRY_PORT" \
"$BINARY" > /tmp/terraform-registry-test.log 2>&1 &

echo $! > "$PID_FILE"
sleep 2

# Check if server started
if ! kill -0 $(cat "$PID_FILE") 2>/dev/null; then
    log_error "Failed to start registry server"
    cat /tmp/terraform-registry-test.log
    exit 1
fi

log_info "Registry server started with PID $(cat $PID_FILE)"

# Test 1: Health check
run_test "Health check endpoint"
response=$(curl -s -o /dev/null -w "%{http_code}" "$REGISTRY_URL/health")
if [ "$response" = "200" ]; then
    pass
else
    fail "Expected 200, got $response"
fi

# Test 2: Well-known discovery
run_test "Terraform discovery endpoint"
response=$(curl -s "$REGISTRY_URL/.well-known/terraform.json")
if echo "$response" | grep -q "providers.v1" && echo "$response" | grep -q "modules.v1"; then
    pass
else
    fail "Missing required fields in well-known response"
fi

# Test 3: Upload and retrieve provider
run_test "Upload provider and retrieve versions"

# Create test provider structure
PROVIDER_NAMESPACE="hashicorp"
PROVIDER_NAME="test"
PROVIDER_VERSION="1.0.0"
PROVIDER_DIR="$STORAGE_PATH/providers/$PROVIDER_NAMESPACE/$PROVIDER_NAME/$PROVIDER_VERSION"
mkdir -p "$PROVIDER_DIR"

# Create metadata
cat > "$PROVIDER_DIR/linux_amd64.json" <<EOF
{
  "filename": "terraform-provider-test_v1.0.0_linux_amd64.zip",
  "shasum": "abc123def456"
}
EOF

# Create fake zip file
echo "fake provider binary" > "$PROVIDER_DIR/terraform-provider-test_v1.0.0_linux_amd64.zip"

# Create index
cat > "$STORAGE_PATH/providers/$PROVIDER_NAMESPACE/$PROVIDER_NAME/index.json" <<EOF
{
  "versions": ["1.0.0"]
}
EOF

# Test provider versions endpoint
response=$(curl -s "$REGISTRY_URL/v1/providers/$PROVIDER_NAMESPACE/$PROVIDER_NAME/versions")
if echo "$response" | grep -q '"version":"1.0.0"' && echo "$response" | grep -q '"os":"linux"'; then
    pass
else
    fail "Provider versions response incorrect: $response"
fi

# Test 4: Provider download metadata
run_test "Retrieve provider download metadata"
response=$(curl -s "$REGISTRY_URL/v1/providers/$PROVIDER_NAMESPACE/$PROVIDER_NAME/$PROVIDER_VERSION/download/linux/amd64")
if echo "$response" | grep -q "download_url" && echo "$response" | grep -q "abc123def456"; then
    pass
else
    fail "Provider download response incorrect"
fi

# Test 5: Download provider binary
run_test "Download provider binary via direct URL"
download_url=$(echo "$response" | grep -o '"download_url":"[^"]*"' | cut -d'"' -f4)
if [ -n "$download_url" ]; then
    binary_response=$(curl -s "$download_url")
    if echo "$binary_response" | grep -q "fake provider binary"; then
        pass
    else
        fail "Provider binary content incorrect"
    fi
else
    fail "No download URL in response"
fi

# Test 6: Upload and retrieve module
run_test "Upload module and retrieve versions"

MODULE_NAMESPACE="example"
MODULE_NAME="vpc"
MODULE_PROVIDER="aws"
MODULE_VERSION="1.0.0"
MODULE_DIR="$STORAGE_PATH/modules/$MODULE_NAMESPACE/$MODULE_NAME/$MODULE_PROVIDER/$MODULE_VERSION"
mkdir -p "$MODULE_DIR"

# Create fake module tarball
echo "fake module content" | gzip > "$MODULE_DIR/module.tar.gz"

# Create index
cat > "$STORAGE_PATH/modules/$MODULE_NAMESPACE/$MODULE_NAME/$MODULE_PROVIDER/index.json" <<EOF
{
  "versions": ["1.0.0"]
}
EOF

# Test module versions endpoint
response=$(curl -s "$REGISTRY_URL/v1/modules/$MODULE_NAMESPACE/$MODULE_NAME/$MODULE_PROVIDER/versions")
if echo "$response" | grep -q '"version":"1.0.0"'; then
    pass
else
    fail "Module versions response incorrect"
fi

# Test 7: Module download
run_test "Download module via redirect"
response=$(curl -s -o /dev/null -w "%{http_code}" -L "$REGISTRY_URL/v1/modules/$MODULE_NAMESPACE/$MODULE_NAME/$MODULE_PROVIDER/$MODULE_VERSION/download")
if [ "$response" = "200" ]; then
    pass
else
    fail "Expected 200, got $response"
fi

# Test 8: Module latest download
run_test "Download latest module version"
# Use -i to include headers in output
response=$(curl -s -i "$REGISTRY_URL/v1/modules/$MODULE_NAMESPACE/$MODULE_NAME/$MODULE_PROVIDER/download")
if echo "$response" | grep -iq "X-Terraform-Get" && echo "$response" | grep -q '"version"'; then
    pass
else
    fail "Missing X-Terraform-Get header or invalid response"
fi

# Test 9: Provider not found
run_test "Handle non-existent provider gracefully"
response=$(curl -s -o /dev/null -w "%{http_code}" "$REGISTRY_URL/v1/providers/nonexistent/provider/versions")
if [ "$response" = "200" ] || [ "$response" = "404" ]; then
    pass
else
    fail "Expected 200 or 404, got $response"
fi

# Test 10: Module not found
run_test "Handle non-existent module gracefully"
response=$(curl -s -o /dev/null -w "%{http_code}" "$REGISTRY_URL/v1/modules/nonexistent/module/aws/versions")
if [ "$response" = "200" ] || [ "$response" = "404" ]; then
    pass
else
    fail "Expected 200 or 404, got $response"
fi

# Print summary
echo ""
echo "========================================"
echo "Integration Test Summary"
echo "========================================"
echo "Total Tests: $TESTS_RUN"
echo -e "Passed: ${GREEN}$TESTS_PASSED${NC}"
echo -e "Failed: ${RED}$TESTS_FAILED${NC}"
echo "========================================"

if [ $TESTS_FAILED -gt 0 ]; then
    log_error "Some tests failed"
    exit 1
else
    log_info "All tests passed!"
    exit 0
fi
