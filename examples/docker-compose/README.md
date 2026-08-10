# Docker Compose Example

The repository root contains the maintained Compose deployment:

```bash
export BASE_URL=http://localhost:5000
export REGISTRY_API_KEY="$(openssl rand -hex 32)"
docker compose -f ../../docker-compose.yml up -d
```

It uses a persistent named volume and the container's non-root runtime user. For production HTTPS, backup, and proxy guidance, see [`docs/DEPLOYMENT.md`](../../docs/DEPLOYMENT.md).
