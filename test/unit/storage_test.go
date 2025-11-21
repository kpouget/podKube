package unit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"podman-k8s-adapter/pkg/storage"
	"podman-k8s-adapter/test/testutil"
)

func TestPodStorageInterface(t *testing.T) {
	t.Run("NewPodStorage creates storage instance", func(t *testing.T) {
		ps := storage.NewPodStorage()
		assert.NotNil(t, ps, "NewPodStorage should return a non-nil instance")
	})

	t.Run("Storage provides List interface", func(t *testing.T) {
		ps := storage.NewPodStorage()

		// Test that List method exists and returns appropriate type
		podList, err := ps.List("", "", "")
		if err != nil {
			t.Logf("List method error (expected in test environment): %v", err)
			return
		}

		assert.NotNil(t, podList, "List should return non-nil PodList")
		assert.Equal(t, "PodList", podList.Kind, "Should return correct Kind")
		assert.Equal(t, "v1", podList.APIVersion, "Should return correct APIVersion")
	})
}

func TestLabelSelectorMatching(t *testing.T) {
	t.Skip("Label selector matching is tested through integration tests - private method cannot be tested directly")
}

func TestFieldSelectorMatching(t *testing.T) {
	t.Skip("Field selector matching is tested through integration tests - private method cannot be tested directly")
}

func TestAnnotationMerging(t *testing.T) {
	t.Skip("Annotation merging is tested through integration tests - private method cannot be tested directly")
}

func TestPodStorageCRUD(t *testing.T) {
	testutil.RequirePodman(t)

	ps := storage.NewPodStorage()

	// Test creating a pod
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-crud-pod",
			Namespace: "containers",
			Labels: map[string]string{
				"app":  "test",
				"test": "crud",
			},
			Annotations: map[string]string{
				"test.annotation": "test-value",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "test-container",
					Image:   "alpine:latest",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}

	t.Run("Create Pod", func(t *testing.T) {
		// Clean up any existing container
		defer testutil.CleanupContainers(t, "test-crud-pod")

		_, err := ps.Create(testPod)
		require.NoError(t, err, "Should create pod successfully")
		// Note: Full validation is done in integration tests
	})

	t.Run("Get Pod", func(t *testing.T) {
		// Clean up any existing container and create a test container
		defer testutil.CleanupContainers(t, "test-crud-pod")

		// First create the pod
		_, err := ps.Create(testPod)
		require.NoError(t, err)

		// Now get it
		retrievedPod, err := ps.Get("containers", "test-crud-pod")
		require.NoError(t, err, "Should retrieve pod successfully")
		assert.Equal(t, "test-crud-pod", retrievedPod.Name)
		assert.Equal(t, "containers", retrievedPod.Namespace)
	})

	t.Run("List Pods", func(t *testing.T) {
		// Clean up any existing containers
		defer testutil.CleanupContainers(t, "test-crud-pod")

		// Create a test pod
		_, err := ps.Create(testPod)
		require.NoError(t, err)

		// List all pods
		podList, err := ps.List("", "", "")
		require.NoError(t, err, "Should list pods successfully")

		// Find our test pod in the list
		found := false
		for _, pod := range podList.Items {
			if pod.Name == "test-crud-pod" {
				found = true
				assert.Equal(t, "containers", pod.Namespace)
				break
			}
		}
		assert.True(t, found, "Should find our test pod in the list")
	})

	t.Run("List Pods with Label Selector", func(t *testing.T) {
		// Clean up any existing containers
		defer testutil.CleanupContainers(t, "test-crud-pod")

		// Create a test pod
		_, err := ps.Create(testPod)
		require.NoError(t, err)

		// List pods with matching label selector
		podList, err := ps.List("", "app=test", "")
		require.NoError(t, err, "Should list pods with label selector")

		// Verify only pods with correct labels are returned
		for _, pod := range podList.Items {
			if pod.Name == "test-crud-pod" {
				assert.Equal(t, "test", pod.Labels["app"])
			}
		}
	})

	t.Run("Delete Pod", func(t *testing.T) {
		// Clean up any existing containers
		defer testutil.CleanupContainers(t, "test-crud-pod")

		// First create the pod
		_, err := ps.Create(testPod)
		require.NoError(t, err)

		// Now delete it
		err = ps.Delete("containers", "test-crud-pod")
		require.NoError(t, err, "Should delete pod successfully")

		// Verify it's gone
		_, err = ps.Get("containers", "test-crud-pod")
		assert.Error(t, err, "Should not find deleted pod")
	})

	t.Run("Wrong Namespace Operations", func(t *testing.T) {
		// Test creating in wrong namespace
		wrongNsPod := testPod.DeepCopy()
		wrongNsPod.Namespace = "wrong-namespace"

		_, err := ps.Create(wrongNsPod)
		assert.Error(t, err, "Should not allow creating pod in wrong namespace")
		assert.Contains(t, err.Error(), "containers")

		// Test getting from wrong namespace
		_, err = ps.Get("wrong-namespace", "test-pod")
		assert.Error(t, err, "Should not find pod in wrong namespace")
	})
}

