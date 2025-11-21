# Podman Kubernetes API Server Makefile

BINARY_NAME=server
BUILD_DIR=./cmd/server
PORT=8443
HOST=127.0.0.1
PID_FILE=/tmp/podman-adapter.pid

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
run:
	@echo "Starting Podman Kubernetes API Server on $(HOST):$(PORT)..."
	./$(BINARY_NAME) --port $(PORT) --host $(HOST)

# Run in background
.PHONY: run-bg
run-bg: build
	@echo "Starting Podman Kubernetes API Server in background on $(HOST):$(PORT)..."
	./$(BINARY_NAME) --port $(PORT) --host $(HOST) > /tmp/podman-adapter 2>&1 & echo $$! > $(PID_FILE)
	@echo "Server started with PID $$(cat $(PID_FILE)). Use 'make stop' to stop it."

# Stop background server
.PHONY: stop
stop:
	@echo "Stopping server..."
	@if [ -f $(PID_FILE) ]; then \
		PID=$$(cat $(PID_FILE)); \
		echo "Killing process with PID $$PID"; \
		kill $$PID && echo "Server stopped" || echo "Failed to stop server (PID $$PID may not exist)"; \
		rm -f $(PID_FILE); \
	else \
		echo "No PID file found ($(PID_FILE)). Server may not be running."; \
	fi

# Check server status
.PHONY: status
status:
	@if [ -f $(PID_FILE) ]; then \
		PID=$$(cat $(PID_FILE)); \
		if ps -p $$PID > /dev/null 2>&1; then \
			echo "Server is running with PID $$PID"; \
		else \
			echo "PID file exists but process $$PID is not running"; \
			rm -f $(PID_FILE); \
		fi \
	else \
		echo "Server is not running (no PID file found)"; \
	fi

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

# Run unit tests
.PHONY: test-unit
test-unit:
	@echo "Running unit tests..."
	go test -v ./test/unit/... -timeout=30m

# Run integration tests (requires podman and oc)
.PHONY: test-integration
test-integration:
	@echo "Running integration tests..."
	@echo "Prerequisites: podman and oc must be installed and available"
	go test -v ./test/integration/... -timeout=30m

# Run all Go tests
.PHONY: test-all
test-all: test-unit test-integration

# Run tests with coverage
.PHONY: test-coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./test/... -timeout=30m
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Test CLI compatibility
.PHONY: test-cli
test-cli: run-bg
	@echo "Testing CLI compatibility (requires server to be running)..."
	@sleep 2  # Wait for server to start
	@echo "Running oc compatibility tests..."
	go test -v ./test/integration/cli_compatibility_test.go -timeout=10m || true
	@$(MAKE) stop

# Test resource consistency (requires server to be running)
.PHONY: test-resources
test-resources: run-bg
	@echo "Testing resource consistency (requires server to be running)..."
	@sleep 2  # Wait for server to start
	@echo "Running resource consistency tests..."
	go test -v ./test/integration/resource_consistency_test.go -timeout=15m || true
	@$(MAKE) stop

# Test streaming functionality (requires server to be running)
.PHONY: test-streaming
test-streaming: run-bg
	@echo "Testing streaming functionality (requires server to be running)..."
	@sleep 2  # Wait for server to start
	@echo "Running streaming tests..."
	go test -v ./test/integration/streaming_test.go -timeout=10m || true
	@$(MAKE) stop

# Full integration test suite with server lifecycle
.PHONY: test-integration-full
test-integration-full: build
	@echo "Running full integration test suite..."
	@$(MAKE) run-bg
	@sleep 3  # Wait for server to start properly
	@echo "Testing CLI compatibility..."
	go test -v ./test/integration/cli_compatibility_test.go -timeout=10m || echo "CLI tests completed with some failures"
	@echo "Testing resource consistency..."
	go test -v ./test/integration/resource_consistency_test.go -timeout=15m || echo "Resource tests completed with some failures"
	@echo "Testing streaming functionality..."
	go test -v ./test/integration/streaming_test.go -timeout=10m || echo "Streaming tests completed with some failures"
	@$(MAKE) stop
	@echo "Full integration test suite completed"

# Test prerequisites check
.PHONY: test-prereqs
test-prereqs:
	@echo "Checking test prerequisites..."
	@command -v go >/dev/null 2>&1 || { echo "Error: Go is required but not installed"; exit 1; }
	@command -v podman >/dev/null 2>&1 || { echo "Error: podman is required but not installed"; exit 1; }
	@command -v oc >/dev/null 2>&1 || echo "Warning: oc CLI not found - CLI tests will be skipped"
	@echo "Checking Go test packages..."
	@go list -m github.com/stretchr/testify >/dev/null 2>&1 || { echo "Error: testify package missing - run 'make deps'"; exit 1; }
	@echo "Prerequisites check completed"

# Clean test artifacts
.PHONY: test-clean
test-clean:
	@echo "Cleaning test artifacts..."
	@rm -f coverage.out coverage.html
	@echo "Cleaning up test containers..."
	@podman ps -a --format "{{.Names}}" | grep -E "(test-|cli-|podman-|oc-|exec-|logs-|streaming-|consistency-)" | xargs -r podman rm -f || true
	@echo "Test cleanup completed"

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
	rm -f $(PID_FILE)

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
	@echo "Build and Run:"
	@echo "  build             - Build the server binary"
	@echo "  run               - Build and run the server"
	@echo "  run-bg            - Build and run the server in background"
	@echo "  stop              - Stop background server"
	@echo "  status            - Check if background server is running"
	@echo "  dev               - Format, build, and run (development workflow)"
	@echo ""
	@echo "Testing:"
	@echo "  test              - Test server endpoints with curl"
	@echo "  test-unit         - Run unit tests"
	@echo "  test-integration  - Run integration tests (requires podman and oc)"
	@echo "  test-all          - Run all Go tests"
	@echo "  test-coverage     - Run tests with coverage report"
	@echo "  test-cli          - Test CLI compatibility with oc commands"
	@echo "  test-resources    - Test oc<->podman resource consistency"
	@echo "  test-streaming    - Test exec and streaming protocol functionality"
	@echo "  test-integration-full - Run complete integration test suite"
	@echo "  test-oc           - Test server with oc CLI (legacy)"
	@echo "  test-prereqs      - Check test prerequisites"
	@echo "  test-clean        - Clean test artifacts and containers"
	@echo ""
	@echo "Code Quality:"
	@echo "  fmt               - Format Go code"
	@echo "  deps              - Download and tidy dependencies"
	@echo "  clean             - Remove build artifacts"
	@echo ""
	@echo "Help:"
	@echo "  help              - Show this help message"
	@echo ""
	@echo "Configuration:"
	@echo "  PORT=$(PORT)"
	@echo "  HOST=$(HOST)"
	@echo "  BINARY_NAME=$(BINARY_NAME)"
	@echo "  PID_FILE=$(PID_FILE)"
	@echo ""
	@echo "Examples:"
	@echo "  make build"
	@echo "  make run"
	@echo "  make test-prereqs && make test-all"
	@echo "  make test-integration-full"
	@echo "  make run-bg && make test-cli && make stop"
	@echo "  PORT=9443 make run"
