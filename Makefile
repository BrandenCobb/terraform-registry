.PHONY: help build test lint clean docker-build docker-push run dev install-tools

# Configuration
REGISTRY ?= ghcr.io/brandencobb
IMAGE_NAME ?= terraform-registry
IMAGE_TAG ?= latest
S3_BUCKET ?= terraform-registry

# Go configuration
GOCMD = go
GOBUILD = $(GOCMD) build
GOTEST = $(GOCMD) test
GOGET = $(GOCMD) get
GOMOD = $(GOCMD) mod
BINARY_NAME = terraform-registry
COVERAGE_FILE = coverage.out

# Directories
SRC_DIR = registry-server
DIST_DIR = dist
SCRIPTS_DIR = scripts

# Default target
.DEFAULT_GOAL := help

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install-tools: ## Install development tools
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	@echo "Tools installed to $(shell go env GOPATH)/bin"
	@echo "Make sure $(shell go env GOPATH)/bin is in your PATH"

deps: ## Download Go dependencies
	@echo "Downloading dependencies..."
	cd $(SRC_DIR) && $(GOMOD) download
	cd $(SRC_DIR) && $(GOMOD) tidy

build: ## Build the registry server binary
	@echo "Building $(BINARY_NAME)..."
	mkdir -p $(DIST_DIR)
	cd $(SRC_DIR) && CGO_ENABLED=0 $(GOBUILD) -v \
		-ldflags="-w -s -X main.version=$(IMAGE_TAG)" \
		-o ../$(DIST_DIR)/$(BINARY_NAME) \
		main.go storage.go
	@echo "Binary built: $(DIST_DIR)/$(BINARY_NAME)"

test: ## Run unit tests
	@echo "Running tests..."
	mkdir -p $(DIST_DIR)
	cd $(SRC_DIR) && SKIP_STORAGE_INIT=true $(GOTEST) -v -coverprofile=../$(DIST_DIR)/$(COVERAGE_FILE) -covermode=atomic ./...
	@echo "Coverage report: $(DIST_DIR)/$(COVERAGE_FILE)"

test-coverage: test ## Run tests and show coverage
	cd $(SRC_DIR) && $(GOCMD) tool cover -html=../$(DIST_DIR)/$(COVERAGE_FILE)

test-integration: ## Run integration tests
	@echo "Running integration tests..."
	@./tests/integration/run-tests.sh || echo "Integration tests not yet implemented"

