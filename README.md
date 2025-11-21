# PodKube

Development was assisted by Claude AI engine.

> A Kubernetes-compatible API adapter for Podman containers

PodKube bridges the gap between Podman container management and Kubernetes-style resource management by providing a Kubernetes-compatible API server that translates Kubernetes operations to Podman commands.

## Features

- **Kubernetes-Compatible API**: Provides standard Kubernetes API endpoints for pod management
- **OpenShift CLI Support**: Full compatibility with `oc` commands for seamless workflow integration
- **HTTPS Server**: Secure communication with TLS certificate support (custom or auto-generated)
- **Self-Signed Certificates**: Automatic certificate generation for development and testing
- **Comprehensive Testing**: Unit, integration, and end-to-end test suites ensuring reliability
- **Resource Consistency**: Maintains state consistency between `oc` operations and `podman` resources

## Quick Start

### Prerequisites

- **Go 1.24.0+**: Required for building from source
- **Podman**: Container runtime engine
- **OpenShift CLI (oc)**: Optional, for CLI compatibility features

Check all prerequisites:
```bash
make test-prereqs
```

### Installation

1. **Clone the repository:**
   ```bash
   git clone <repository-url>
   cd podKube
   ```

2. **Install dependencies:**
   ```bash
   make deps
   ```

3. **Build the server:**
   ```bash
   make build
   ```

### Usage

#### Basic Usage

**Start the server:**
```bash
make run
```
This starts the server on `https://127.0.0.1:8443` with a self-signed certificate.

**Start in background:**
```bash
make run-bg
```

**Check server status:**
```bash
make status
```

**Stop background server:**
```bash
make stop
```

#### Custom Configuration

**Specify port and host:**
```bash
PORT=9443 HOST=0.0.0.0 make run
```

**Use custom TLS certificates:**
```bash
./server --port 8443 --host 0.0.0.0 --cert-file /path/to/cert.pem --key-file /path/to/key.pem
```

#### Using with OpenShift CLI

Once the server is running, you can use standard `oc` commands:

```bash
# List all pods
oc get pods --all-namespaces --server=https://127.0.0.1:8443 --insecure-skip-tls-verify

# Get pod details
oc get pod <pod-name> -o yaml --server=https://127.0.0.1:8443 --insecure-skip-tls-verify

# Create resources
oc apply -f pod.yaml --server=https://127.0.0.1:8443 --insecure-skip-tls-verify
```

## API Endpoints

The server provides standard Kubernetes API endpoints:

- **Health Check**: `GET /healthz`
- **API Discovery**: `GET /api`
- **Pod Operations**:
  - List: `GET /api/v1/pods`
  - Get: `GET /api/v1/pods/{name}`
  - Create: `POST /api/v1/pods`
  - Update: `PUT /api/v1/pods/{name}`
  - Delete: `DELETE /api/v1/pods/{name}`

## Development

### Build Commands

```bash
# Build the server
make build

# Format code
make fmt

# Development workflow (format + build + run)
make dev

# Clean build artifacts
make clean
```

### Testing

#### Quick Tests
```bash
# Run unit tests
make test-unit

# Test server endpoints
make test
```

#### Comprehensive Testing
```bash
# Run all tests
make test-all

# Full integration test suite
make test-integration-full

# Test with coverage report
make test-coverage
```

#### Individual Test Categories
```bash
# CLI compatibility tests
make test-cli

# Resource consistency tests
make test-resources

# Streaming functionality tests
make test-streaming
```

#### Test Cleanup
```bash
# Clean test artifacts and containers
make test-clean
```

For detailed testing information, see [test/README.md](test/README.md).

## Architecture

```
podKube/
├── cmd/
│   └── server/          # Main application entry point
│       └── main.go      # Server configuration and startup
├── pkg/
│   ├── server/          # API server implementation
│   └── storage/         # Resource management and Podman integration
├── test/                # Comprehensive test suite
│   ├── unit/            # Unit tests
│   ├── integration/     # Integration tests
│   ├── testutil/        # Test utilities
│   └── README.md        # Testing documentation
├── Makefile            # Build and test automation
└── README.md           # This file
```

## Configuration

### Environment Variables

- `PORT`: Server port (default: 8443)
- `HOST`: Server host (default: 127.0.0.1)

### Command Line Options

- `--port`: Port to serve on
- `--host`: Host to serve on
- `--cert-file`: Path to TLS certificate file
- `--key-file`: Path to TLS private key file

## Dependencies

### Core Dependencies

- **Kubernetes API**: `k8s.io/api`, `k8s.io/apimachinery` - Kubernetes resource types and utilities
- **Logging**: `k8s.io/klog/v2` - Structured logging
- **PTY Support**: `github.com/creack/pty` - Pseudo-terminal operations
- **YAML Processing**: `sigs.k8s.io/yaml` - YAML marshaling/unmarshaling

### Development Dependencies

- **Testing**: `github.com/stretchr/testify` - Testing utilities
- **Build Tools**: Standard Go toolchain

## Contributing

1. **Prerequisites**: Ensure all development tools are installed
   ```bash
   make test-prereqs
   ```

2. **Development Workflow**:
   ```bash
   # Make changes
   make fmt                    # Format code
   make test-unit             # Run fast tests
   make test-integration-full # Full test suite
   ```

3. **Test Guidelines**: See [test/README.md](test/README.md) for detailed testing practices

4. **Code Style**: Use `make fmt` to ensure consistent formatting

## Limitations and Known Issues

- **Experimental State**: This is an experimental adapter with ongoing development
- **API Coverage**: May not support all Kubernetes API features
- **Resource Mapping**: Some Kubernetes concepts may not have direct Podman equivalents
- **Streaming Protocols**: WebSocket and SPDY support is under active development

## Troubleshooting

### Common Issues

**Server won't start:**
- Check if port is already in use
- Verify Podman is installed and running
- Check file permissions for certificate files

**CLI commands fail:**
- Verify server is running: `make status`
- Use `--insecure-skip-tls-verify` for self-signed certificates
- Check server logs for detailed error messages

**Tests failing:**
- Run prerequisite check: `make test-prereqs`
- Clean test artifacts: `make test-clean`
- Increase timeout values if tests are slow

### Debug Mode

Enable verbose logging:
```bash
# For server
./server --port 8443 --host 0.0.0.0 -v 4

# For tests
go test -v ./test/... -timeout=30m
```

## License

[Add your license information here]

## Support

For issues and feature requests, please use the project's issue tracker.

## Acknowledgments

This project builds upon the excellent work of:
- [Podman](https://podman.io/) - Container management tool
- [Kubernetes](https://kubernetes.io/) - Container orchestration platform
- [OpenShift](https://www.redhat.com/en/technologies/cloud-computing/openshift) - Enterprise Kubernetes platform
