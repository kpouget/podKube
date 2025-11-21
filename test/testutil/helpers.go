package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServer wraps httptest.Server with additional utilities for testing
type TestServer struct {
	*httptest.Server
	T *testing.T
}

// NewTestServer creates a new test server
func NewTestServer(t *testing.T, handler http.Handler) *TestServer {
	server := httptest.NewTLSServer(handler)
	return &TestServer{
		Server: server,
		T:      t,
	}
}

// NewTestServerFromPodKubeServer creates a test server from a podkube server
// Since the podkube server doesn't implement http.Handler directly,
// we need to create our own mux with the same routes
func NewTestServerFromPodKubeServer(t *testing.T) *TestServer {
	// Create a simple handler that mimics the basic endpoints
	mux := http.NewServeMux()

	// Add basic health endpoint
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Add basic API endpoints that return minimal responses
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"kind": "APIVersions",
			"versions": []string{"v1"},
		}
		json.NewEncoder(w).Encode(response)
	})

	mux.HandleFunc("/api/v1/pods", func(w http.ResponseWriter, r *http.Request) {
		// Return empty pod list for now
		podList := map[string]interface{}{
			"kind": "PodList",
			"apiVersion": "v1",
			"items": []interface{}{},
		}
		json.NewEncoder(w).Encode(podList)
	})

	server := httptest.NewTLSServer(mux)
	return &TestServer{
		Server: server,
		T:      t,
	}
}

// GetURL returns the server URL with the given path
func (ts *TestServer) GetURL(path string) string {
	return ts.URL + path
}

// MakeRequest makes an HTTP request to the test server
func (ts *TestServer) MakeRequest(method, path string, body io.Reader, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequest(method, ts.GetURL(path), body)
	if err != nil {
		return nil, err
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := ts.Client()
	return client.Do(req)
}

// AssertJSONResponse asserts that the response contains valid JSON
func (ts *TestServer) AssertJSONResponse(resp *http.Response, expectedStatus int, target interface{}) {
	assert.Equal(ts.T, expectedStatus, resp.StatusCode)
	assert.Equal(ts.T, "application/json", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(ts.T, err)

	err = json.Unmarshal(body, target)
	require.NoError(ts.T, err, "Response body should be valid JSON: %s", string(body))
}

// PodmanHelper provides utilities for testing podman integration
type PodmanHelper struct {
	T *testing.T
}

// NewPodmanHelper creates a new podman test helper
func NewPodmanHelper(t *testing.T) *PodmanHelper {
	return &PodmanHelper{T: t}
}

// RunPodmanCommand executes a podman command and returns the output
func (ph *PodmanHelper) RunPodmanCommand(args ...string) (string, error) {
	cmd := exec.Command("podman", args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// CreateTestContainer creates a test container with podman
func (ph *PodmanHelper) CreateTestContainer(name, image string) error {
	_, err := ph.RunPodmanCommand("run", "-d", "--name", name, image, "sleep", "3600")
	return err
}

// RemoveTestContainer removes a test container
func (ph *PodmanHelper) RemoveTestContainer(name string) error {
	_, err := ph.RunPodmanCommand("rm", "-f", name)
	return err
}

// ListContainers lists all containers
func (ph *PodmanHelper) ListContainers() ([]Container, error) {
	output, err := ph.RunPodmanCommand("ps", "-a", "--format", "json")
	if err != nil {
		return nil, err
	}

	var containers []Container
	err = json.Unmarshal([]byte(output), &containers)
	return containers, err
}

// Container represents a podman container
type Container struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Status string            `json:"Status"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

// OCHelper provides utilities for testing oc/kubectl commands
type OCHelper struct {
	T        *testing.T
	Server   string
	Token    string
	Insecure bool
}

// NewOCHelper creates a new oc command helper
func NewOCHelper(t *testing.T, server string) *OCHelper {
	return &OCHelper{
		T:        t,
		Server:   server,
		Insecure: true, // For testing with self-signed certs
	}
}

// RunOCCommand executes an oc command
func (oh *OCHelper) RunOCCommand(args ...string) (string, error) {
	cmdArgs := []string{"--server", oh.Server}
	if oh.Insecure {
		cmdArgs = append(cmdArgs, "--insecure-skip-tls-verify")
	}
	if oh.Token != "" {
		cmdArgs = append(cmdArgs, "--token", oh.Token)
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("oc", cmdArgs...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// CreatePod creates a pod using oc
func (oh *OCHelper) CreatePod(podSpec string) error {
	cmd := exec.Command("bash", "-c", fmt.Sprintf("echo '%s' | oc --server %s --insecure-skip-tls-verify apply -f -", podSpec, oh.Server))
	_, err := cmd.CombinedOutput()
	return err
}

// DeletePod deletes a pod using oc
func (oh *OCHelper) DeletePod(name, namespace string) error {
	args := []string{"delete", "pod", name}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := oh.RunOCCommand(args...)
	return err
}

// GetPods gets pods using oc
func (oh *OCHelper) GetPods(namespace string) (string, error) {
	args := []string{"get", "pods", "-o", "json"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return oh.RunOCCommand(args...)
}

// WaitForCondition waits for a condition to be true
func WaitForCondition(t *testing.T, condition func() bool, timeout time.Duration, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Timeout waiting for condition: %s", message)
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}

// RequirePodman checks if podman is available
func RequirePodman(t *testing.T) {
	cmd := exec.Command("podman", "version")
	if err := cmd.Run(); err != nil {
		t.Skip("Podman is not available, skipping test")
	}
}

// RequireOC checks if oc is available
func RequireOC(t *testing.T) {
	cmd := exec.Command("oc", "version", "--client")
	if err := cmd.Run(); err != nil {
		t.Skip("oc is not available, skipping test")
	}
}

// CleanupContainers removes all test containers with a specific prefix
func CleanupContainers(t *testing.T, prefix string) {
	cmd := exec.Command("podman", "ps", "-a", "--format", "{{.Names}}")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	names := strings.Split(string(output), "\n")
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			exec.Command("podman", "rm", "-f", name).Run()
		}
	}
}

// TestPodSpec returns a basic test pod specification
func TestPodSpec(name, namespace, image string) string {
	return fmt.Sprintf(`{
  "apiVersion": "v1",
  "kind": "Pod",
  "metadata": {
    "name": "%s",
    "namespace": "%s",
    "labels": {
      "app": "test",
      "test": "podkube"
    }
  },
  "spec": {
    "containers": [
      {
        "name": "test-container",
        "image": "%s",
        "command": ["sleep", "3600"],
        "resources": {
          "limits": {
            "memory": "128Mi",
            "cpu": "100m"
          }
        }
      }
    ]
  }
}`, name, namespace, image)
}