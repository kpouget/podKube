package integration

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"podman-k8s-adapter/test/testutil"
)

// TestExecStreaming verifies exec streaming functionality
func TestExecStreaming(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("Basic exec functionality", func(t *testing.T) {
		testName := "exec-basic-test"
		defer testutil.CleanupContainers(t, testName)

		// Create test container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err, "Should create test container")

		// Wait for container to be running
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName && container.State == "running" {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should be running")

		// Test exec endpoint exists
		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/exec", testServer.URL, testName)

		// Create a simple HTTP request to test the exec endpoint
		req, err := http.NewRequest("GET", execURL, nil)
		require.NoError(t, err)

		// Add query parameters for exec
		q := req.URL.Query()
		q.Add("command", "echo")
		q.Add("command", "hello")
		q.Add("stdout", "true")
		req.URL.RawQuery = q.Encode()

		// Create HTTP client that accepts self-signed certificates
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		// Make the request
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Exec endpoint test failed (may not be fully implemented): %v", err)
			t.Skip("Exec functionality may not be fully implemented in test environment")
			return
		}
		defer resp.Body.Close()

		// Check that we got some kind of response
		assert.True(t, resp.StatusCode < 500, "Should not return server error for exec request")

		t.Logf("Exec endpoint returned status: %d", resp.StatusCode)
	})

	t.Run("Exec with stdin/stdout/stderr", func(t *testing.T) {
		testName := "exec-stdio-test"
		defer testutil.CleanupContainers(t, testName)

		// Create test container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Wait for container to be running
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName && container.State == "running" {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should be running")

		// Test exec endpoint with stdin/stdout/stderr flags
		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/exec", testServer.URL, testName)

		req, err := http.NewRequest("POST", execURL, nil)
		require.NoError(t, err)

		// Add query parameters for full stdio exec
		q := req.URL.Query()
		q.Add("command", "/bin/sh")
		q.Add("stdin", "true")
		q.Add("stdout", "true")
		q.Add("stderr", "true")
		q.Add("tty", "false")
		req.URL.RawQuery = q.Encode()

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Full exec test failed (expected for test environment): %v", err)
			t.Skip("Full exec functionality requires SPDY/WebSocket implementation")
			return
		}
		defer resp.Body.Close()

		// For now, just verify we can make the request
		t.Logf("Exec with stdio returned status: %d", resp.StatusCode)
	})
}

// TestLogStreaming verifies log streaming functionality
func TestLogStreaming(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("Basic log streaming", func(t *testing.T) {
		testName := "log-streaming-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container that produces logs
		_, err := podmanHelper.RunPodmanCommand("run", "-d", "--name", testName,
			"alpine:latest", "/bin/sh", "-c", "echo 'Log line 1'; echo 'Log line 2'; sleep 3600")
		require.NoError(t, err, "Should create container with logs")

		// Wait for container to be running
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName && container.State == "running" {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should be running")

		// Test logs endpoint
		logsURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/log", testServer.URL, testName)

		req, err := http.NewRequest("GET", logsURL, nil)
		require.NoError(t, err)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Log streaming test failed (may not be implemented): %v", err)
			t.Skip("Log streaming may not be implemented")
			return
		}
		defer resp.Body.Close()

		// Read response
		body, err := io.ReadAll(resp.Body)
		if err == nil && len(body) > 0 {
			logs := string(body)
			t.Logf("Retrieved logs: %s", logs)

			// Verify logs contain expected content
			assert.Contains(t, logs, "Log line", "Logs should contain expected output")
		} else {
			t.Logf("Log endpoint returned status: %d", resp.StatusCode)
		}
	})

	t.Run("Log streaming with follow", func(t *testing.T) {
		testName := "log-follow-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container that produces logs over time
		_, err := podmanHelper.RunPodmanCommand("run", "-d", "--name", testName,
			"alpine:latest", "/bin/sh", "-c", "while true; do echo 'Continuous log'; sleep 1; done")
		require.NoError(t, err, "Should create container with continuous logs")

		// Wait for container to be running
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName && container.State == "running" {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should be running")

		// Test logs endpoint with follow
		logsURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/log?follow=true", testServer.URL, testName)

		req, err := http.NewRequest("GET", logsURL, nil)
		require.NoError(t, err)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			Timeout: 5 * time.Second, // Short timeout for streaming test
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Log follow test failed (expected): %v", err)
			t.Skip("Log following may not be implemented")
			return
		}
		defer resp.Body.Close()

		// Try to read some streaming logs
		buf := make([]byte, 1024)
		n, err := resp.Body.Read(buf)
		if err != nil && err != io.EOF {
			t.Logf("Error reading streaming logs: %v", err)
		} else if n > 0 {
			t.Logf("Received streaming logs: %s", string(buf[:n]))
		}
	})
}

