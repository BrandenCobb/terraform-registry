# HTTPS Setup for Terraform Registry

Terraform requires HTTPS URLs for network mirror configurations. This guide shows how to enable HTTPS for local development and production deployments.

## Problem

Terraform's `provider_installation` block requires HTTPS:

```hcl
provider_installation {
  network_mirror {
    url = "http://localhost:5000/"  # ❌ ERROR: must be https
    include = ["*/*"]
  }
}
```

Attempting to use HTTP results in:
```
Error: Invalid URL for provider installation source
Cannot use "http://localhost:5000/" as a URL for a network provider mirror:
the mirror must be at an https: URL.
```

## Solution: Local HTTPS with mkcert + Caddy

For local development, use self-signed certificates that your system trusts.

### Prerequisites

- Docker and Docker Compose installed
- Root/sudo access (for CA installation)

### Step 1: Install mkcert

**Linux:**
```bash
# Install dependencies
sudo apt install libnss3-tools

# Download and install mkcert
wget https://github.com/FiloSottile/mkcert/releases/download/v1.4.4/mkcert-v1.4.4-linux-amd64
chmod +x mkcert-v1.4.4-linux-amd64
sudo mv mkcert-v1.4.4-linux-amd64 /usr/local/bin/mkcert
```

**macOS:**
```bash
brew install mkcert
```

**Windows:**
```powershell
choco install mkcert
```

### Step 2: Install Local CA

This installs a trusted root certificate authority on your system:

```bash
mkcert -install
```

Output:
```
Created a new local CA 💥
The local CA is now installed in the system trust store! ⚡️
```

### Step 3: Generate Certificates

```bash
cd ~/path/to/terraform-registry
mkcert registry.local localhost 127.0.0.1
```

This creates:
- `registry.local+2.pem` - Certificate
- `registry.local+2-key.pem` - Private key

### Step 4: Configure Caddy Proxy

Already created for you:

**Caddyfile:**
```caddyfile
registry.local {
    tls /certs/registry.local.pem /certs/registry.local-key.pem
    reverse_proxy terraform-registry:8080
}
```

**docker-compose.override.yml:**
```yaml
version: '3.8'

services:
  terraform-registry:
    environment:
      BASE_URL: https://registry.local

  caddy:
    image: caddy:latest
    container_name: terraform-registry-proxy
    ports:
      - "443:443"
      - "80:80"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./registry.local+2.pem:/certs/registry.local.pem:ro
      - ./registry.local+2-key.pem:/certs/registry.local-key.pem:ro
    restart: unless-stopped
    depends_on:
      - terraform-registry
```

### Step 5: Add DNS Entry

Add `registry.local` to your hosts file:

**Linux/macOS:**
```bash
echo "127.0.0.1 registry.local" | sudo tee -a /etc/hosts
```

**Windows (as Administrator):**
```powershell
Add-Content -Path C:\Windows\System32\drivers\etc\hosts -Value "127.0.0.1 registry.local"
```

### Step 6: Start Services

```bash
docker-compose down
docker-compose up -d
```

This starts both the registry and Caddy proxy.

### Step 7: Verify HTTPS

```bash
curl https://registry.local/.well-known/terraform.json
```

Should return:
```json
{
  "providers.v1": "/v1/providers/",
  "modules.v1": "/v1/modules/"
}
```

### Step 8: Configure Terraform

Update your `~/.terraformrc`:

```hcl
provider_installation {
  network_mirror {
    url = "https://registry.local/"
    include = ["*/*"]
  }
}
```

### Step 9: Test with Terraform

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.31.0"
    }
  }
}
```

```bash
terraform init
```

Should successfully download from your local registry!

---

## Production Deployments

For production environments, use one of these approaches:

### Option 1: Let's Encrypt (Public Internet)

Use Caddy's automatic HTTPS with Let's Encrypt:

**Caddyfile:**
```caddyfile
registry.example.com {
    # Caddy automatically gets Let's Encrypt certs
    reverse_proxy terraform-registry:8080
}
```

Requirements:
- Public DNS pointing to your server
- Ports 80 and 443 accessible from internet
- Valid domain name

### Option 2: Corporate CA (Private Network)

Use certificates from your organization's CA:

**Caddyfile:**
```caddyfile
registry.company.internal {
    tls /certs/registry.crt /certs/registry.key
    reverse_proxy terraform-registry:8080
}
```

Mount your corporate certificates:
```yaml
volumes:
  - /path/to/corp-certs/registry.crt:/certs/registry.crt:ro
  - /path/to/corp-certs/registry.key:/certs/registry.key:ro
```

### Option 3: Kubernetes Ingress

Use cert-manager for automatic certificate management:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: terraform-registry
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  tls:
  - hosts:
    - registry.example.com
    secretName: terraform-registry-tls
  rules:
  - host: registry.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: terraform-registry
            port:
              number: 80
```

See [Kubernetes Deployment Guide](../examples/kubernetes/README.md) for details.

---

## Troubleshooting

### Certificate Not Trusted

If you get SSL errors:

1. **Verify CA is installed:**
   ```bash
   mkcert -CAROOT
   ls -la $(mkcert -CAROOT)
   ```

2. **Reinstall CA:**
   ```bash
   mkcert -uninstall
   mkcert -install
   ```

3. **Check system trust store:**
   ```bash
   # Linux
   sudo update-ca-certificates
   
   # macOS
   security find-certificate -a -c "mkcert"
   ```

### DNS Not Resolving

Verify hosts file entry:

```bash
grep registry.local /etc/hosts
ping registry.local
```

### Caddy Not Starting

Check logs:
```bash
docker logs terraform-registry-proxy
```

Common issues:
- Certificate files not found (check paths)
- Port 443 already in use
- Invalid Caddyfile syntax

### Provider Downloads Fail

Check that `BASE_URL` matches your domain:

```bash
docker exec terraform-registry env | grep BASE_URL
# Should show: BASE_URL=https://registry.local
```

Restart after changing:
```bash
docker-compose restart terraform-registry
```

---

## Alternative: Filesystem Mirror (No HTTPS Required)

If you can't use HTTPS, use Terraform's filesystem mirror instead:

```hcl
provider_installation {
  filesystem_mirror {
    path = "/usr/local/share/terraform/plugins"
    include = ["*/*"]
  }
}
```

Download providers manually and place in the mirror structure. See [Terraform Documentation](https://developer.hashicorp.com/terraform/cli/config/config-file#filesystem_mirror) for details.

---

## See Also

- [Quick Start Guide](QUICKSTART.md)
- [Configuration Guide](CONFIGURATION.md)
- [Deployment Guide](DEPLOYMENT.md)
- [mkcert GitHub](https://github.com/FiloSottile/mkcert)
- [Caddy Documentation](https://caddyserver.com/docs/)
