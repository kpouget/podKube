package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"podman-k8s-adapter/test/testutil"
)

// TestOCPodmanResourceConsistency verifies that operations through oc and podman remain consistent
func TestOCPodmanResourceConsistency(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)
	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("oc create -> podman verify", func(t *testing.T) {
		testName := "oc-create-test"
		defer testutil.CleanupContainers(t, testName)

		// Create pod using oc
		podSpec := testutil.TestPodSpec(testName, "containers", "alpine:latest")
		err := ocHelper.CreatePod(podSpec)
		require.NoError(t, err, "oc create should succeed")

		// Wait for container to appear in podman
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
		}, 10*time.Second, "container should appear in podman")

		// Verify container exists in podman
		containers, err := podmanHelper.ListContainers()
		require.NoError(t, err, "Should list podman containers")

		found := false
		var targetContainer testutil.Container
		for _, container := range containers {
			for _, name := range container.Names {
				if name == testName {
					found = true
					targetContainer = container
					break
				}
			}
		}
		require.True(t, found, "Container should exist in podman after oc create")

		// Verify container properties
		assert.Equal(t, "alpine:latest", targetContainer.Image, "Container should have correct image")
		assert.Equal(t, "running", targetContainer.State, "Container should be running")

		// Verify labels are consistent
		if targetContainer.Labels != nil {
			assert.Equal(t, "test", targetContainer.Labels["app"], "Container should have correct label")
		}
	})

	t.Run("podman create -> oc verify", func(t *testing.T) {
		testName := "podman-create-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container using podman
		err := podmanHelper.CreateTestContainer(testName, "busybox:latest")
		require.NoError(t, err, "podman create should succeed")

		// Wait for pod to appear in oc
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
				if pod.Name == testName {
					return true
				}
			}
			return false
		}, 10*time.Second, "pod should appear in oc get pods")

		// Verify pod exists via oc
		output, err := ocHelper.GetPods("")
		require.NoError(t, err, "oc get pods should work")

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err, "oc output should be valid JSON")

		found := false
		var targetPod corev1.Pod
		for _, pod := range podList.Items {
			if pod.Name == testName {
				found = true
				targetPod = pod
				break
			}
		}
		require.True(t, found, "Pod should appear in oc after podman create")

		// Verify pod properties
		assert.Equal(t, "containers", targetPod.Namespace, "Pod should be in containers namespace")
		assert.Equal(t, corev1.PodRunning, targetPod.Status.Phase, "Pod should be running")
		require.Len(t, targetPod.Status.ContainerStatuses, 1, "Pod should have one container")
		assert.Contains(t, targetPod.Status.ContainerStatuses[0].Image, "busybox", "Pod should have correct image")
	})

	t.Run("oc delete -> podman verify", func(t *testing.T) {
		testName := "oc-delete-test"
		defer testutil.CleanupContainers(t, testName)

		// Create pod using oc
		podSpec := testutil.TestPodSpec(testName, "containers", "alpine:latest")
		err := ocHelper.CreatePod(podSpec)
		require.NoError(t, err)

		// Wait for container to be created
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
		}, 10*time.Second, "container should be created")

		// Delete pod using oc
		err = ocHelper.DeletePod(testName, "containers")
		require.NoError(t, err, "oc delete should succeed")

		// Wait for container to be removed from podman
		testutil.WaitForCondition(t, func() bool {
			containers, err := podmanHelper.ListContainers()
			if err != nil {
				return true // Error might mean no containers
			}
			for _, container := range containers {
				for _, name := range container.Names {
					if name == testName {
						return false // Still found, not deleted
					}
				}
			}
			return true // Not found, successfully deleted
		}, 15*time.Second, "container should be removed from podman")

		// Verify container no longer exists in podman
		containers, err := podmanHelper.ListContainers()
		if err == nil { // Only check if we can list containers
			for _, container := range containers {
				for _, name := range container.Names {
					assert.NotEqual(t, testName, name, "Container should not exist in podman after oc delete")
				}
			}
		}
	})

	t.Run("podman stop -> oc verify", func(t *testing.T) {
		testName := "podman-stop-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container using podman
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Wait for pod to appear as running in oc
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
			if err != nil {
				return false
			}
			var pod corev1.Pod
			if err := json.Unmarshal([]byte(output), &pod); err != nil {
				return false
			}
			return pod.Status.Phase == corev1.PodRunning
		}, 10*time.Second, "pod should be running")

		// Stop container using podman
		_, err = podmanHelper.RunPodmanCommand("stop", testName)
		require.NoError(t, err, "podman stop should succeed")

		// Wait for pod status to change in oc
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
			if err != nil {
				return false
			}
			var pod corev1.Pod
			if err := json.Unmarshal([]byte(output), &pod); err != nil {
				return false
			}
			// Pod should either be Succeeded (exit 0) or Failed (non-zero exit), but not Running
			return pod.Status.Phase != corev1.PodRunning
		}, 15*time.Second, "pod should not be running after podman stop")

		// Verify pod status reflects stopped container
		output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
		require.NoError(t, err)

		var pod corev1.Pod
		err = json.Unmarshal([]byte(output), &pod)
		require.NoError(t, err)

		assert.NotEqual(t, corev1.PodRunning, pod.Status.Phase, "Pod should not be running after container stop")
		assert.Contains(t, []corev1.PodPhase{corev1.PodSucceeded, corev1.PodFailed}, pod.Status.Phase, "Pod should be Succeeded or Failed")
	})

	t.Run("Labels consistency across oc and podman", func(t *testing.T) {
		testName := "labels-consistency-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container with labels using podman
		_, err := podmanHelper.RunPodmanCommand("run", "-d", "--name", testName,
			"--label", "app=test-app",
			"--label", "version=v1.2.3",
			"--label", "environment=testing",
			"alpine:latest", "sleep", "3600")
		require.NoError(t, err, "Should create container with labels")

		// Wait for pod to appear in oc
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should appear")

		// Verify labels are consistent in oc
		output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
		require.NoError(t, err)

		var pod corev1.Pod
		err = json.Unmarshal([]byte(output), &pod)
		require.NoError(t, err)

		assert.Equal(t, "test-app", pod.Labels["app"], "app label should be consistent")
		assert.Equal(t, "v1.2.3", pod.Labels["version"], "version label should be consistent")
		assert.Equal(t, "testing", pod.Labels["environment"], "environment label should be consistent")

		// Verify label selectors work
		output, err = ocHelper.RunOCCommand("get", "pods", "-l", "app=test-app", "-o", "json")
		require.NoError(t, err)

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		found := false
		for _, p := range podList.Items {
			if p.Name == testName {
				found = true
				break
			}
		}
		assert.True(t, found, "Pod should be found with label selector")
	})

	t.Run("Annotations consistency across oc and podman", func(t *testing.T) {
		testName := "annotations-consistency-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container with annotations using podman
		_, err := podmanHelper.RunPodmanCommand("run", "-d", "--name", testName,
			"--annotation", "deployment.kubernetes.io/revision=1",
			"--annotation", "custom.annotation=test-value",
			"alpine:latest", "sleep", "3600")
		require.NoError(t, err, "Should create container with annotations")

		// Wait for pod to appear in oc
		testutil.WaitForCondition(t, func() bool {
			_, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
			return err == nil
		}, 10*time.Second, "pod should appear")

		// Verify annotations are consistent in oc
		output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
		require.NoError(t, err)

		var pod corev1.Pod
		err = json.Unmarshal([]byte(output), &pod)
		require.NoError(t, err)

		// Verify custom annotations are preserved
		assert.Equal(t, "1", pod.Annotations["deployment.kubernetes.io/revision"], "deployment annotation should be consistent")
		assert.Equal(t, "test-value", pod.Annotations["custom.annotation"], "custom annotation should be consistent")

		// Verify podman.io annotations are added
		assert.Contains(t, pod.Annotations, "podman.io/container-id", "Should have podman container ID annotation")
		assert.Contains(t, pod.Annotations, "podman.io/image-id", "Should have podman image ID annotation")
	})

	t.Run("Resource state transitions consistency", func(t *testing.T) {
		testName := "state-transition-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container using podman
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Verify initial running state in oc
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
			if err != nil {
				return false
			}
			var pod corev1.Pod
			if err := json.Unmarshal([]byte(output), &pod); err != nil {
				return false
			}
			return pod.Status.Phase == corev1.PodRunning
		}, 10*time.Second, "pod should start running")

		// Stop container using podman
		_, err = podmanHelper.RunPodmanCommand("stop", testName)
		require.NoError(t, err)

		// Verify stopped state in oc
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
			if err != nil {
				return false
			}
			var pod corev1.Pod
			if err := json.Unmarshal([]byte(output), &pod); err != nil {
				return false
			}
			return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
		}, 15*time.Second, "pod should show as terminated")

		// Start container again using podman
		_, err = podmanHelper.RunPodmanCommand("start", testName)
		require.NoError(t, err)

		// Verify running state again in oc
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.RunOCCommand("get", "pod", testName, "-o", "json")
			if err != nil {
				return false
			}
			var pod corev1.Pod
			if err := json.Unmarshal([]byte(output), &pod); err != nil {
				return false
			}
			return pod.Status.Phase == corev1.PodRunning
		}, 10*time.Second, "pod should be running again after restart")
	})
}

