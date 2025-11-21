# PodKube Test Suite

This directory contains comprehensive tests for the PodKube project to ensure consistency between `oc` commands and `podman` resources.

## Test Structure

```
test/
├── unit/               # Unit tests for individual components
├── integration/        # Integration tests requiring server and external tools
├── e2e/               # End-to-end tests (placeholder for future use)
├── testutil/          # Test utilities and helpers
└── README.md          # This documentation
```

## Test Categories

### 1. Unit Tests (`test/unit/`)

**Purpose**: Test individual components in isolation without external dependencies.

**Files**:
- `storage_test.go` - Tests storage layer functionality and podman<->k8s resource translation

**Run**:
```bash
make test-unit
```

**Features Tested**:
- Pod storage CRUD operations
- Container to Pod conversion logic
- Label and field selector matching
- Annotation merging
- Resource state mapping consistency

### 2. Integration Tests (`test/integration/`)

**Purpose**: Test interactions between components and external tools (podman, oc).

**Files**:
- `cli_compatibility_test.go` - Tests `oc` command consistency and compatibility
- `resource_consistency_test.go` - Tests consistency between `oc` and `podman` resources
- `streaming_test.go` - Tests exec and streaming protocol functionality

**Run**:
```bash
make test-integration
# or individually:
make test-cli
make test-resources
make test-streaming
```

**Features Tested**:
- CLI command compatibility across different operations
- Bidirectional resource consistency (oc ↔ podman)
- Streaming protocols (WebSocket, SPDY)
- Exec functionality
- Log streaming
- Port forwarding
- Error handling

### 3. Test Utilities (`test/testutil/`)

**Purpose**: Shared utilities for testing across all test categories.

**Files**:
- `helpers.go` - Common test helpers, server utilities, podman/oc helpers

**Key Utilities**:
- `TestServer` - HTTP test server wrapper
- `PodmanHelper` - Podman command utilities
- `OCHelper` - OpenShift CLI utilities
- `WaitForCondition` - Condition waiting utility
- Container cleanup functions

## Prerequisites

### Required
- **Go 1.24+**: For running tests
- **Podman**: For container operations
- **Make**: For running test targets

### Optional
- **OpenShift CLI (`oc`)**: For CLI compatibility tests
- **Git**: For CI/CD workflows

### Check Prerequisites
```bash
make test-prereqs
```

## Running Tests

### Quick Development Tests
```bash
# Fast test suite (unit tests + build check)
make test-unit

# Check prerequisites
make test-prereqs
```

### Full Test Suite
```bash
# All tests (requires server lifecycle management)
make test-integration-full

# Individual test categories
make test-cli              # CLI compatibility
make test-resources        # Resource consistency
make test-streaming        # Streaming protocols
```

### Test with Coverage
```bash
make test-coverage
# Generates coverage.html report
```

### Clean Up
```bash
make test-clean            # Remove test artifacts and containers
```

## Test Design Principles

### 1. Consistency Verification
Tests ensure that operations performed through different interfaces (`oc` vs direct `podman`) result in consistent resource states.

### 2. Real Environment Testing
Integration tests use actual `podman` and `oc` commands to verify real-world compatibility.

### 3. Isolated Test Containers
Each test uses uniquely named containers to avoid conflicts and enable parallel execution.

### 4. Graceful Degradation
Tests handle missing dependencies gracefully and provide informative skip messages.

### 5. Comprehensive Protocol Testing
Streaming tests verify protocol compatibility including WebSocket and SPDY upgrades.

## CI/CD Integration

### GitHub Actions Workflows

**Main CI** (`.github/workflows/ci.yml`):
- Comprehensive test suite with parallel job execution
- Separate jobs for different test categories
- Full dependency installation and setup
- Coverage reporting

**Fast CI** (`.github/workflows/fast-ci.yml`):
- Quick feedback for pull requests
- Unit tests and basic build verification
- Minimal external dependencies

### Local Development Workflow

```bash
# 1. Check prerequisites
make test-prereqs

# 2. Run fast tests during development
make test-unit

# 3. Run full integration tests before committing
make test-integration-full

# 4. Clean up test artifacts
make test-clean
```

## Test Implementation Guidelines

### Unit Tests
- Test individual functions and methods
- Mock external dependencies
- Focus on business logic correctness
- Fast execution (< 1 second per test)

### Integration Tests
- Test component interactions
- Use real external tools (podman, oc)
- Verify end-to-end workflows
- Include server lifecycle management
- Test error conditions and edge cases

### Test Naming Convention
```go
func TestFeatureName(t *testing.T) {
    t.Run("specific scenario", func(t *testing.T) {
        // Test implementation
    })
}
```

### Container Naming
Use descriptive prefixes for test containers:
- `test-*`: General test containers
- `cli-*`: CLI compatibility test containers
- `consistency-*`: Resource consistency test containers
- `streaming-*`: Streaming protocol test containers

### Error Handling
```go
// For expected failures in test environment
if err != nil {
    t.Logf("Expected error in test environment: %v", err)
    t.Skip("Feature may not be fully implemented")
    return
}
```

## Troubleshooting

### Common Issues

1. **Podman not available**
   ```bash
   sudo apt-get install podman
   # or on RHEL/Fedora:
   sudo dnf install podman
   ```

2. **OpenShift CLI not available**
   ```bash
   curl -LO "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz"
   tar -xzf openshift-client-linux.tar.gz
   sudo mv oc /usr/local/bin/
   ```

3. **Tests timing out**
   - Increase timeout values in test files
   - Check if containers are starting properly
   - Verify podman service is running

4. **Port conflicts**
   - Change the PORT environment variable
   - Clean up background server processes

5. **Container cleanup issues**
   ```bash
   make test-clean
   # or manually:
   podman ps -a | grep test- | awk '{print $1}' | xargs podman rm -f
   ```

### Debug Mode

Set verbose mode for detailed test output:
```bash
go test -v ./test/... -timeout=30m
```

## Contributing

When adding new tests:

1. **Choose the appropriate category** (unit vs integration)
2. **Follow naming conventions** for tests and containers
3. **Include cleanup** in defer statements
4. **Add documentation** for complex test scenarios
5. **Update this README** if adding new test categories

### Example Test Structure
```go
func TestNewFeature(t *testing.T) {
    testutil.RequirePodman(t) // Check prerequisites

    t.Run("specific scenario", func(t *testing.T) {
        testName := "feature-test"
        defer testutil.CleanupContainers(t, testName) // Cleanup

        // Test implementation
        // Use testutil helpers for common operations
        // Assert expected behavior
    })
}
```