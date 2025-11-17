package storage

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

// PodmanContainer represents a container from Podman JSON output
type PodmanContainer struct {
	AutoRemove    bool                   `json:"AutoRemove"`
	Command       []string               `json:"Command"`
	CreatedAt     string                 `json:"CreatedAt"`
	Exited        bool                   `json:"Exited"`
	ExitCode      int                    `json:"ExitCode"`
	Id            string                 `json:"Id"`
	Image         string                 `json:"Image"`
	ImageID       string                 `json:"ImageID"`
	Labels        map[string]string      `json:"Labels"`
	Mounts        []string               `json:"Mounts"`
	Names         []string               `json:"Names"`
	Pid           int                    `json:"Pid"`
	Pod           string                 `json:"Pod"`
	Ports         interface{}            `json:"Ports"`
	Restarts      int                    `json:"Restarts"`
	StartedAt     int64                  `json:"StartedAt"`
	State         string                 `json:"State"`
	Status        string                 `json:"Status"`
	Created       int64                  `json:"Created"`
}

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

// getPodmanContainers calls podman ps --format json to get running containers
func (ps *PodStorage) getPodmanContainers() ([]PodmanContainer, error) {
	cmd := exec.Command("podman", "ps", "--format", "json", "--all")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run podman ps: %v", err)
	}

	var containers []PodmanContainer
	if err := json.Unmarshal(output, &containers); err != nil {
		return nil, fmt.Errorf("failed to parse podman output: %v", err)
	}

	return containers, nil
}

// getPodmanK8sContainer calls podman kube generate NAME to get the container details
func (ps *PodStorage) getPodmanK8sContainer(containerName string) (*corev1.Pod, error) {
	cmd := exec.Command("podman", "kube", "generate", "-t", "pod", containerName)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run podman kube generate: %v", err)
	}

	var pod corev1.Pod
	if err := yaml.Unmarshal(output, &pod); err != nil {
		return nil, fmt.Errorf("failed to parse YAML output: %v", err)
	}

	return &pod, nil
}

// getPodmanContainer gets details for a specific container by ID
func (ps *PodStorage) getPodmanContainer(containerID string) (*PodmanContainer, error) {
	containers, err := ps.getPodmanContainers()
	if err != nil {
		return nil, err
	}

	for _, container := range containers {
		if container.Id == containerID || (len(container.Names) > 0 && container.Names[0] == containerID) {
			return &container, nil
		}
	}

	return nil, fmt.Errorf("container %s not found", containerID)
}

// podmanContainerToPod converts a Podman container to a Kubernetes Pod
func (ps *PodStorage) podmanContainerToPod(container *PodmanContainer) *corev1.Pod {
	// Use the first name as pod name, fall back to truncated container ID
	podName := "unknown"
	podNamespace := ps.namespace

	if len(container.Names) > 0 {
		podName = container.Names[0]
	} else {
		// Use first 12 chars of container ID
		if len(container.Id) >= 12 {
			podName = container.Id[:12]
		} else {
			podName = container.Id
		}
	}

	// generate the podSpec
	var podSpec corev1.PodSpec

	if container.Pod == "" {
		podmanPod, err := ps.getPodmanK8sContainer(container.Id)
		if err != nil {
			klog.Warningf("Failed to get detailed pod spec from podman for id=%s: %v", container.Id, err)
		} else {
			podSpec = podmanPod.Spec
		}

		if container.State == "exited" {
			podNamespace = "containers-exited"
		}
	} else {
		return nil
		podSpec = corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    podName, // Use the same name as the pod
					Image:   container.Image,
					Command: container.Command,
				},
			},
		}
		podNamespace = "pods"
	}

	// Convert Podman state to Kubernetes phase
	var phase corev1.PodPhase
	var conditions []corev1.PodCondition

	switch container.State {
	case "running":
		phase = corev1.PodRunning
		conditions = []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		}
	case "exited":
		if container.ExitCode == 0 {
			phase = corev1.PodSucceeded
		} else {
			phase = corev1.PodFailed
		}
		conditions = []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
		}
	case "created", "configured":
		phase = corev1.PodPending
		conditions = []corev1.PodCondition{
			{
				Type:   corev1.PodScheduled,
				Status: corev1.ConditionTrue,
			},
		}
	default:
		phase = corev1.PodUnknown
		conditions = []corev1.PodCondition{}
	}

	// Convert creation and start times
	var creationTime, startTime *metav1.Time
	if container.Created > 0 {
		t := metav1.NewTime(time.Unix(container.Created, 0))
		creationTime = &t
	}
	if container.StartedAt > 0 {
		t := metav1.NewTime(time.Unix(container.StartedAt, 0))
		startTime = &t
	}

	// Create the Pod object
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: podNamespace,
			Labels:    container.Labels, // Use Podman labels directly
			Annotations: map[string]string{
				"podman.io/container-id": container.Id,
				"podman.io/image-id":     container.ImageID,
			},
		},
		Spec: podSpec,
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: conditions,
			StartTime:  startTime,
		},
	}

	if creationTime != nil {
		pod.ObjectMeta.CreationTimestamp = *creationTime
	}

	return pod
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

	// For now, we only support single-container pods
	if len(pod.Spec.Containers) != 1 {
		return nil, fmt.Errorf("only single-container pods are supported")
	}

	container := pod.Spec.Containers[0]

	// Build podman run command
	args := []string{"run", "-d", "--name", pod.Name}

	// Add environment variables
	for _, env := range container.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", env.Name, env.Value))
	}

	// Add labels from pod
	for key, value := range pod.Labels {
		args = append(args, "--label", fmt.Sprintf("%s=%s", key, value))
	}

	// Add the image and command
	args = append(args, container.Image)
	if len(container.Command) > 0 {
		args = append(args, container.Command...)
	}

	// Run the container
	cmd := exec.Command("podman", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %v", err)
	}

	containerID := strings.TrimSpace(string(output))
	klog.Infof("Created container %s with ID: %s", pod.Name, containerID)

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

	// Stop the container first
	stopCmd := exec.Command("podman", "stop", name)
	if err := stopCmd.Run(); err != nil {
		klog.Warningf("Failed to stop container %s: %v", name, err)
		// Continue to try removal even if stop fails
	}

	// Remove the container
	rmCmd := exec.Command("podman", "rm", name)
	if err := rmCmd.Run(); err != nil {
		return fmt.Errorf("failed to remove container %s: %v", name, err)
	}

	klog.Infof("Deleted container %s", name)
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