// TestSecretConsistency verifies secret consistency between oc and podman
func TestSecretConsistency(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)
	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("oc create secret -> podman verify", func(t *testing.T) {
		secretName := "test-secret-oc-to-podman"

		// Clean up any existing secret
		defer func() {
			ocHelper.RunOCCommand("delete", "secret", secretName)
			podmanHelper.RunPodmanCommand("secret", "rm", secretName)
		}()

		// Create secret using oc
		_, err := ocHelper.RunOCCommand("create", "secret", "generic", secretName,
			"--from-literal=data=secret-test-value")
		require.NoError(t, err, "oc create secret should succeed")

		// Wait for secret to appear in podman
		testutil.WaitForCondition(t, func() bool {
			output, err := podmanHelper.RunPodmanCommand("secret", "ls", "--format", "json")
			if err != nil {
				return false
			}
			return strings.Contains(output, secretName)
		}, 10*time.Second, "secret should appear in podman")

		// Verify secret exists in podman
		output, err := podmanHelper.RunPodmanCommand("secret", "ls", "--format", "{{.Name}}")
		require.NoError(t, err, "Should list podman secrets")
		assert.Contains(t, output, secretName, "Secret should exist in podman after oc create")
	})

	t.Run("podman create secret -> oc verify", func(t *testing.T) {
		secretName := "test-secret-podman-to-oc"

		// Clean up any existing secret
		defer func() {
			ocHelper.RunOCCommand("delete", "secret", secretName)
			podmanHelper.RunPodmanCommand("secret", "rm", secretName)
		}()

		// Create secret using podman
		_, err := podmanHelper.RunPodmanCommand("secret", "create", secretName, "-")
		if err != nil {
			// Try alternative method for creating podman secrets
			_, err = podmanHelper.RunPodmanCommand("bash", "-c",
				fmt.Sprintf("echo 'podman-secret-value' | podman secret create %s -", secretName))
		}
		require.NoError(t, err, "podman create secret should succeed")

		// Wait for secret to appear in oc
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.RunOCCommand("get", "secrets", "-o", "json")
			if err != nil {
				return false
			}
			var secretList corev1.SecretList
			if err := json.Unmarshal([]byte(output), &secretList); err != nil {
				return false
			}
			for _, secret := range secretList.Items {
				if secret.Name == secretName {
					return true
				}
			}
			return false
		}, 10*time.Second, "secret should appear in oc")

		// Verify secret exists via oc
		output, err := ocHelper.RunOCCommand("get", "secrets", "-o", "json")
		require.NoError(t, err, "oc get secrets should work")

		var secretList corev1.SecretList
		err = json.Unmarshal([]byte(output), &secretList)
		require.NoError(t, err)

		found := false
		for _, secret := range secretList.Items {
			if secret.Name == secretName {
				found = true
				assert.Contains(t, secret.Data, "data", "Secret should have data field")
				break
			}
		}
		assert.True(t, found, "Secret should appear in oc after podman create")
	})
}

