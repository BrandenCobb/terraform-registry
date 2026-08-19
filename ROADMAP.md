# Project Roadmap

This roadmap describes intended direction, not a compatibility promise. Release scope remains subject to design review, tests, and migration safety.

## v2.3.x — Maintenance and supply-chain hygiene

- Keep Go, base images, scanners, and pinned GitHub Actions current.
- Preserve blocking `govulncheck`, `gosec`, and fixed HIGH/CRITICAL container gates.
- Improve scanner-build reproducibility and remove dependency-metadata workarounds when upstream releases permit it.
- Sign container indexes and release checksum manifests in addition to existing SBOM and provenance attestations.

## v2.4.0 — Reliability

- Exercise the exact production router and middleware stack through black-box tests.
- Run real `terraform init` acceptance tests through a temporary trusted HTTPS endpoint.
- Expand CLI behavioral coverage for push, pull, bundle, list, deprecate, delete, and failure cleanup.
- Add a durable filesystem webhook outbox with retries, stable event IDs, dead-letter handling, metrics, and graceful shutdown draining.
- Add backup, verification, and restore commands plus an automated restore drill.
- Strengthen structured audit events, correlation IDs, rotation, and external export.

## v2.5.0 — Scale and identity

- Maintain a disposable in-memory scan inventory index backed by the filesystem source of truth.
- Add cached security aggregates, conditional HTTP responses, and large-inventory benchmarks.
- Add optional OIDC login and group-to-role mapping while retaining API keys for automation.
- Publish and validate an OpenAPI contract and generated client examples.
- Add controlled artifact promotion channels with policy, signature, and audit requirements.

## Architectural constraints

Unless a future design explicitly replaces them, releases will preserve:

- Filesystem-only durable storage.
- Exactly one registry server process/single writer per persistent volume.
- Atomic file publication and immutable artifact names.
- Public Terraform protocol compatibility.
- Non-root, read-only-root-compatible containers.
- No Docker socket exposure to the registry or scanner runtime.