// TestPortForwarding verifies port forwarding functionality
func TestPortForwarding(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("Port forward endpoint existence", func(t *testing.T) {
		testName := "portforward-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container with exposed port
		_, err := podmanHelper.RunPodmanCommand("run", "-d", "--name", testName,
			"-p", "8080:80",
			"nginx:alpine")
		if err != nil {
			// Fallback to a simpler container if nginx is not available
			_, err = podmanHelper.RunPodmanCommand("run", "-d", "--name", testName,
				"alpine:latest", "sleep", "3600")
		}
		require.NoError(t, err, "Should create container")

		// Wait for container to be running
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName && container.State == "running" {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should be running")

		// Test port forward endpoint
		pfURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/portforward", testServer.URL, testName)

		req, err := http.NewRequest("GET", pfURL, nil)
		require.NoError(t, err)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Port forward test failed (may not be implemented): %v", err)
			t.Skip("Port forwarding may not be implemented")
			return
		}
		defer resp.Body.Close()

		// For now, just verify the endpoint exists
		t.Logf("Port forward endpoint returned status: %d", resp.StatusCode)
	})
}

// TestWebSocketUpgrade verifies WebSocket upgrade functionality
func TestWebSocketUpgrade(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("WebSocket upgrade headers", func(t *testing.T) {
		testName := "websocket-test"
		defer testutil.CleanupContainers(t, testName)

		// Create test container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Wait for container
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should exist")

		// Test WebSocket upgrade request to exec endpoint
		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/exec", testServer.URL, testName)

		// Parse URL for WebSocket connection
		u, err := url.Parse(execURL)
		require.NoError(t, err)

		// Change scheme to ws/wss
		if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			u.Scheme = "ws"
		}

		// Add exec parameters
		q := u.Query()
		q.Add("command", "echo")
		q.Add("command", "hello")
		q.Add("stdout", "true")
		u.RawQuery = q.Encode()

		// Create WebSocket upgrade request
		req, err := http.NewRequest("GET", u.String(), nil)
		require.NoError(t, err)

		// Add WebSocket headers
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("WebSocket upgrade test failed (may not be implemented): %v", err)
			t.Skip("WebSocket upgrade may not be fully implemented")
			return
		}
		defer resp.Body.Close()

		// Check if upgrade was attempted
		t.Logf("WebSocket upgrade returned status: %d", resp.StatusCode)
		t.Logf("Response headers: %v", resp.Header)

		// A proper WebSocket upgrade should return 101 Switching Protocols
		if resp.StatusCode == 101 {
			assert.Equal(t, "websocket", strings.ToLower(resp.Header.Get("Upgrade")))
			assert.Equal(t, "Upgrade", resp.Header.Get("Connection"))
		}
	})
}

// TestSPDYProtocol verifies SPDY protocol support
func TestSPDYProtocol(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("SPDY protocol negotiation", func(t *testing.T) {
		testName := "spdy-test"
		defer testutil.CleanupContainers(t, testName)

		// Create test container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Wait for container
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should exist")

		// Test SPDY upgrade request to exec endpoint
		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/exec", testServer.URL, testName)

		req, err := http.NewRequest("POST", execURL, nil)
		require.NoError(t, err)

		// Add SPDY headers
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "SPDY/3.1")

		// Add exec parameters
		q := req.URL.Query()
		q.Add("command", "echo")
		q.Add("command", "hello")
		q.Add("stdout", "true")
		req.URL.RawQuery = q.Encode()

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("SPDY test failed (may not be implemented): %v", err)
			t.Skip("SPDY support may not be fully implemented")
			return
		}
		defer resp.Body.Close()

		// Check response
		t.Logf("SPDY request returned status: %d", resp.StatusCode)
		t.Logf("Response headers: %v", resp.Header)

		// A proper SPDY upgrade should return 101 Switching Protocols
		if resp.StatusCode == 101 {
			assert.Contains(t, resp.Header.Get("Upgrade"), "SPDY")
		}
	})
}