// TestNamespaceMapping verifies namespace consistency between oc and podman
func TestNamespaceMapping(t *testing.T) {
	testutil.RequireOC(t)
	testutil.RequirePodman(t)

	// Start the test server
	testServer := testutil.NewTestServerFromPodKubeServer(t)
	defer testServer.Close()

	ocHelper := testutil.NewOCHelper(t, testServer.URL)
	podmanHelper := testutil.NewPodmanHelper(t)

	t.Run("Running containers appear in containers namespace", func(t *testing.T) {
		testName := "running-namespace-test"
		defer testutil.CleanupContainers(t, testName)

		// Create running container
		err := podmanHelper.CreateTestContainer(testName, "alpine:latest")
		require.NoError(t, err)

		// Wait for pod to appear
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.GetPods("containers")
			if err != nil {
				return false
			}
			return strings.Contains(output, testName)
		}, 10*time.Second, "pod should appear in containers namespace")

		// Verify pod is in containers namespace
		output, err := ocHelper.GetPods("containers")
		require.NoError(t, err)

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		found := false
		for _, pod := range podList.Items {
			if pod.Name == testName {
				found = true
				assert.Equal(t, "containers", pod.Namespace)
				assert.Equal(t, corev1.PodRunning, pod.Status.Phase)
				break
			}
		}
		assert.True(t, found, "Running container should appear in containers namespace")
	})

	t.Run("Exited containers appear in containers-exited namespace", func(t *testing.T) {
		testName := "exited-namespace-test"
		defer testutil.CleanupContainers(t, testName)

		// Create and immediately exit a container
		_, err := podmanHelper.RunPodmanCommand("run", "--name", testName, "alpine:latest", "echo", "hello")
		require.NoError(t, err, "Should create and exit container")

		// Wait for pod to appear in exited namespace
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.GetPods("containers-exited")
			if err != nil {
				return false
			}
			return strings.Contains(output, testName)
		}, 10*time.Second, "pod should appear in containers-exited namespace")

		// Verify pod is in containers-exited namespace
		output, err := ocHelper.GetPods("containers-exited")
		if err != nil {
			t.Logf("Could not get pods from containers-exited namespace: %v", err)
			return
		}

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		found := false
		for _, pod := range podList.Items {
			if pod.Name == testName {
				found = true
				assert.Equal(t, "containers-exited", pod.Namespace)
				assert.Contains(t, []corev1.PodPhase{corev1.PodSucceeded, corev1.PodFailed}, pod.Status.Phase)
				break
			}
		}
		assert.True(t, found, "Exited container should appear in containers-exited namespace")
	})

	t.Run("Debug containers stay in main namespace", func(t *testing.T) {
		testName := "debug-namespace-test"
		defer testutil.CleanupContainers(t, testName)

		// Create container with debug annotation that will exit
		_, err := podmanHelper.RunPodmanCommand("run", "--name", testName,
			"--annotation", "debug.openshift.io/source-container=original-pod",
			"alpine:latest", "echo", "debug-output")
		require.NoError(t, err)

		// Wait for pod to appear
		testutil.WaitForCondition(t, func() bool {
			output, err := ocHelper.GetPods("containers")
			if err != nil {
				return false
			}
			return strings.Contains(output, testName)
		}, 10*time.Second, "debug pod should appear in containers namespace")

		// Verify debug pod stays in containers namespace even after exit
		output, err := ocHelper.GetPods("containers")
		require.NoError(t, err)

		var podList corev1.PodList
		err = json.Unmarshal([]byte(output), &podList)
		require.NoError(t, err)

		found := false
		for _, pod := range podList.Items {
			if pod.Name == testName {
				found = true
				assert.Equal(t, "containers", pod.Namespace, "Debug pod should stay in containers namespace")
				assert.Equal(t, "original-pod", pod.Annotations["debug.openshift.io/source-container"])
				break
			}
		}
		assert.True(t, found, "Debug container should stay in main namespace even after exit")
	})
}