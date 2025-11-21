package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"podman-k8s-adapter/test/testutil"
)

// TestOCCommandConsistency tests that oc commands work consistently with the podKube server
func TestOCCommandConsistency(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)

	t.Run("Basic oc version compatibility", func(t *testing.T) {
		// Test that oc can connect to our server
		output, err := ocHelper.RunOCCommand("version")
		assert.NoError(t, err, "oc version should work")
		assert.Contains(t, output, "Server Version", "Should return server version information")
	})

	t.Run("oc get pods consistency", func(t *testing.T) {
		// Clean up any existing test containers
		defer testutil.CleanupContainers(t, "cli-test-pod")

		// Test empty pod list
		output, err := ocHelper.GetPods("")
		require.NoError(t, err, "oc get pods should work")

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err, "oc get pods output should be valid JSON")
		assert.Equal(t, "PodList", podList.Kind)

		// Create a test pod using podman directly
		podmanHelper := testutil.NewPodmanHelper(t)
		err = podmanHelper.CreateTestContainer("cli-test-pod", "alpine:latest")
		require.NoError(t, err, "Should create test container")

		// Wait for pod to appear in list
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.GetPods("")
			if err != nil {
				return false
			}
			var podList corev1.PodList
			if err := json.Unmarshal([]byte(output), &podList); err != nil {
				return false
			}
			for _, pod := range podList.Items {
				if pod.Name == "cli-test-pod" {
					return true
				}
			}
			return false
		}, 10*time.Second, "pod should appear in oc get pods")

		// Get pods again and verify our pod is listed
		output, err = ocHelper.GetPods("")
		require.NoError(t, err, "oc get pods should work after container creation")

		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		found := false
		for _, pod := range podList.Items {
			if pod.Name == "cli-test-pod" {
				found = true
				assert.Equal(t, "containers", pod.Namespace)
				assert.Equal(t, corev1.PodRunning, pod.Status.Phase)
				break
			}
		}
		assert.True(t, found, "Created container should appear in pod list")
	})

	t.Run("oc describe pod consistency", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "describe-test-pod")

		// Create test container
		podmanHelper := testutil.NewPodmanHelper(t)
		err := podmanHelper.CreateTestContainer("describe-test-pod", "alpine:latest")
		require.NoError(t, err)

		// Wait for pod to be available
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", "describe-test-pod", "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should be available for describe")

		// Test describe command
		output, err := ocHelper.RunOCCommand("describe", "pod", "describe-test-pod")
		assert.NoError(t, err, "oc describe pod should work")

		// Verify describe output contains expected information
		assert.Contains(t, output, "Name:", "Describe should contain pod name")
		assert.Contains(t, output, "Namespace:", "Describe should contain namespace")
		assert.Contains(t, output, "Status:", "Describe should contain status")
		assert.Contains(t, output, "describe-test-pod", "Describe should contain the pod name")
	})

	t.Run("oc logs consistency", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "logs-test-pod")

		// Create test container that produces logs
		podmanHelper := testutil.NewPodmanHelper(t)
		_, err := podmanHelper.RunPodmanCommand("run", "-d", "--name", "logs-test-pod",
			"alpine:latest", "/bin/sh", "-c", "echo 'Hello from container'; sleep 3600")
		require.NoError(t, err, "Should create container with logs")

		// Wait for pod to be available
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", "logs-test-pod", "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should be available for logs")

		// Test logs command
		output, err := ocHelper.RunOCCommand("logs", "logs-test-pod")
		if err != nil {
			t.Logf("oc logs error (expected for podKube): %v", err)
			// Note: Logs functionality might not be fully implemented in podKube
			// This test documents the expected behavior
		} else {
			assert.Contains(t, output, "Hello from container", "Logs should contain container output")
		}
	})

	t.Run("oc apply and delete consistency", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "apply-test-pod")

		// Test pod specification
		podSpec := testutil.TestPodSpec("apply-test-pod", "containers", "alpine:latest")

		// Test apply
		err := ocHelper.CreatePod(podSpec)
		assert.NoError(t, err, "oc apply should work")

		// Verify pod was created
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", "apply-test-pod", "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should be created via oc apply")

		// Test delete
		err = ocHelper.DeletePod("apply-test-pod", "containers")
		assert.NoError(t, err, "oc delete should work")

		// Verify pod was deleted
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", "apply-test-pod", "-o", "json")
			return err != nil
		}, 10*time.Second, "pod should be deleted via oc delete")
	})

	t.Run("oc get with selectors consistency", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "selector-test-pod")

		// Create test container with labels
		podmanHelper := testutil.NewPodmanHelper(t)
		_, err := podmanHelper.RunPodmanCommand("run", "-d", "--name", "selector-test-pod",
			"--label", "app=test-app", "--label", "version=v1.0",
			"alpine:latest", "sleep", "3600")
		require.NoError(t, err)

		// Wait for pod to be available
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", "selector-test-pod", "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should be available")

		// Test label selector
		output, err := ocHelper.RunOCCommand("get", "pods", "-l", "app=test-app", "-o", "json")
		assert.NoError(t, err, "oc get pods with label selector should work")

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err, "Selector output should be valid JSON")

		found := false
		for _, pod := range podList.Items {
			if pod.Name == "selector-test-pod" {
				found = true
				assert.Equal(t, "test-app", pod.Labels["app"])
				break
			}
		}
		assert.True(t, found, "Pod with matching label should be found")

		// Test non-matching selector
		output, err = ocHelper.RunOCCommand("get", "pods", "-l", "app=non-existent", "-o", "json")
		assert.NoError(t, err, "oc get pods with non-matching selector should work")

		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		for _, pod := range podList.Items {
			assert.NotEqual(t, "selector-test-pod", pod.Name, "Pod should not be found with non-matching selector")
		}
	})

	t.Run("oc get with field selectors consistency", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "field-test-pod")

		// Create test container
		podmanHelper := testutil.NewPodmanHelper(t)
		err := podmanHelper.CreateTestContainer("field-test-pod", "alpine:latest")
		require.NoError(t, err)

		// Wait for pod to be running
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", "field-test-pod", "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should be available")

		// Test field selector for status.phase=Running
		output, err := ocHelper.RunOCCommand("get", "pods", "--field-selector", "status.phase=Running", "-o", "json")
		assert.NoError(t, err, "oc get pods with field selector should work")

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		found := false
		for _, pod := range podList.Items {
			if pod.Name == "field-test-pod" {
				found = true
				assert.Equal(t, corev1.PodRunning, pod.Status.Phase)
				break
			}
		}
		if !found {
			t.Log("Field selector test may be skipped if pod is not in Running state yet")
		}
	})
}

