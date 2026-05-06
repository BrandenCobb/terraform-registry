# Kubernetes Manifests

This directory contains Kubernetes manifests for deploying the Terraform Registry.

## Files

- **namespace.yaml** - Creates `terraform-registry` namespace
- **serviceaccount.yaml** - ServiceAccount for IRSA (S3 storage)
- **deployment.yaml** - Main registry deployment
- **service.yaml** - ClusterIP service
- **ingress.yaml** - Ingress for external access
- **istio-virtualservice.yaml** - Istio VirtualService (alternative to Ingress)

## Quick Deploy

### All-in-one

```bash
kubectl apply -f .
```

### Step-by-step

```bash
# 1. Create namespace
kubectl apply -f namespace.yaml

# 2. Create service account (for IRSA with S3)
kubectl apply -f serviceaccount.yaml

# 3. Create deployment
kubectl apply -f deployment.yaml

# 4. Create service
kubectl apply -f service.yaml

# 5. Create ingress (choose one)
kubectl apply -f ingress.yaml
# OR for Istio
kubectl apply -f istio-virtualservice.yaml
```

## Configuration

### Filesystem Storage

The default `deployment.yaml` uses filesystem storage with a PVC. You'll need to:

1. Create a PersistentVolumeClaim named `terraform-registry-data`
2. Or modify the deployment to use an existing PVC

### S3 Storage with IRSA

To use S3 storage:

1. Set up IRSA (see `../../s3/irsa/`)
2. Update `deployment.yaml`:
   - Change `STORAGE_TYPE` to `s3`
   - Add `S3_BUCKET` environment variable
   - Add `AWS_REGION` environment variable
   - Ensure `serviceAccountName: terraform-registry` is set

Example environment variables for S3:

```yaml
env:
- name: STORAGE_TYPE
  value: "s3"
- name: S3_BUCKET
  value: "terraform-registry"
- name: AWS_REGION
  value: "us-east-1"
- name: BASE_URL
  value: "https://registry.example.com"
```

## Customization

### Replicas

For S3 storage, you can scale horizontally:

```yaml
spec:
  replicas: 3
```

For filesystem storage, keep at 1 replica (PVC is ReadWriteOnce).

### Resources

Adjust resource limits based on your needs:

```yaml
resources:
  requests:
    memory: "64Mi"
    cpu: "100m"
  limits:
    memory: "256Mi"
    cpu: "500m"
```

### Ingress

Update `ingress.yaml` with your domain:

```yaml
spec:
  tls:
  - hosts:
    - registry.example.com
    secretName: terraform-registry-tls
  rules:
  - host: registry.example.com
```

### Istio

If using Istio, update `istio-virtualservice.yaml`:

```yaml
spec:
  hosts:
  - registry.example.com
  gateways:
  - your-gateway
```

## Verification

```bash
# Check pods
kubectl get pods -n terraform-registry

# Check service
kubectl get svc -n terraform-registry

# Check ingress
kubectl get ingress -n terraform-registry

# Test health
kubectl port-forward -n terraform-registry svc/terraform-registry 8080:80
curl http://localhost:8080/health
```

## See Also

- [Kubernetes Example Documentation](../README.md)
- [IRSA Setup](../../s3/irsa/)
