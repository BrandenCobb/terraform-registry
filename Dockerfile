FROM golang:1.26.5-alpine AS builder

ARG VERSION=dev
WORKDIR /app

COPY registry-server/go.mod registry-server/go.sum ./registry-server/
RUN cd registry-server && go mod download
COPY registry-server/*.go ./registry-server/
COPY registry-server/ui/ ./registry-server/ui/
RUN cd registry-server && CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-w -s -X main.version=${VERSION}" -o /app/terraform-registry .

COPY cmd/tfreg/go.mod ./cmd/tfreg/
COPY cmd/tfreg/*.go ./cmd/tfreg/
RUN cd cmd/tfreg && CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-w -s -X main.version=${VERSION}" -o /app/tfreg .

FROM alpine:3.24

ARG VERSION=dev
LABEL org.opencontainers.image.source="https://github.com/BrandenCobb/terraform-registry" \
      org.opencontainers.image.description="Self-hosted Terraform registry for providers and modules" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

RUN apk --no-cache add ca-certificates && \
    mkdir -p /var/lib/terraform-registry/providers /var/lib/terraform-registry/modules /var/lib/terraform-registry/tmp && \
    chown -R nobody:nobody /var/lib/terraform-registry

WORKDIR /app
COPY --from=builder --chown=nobody:nobody /app/terraform-registry /app/terraform-registry
COPY --from=builder /app/tfreg /usr/local/bin/tfreg

EXPOSE 8080
VOLUME ["/var/lib/terraform-registry"]
USER nobody

ENV STORAGE_PATH=/var/lib/terraform-registry \
    BASE_URL=http://localhost:8080 \
    PORT=8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/terraform-registry"]
