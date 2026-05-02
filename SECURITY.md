# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.x.x   | :white_check_mark: |
| < 1.0   | :x:                |

## Reporting a Vulnerability

We take security vulnerabilities seriously. If you discover a security issue, please follow these steps:

### 1. Do Not Open a Public Issue

Please do not open a public GitHub issue for security vulnerabilities. This helps prevent exploitation before a fix is available.

### 2. Report Privately

Send an email to brandencobb@gmail.com with:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### 3. Response Timeline

- **Initial Response**: Within 48 hours
- **Status Update**: Within 7 days
- **Fix Timeline**: Depends on severity

### 4. Disclosure Process

1. We will confirm the vulnerability
2. We will develop and test a fix
3. We will release a security patch
4. We will publicly disclose the vulnerability after the patch is released

## Security Best Practices

### Deployment

#### Network Security

- **Firewall**: Restrict access to registry port (default 8080)
- **Internal Only**: Deploy on internal network only
- **TLS**: Use HTTPS via reverse proxy (NGINX, Istio, etc.)
- **Authentication**: Implement authentication at proxy level

#### Container Security

- **Non-Root**: Registry runs as `nobody` user (UID 65534)
- **Read-Only**: Supports read-only root filesystem
- **No Capabilities**: No special Linux capabilities required
- **Security Context**: 
  ```yaml
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    readOnlyRootFilesystem: true
    allowPrivilegeEscalation: false
    capabilities:
      drop:
      - ALL
  ```

#### Storage Security

**Filesystem Storage:**
- **Permissions**: Ensure volume permissions are 755
- **Isolation**: Use dedicated volumes
- **Backup**: Regular backups of registry data

**S3 Storage:**
- **IRSA**: Use IAM Roles for Service Accounts (no static credentials)
- **Encryption**: Enable S3 server-side encryption
- **Versioning**: Enable S3 versioning for audit trail
- **Access Logs**: Enable S3 access logging
- **Bucket Policy**: Restrict access to specific IAM roles

### Kubernetes Security

```yaml
# Example secure deployment
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      serviceAccountName: terraform-registry
      securityContext:
        runAsNonRoot: true
        seccompProfile:
          type: RuntimeDefault
      containers:
      - name: registry
        securityContext:
          runAsUser: 65534
          runAsGroup: 65534
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop:
            - ALL
```

### Network Policies

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: terraform-registry
spec:
  podSelector:
    matchLabels:
      app: terraform-registry
  policyTypes:
  - Ingress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: terraform-clients
    ports:
    - protocol: TCP
      port: 8080
```

## Known Security Considerations

### 1. No Built-in Authentication

The registry does not implement authentication. You must:

- Deploy behind an authenticating reverse proxy
- Use Kubernetes ingress with authentication
- Implement network-level access controls

### 2. No Provider Verification

The registry does not verify provider signatures. You should:

- Only upload trusted provider binaries
- Implement verification in your CI/CD pipeline
- Use provider checksums for validation

### 3. Module Content

The registry does not scan module content. You should:

- Review module code before uploading
- Implement static analysis in your pipeline
- Use separate registries for untrusted modules

### 4. Rate Limiting

The registry does not implement rate limiting. You should:

- Use a reverse proxy with rate limiting
- Monitor for unusual access patterns
- Implement IP-based restrictions if needed

## Security Updates

### Automatic Updates

We recommend:

- Using Dependabot for Go module updates
- Subscribing to GitHub security advisories
- Monitoring CVE databases for dependencies

### Manual Updates

```bash
# Update Go dependencies
cd registry-server
go get -u all
go mod tidy

# Rebuild and test
go test ./...
docker build -t terraform-registry:latest .
```

## Vulnerability Scanning

### Container Scanning

```bash
# Using Trivy
trivy image terraform-registry:latest

# Using Grype
grype terraform-registry:latest
```

### Dependency Scanning

```bash
# Using govulncheck
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

## Compliance

### FIPS Compliance

For FIPS 140-2 compliance:

- Use FIPS-validated base images
- Compile providers with FIPS-compliant Go
- See [Provider Build Guide](docs/PROVIDER_BUILD.md)

### Audit Logging

Enable audit logging:

**S3 Storage:**
- S3 access logs
- CloudTrail for API calls

**Filesystem Storage:**
- Container logs (stdout/stderr)
- OS-level audit logs (auditd)

## Incident Response

If a security incident is detected:

1. **Isolate**: Stop the affected registry instance
2. **Assess**: Determine scope of compromise
3. **Notify**: Contact security team
4. **Remediate**: Apply fixes and redeploy
5. **Review**: Analyze logs and improve security

## Contact

For security concerns:
- Email: brandencobb@gmail.com
- PGP Key: Available on request

For general questions:
- GitHub Issues: https://github.com/BrandenCobb/terraform-registry/issues
- GitHub Discussions: https://github.com/BrandenCobb/terraform-registry/discussions
