# Quick Start

```bash
git clone https://github.com/BrandenCobb/terraform-registry.git
cd terraform-registry
export REGISTRY_API_KEY="$(openssl rand -hex 32)"
export BASE_URL=http://localhost:5000
docker compose up -d
curl -fsS http://localhost:5000/health
```

Open <http://localhost:5000/ui> and configure the CLI:

```bash
export TFREG_REGISTRY=http://localhost:5000
export TFREG_API_KEY="$REGISTRY_API_KEY"
```

Package and publish a module:

```bash
tfreg bundle module --namespace acme --name vpc --provider aws --version 1.0.0 --source ./vpc
tfreg push module --namespace acme --name vpc --provider aws --version 1.0.0 --file ./acme-vpc-aws-1.0.0.tar.gz
```

For provider packaging, production TLS, RBAC key files, backups, Kubernetes, and complete API semantics, use the root [`README.md`](../README.md), [`CONFIGURATION.md`](CONFIGURATION.md), and [`DEPLOYMENT.md`](DEPLOYMENT.md).
