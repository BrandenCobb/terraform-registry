# Contributing

## Setup

Requirements: Go 1.26.6+, Docker with Compose, GNU Make, and Git.

```bash
git clone https://github.com/BrandenCobb/terraform-registry.git
cd terraform-registry
make check
```

This repository contains two independent Go modules (`registry-server` and `cmd/tfreg`); do not run root-level `go test ./...`.

## Development workflow

1. Create a focused branch.
2. Add or update tests for behavioral changes.
3. Run `gofmt -s -w` on changed Go files.
4. Run `make check` and `make docker-build VERSION=dev`.
5. Exercise changed CLI/API behavior against the built container.
6. Update documentation and the unreleased changelog section.
7. Open a pull request describing impact, compatibility, and verification.

Production constraints are intentional: filesystem-only storage, one writer per volume, non-root execution, bounded streaming requests, public protocol/read routes, and authenticated management mutations. Architectural changes must preserve Terraform protocol compatibility and migration safety.

## Commit style

Use concise conventional commits where practical:

```text
feat(api): add provider endpoint
fix(storage): preserve atomic publication
security(auth): reject query-string credentials
docs(deploy): document PVC ownership
```

Never commit API keys, generated `keys.json`, filled Kubernetes Secrets, provider binaries, archives, or registry data.

## Reporting bugs and vulnerabilities

Use GitHub issues for reproducible non-sensitive bugs. Follow [`SECURITY.md`](SECURITY.md) for vulnerabilities; do not disclose them publicly before coordination.
