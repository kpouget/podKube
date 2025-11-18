package storage

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/yaml"
)

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

// createPodmanContainer runs a Podman container with the given arguments
func (ps *PodStorage) createPodmanContainer(pod *corev1.Pod) (string, error) {
	// For now, we only support single-container pods
	if len(pod.Spec.Containers) != 1 {
		return "", fmt.Errorf("only single-container pods are supported")
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
		return "", fmt.Errorf("failed to create container: %v", err)
	}

	containerID := strings.TrimSpace(string(output))
	klog.Infof("Created container %s with ID: %s", pod.Name, containerID)

	return containerID, nil
}

// stopPodmanContainer stops a Podman container
func (ps *PodStorage) stopPodmanContainer(name string) error {
	stopCmd := exec.Command("podman", "stop", name)
	if err := stopCmd.Run(); err != nil {
		klog.Warningf("Failed to stop container %s: %v", name, err)
		// Continue to try removal even if stop fails
	}
	return nil
}

// removePodmanContainer removes a Podman container
func (ps *PodStorage) removePodmanContainer(name string) error {
	rmCmd := exec.Command("podman", "rm", name)
	if err := rmCmd.Run(); err != nil {
		return fmt.Errorf("failed to remove container %s: %v", name, err)
	}

	klog.Infof("Deleted container %s", name)
	return nil
}