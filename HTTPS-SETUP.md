# Local HTTPS Setup

This repository includes configuration for running the Terraform Registry with HTTPS using Caddy as a reverse proxy.

## Files Created

- **Caddyfile** - Caddy reverse proxy configuration
- **docker-compose.override.yml** - Adds Caddy service and updates BASE_URL
- **registry.local+2.pem** - SSL certificate (gitignored)
- **registry.local+2-key.pem** - SSL private key (gitignored)

## Quick Start

1. **Install mkcert CA** (one-time):
   ```bash
   sudo mkcert -install
   ```

2. **Add to /etc/hosts**:
   ```bash
   echo "127.0.0.1 registry.local" | sudo tee -a /etc/hosts
   ```

3. **Start services**:
   ```bash
   docker-compose down
   docker-compose up -d
   ```

4. **Test HTTPS**:
   ```bash
   curl https://registry.local/.well-known/terraform.json
   ```

5. **Configure Terraform** (`~/.terraformrc`):
   ```hcl
   provider_installation {
     network_mirror {
       url = "https://registry.local/"
       include = ["*/*"]
     }
   }
   ```

## What Gets Started

- **terraform-registry** - Main registry server on port 8080 (internal)
- **caddy** - HTTPS reverse proxy on port 443

The override file automatically:
- Adds the Caddy service
- Updates BASE_URL to `https://registry.local`
- Mounts certificate files
- Creates dependency chain

## Regenerate Certificates

If certificates expire (every 2+ years):

```bash
rm registry.local+2.pem registry.local+2-key.pem
mkcert registry.local localhost 127.0.0.1
docker-compose restart caddy
```

## Remove HTTPS

To go back to HTTP-only:

```bash
docker-compose down
rm docker-compose.override.yml
docker-compose up -d
```

## See Also

- [docs/HTTPS.md](docs/HTTPS.md) - Complete HTTPS documentation
- [docs/QUICKSTART.md](docs/QUICKSTART.md) - Quick start guide