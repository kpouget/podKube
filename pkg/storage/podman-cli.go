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

	// Enhance each container with detailed annotations from inspect
	for i := range containers {
		if annotations, err := ps.getPodmanContainerAnnotations(containers[i].Id); err == nil {
			containers[i].Annotations = annotations
		} else {
			klog.Warningf("Failed to get annotations for container %s: %v", containers[i].Id, err)
		}
	}

	return containers, nil
}

// getPodmanContainerAnnotations gets annotations for a specific container using inspect
func (ps *PodStorage) getPodmanContainerAnnotations(containerID string) (map[string]string, error) {
	cmd := exec.Command("podman", "inspect", containerID)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %v", containerID, err)
	}

	// Parse the inspect output to get annotations
	var inspectResult []struct {
		Config struct {
			Annotations map[string]string `json:"Annotations"`
		} `json:"Config"`
	}

	if err := json.Unmarshal(output, &inspectResult); err != nil {
		return nil, fmt.Errorf("failed to parse inspect output: %v", err)
	}

	if len(inspectResult) == 0 {
		return map[string]string{}, nil
	}

	annotations := inspectResult[0].Config.Annotations
	if annotations == nil {
		return map[string]string{}, nil
	}

	return annotations, nil
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

	// Add annotations from pod
	for key, value := range pod.Annotations {
		args = append(args, "--annotation", fmt.Sprintf("%s=%s", key, value))
	}

	// Add the image and command
	args = append(args, container.Image)

	// Use the specified command from the container spec
	if len(container.Command) > 0 {
		// For debug pods with specific commands, we need to keep them running
		// so that oc debug can attach and capture output
		if _, hasDebugAnnotation := pod.Annotations["debug.openshift.io/source-container"]; hasDebugAnnotation {
			klog.Infof("Debug pod %s: wrapping command to allow attachment", pod.Name)
			// Wrap the command in a shell that stays open briefly for attachment
			args = append(args, "/bin/sh", "-c",
				fmt.Sprintf("(%s) & pid=$!; sleep 2; wait $pid", strings.Join(container.Command, " ")))
		} else {
			args = append(args, container.Command...)
		}
	} else {
		// If no command specified, use sleep to keep container running for interactive debugging
		klog.Infof("No command specified for pod %s: using sleep to keep container alive", pod.Name)
		args = append(args, "sleep", "3600")
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

// getPodmanSecrets calls podman secret ls with custom format to get secrets
func (ps *PodStorage) getPodmanSecrets() ([]PodmanSecret, error) {
	cmd := exec.Command("podman", "secret", "ls", "--format", "{{.ID}}\t{{.Name}}\t{{.Driver}}\t{{.CreatedAt}}\t{{.UpdatedAt}}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to run podman secret ls: %v", err)
	}

	// Handle empty output (no secrets)
	outputStr := strings.TrimSpace(string(output))
	if len(outputStr) == 0 {
		return []PodmanSecret{}, nil
	}

	var secrets []PodmanSecret
	lines := strings.Split(outputStr, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) >= 5 {
			secret := PodmanSecret{
				ID:        parts[0],
				Name:      parts[1],
				Driver:    parts[2],
				CreatedAt: parts[3],
				UpdatedAt: parts[4],
			}
			secrets = append(secrets, secret)
		}
	}

	return secrets, nil
}

// getPodmanSecret gets details for a specific secret by name
func (ps *PodStorage) getPodmanSecret(secretName string) (*PodmanSecret, error) {
	secrets, err := ps.getPodmanSecrets()
	if err != nil {
		return nil, err
	}

	for _, secret := range secrets {
		if secret.Name == secretName {
			return &secret, nil
		}
	}

	return nil, fmt.Errorf("secret %s not found", secretName)
}

// createPodmanSecret creates a Podman secret
func (ps *PodStorage) createPodmanSecret(secret *corev1.Secret) error {
	// Validate secret data - must have exactly one key named "data"
	if len(secret.Data) == 0 {
		return fmt.Errorf("secret must contain data")
	}

	if len(secret.Data) > 1 {
		return fmt.Errorf("secret must contain exactly one data entry, got %d", len(secret.Data))
	}

	// Check that the single key is named "data"
	var secretValue []byte
	for key, value := range secret.Data {
		if key != "data" {
			return fmt.Errorf("secret key must be 'data', got '%s'", key)
		}
		secretValue = value
		break
	}

	// Create secret using echo and pipe (use -n to avoid trailing newline)
	cmd := exec.Command("sh", "-c", fmt.Sprintf("echo -n '%s' | podman secret create %s -", string(secretValue), secret.Name))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create secret %s: %v", secret.Name, err)
	}

	klog.Infof("Created secret %s", secret.Name)
	return nil
}

// getPodmanSecretData retrieves the actual secret data by temporarily mounting it in a container
func (ps *PodStorage) getPodmanSecretData(secretName string) (map[string][]byte, error) {
	// Create a temporary container to access the secret data
	// Use a minimal image and mount the secret to read its content
	containerName := fmt.Sprintf("temp-secret-reader-%s", secretName)

	// Run a temporary container that mounts the secret and outputs its content
	cmd := exec.Command("podman", "run", "--rm", "--name", containerName,
		"--secret", fmt.Sprintf("%s,type=mount,target=/tmp/secret", secretName),
		"alpine:latest", "cat", "/tmp/secret")

	output, err := cmd.Output()
	if err != nil {
		klog.Warningf("Failed to read secret data for %s: %v", secretName, err)
		// Return placeholder data if we can't read the secret
		return map[string][]byte{
			"data": []byte("stored-in-podman"),
		}, nil
	}

	// Always return the raw secret data under the "data" key
	// Since we now store only the value (ignoring the original key name)
	return map[string][]byte{
		"data": output,
	}, nil
}

// removePodmanSecret removes a Podman secret
func (ps *PodStorage) removePodmanSecret(name string) error {
	rmCmd := exec.Command("podman", "secret", "rm", name)
	if err := rmCmd.Run(); err != nil {
		return fmt.Errorf("failed to remove secret %s: %v", name, err)
	}

	klog.Infof("Deleted secret %s", name)
	return nil
}