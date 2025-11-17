package storage

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodStorage provides Pod storage operations
type PodStorage struct {
	// In-memory pod storage for now
	pods map[string]*corev1.Pod
}

// NewPodStorage creates a new PodStorage instance
func NewPodStorage() *PodStorage {
	storage := &PodStorage{
		pods: make(map[string]*corev1.Pod),
	}

	// Initialize with some sample pods for testing
	storage.initSamplePods()

	return storage
}

// initSamplePods creates some sample pods for testing
func (ps *PodStorage) initSamplePods() {
	samplePods := []*corev1.Pod{
		{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Pod",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sample-pod-1",
				Namespace: "default",
				Labels: map[string]string{
					"app": "nginx",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "nginx",
						Image: "nginx:latest",
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 80,
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
		{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Pod",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sample-pod-2",
				Namespace: "kube-system",
				Labels: map[string]string{
					"app":       "redis",
					"component": "cache",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "redis",
						Image: "redis:alpine",
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 6379,
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
		{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Pod",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sample-pod-3",
				Namespace: "default",
				Labels: map[string]string{
					"app":     "mysql",
					"tier":    "database",
					"version": "5.7",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "mysql",
						Image: "mysql:5.7",
						Env: []corev1.EnvVar{
							{
								Name:  "MYSQL_ROOT_PASSWORD",
								Value: "secret",
							},
						},
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 3306,
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodScheduled,
						Status: corev1.ConditionTrue,
					},
				},
			},
		},
	}

	// Store the sample pods
	for _, pod := range samplePods {
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		ps.pods[key] = pod
	}
}

// List returns a list of pods, optionally filtered by namespace and selectors
func (ps *PodStorage) List(namespace, labelSelector, fieldSelector string) (*corev1.PodList, error) {
	var pods []corev1.Pod

	for _, pod := range ps.pods {
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
	key := fmt.Sprintf("%s/%s", namespace, name)
	pod, exists := ps.pods[key]
	if !exists {
		return nil, fmt.Errorf("pod %s/%s not found", namespace, name)
	}

	return pod, nil
}

// Create adds a new pod to storage
func (ps *PodStorage) Create(pod *corev1.Pod) (*corev1.Pod, error) {
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	// Check if pod already exists
	if _, exists := ps.pods[key]; exists {
		return nil, fmt.Errorf("pod %s/%s already exists", pod.Namespace, pod.Name)
	}

	// Set some default values
	if pod.Status.Phase == "" {
		pod.Status.Phase = corev1.PodPending
	}

	ps.pods[key] = pod
	return pod, nil
}

// Update modifies an existing pod in storage
func (ps *PodStorage) Update(pod *corev1.Pod) (*corev1.Pod, error) {
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	// Check if pod exists
	if _, exists := ps.pods[key]; !exists {
		return nil, fmt.Errorf("pod %s/%s not found", pod.Namespace, pod.Name)
	}

	ps.pods[key] = pod
	return pod, nil
}

// Delete removes a pod from storage
func (ps *PodStorage) Delete(namespace, name string) error {
	key := fmt.Sprintf("%s/%s", namespace, name)

	// Check if pod exists
	if _, exists := ps.pods[key]; !exists {
		return fmt.Errorf("pod %s/%s not found", namespace, name)
	}

	delete(ps.pods, key)
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