# Publishing a Provider

Build the provider for each target OS/architecture, package each executable as a ZIP, and upload it with `tfreg`.

```bash
VERSION=1.2.3
NAME=example
NAMESPACE=acme

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -o "terraform-provider-${NAME}_v${VERSION}" .

tfreg bundle provider \
  --namespace "$NAMESPACE" --name "$NAME" --version "$VERSION" \
  --os linux --arch amd64 \
  --binary "terraform-provider-${NAME}_v${VERSION}"

tfreg push provider \
  --namespace "$NAMESPACE" --name "$NAME" --version "$VERSION" \
  --os linux --arch amd64 \
  --file "terraform-provider-${NAME}_${VERSION}_linux_amd64.zip"
```

Set `TFREG_REGISTRY` and `TFREG_API_KEY` before publishing. Repeat for all supported targets. The registry validates ZIP magic bytes, streams the upload with a configured limit, stores it atomically, computes SHA-256, and publishes platform metadata.

CI should run provider tests before packaging. Signing policy and source provenance remain the publisher's responsibility; this registry stores and serves artifacts but does not independently attest that uploaded code is trustworthy.
