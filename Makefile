.PHONY: help build test lint fmt vet security-scan docker-build docker-run docker-stop run dev clean check ci release

REGISTRY ?= ghcr.io/brandencobb
IMAGE_NAME ?= terraform-registry
IMAGE_TAG ?= dev
VERSION ?= $(IMAGE_TAG)
DIST_DIR := dist

.DEFAULT_GOAL := help

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build server and CLI
	mkdir -p $(DIST_DIR)
	cd registry-server && CGO_ENABLED=0 go build -trimpath -ldflags="-w -s -X main.version=$(VERSION)" -o ../$(DIST_DIR)/terraform-registry .
	cd cmd/tfreg && CGO_ENABLED=0 go build -trimpath -ldflags="-w -s -X main.version=$(VERSION)" -o ../../$(DIST_DIR)/tfreg .

test: ## Run race-enabled tests for server and CLI
	cd registry-server && go test -race -cover ./...
	cd cmd/tfreg && go test -race ./...

fmt: ## Format Go sources
	gofmt -s -w registry-server cmd/tfreg

lint: ## Verify formatting and run go vet
	@test -z "$$(gofmt -s -l registry-server cmd/tfreg)" || (gofmt -s -l registry-server cmd/tfreg && exit 1)
	cd registry-server && go vet ./...
	cd cmd/tfreg && go vet ./...

vet: lint ## Alias for lint

security-scan: ## Scan both Go modules for known vulnerabilities
	cd registry-server && go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
	cd cmd/tfreg && go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
	cd registry-server && go run github.com/securego/gosec/v2/cmd/gosec@v2.28.0 ./...
	cd cmd/tfreg && go run github.com/securego/gosec/v2/cmd/gosec@v2.28.0 ./...

docker-build: ## Build the production container
	docker build --build-arg VERSION=$(VERSION) -t $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) .

docker-run: docker-build ## Run with a writable named volume
	docker rm -f $(IMAGE_NAME) 2>/dev/null || true
	docker volume create $(IMAGE_NAME)-data >/dev/null
	docker run -d --name $(IMAGE_NAME) -p 5000:8080 -v $(IMAGE_NAME)-data:/var/lib/terraform-registry -e BASE_URL=http://localhost:5000 $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

docker-stop: ## Stop the local container
	docker rm -f $(IMAGE_NAME) 2>/dev/null || true

run: build ## Run the built server locally
	mkdir -p /tmp/terraform-registry
	STORAGE_PATH=/tmp/terraform-registry BASE_URL=http://localhost:8080 PORT=8080 ./$(DIST_DIR)/terraform-registry

dev: ## Run server from source
	mkdir -p /tmp/terraform-registry
	cd registry-server && STORAGE_PATH=/tmp/terraform-registry BASE_URL=http://localhost:8080 PORT=8080 go run .

clean: ## Remove build outputs
	rm -rf $(DIST_DIR)

check: lint test build ## Run local quality gates

ci: check docker-build ## Run all CI-equivalent gates

release: ## Tag and push VERSION=x.y.z
	@test -n "$(VERSION)" && test "$(VERSION)" != "$(IMAGE_TAG)" || (echo "Usage: make release VERSION=x.y.z"; exit 1)
	@printf '%s\n' "$(VERSION)" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$$' || (echo "VERSION must be SemVer without a v prefix"; exit 1)
	@test "$$(git branch --show-current)" = main || (echo "Release must run from main"; exit 1)
	@git diff --quiet && git diff --cached --quiet && test -z "$$(git status --porcelain)" || (echo "Release requires a clean worktree"; exit 1)
	@git fetch origin main --tags
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" || (echo "HEAD must equal origin/main"; exit 1)
	@! git rev-parse -q --verify "refs/tags/v$(VERSION)" >/dev/null || (echo "Tag v$(VERSION) already exists"; exit 1)
	@$(MAKE) check security-scan VERSION=v$(VERSION)
	git tag -a v$(VERSION) -m "Release v$(VERSION)"
	git push origin v$(VERSION)