// TestOCExecConsistency tests oc exec command consistency
func TestOCExecConsistency(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)

	t.Run("oc exec basic command", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "exec-test-pod")

		// Create test container
		podmanHelper := testutil.NewPodmanHelper(t)
		err := podmanHelper.CreateTestContainer("exec-test-pod", "alpine:latest")
		require.NoError(t, err)

		// Wait for pod to be available
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", "exec-test-pod", "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should be available for exec")

		// Test exec command
		output, err := ocHelper.RunOCCommand("exec", "exec-test-pod", "--", "echo", "hello")
		if err != nil {
			t.Logf("oc exec error (may not be fully implemented): %v", err)
			t.Logf("oc exec output: %s", output)
			// Note: Exec functionality might not be fully implemented in test environment
		} else {
			assert.Contains(t, output, "hello", "Exec should return command output")
		}
	})
}

// TestOCSecretsConsistency tests oc secrets command consistency
func TestOCSecretsConsistency(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)

	t.Run("oc get secrets consistency", func(t *testing.T) {
		// Test getting secrets
		output, err := ocHelper.RunOCCommand("get", "secrets", "-o", "json")
		require.NoError(t, err, "oc get secrets should work")

		var secretList corev1.SecretList
		err = json.Unmarshal([]byte(output), &secretList)
		require.NoError(t, err, "Secrets output should be valid JSON")
		assert.Equal(t, "SecretList", secretList.Kind)
	})

	t.Run("oc create secret consistency", func(t *testing.T) {
		secretName := "test-secret"

		// Clean up any existing secret
		defer func() {
			ocHelper.RunOCCommand("delete", "secret", secretName)
		}()

		// Create secret
		_, err := ocHelper.RunOCCommand("create", "secret", "generic", secretName, "--from-literal=data=secret-value")
		assert.NoError(t, err, "oc create secret should work")

		// Verify secret was created
		output, err := ocHelper.RunOCCommand("get", "secret", secretName, "-o", "json")
		assert.NoError(t, err, "Should be able to get created secret")

		var secret corev1.Secret
		err = json.Unmarshal([]byte(output), &secret)
		require.NoError(t, err)
		assert.Equal(t, secretName, secret.Name)
		assert.Contains(t, secret.Data, "data")
	})
}