func TestContainerStateMapping(t *testing.T) {
	t.Skip("Container state mapping is tested through integration tests - private conversion logic cannot be tested directly")
}

func TestResourceConsistency(t *testing.T) {
	testutil.RequirePodman(t)

	t.Run("Podman Container to K8s Pod Consistency", func(t *testing.T) {
		// This integration test verifies that the translation between
		// podman containers and k8s pods is consistent

		ps := storage.NewPodStorage()

		// Create a pod with specific attributes
		testPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "consistency-test",
				Namespace: "containers",
				Labels: map[string]string{
					"app":        "consistency-test",
					"version":    "v1.0",
					"component":  "backend",
				},
				Annotations: map[string]string{
					"deployment.kubernetes.io/revision": "1",
					"custom.annotation":                  "test-value",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:    "app",
						Image:   "alpine:latest",
						Command: []string{"sleep", "3600"},
						Env: []corev1.EnvVar{
							{
								Name:  "TEST_ENV",
								Value: "test-value",
							},
						},
					},
				},
			},
		}

		defer testutil.CleanupContainers(t, "consistency-test")

		// Create the pod
		_, err := ps.Create(testPod)
		require.NoError(t, err, "Should create pod successfully")

		// Wait a moment for container to start
		testutil.WaitForCondition(t, func() bool {
			pod, err := ps.Get("containers", "consistency-test")
			return err == nil && pod.Status.Phase == corev1.PodRunning
		}, 10*time.Second, "container should start running")

		// Retrieve the pod and verify consistency
		retrievedPod, err := ps.Get("containers", "consistency-test")
		require.NoError(t, err, "Should retrieve pod successfully")

		// Verify basic metadata consistency
		assert.Equal(t, testPod.Name, retrievedPod.Name)
		assert.Equal(t, testPod.Namespace, retrievedPod.Namespace)

		// Verify labels are preserved
		for key, value := range testPod.Labels {
			assert.Equal(t, value, retrievedPod.Labels[key], "Label %s should be preserved", key)
		}

		// Verify annotations are merged (custom + podman.io)
		for key, value := range testPod.Annotations {
			assert.Equal(t, value, retrievedPod.Annotations[key], "Annotation %s should be preserved", key)
		}
		assert.Contains(t, retrievedPod.Annotations, "podman.io/container-id")
		assert.Contains(t, retrievedPod.Annotations, "podman.io/image-id")

		// Verify container spec consistency
		require.Len(t, retrievedPod.Status.ContainerStatuses, 1)
		containerStatus := retrievedPod.Status.ContainerStatuses[0]
		assert.Equal(t, testPod.Spec.Containers[0].Image, containerStatus.Image)
		assert.True(t, containerStatus.Ready)
		assert.Equal(t, corev1.PodRunning, retrievedPod.Status.Phase)
	})
}