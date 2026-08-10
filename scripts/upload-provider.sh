#!/usr/bin/env bash
set -euo pipefail

# Compatibility wrapper. Prefer invoking `tfreg push provider` directly.
exec "${TFREG_BIN:-tfreg}" push provider "$@"