lint: ## Run linters
	@echo "Running linters..."
	cd $(SRC_DIR) && gofmt -l -w .
	cd $(SRC_DIR) && golangci-lint run ./... || echo "golangci-lint not installed"
	@echo "Running ShellCheck on scripts..."
	@shellcheck $(SCRIPTS_DIR)/*.sh || echo "ShellCheck not installed"

fmt: ## Format Go code
	@echo "Formatting code..."
	cd $(SRC_DIR) && gofmt -s -w .

vet: ## Run go vet
	@echo "Running go vet..."
	cd $(SRC_DIR) && $(GOCMD) vet ./...

security-scan: ## Run security vulnerability scan
	@echo "Running security scan..."
	cd $(SRC_DIR) && govulncheck ./... || echo "govulncheck not installed"

docker-build: ## Build Docker image
	@echo "Building Docker image $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)..."
	docker build -t $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) \
		-f $(SRC_DIR)/Dockerfile \
		$(SRC_DIR)
	docker tag $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) $(REGISTRY)/$(IMAGE_NAME):latest

docker-push: docker-build ## Push Docker image to registry
	@echo "Pushing Docker image..."
	docker push $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	docker push $(REGISTRY)/$(IMAGE_NAME):latest

docker-run: docker-build ## Run Docker container locally
	@echo "Starting Docker container..."
	mkdir -p data
	docker run -d --name $(IMAGE_NAME) \
		-p 5000:8080 \
		-v $(PWD)/data:/var/lib/terraform-registry \
		-e STORAGE_TYPE=filesystem \
		-e BASE_URL=http://localhost:5000 \
		$(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	@echo "Registry running at http://localhost:5000"

docker-stop: ## Stop Docker container
	@echo "Stopping Docker container..."
	docker stop $(IMAGE_NAME) || true
	docker rm $(IMAGE_NAME) || true

run: build ## Run registry server locally
	@echo "Starting registry server..."
	mkdir -p /tmp/terraform-registry
	STORAGE_TYPE=filesystem \
	STORAGE_PATH=/tmp/terraform-registry \
	BASE_URL=http://localhost:8080 \
	PORT=8080 \
	./$(DIST_DIR)/$(BINARY_NAME)

dev: ## Run in development mode with hot reload
	@echo "Starting development mode..."
	mkdir -p /tmp/terraform-registry
	cd $(SRC_DIR) && \
	STORAGE_TYPE=filesystem \
	STORAGE_PATH=/tmp/terraform-registry \
	BASE_URL=http://localhost:8080 \
	PORT=8080 \
	$(GOCMD) run main.go storage.go

clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	rm -rf $(DIST_DIR)
	rm -rf $(SRC_DIR)/$(BINARY_NAME)
	rm -rf /tmp/terraform-registry
	docker rmi $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true
	docker rmi $(REGISTRY)/$(IMAGE_NAME):latest 2>/dev/null || true

dist-clean: clean ## Clean all generated files including dependencies
	@echo "Cleaning all generated files..."
	rm -rf $(SRC_DIR)/vendor

init-s3: ## Initialize S3 bucket
	@echo "Initializing S3 bucket..."
	$(SCRIPTS_DIR)/init-s3-bucket.sh $(S3_BUCKET) us-west-1

upload-provider: ## Upload a provider (requires NAMESPACE, NAME, VERSION, BINARY)
	@if [ -z "$(NAMESPACE)" ] || [ -z "$(NAME)" ] || [ -z "$(VERSION)" ] || [ -z "$(BINARY)" ]; then \
		echo "Error: NAMESPACE, NAME, VERSION, and BINARY are required"; \
		echo "Usage: make upload-provider NAMESPACE=hashicorp NAME=aws VERSION=6.31.0 BINARY=path/to/binary"; \
		exit 1; \
	fi
	mkdir -p data
	$(SCRIPTS_DIR)/upload-provider.sh \
		--storage filesystem \
		--path ./data \
		--namespace $(NAMESPACE) \
		--name $(NAME) \
		--version $(VERSION) \
		--binary $(BINARY)

upload-module: ## Upload a module (requires NAMESPACE, NAME, PROVIDER, VERSION, SOURCE)
	@if [ -z "$(NAMESPACE)" ] || [ -z "$(NAME)" ] || [ -z "$(PROVIDER)" ] || [ -z "$(VERSION)" ] || [ -z "$(SOURCE)" ]; then \
		echo "Error: NAMESPACE, NAME, PROVIDER, VERSION, and SOURCE are required"; \
		echo "Usage: make upload-module NAMESPACE=example NAME=vpc PROVIDER=aws VERSION=1.0.0 SOURCE=path/to/module"; \
		exit 1; \
	fi
	mkdir -p data
	$(SCRIPTS_DIR)/upload-module.sh \
		--storage filesystem \
		--path ./data \
		--namespace $(NAMESPACE) \
		--name $(NAME) \
		--provider $(PROVIDER) \
		--version $(VERSION) \
		--source $(SOURCE)

check: lint test vet ## Run all checks (lint, test, vet)
	@echo "All checks passed!"

ci: deps check build docker-build ## Run CI pipeline locally

release: ## Create a release (requires VERSION)
	@if [ -z "$(VERSION)" ]; then \
		echo "Error: VERSION is required"; \
		echo "Usage: make release VERSION=1.0.0"; \
		exit 1; \
	fi
	@echo "Creating release $(VERSION)..."
	git tag -a v$(VERSION) -m "Release v$(VERSION)"
	git push origin v$(VERSION)

all: clean deps check build docker-build ## Build everything

.PHONY: all
