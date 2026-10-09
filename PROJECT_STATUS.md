# Project Status

The current release line is a filesystem-backed, single-writer Terraform provider/module registry designed for air-gapped and secure environments.

## Production capabilities

- Provider registry and network mirror protocols
- Module registry protocol
- Non-root container with persistent-volume support
- `tfreg` CLI: push, pull, bundle, publish, import, list, deprecate, delete
- OCI 1.1 artifact export and validated import for ECR and other OCI registries (promotion pipeline integration)
- RBAC API keys for management mutations
- Streaming/atomic artifacts, checksums, deprecation, metrics, and audit logs
- Multi-platform release binaries and multi-architecture OCI images
- Race tests, security scans (CI-only), container scan, and end-to-end CI

## Explicit constraints

- OCI copies are replication targets; the server's primary storage remains filesystem-backed
- One registry process per volume
- TLS supplied by a reverse proxy/ingress
- Protocol/read endpoints public unless restricted externally
- No runtime artifact scanning — scanning is handled externally in the promotion pipeline
- No webhooks — not needed for the target deployment model
- Air-gapped by default: no mandatory external service calls

## What was removed

The following were removed to keep the registry focused and avoid duplicating capabilities already covered by Advana/WDP:

- Continuous artifact scanning (Trivy/Checkov, quarantine, waivers, security dashboard)
- Webhook notifications and durable outbox
- Promotion channel enforcement

See the README and deployment guide for the supported operating model.
