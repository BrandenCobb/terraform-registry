# Project Status

The current release line is a filesystem-backed, single-writer Terraform provider/module registry.

## Production capabilities

- Provider registry and network mirror protocols
- Module registry protocol
- Non-root container with persistent-volume support
- `tfreg` one-command source publishing for providers/modules, plus package, pull, list, and delete workflows
- OCI 1.1 artifact export and validated import for ECR and other OCI registries, with Docker credential-store or password-stdin authentication
- Continuous Trivy/Checkov scanning, quarantine enforcement, waivers, and scheduled rescans in the default Compose deployment
- RBAC API keys for management mutations
- Streaming/atomic artifacts, checksums, deprecation, metrics, audit logs, and webhooks
- Multi-platform release binaries and multi-architecture OCI images
- Race tests, security scans, container scan, and end-to-end CI

## Explicit constraints

- OCI copies are replication targets; the server's primary storage remains filesystem-backed
- One registry process per volume
- TLS supplied by a reverse proxy/ingress
- Protocol/read endpoints public unless restricted externally
- No malware analysis or trust attestation of uploaded artifacts
- Webhook delivery is best-effort without durable retries

See the README and deployment guide for the supported operating model. Historical capabilities removed from the current architecture may still appear in old changelog entries; they are not current product features.
