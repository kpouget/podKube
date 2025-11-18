# Podman Kubernetes API Server Makefile

BINARY_NAME=server
BUILD_DIR=./cmd/server
PORT=8443
HOST=127.0.0.1

# Default target
.PHONY: all
all: build

# Build the server
.PHONY: build
build:
	@echo "Building $(BINARY_NAME)..."
	go build -o $(BINARY_NAME) $(BUILD_DIR)

# Run the server
.PHONY: run
run: build
	@echo "Starting Podman Kubernetes API Server on $(HOST):$(PORT)..."
	./$(BINARY_NAME) --port $(PORT) --host $(HOST)

# Run in background
.PHONY: run-bg
run-bg: build
	@echo "Starting Podman Kubernetes API Server in background on $(HOST):$(PORT)..."
	./$(BINARY_NAME) --port $(PORT) --host $(HOST) > /tmp/podman-adapter 2>&1 &
	@echo "Server started. Use 'make stop' to stop it."

# Stop background server
.PHONY: stop
stop:
	@echo "Stopping server..."
	pkill -f "./$(BINARY_NAME)" || echo "No server process found"

# Test the server endpoints
.PHONY: test
test:
	@echo "Testing API endpoints..."
	@echo "1. Testing health endpoint:"
	curl -k https://$(HOST):$(PORT)/healthz || echo "Health check failed"
	@echo -e "\n2. Testing API discovery:"
	curl -k https://$(HOST):$(PORT)/api || echo "API discovery failed"
	@echo -e "\n3. Testing pod listing:"
	curl -k https://$(HOST):$(PORT)/api/v1/pods || echo "Pod listing failed"

# Test with oc CLI
.PHONY: test-oc
test-oc:
	@echo "Testing with OpenShift CLI:"
	@echo "1. List all pods:"
	oc get pods --all-namespaces --server=https://$(HOST):$(PORT) --insecure-skip-tls-verify
	@echo -e "\n2. Get specific pod as YAML:"
	oc get pod sample-pod-1 -o yaml --server=https://$(HOST):$(PORT) --insecure-skip-tls-verify

# Clean build artifacts
.PHONY: clean
clean:
	@echo "Cleaning up..."
	rm -f $(BINARY_NAME)

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting Go code..."
	go fmt ./...

# Download dependencies
.PHONY: deps
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Development workflow - build, format, and run
.PHONY: dev
dev: fmt build run

# Show help
.PHONY: help
help:
	@echo "Podman Kubernetes API Server"
	@echo ""
	@echo "Available targets:"
	@echo "  build     - Build the server binary"
	@echo "  run       - Build and run the server"
	@echo "  run-bg    - Build and run the server in background"
	@echo "  stop      - Stop background server"
	@echo "  test      - Test server endpoints with curl"
	@echo "  test-oc   - Test server with oc CLI"
	@echo "  clean     - Remove build artifacts"
	@echo "  fmt       - Format Go code"
	@echo "  deps      - Download and tidy dependencies"
	@echo "  dev       - Format, build, and run (development workflow)"
	@echo "  help      - Show this help message"
	@echo ""
	@echo "Configuration:"
	@echo "  PORT=$(PORT)"
	@echo "  HOST=$(HOST)"
	@echo "  BINARY_NAME=$(BINARY_NAME)"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make run"
	@echo "  make run-bg && make test-oc"
	@echo "  PORT=9443 make run"
