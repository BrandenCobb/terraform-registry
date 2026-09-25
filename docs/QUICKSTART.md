# Quick Start

## 1. Run the secure registry

```bash
git clone https://github.com/BrandenCobb/terraform-registry.git
cd terraform-registry
export REGISTRY_API_KEY="$(openssl rand -hex 32)"
export BASE_URL=http://localhost:5000
docker compose up -d --build
curl -fsS http://localhost:5000/health
curl -fsS http://localhost:5000/api/v1/security/health
```

Open <http://localhost:5000/ui>. The default deployment continuously scans artifacts and quarantines anything unknown, stale, errored, or denied by the HIGH/CRITICAL policy.

## 2. Configure the CLI

Build and install `tfreg` from the checkout, then set connection defaults:

```bash
make build
sudo install dist/tfreg /usr/local/bin/tfreg
export TFREG_REGISTRY=http://localhost:5000
export TFREG_API_KEY="$REGISTRY_API_KEY"
```

## 3. Publish a provider from source

From a cloned Go provider repository:

```bash
tfreg publish provider \
  --namespace acme --name example --version 1.0.0 \
  --source . --os linux --arch amd64
```

This runs provider tests, builds the binary, creates the provider ZIP, and uploads it. Repeat for each OS/architecture.

## 4. Publish a module repository

```bash
tfreg publish module \
  --namespace acme --name vpc --provider aws --version 1.0.0 \
  --source ./vpc
```

## 5. Optional OCI/ECR copy

Add `--oci-ref`, `--oci-username AWS`, and `--oci-password-stdin` to either publish command:

```bash
aws ecr get-login-password --region us-east-1 | \
  tfreg publish module \
    --namespace acme --name vpc --provider aws --version 1.0.0 --source ./vpc \
    --oci-ref 123456789012.dkr.ecr.us-east-1.amazonaws.com/terraform/modules/acme/vpc/aws:1.0.0 \
    --oci-username AWS --oci-password-stdin
```

The ECR repository must already exist. OCI stores a portable copy of the exact package; Terraform clients still use the Terraform registry protocol endpoint.

For production TLS, RBAC key files, waivers, backups, Kubernetes, and complete API semantics, see the root [`README.md`](../README.md), [`CONFIGURATION.md`](CONFIGURATION.md), and [`DEPLOYMENT.md`](DEPLOYMENT.md).
