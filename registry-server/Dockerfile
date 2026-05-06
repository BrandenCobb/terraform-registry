FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o terraform-registry .

FROM alpine:latest

LABEL org.opencontainers.image.source="https://github.com/BrandenCobb/terraform-registry"
LABEL org.opencontainers.image.description="Self-hosted Terraform registry for providers and modules"
LABEL org.opencontainers.image.licenses="MIT"

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/terraform-registry .

# Create default storage directory
RUN mkdir -p /var/lib/terraform-registry/providers /var/lib/terraform-registry/modules && \
    chmod -R 755 /var/lib/terraform-registry && \
    chmod +x /app/terraform-registry

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
