# Security Policy

## Supported versions

Only the latest release line receives security fixes. Upgrade promptly and pin immutable image tags or digests.

## Report a vulnerability

Do not open a public issue. Email **brandencobb@gmail.com** with impact, reproduction steps, affected versions, and any proposed mitigation. Expect initial acknowledgement within 48 hours.

## Security model

- Terraform protocol, network mirror, artifact downloads, UI, health, metrics, and management reads are public.
- Every management mutation requires a hashed RBAC API key.
- The server runs as UID/GID 65534, requires no Linux capabilities, and supports a read-only root filesystem.
- Artifact paths and route variables are validated; storage operations are confined beneath `STORAGE_PATH`.
- Uploads and downloads are bounded/streamed. Provider pulls through `tfreg` verify SHA-256.
- Audit events and application logs are structured JSON.
- Webhook signatures use HMAC-SHA256.

This registry does **not** review provider binaries or module source for malicious behavior. Restrict publish credentials to trusted CI and review artifacts before upload.

## Production checklist

1. Terminate TLS at a trusted reverse proxy or ingress; never expose raw HTTP publicly.
2. Set `BASE_URL` to the exact public HTTPS origin.
3. Generate high-entropy API keys, store them in a secret manager, and persist only hashes in `keys.json`.
4. Use separate `write` keys for CI and reserve `admin` keys for operators.
5. Keep the service at one replica per persistent filesystem volume.
6. Ensure the volume is writable only by the service UID/GID and back it up regularly.
7. Run the container with `readOnlyRootFilesystem`, `no-new-privileges`, and all capabilities dropped.
8. Leave `TRUST_PROXY_HEADERS=false` unless a trusted proxy overwrites client-IP headers and direct access is blocked.
9. Restrict `/metrics`, `/ui`, and management reads at the network/proxy layer if inventory is sensitive.
10. Monitor auth failures, rate-limit hits, errors, restarts, storage capacity, and webhook failures.

## Key rotation

Add the replacement key hash to `keys.json`, deploy clients, then disable/remove the old entry. Replace the JSON file atomically. Never send keys in query strings.

## Container and dependency scanning

CI runs `govulncheck`, `gosec`, and a blocking Trivy scan. Local equivalents:

```bash
make security-scan
trivy image ghcr.io/brandencobb/terraform-registry:v2.3.1
```

The release image is built with a patched Go toolchain and pinned Alpine release. Continue rebuilding releases as base-image and standard-library fixes become available.

## Incident response

1. Remove network access or stop the instance.
2. Revoke affected API keys.
3. Preserve application/audit/proxy logs and a storage snapshot.
4. Identify modified or downloaded artifacts.
5. Restore trusted data, deploy a patched image, and rotate all credentials.
6. Notify affected users and publish an advisory when appropriate.
