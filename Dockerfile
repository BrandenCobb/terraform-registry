FROM golang:1.26-alpine AS builder

WORKDIR /app

# Build registry server
COPY registry-server/go.mod registry-server/go.sum ./registry-server/
RUN cd registry-server && go mod download

COPY registry-server/*.go ./registry-server/
COPY registry-server/ui/ ./registry-server/ui/

RUN cd registry-server && CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /app/terraform-registry .

# Build CLI tool
COPY cmd/tfreg/go.mod ./cmd/tfreg/
COPY cmd/tfreg/*.go ./cmd/tfreg/

RUN cd cmd/tfreg && CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o /app/tfreg .

FROM alpine:latest

LABEL org.opencontainers.image.source="https://github.com/BrandenCobb/terraform-registry"
LABEL org.opencontainers.image.description="Self-hosted Terraform registry for providers and modules"
LABEL org.opencontainers.image.licenses="MIT"

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/terraform-registry .
COPY --from=builder /app/tfreg /usr/local/bin/tfreg

# Create default storage directory
RUN mkdir -p /var/lib/terraform-registry/providers /var/lib/terraform-registry/modules && \
    chmod -R 755 /var/lib/terraform-registry && \
    chmod +x /app/terraform-registry /usr/local/bin/tfreg

EXPOSE 8080

# Volume for persistent storage
VOLUME ["/var/lib/terraform-registry"]

USER nobody

ENV STORAGE_TYPE=filesystem
ENV STORAGE_PATH=/var/lib/terraform-registry
ENV BASE_URL=http://localhost:8080
ENV PORT=8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

CMD ["./terraform-registry"]
