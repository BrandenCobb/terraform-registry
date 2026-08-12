# Filesystem Storage Example

The registry stores all provider and module data on a local filesystem. Run one
registry process (single writer) per data volume.

## Start the registry

```bash
docker volume create terraform-registry-data
docker run -d \
  --name terraform-registry \
  -p 127.0.0.1:5000:8080 \
  -v terraform-registry-data:/var/lib/terraform-registry \
  -e BASE_URL=http://localhost:5000 \
  -e REGISTRY_API_KEY='replace-with-a-long-random-secret' \
  --read-only \
  --tmpfs /tmp:size=64m,mode=1777 \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  ghcr.io/brandencobb/terraform-registry:v2.2.0
```

The image runs as UID/GID 65534. A named volume is recommended because Docker
initializes it with the image's writable storage ownership.

## Upload and download artifacts

Install `tfreg` from the matching release archive, then configure it:

```bash
export TFREG_REGISTRY=http://localhost:5000
export TFREG_API_KEY='replace-with-a-long-random-secret'

tfreg push provider \
  --namespace hashicorp \
  --name random \
  --version 3.5.1 \
  --file terraform-provider-random_3.5.1_linux_amd64.zip

tfreg list providers
```

Use `tfreg push module`, `tfreg pull provider`, and `tfreg pull module` for the
corresponding operations. Run `tfreg help` for complete usage.

## Terraform network mirror

Terraform requires an **HTTPS** URL for a network mirror, including local use.
Put the registry behind a TLS reverse proxy and set `BASE_URL` to that external
HTTPS origin before configuring Terraform:

```hcl
provider_installation {
  network_mirror {
    url = "https://registry.example.com/"
  }
}
```

See [`../../docs/HTTPS.md`](../../docs/HTTPS.md) and
[`../../docs/DEPLOYMENT.md`](../../docs/DEPLOYMENT.md).

## Backups

Back up the complete Docker volume while the registry is stopped, or use a
filesystem snapshot that captures the volume atomically. Restore the entire
volume as a unit, including `signing-key.asc` and `api-keys.json` if those files
are stored there.

## Clean up

```bash
docker rm -f terraform-registry
docker volume rm terraform-registry-data
```
