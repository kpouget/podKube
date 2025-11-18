package storage

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// PodStorage provides Pod storage operations backed by Podman
type PodStorage struct {
	namespace string // All containers go in this namespace
}

// NewPodStorage creates a new PodStorage instance
func NewPodStorage() *PodStorage {
	return &PodStorage{
		namespace: "containers", // All Podman containers go in "containers" namespace
	}
}

// List returns a list of pods, optionally filtered by namespace and selectors
func (ps *PodStorage) List(namespace, labelSelector, fieldSelector string) (*corev1.PodList, error) {
	// Get containers from Podman
	containers, err := ps.getPodmanContainers()
	if err != nil {
		klog.Errorf("Failed to get Podman containers: %v", err)
		return nil, fmt.Errorf("failed to get containers: %v", err)
	}

	var pods []corev1.Pod
	for _, container := range containers {
		pod := ps.podmanContainerToPod(&container)

		if pod == nil {
			continue
		}

		// Filter by namespace if specified
		if namespace != "" && pod.Namespace != namespace {
			continue
		}

		// Apply label selector filtering (simple implementation)
		if labelSelector != "" && !ps.matchesLabelSelector(pod, labelSelector) {
			continue
		}

		// Apply field selector filtering (simple implementation)
		if fieldSelector != "" && !ps.matchesFieldSelector(pod, fieldSelector) {
			continue
		}

		pods = append(pods, *pod)
	}

	return &corev1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PodList",
			APIVersion: "v1",
		},
		ListMeta: metav1.ListMeta{},
		Items:    pods,
	}, nil
}

// Get returns a specific pod by namespace and name
func (ps *PodStorage) Get(namespace, name string) (*corev1.Pod, error) {
	// Only support our containers namespace
	if namespace != "" && namespace != ps.namespace {
		return nil, fmt.Errorf("pod %s/%s not found", namespace, name)
	}

	// Get specific container by name
	container, err := ps.getPodmanContainer(name)
	if err != nil {
		return nil, fmt.Errorf("pod %s/%s not found", namespace, name)
	}

	pod := ps.podmanContainerToPod(container)
	return pod, nil
}

// Create adds a new pod to storage by running a Podman container
func (ps *PodStorage) Create(pod *corev1.Pod) (*corev1.Pod, error) {
	// Validate namespace
	if pod.Namespace != ps.namespace {
		return nil, fmt.Errorf("pods can only be created in namespace %s", ps.namespace)
	}

	// Check if container already exists
	existing, err := ps.getPodmanContainer(pod.Name)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("pod %s/%s already exists", pod.Namespace, pod.Name)
	}

	// Create the Podman container using CLI layer
	_, err = ps.createPodmanContainer(pod)
	if err != nil {
		return nil, err
	}

	// Get the created container details and return as Pod
	createdContainer, err := ps.getPodmanContainer(pod.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get created container: %v", err)
	}

	return ps.podmanContainerToPod(createdContainer), nil
}

// Update modifies an existing pod in storage (limited support for containers)
func (ps *PodStorage) Update(pod *corev1.Pod) (*corev1.Pod, error) {
	// Validate namespace
	if pod.Namespace != ps.namespace {
		return nil, fmt.Errorf("pods can only be updated in namespace %s", ps.namespace)
	}

	// Check if container exists
	_, err := ps.getPodmanContainer(pod.Name)
	if err != nil {
		return nil, fmt.Errorf("pod %s/%s not found", pod.Namespace, pod.Name)
	}

	// For containers, we can't update much - mainly just return current state
	// In a real implementation, you might support label updates via podman update
	klog.Infof("Update request for pod %s - containers have limited update support", pod.Name)

	// Get current state and return it
	current, err := ps.Get(pod.Namespace, pod.Name)
	if err != nil {
		return nil, err
	}

	return current, nil
}

// Delete removes a pod from storage by stopping and removing the Podman container
func (ps *PodStorage) Delete(namespace, name string) error {
	// Validate namespace
	if namespace != "" && namespace != ps.namespace {
		return fmt.Errorf("pod %s/%s not found", namespace, name)
	}

	// Check if container exists
	_, err := ps.getPodmanContainer(name)
	if err != nil {
		return fmt.Errorf("pod %s/%s not found", namespace, name)
	}

	// Stop the container using CLI layer
	ps.stopPodmanContainer(name)

	// Remove the container using CLI layer
	err = ps.removePodmanContainer(name)
	if err != nil {
		return err
	}

	return nil
}

// matchesLabelSelector performs simple label selector matching
func (ps *PodStorage) matchesLabelSelector(pod *corev1.Pod, selector string) bool {
	// Simple implementation: supports "key=value" format
	if selector == "" {
		return true
	}

	parts := strings.Split(selector, "=")
	if len(parts) != 2 {
		return true // Skip complex selectors for now
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	if pod.Labels == nil {
		return false
	}

	podValue, exists := pod.Labels[key]
	return exists && podValue == value
}

// matchesFieldSelector performs simple field selector matching
func (ps *PodStorage) matchesFieldSelector(pod *corev1.Pod, selector string) bool {
	// Simple implementation: supports "status.phase=Running" format
	if selector == "" {
		return true
	}

	parts := strings.Split(selector, "=")
	if len(parts) != 2 {
		return true // Skip complex selectors for now
	}

	field := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	switch field {
	case "status.phase":
		return string(pod.Status.Phase) == value
	case "metadata.namespace":
		return pod.Namespace == value
	case "metadata.name":
		return pod.Name == value
	default:
		return true // Unknown fields are ignored
	}
}