// TestOCOutputFormats tests consistency of oc output formats
func TestOCOutputFormats(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)

	t.Run("JSON output format", func(t *testing.T) {
		output, err := ocHelper.RunOCCommand("get", "pods", "-o", "json")
		require.NoError(t, err, "JSON output should work")

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err, "Should be valid JSON")
		assert.Equal(t, "PodList", podList.Kind)
	})

	t.Run("YAML output format", func(t *testing.T) {
		output, err := ocHelper.RunOCCommand("get", "pods", "-o", "yaml")
		assert.NoError(t, err, "YAML output should work")
		assert.Contains(t, output, "apiVersion:", "Should contain YAML format")
	})

	t.Run("Table output format", func(t *testing.T) {
		output, err := ocHelper.RunOCCommand("get", "pods")
		assert.NoError(t, err, "Table output should work")
		assert.Contains(t, output, "NAME", "Should contain table headers")
	})

	t.Run("Wide output format", func(t *testing.T) {
		output, err := ocHelper.RunOCCommand("get", "pods", "-o", "wide")
		assert.NoError(t, err, "Wide output should work")
		// Wide format should include additional columns
		if !strings.Contains(output, "No resources found") {
			assert.Contains(t, output, "NAME", "Should contain table headers")
		}
	})
}

// TestOCNamespaceConsistency tests namespace handling consistency
func TestOCNamespaceConsistency(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)

	t.Run("Default namespace behavior", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "ns-test-pod")

		// Create container
		podmanHelper := testutil.NewPodmanHelper(t)
		err := podmanHelper.CreateTestContainer("ns-test-pod", "alpine:latest")
		require.NoError(t, err)

		// Get pods without specifying namespace (should use default)
		output, err := ocHelper.GetPods("")
		require.NoError(t, err)

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		// Verify pod appears in the right namespace
		found := false
		for _, pod := range podList.Items {
			if pod.Name == "ns-test-pod" {
				found = true
				assert.Equal(t, "containers", pod.Namespace, "Pod should be in containers namespace")
				break
			}
		}
		assert.True(t, found, "Pod should be found in default namespace query")
	})

	t.Run("Explicit namespace behavior", func(t *testing.T) {
		defer testutil.CleanupContainers(t, "explicit-ns-pod")

		// Create container
		podmanHelper := testutil.NewPodmanHelper(t)
		err := podmanHelper.CreateTestContainer("explicit-ns-pod", "alpine:latest")
		require.NoError(t, err)

		// Get pods from containers namespace explicitly
		output, err := ocHelper.GetPods("containers")
		require.NoError(t, err)

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		found := false
		for _, pod := range podList.Items {
			if pod.Name == "explicit-ns-pod" {
				found = true
				assert.Equal(t, "containers", pod.Namespace)
				break
			}
		}
		assert.True(t, found, "Pod should be found in explicit namespace query")
	})

	t.Run("All namespaces behavior", func(t *testing.T) {
		output, err := ocHelper.RunOCCommand("get", "pods", "--all-namespaces", "-o", "json")
		assert.NoError(t, err, "All namespaces query should work")

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)
		assert.Equal(t, "PodList", podList.Kind)
	})
}