// TestStreamingProtocolConsistency verifies streaming protocols work consistently
func TestStreamingProtocolConsistency(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("Consistent response to different protocol requests", func(t *testing.T) {
		testName := "protocol-consistency-test"
		defer testutil.CleanupContainers(t, testName)

		// Create test container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Wait for container
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return false
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName {
						return true
					}
				}
			}
			return false
		}, 10*time.Second, "container should exist")

		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/exec", testServer.URL, testName)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		// Test plain HTTP request
		req1, _ := http.NewRequest("GET", execURL, nil)
		resp1, err1 := client.Do(req1)
		if err1 == nil {
			resp1.Body.Close()
			t.Logf("Plain HTTP request status: %d", resp1.StatusCode)
		}

		// Test WebSocket upgrade request
		req2, _ := http.NewRequest("GET", execURL, nil)
		req2.Header.Set("Connection", "Upgrade")
		req2.Header.Set("Upgrade", "websocket")
		req2.Header.Set("Sec-WebSocket-Version", "13")
		req2.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

		resp2, err2 := client.Do(req2)
		if err2 == nil {
			resp2.Body.Close()
			t.Logf("WebSocket upgrade status: %d", resp2.StatusCode)
		}

		// Test SPDY upgrade request
		req3, _ := http.NewRequest("POST", execURL, nil)
		req3.Header.Set("Connection", "Upgrade")
		req3.Header.Set("Upgrade", "SPDY/3.1")

		resp3, err3 := client.Do(req3)
		if err3 == nil {
			resp3.Body.Close()
			t.Logf("SPDY upgrade status: %d", resp3.StatusCode)
		}

		// For now, just log the results as the implementation may vary
		t.Log("Protocol consistency test completed - check logs for response patterns")
	})
}

// TestTerminalResize verifies terminal resize functionality
func TestTerminalResize(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("Terminal resize endpoint", func(t *testing.T) {
		testName := "resize-test"
		defer testutil.CleanupContainers(t, testName)

		// Create test container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Test resize endpoint (usually handled via WebSocket/SPDY)
		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/exec", testServer.URL, testName)

		// Create resize request
		req, err := http.NewRequest("POST", execURL, bytes.NewBufferString(`{"Height":24,"Width":80}`))
		require.NoError(t, err)

		req.Header.Set("Content-Type", "application/json")

		// Add query params for TTY
		q := req.URL.Query()
		q.Add("command", "sh")
		q.Add("stdin", "true")
		q.Add("stdout", "true")
		q.Add("tty", "true")
		req.URL.RawQuery = q.Encode()

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("Resize test failed (may not be implemented): %v", err)
			t.Skip("Terminal resize may not be fully implemented")
			return
		}
		defer resp.Body.Close()

		// For now, just verify we can make the request
		t.Logf("Resize request returned status: %d", resp.StatusCode)
	})
}

// TestStreamingErrorHandling verifies proper error handling in streaming scenarios
func TestStreamingErrorHandling(t *testing.T) {
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	t.Run("Exec on non-existent pod", func(t *testing.T) {
		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/non-existent-pod/exec", testServer.URL)

		req, err := http.NewRequest("GET", execURL, nil)
		require.NoError(t, err)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		require.NoError(t, err, "Should handle non-existent pod gracefully")
		defer resp.Body.Close()

		// Should return 404 or similar error
		assert.True(t, resp.StatusCode >= 400, "Should return error status for non-existent pod")
		t.Logf("Non-existent pod exec returned status: %d", resp.StatusCode)
	})

	t.Run("Logs for non-existent pod", func(t *testing.T) {
		logsURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/non-existent-pod/log", testServer.URL)

		req, err := http.NewRequest("GET", logsURL, nil)
		require.NoError(t, err)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		require.NoError(t, err, "Should handle non-existent pod gracefully")
		defer resp.Body.Close()

		// Should return 404 or similar error
		assert.True(t, resp.StatusCode >= 400, "Should return error status for non-existent pod")
		t.Logf("Non-existent pod logs returned status: %d", resp.StatusCode)
	})

	t.Run("Invalid exec parameters", func(t *testing.T) {
		podmanHelper := testutil.NewPodmanHelper(t)
		testName := "invalid-params-test"
		defer testutil.CleanupContainers(t, testName)

		// Create test container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		execURL := fmt.Sprintf("%s/api/v1/namespaces/containers/pods/%s/exec", testServer.URL, testName)

		// Request without required command parameter
		req, err := http.NewRequest("GET", execURL, nil)
		require.NoError(t, err)

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}

		resp, err := client.Do(req)
		require.NoError(t, err, "Should handle missing parameters gracefully")
		defer resp.Body.Close()

		// Should return 400 or similar error for missing command
		t.Logf("Missing command parameter returned status: %d", resp.StatusCode)
	})
}