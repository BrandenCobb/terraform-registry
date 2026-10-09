# Project Roadmap

This roadmap describes intended direction, not a compatibility promise. Release scope remains subject to design review, tests, and migration safety.

## Mission

A small, lightweight, portable Terraform provider and module registry for air-gapped and secure environments. Single Go binary, filesystem-backed, zero cloud dependencies. The `tfreg` CLI builds and pushes artifacts; OCI import/export enables promotion through ECR-based pipelines.

## Out of scope

The following capabilities are handled by other systems in our environment (Advana/WDP) and will not be built into this registry:

- **Artifact scanning and vulnerability management** (Trivy, Checkov, quarantine policies, waivers, security dashboards) — removed from codebase; use external scanning in the promotion pipeline.
- **Promotion channels with policy/signature/audit gates** — handled by Advana pipeline.
- **Webhooks** — removed; not needed for the target deployment model.

## v2.3.x — Cleanup and slimming

- Remove scanning subsystem (scanning.go, scanner_manager.go, scanning_api.go, scanning-related API endpoints, scanner Dockerfile, scanner compose overlay).
- Remove webhook subsystem (webhooks.go, WEBHOOK_CONFIG).
- Remove scanning references from UI, README, AGENTS.md, and configuration.
- Keep Go, base images, and pinned GitHub Actions current.
- Preserve blocking `govulncheck`, `gosec`, and container security gates in CI.

## v2.4.0 — Reliability

- Exercise the exact production router and middleware stack through black-box tests.
- Run real `terraform init` acceptance tests through a temporary trusted HTTPS endpoint.
- Expand CLI behavioral coverage for push, pull, bundle, import, list, deprecate, delete, and failure cleanup.
- Add backup, verification, and restore commands plus an automated restore drill.
- Strengthen structured audit events and correlation IDs.

## v2.5.0 — Identity and OCI

- Add optional OIDC login and group-to-role mapping (Keycloak or e-ICAM as DoD OIDC provider) while retaining API keys for automation.
- Harden OCI 1.1 import/export for air-gapped promotion workflows: validate manifests, handle partial failures, support digest-pinned references in CI.
- Publish and validate an OpenAPI contract and generated client examples.

## Architectural constraints

Unless a future design explicitly replaces them, releases will preserve:

- Filesystem-only durable storage.
- Exactly one registry server process/single writer per persistent volume.
- Atomic file publication and immutable artifact names.
- Public Terraform protocol compatibility.
- Non-root, read-only-root-compatible containers.
- Air-gapped operability: no mandatory external service calls, no telemetry, no auto-update checks.
