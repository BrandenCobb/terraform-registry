#!/usr/bin/env bash
set -euo pipefail

# Compatibility wrapper. Prefer invoking `tfreg push module` directly.
exec "${TFREG_BIN:-tfreg}" push module "$@"
