# Kubernetes Example

The `k3s/` manifests demonstrate the supported deployment model: one replica, `Recreate` strategy, and a persistent filesystem volume.

Before applying:

1. Replace the image with an immutable release tag or digest.
2. Replace `BASE_URL` with the public HTTPS origin.
3. Adapt the PVC storage class and size.
4. Store the initial `REGISTRY_API_KEY` in a Kubernetes Secret rather than plaintext.
5. Configure ingress/TLS and any desired authentication for public read/UI routes.
6. Keep UID/GID/fsGroup 65534 and one pod per volume.

Copy `k3s/secret.example.yaml` to an untracked secret file, replace its placeholder, and then apply:

```bash
kubectl apply -f k3s/namespace.yaml
kubectl apply -f k3s/secret.yaml
kubectl apply -f k3s/pvc.yaml
kubectl apply -f k3s/deployment.yaml
kubectl apply -f k3s/service.yaml
```

See [`docs/DEPLOYMENT.md`](../../docs/DEPLOYMENT.md) for production requirements.
