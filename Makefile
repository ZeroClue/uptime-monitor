# Makefile for Uptime Monitor
# Common development tasks

.PHONY: build test lint docker-build docker-push run clean deps fmt vet help

# Build variables
BINARY_NAME := uptime-monitor
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

# Go variables
GO := go
GOFLAGS := -trimpath
BUILD_DIR := ./bin

# Docker variables
DOCKER_IMAGE := uptime-monitor
DOCKER_TAG := $(VERSION)
DOCKERFILE := Dockerfile

# Default target
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary"
	@echo "  test          - Run unit tests"
	@echo "  integration   - Run integration tests (requires Docker)"
	@echo "  lint          - Run linters (golangci-lint)"
	@echo "  fmt           - Format code (gofmt)"
	@echo "  vet           - Run go vet"
	@echo "  docker-build  - Build Docker image"
	@echo "  docker-push   - Push Docker image to registry"
	@echo "  run           - Run locally with config"
	@echo "  clean         - Clean build artifacts"
	@echo "  deps          - Download and tidy dependencies"

# Build the binary
build: $(BUILD_DIR)/$(BINARY_NAME)

$(BUILD_DIR)/$(BINARY_NAME):
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/monitor

# Run unit tests
test:
	$(GO) test $(GOFLAGS) -v -race -coverprofile=coverage.out ./...

# Run integration tests (requires Docker)
integration:
	$(GO) test $(GOFLAGS) -v -tags=integration ./...

# Run linters
lint:
	@which golangci-lint > /dev/null || (echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest" && exit 1)
	golangci-lint run ./...

# Format code
fmt:
	$(GO) fmt $(GOFLAGS) ./...

# Run go vet
vet:
	$(GO) vet $(GOFLAGS) ./...

# Build Docker image
docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):latest -f $(DOCKERFILE) .

# Push Docker image (requires login)
docker-push:
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):latest

# Run locally (requires config in ./config)
run: build
	@mkdir -p data
	DASHBOARD_PASSWORD=dev \
	POLL_INTERVAL=30s \
	LOG_LEVEL=debug \
	$(BUILD_DIR)/$(BINARY_NAME) \
		-config ./config \
		-data ./data

# Download and tidy dependencies
deps:
	$(GO) mod download
	$(GO) mod tidy

# Generate code (if using code generation)
generate:
	$(GO) generate $(GOFLAGS) ./...

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	rm -f $(BINARY_NAME)

# Run all checks (CI pipeline)
ci: deps fmt vet lint test

# Development: run with live reload (requires air: go install github.com/air-verse/air@latest)
dev:
	@which air > /dev/null || (echo "air not installed. Run: go install github.com/air-verse/air@latest" && exit 1)
	air -c .air.toml

# Install development tools
install-tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/air-verse/air@latest
	go install golang.org/x/tools/cmd/goimports@latest