#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SERVER=${SERVER:-"$ROOT_DIR/dist/terraform-registry"}
TFREG=${TFREG:-"$ROOT_DIR/dist/tfreg"}
PORT=${PORT:-18080}
URL="http://127.0.0.1:${PORT}"
API_KEY=${API_KEY:-integration-secret}
WORK_DIR=$(mktemp -d)
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

for command in curl zip tar; do
  command -v "$command" >/dev/null || { echo "missing dependency: $command" >&2; exit 1; }
done
[[ -x "$SERVER" && -x "$TFREG" ]] || { echo "run 'make build' first" >&2; exit 1; }

STORAGE_PATH="$WORK_DIR/storage" \
BASE_URL="$URL" \
PORT="$PORT" \
REGISTRY_API_KEY="$API_KEY" \
RATE_LIMIT=10000 \
"$SERVER" >"$WORK_DIR/server.log" 2>&1 &
SERVER_PID=$!

for _ in {1..30}; do
  curl -fsS "$URL/health" >/dev/null && break
  sleep 1
done
curl -fsS "$URL/health" >/dev/null || { cat "$WORK_DIR/server.log" >&2; exit 1; }

printf '#!/bin/sh\nexit 0\n' >"$WORK_DIR/terraform-provider-example_v1.0.0"
chmod +x "$WORK_DIR/terraform-provider-example_v1.0.0"
(
  cd "$WORK_DIR"
  zip -q provider.zip terraform-provider-example_v1.0.0
)
mkdir "$WORK_DIR/module"
printf 'variable "name" { type = string }\n' >"$WORK_DIR/module/main.tf"
tar czf "$WORK_DIR/module.tar.gz" -C "$WORK_DIR/module" .
mkdir "$WORK_DIR/downloads"

TFREG_REGISTRY="$URL" TFREG_API_KEY="$API_KEY" "$TFREG" push provider \
  --namespace test --name example --version 1.0.0 --os linux --arch amd64 --file "$WORK_DIR/provider.zip"
TFREG_REGISTRY="$URL" TFREG_API_KEY="$API_KEY" "$TFREG" push module \
  --namespace test --name vpc --provider aws --version 1.0.0 --file "$WORK_DIR/module.tar.gz"
TFREG_REGISTRY="$URL" "$TFREG" pull provider \
  --namespace test --name example --version 1.0.0 --os linux --arch amd64 --output "$WORK_DIR/downloads"
TFREG_REGISTRY="$URL" "$TFREG" pull module \
  --namespace test --name vpc --provider aws --version 1.0.0 --output "$WORK_DIR/downloads"

curl -fsS "$URL/.well-known/terraform.json" | grep -q 'providers.v1'
curl -fsS "$URL/v1/providers/test/example/versions" | grep -q '1.0.0'
curl -fsS "$URL/v1/modules/test/vpc/aws/versions" | grep -q '1.0.0'

[[ $(curl -sS -o /dev/null -w '%{http_code}' -X DELETE "$URL/api/v1/providers/test/example/1.0.0") == 401 ]]
[[ $(curl -sS -o /dev/null -w '%{http_code}' -X DELETE -H "X-API-Key: $API_KEY" "$URL/api/v1/providers/test/example/1.0.0") == 200 ]]

printf 'integration test passed\n'
