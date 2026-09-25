# Publishing a Provider

Set the registry connection once:

```bash
export TFREG_REGISTRY=https://registry.example.com
export TFREG_API_KEY='your-write-or-admin-key'
```

From a cloned Go provider repository, publish a platform in one command:

```bash
tfreg publish provider \
  --namespace acme --name example --version 1.2.3 \
  --source . --os linux --arch amd64
```

`tfreg` runs `go test ./...`, cross-compiles with `CGO_ENABLED=0`, names the executable according to Terraform conventions, creates the ZIP, uploads it, and removes temporary build files. Repeat for every supported OS/architecture. Providers that require CGO need an external build pipeline followed by the existing `tfreg bundle provider` and `tfreg push provider` commands.

To replicate the exact package as an OCI 1.1 artifact, add a full `--oci-ref`. For ECR:

```bash
aws ecr get-login-password --region us-east-1 | \
  tfreg publish provider \
    --namespace acme --name example --version 1.2.3 \
    --source . --os linux --arch amd64 \
    --oci-ref 123456789012.dkr.ecr.us-east-1.amazonaws.com/terraform/providers/acme/example:1.2.3-linux-amd64 \
    --oci-username AWS --oci-password-stdin
```

The ECR repository must already exist. Without explicit credentials, `tfreg` reads Docker's configured credential store.

The registry validates ZIP magic bytes, streams uploads with the configured limit, stores them atomically, computes SHA-256, scans them with Trivy, and publishes platform metadata only when the configured security policy allows it. Vulnerability scanning is not source provenance: signing and build attestations remain the publisher's responsibility.
