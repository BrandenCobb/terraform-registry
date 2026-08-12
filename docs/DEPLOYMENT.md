# Production Deployment

## Requirements

- Persistent filesystem volume writable by UID/GID 65534
- One registry replica per volume (single-writer design)
- HTTPS reverse proxy or ingress
- A stable `BASE_URL` matching the public origin
- An admin API key stored in a secret manager
- Volume backups and monitoring of `/health` and `/metrics`

## Docker Compose

```bash
export BASE_URL=https://registry.example.com
export REGISTRY_API_KEY="$(openssl rand -hex 32)"
docker compose pull
docker compose up -d
curl -fsS https://registry.example.com/health
```

The included Compose file uses a named volume, a non-root user, a read-only root filesystem, no Linux capabilities, and `no-new-privileges`.

### Scanner-enabled deployment

Use the scanner image overlay and begin in visibility mode:

```bash
export SCAN_MODE=visibility
docker compose -f docker-compose.yml -f docker-compose.scanning.yml up -d
curl -fsS https://registry.example.com/api/v1/security/health
```

The scanner image contains Trivy and Checkov, runs them without executing provider binaries, and requires no Docker socket. Give `/tmp` enough ephemeral capacity for extracted artifacts and keep `/var/lib/terraform-registry/trivy-cache` on the persistent volume. Use `examples/kubernetes/k3s/deployment-scanning.yaml` for a hardened Kubernetes starting point.

Existing artifacts are backfilled asynchronously and remain unknown until scanned. Keep `SCAN_MODE=visibility` through migration. Switch to `quarantine` or `enforce` only after the queue drains and required waivers exist; blocking modes fail closed for unknown, stale, errored, and denied artifacts.

## Kubernetes

Apply the manifests in `examples/kubernetes/k3s` after replacing the image tag, `BASE_URL`, storage class, ingress hostname, and secret values.

Production rules:

1. Keep `replicas: 1` and use a `Recreate` strategy.
2. Mount the PVC at `/var/lib/terraform-registry`.
3. Set pod `fsGroup`, `runAsUser`, and `runAsGroup` to `65534`.
4. Pin an immutable image tag such as `v2.3.0` (or a digest), never `latest`.
5. Put `REGISTRY_API_KEY` in a Kubernetes Secret for initial bootstrap. After `keys.json` exists, manage hashed keys on the persistent volume.
6. Route TLS traffic through an ingress and set `BASE_URL` to that HTTPS origin.
7. Use `/health` for liveness and readiness; it performs a storage write/delete check.

The filesystem backend is not a horizontally scalable shared-database design. Do not run multiple pods against one PVC.

## Backup and restore

Back up `STORAGE_PATH` as one consistency unit. For a strict point-in-time backup:

```bash
docker stop terraform-registry
# Snapshot or copy the named volume here.
docker start terraform-registry
```

Restore with the server stopped, preserve ownership for UID/GID 65534, then verify:

```bash
curl -fsS https://registry.example.com/health
curl -fsS https://registry.example.com/api/v1/stats
```

## Upgrade

1. Back up the volume.
2. Pull the immutable target image.
3. Replace the single container/pod.
4. Check `/health`, logs, provider/module listing, and one real `terraform init` from a canary workspace.
5. Keep the previous image available for rollback. Storage formats are JSON/files and should still be backed up before upgrades.

## Monitoring

Prometheus request:

```yaml
scrape_configs:
  - job_name: terraform-registry
    metrics_path: /metrics
    scheme: https
    static_configs:
      - targets: [registry.example.com]
```

Prometheus normally sends a text-compatible `Accept` header. To force the text format, set `Accept: text/plain`.

Alert on:

- `/health` non-200
- elevated `terraform_registry_requests_err`
- elevated `terraform_registry_auth_failures`
- elevated `terraform_registry_rate_limit_hits`
- persistent-volume capacity/inode pressure
- restart loops and webhook delivery warnings
- persistent `terraform_registry_scan_queue_depth`
- elevated `terraform_registry_scan_errors_total`
- scanner readiness failures from `/api/v1/security/health`
- stale or unknown artifacts reported by the security dashboard

## TLS

The application intentionally serves HTTP. Terminate TLS with Caddy, nginx, Traefik, an ingress controller, or a cloud load balancer. Do not expose raw port 8080 to the internet.
