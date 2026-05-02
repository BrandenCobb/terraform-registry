# Kubernetes Deployment Example

This example shows how to deploy the registry to Kubernetes with both filesystem and S3 storage options.

## Option 1: Filesystem Storage (PVC)

### 1. Create Namespace

```bash
kubectl create namespace terraform-registry
```

### 2. Create PersistentVolumeClaim

Create `pvc.yaml`:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: terraform-registry-data
  namespace: terraform-registry
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
  storageClassName: standard  # Adjust for your cluster
```

Apply:

```bash
kubectl apply -f pvc.yaml
```

### 3. Create Deployment

Create `deployment.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: terraform-registry
  namespace: terraform-registry
  labels:
    app: terraform-registry
spec:
  replicas: 1  # Use 1 for filesystem storage
  selector:
    matchLabels:
      app: terraform-registry
  template:
    metadata:
      labels:
        app: terraform-registry
    spec:
      securityContext:
        fsGroup: 65534
        runAsNonRoot: true
      containers:
      - name: registry
        image: ghcr.io/brandencobb/terraform-registry:latest
        imagePullPolicy: Always
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: STORAGE_TYPE
          value: "filesystem"
        - name: BASE_URL
          value: "http://terraform-registry.terraform-registry.svc.cluster.local"
        - name: PORT
          value: "8080"
        volumeMounts:
        - name: data
          mountPath: /var/lib/terraform-registry
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          requests:
            memory: "64Mi"
            cpu: "100m"
          limits:
            memory: "256Mi"
            cpu: "500m"
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: terraform-registry-data
```

Apply:

```bash
kubectl apply -f deployment.yaml
```

### 4. Create Service

Create `service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: terraform-registry
  namespace: terraform-registry
  labels:
    app: terraform-registry
spec:
  type: ClusterIP
  ports:
  - port: 80
    targetPort: 8080
    protocol: TCP
    name: http
  selector:
    app: terraform-registry
```

Apply:

```bash
kubectl apply -f service.yaml
```

### 5. Create Ingress (Optional)

Create `ingress.yaml`:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: terraform-registry
  namespace: terraform-registry
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
spec:
  ingressClassName: nginx
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

Apply:

```bash
kubectl apply -f ingress.yaml
```

## Option 2: S3 Storage with IRSA

### 1. Create IAM Role with IRSA

```bash
# Set variables
CLUSTER_NAME="my-cluster"
ACCOUNT_ID="123456789012"
REGION="us-east-1"

# Create IAM policy
cat > registry-policy.json <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:ListBucket"
      ],
      "Resource": [
        "arn:aws:s3:::terraform-registry",
        "arn:aws:s3:::terraform-registry/*"
      ]
    }
  ]
}
EOF

aws iam create-policy \
  --policy-name TerraformRegistryS3Access \
  --policy-document file://registry-policy.json

# Create service account with IRSA
eksctl create iamserviceaccount \
  --name terraform-registry \
  --namespace terraform-registry \
  --cluster ${CLUSTER_NAME} \
  --attach-policy-arn arn:aws:iam::${ACCOUNT_ID}:policy/TerraformRegistryS3Access \
  --approve \
  --override-existing-serviceaccounts
```

### 2. Create Deployment with S3

Create `deployment-s3.yaml`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: terraform-registry
  namespace: terraform-registry
spec:
  replicas: 3  # Can scale with S3 storage
  selector:
    matchLabels:
      app: terraform-registry
  template:
    metadata:
      labels:
        app: terraform-registry
    spec:
      serviceAccountName: terraform-registry
      securityContext:
        runAsNonRoot: true
      containers:
      - name: registry
        image: ghcr.io/brandencobb/terraform-registry:latest
        imagePullPolicy: Always
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: STORAGE_TYPE
          value: "s3"
        - name: S3_BUCKET
          value: "terraform-registry"
        - name: AWS_REGION
          value: "us-east-1"
        - name: BASE_URL
          value: "https://registry.example.com"
        - name: PORT
          value: "8080"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          requests:
            memory: "64Mi"
            cpu: "100m"
          limits:
            memory: "256Mi"
            cpu: "500m"
```

Apply:

```bash
kubectl apply -f deployment-s3.yaml
```

## Verify Deployment

```bash
# Check pods
kubectl get pods -n terraform-registry

# Check logs
kubectl logs -n terraform-registry -l app=terraform-registry

# Test health endpoint
kubectl port-forward -n terraform-registry svc/terraform-registry 8080:80
curl http://localhost:8080/health
```

## Configure Terraform

Create `~/.terraformrc`:

```hcl
provider_installation {
  network_mirror {
    url = "https://registry.example.com"
  }
}
```

## Upload Provider from CI/CD

```bash
# From GitLab CI or GitHub Actions
kubectl exec -n terraform-registry deployment/terraform-registry -- \
  /scripts/upload-provider.sh \
    --storage filesystem \
    --path /var/lib/terraform-registry \
    --namespace hashicorp \
    --name aws \
    --version 6.31.0 \
    --binary ./terraform-provider-aws_v6.31.0_linux_amd64
```

## Monitoring

Add Prometheus annotations:

```yaml
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
    prometheus.io/path: "/metrics"
```

## Scaling

**Filesystem storage**: Max 1 replica (ReadWriteOnce PVC)
**S3 storage**: Scale horizontally with multiple replicas

```bash
kubectl scale deployment/terraform-registry -n terraform-registry --replicas=5
```

## Clean Up

```bash
kubectl delete namespace terraform-registry
```
