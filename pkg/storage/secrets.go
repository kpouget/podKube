package storage

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// PodmanSecret represents a secret from Podman JSON output
type PodmanSecret struct {
	ID          string            `json:"ID"`
	Name        string            `json:"Name"`
	Driver      string            `json:"Driver"`
	DriverOpts  map[string]string `json:"DriverOpts"`
	CreatedAt   string            `json:"CreatedAt"`
	UpdatedAt   string            `json:"UpdatedAt"`
}

// parseRelativeTime parses relative time strings like "2 minutes ago" into actual time
func (ps *PodStorage) parseRelativeTime(relativeTime string) time.Time {
	if relativeTime == "" {
		return time.Now()
	}

	// Handle "X time ago" format
	re := regexp.MustCompile(`^(\d+)\s+(second|minute|hour|day|week|month|year)s?\s+ago$`)
	matches := re.FindStringSubmatch(relativeTime)

	if len(matches) != 3 {
		klog.Warningf("Failed to parse relative time '%s', using current time", relativeTime)
		return time.Now()
	}

	amount, err := strconv.Atoi(matches[1])
	if err != nil {
		klog.Warningf("Failed to parse time amount '%s', using current time", matches[1])
		return time.Now()
	}

	unit := strings.ToLower(matches[2])
	now := time.Now()

	switch unit {
	case "second":
		return now.Add(-time.Duration(amount) * time.Second)
	case "minute":
		return now.Add(-time.Duration(amount) * time.Minute)
	case "hour":
		return now.Add(-time.Duration(amount) * time.Hour)
	case "day":
		return now.Add(-time.Duration(amount) * 24 * time.Hour)
	case "week":
		return now.Add(-time.Duration(amount) * 7 * 24 * time.Hour)
	case "month":
		return now.AddDate(0, -amount, 0)
	case "year":
		return now.AddDate(-amount, 0, 0)
	default:
		klog.Warningf("Unknown time unit '%s', using current time", unit)
		return now
	}
}

// podmanSecretToSecret converts a Podman secret to a Kubernetes Secret
func (ps *PodStorage) podmanSecretToSecret(secret *PodmanSecret) *corev1.Secret {
	// Parse creation time from Podman relative format
	creationTime := metav1.NewTime(ps.parseRelativeTime(secret.CreatedAt))

	// Get the actual secret data
	secretData, err := ps.getPodmanSecretData(secret.Name)
	if err != nil {
		klog.Warningf("Failed to get secret data for %s: %v", secret.Name, err)
		// Use placeholder if we can't get the real data
		secretData = map[string][]byte{
			"podman-secret": []byte("stored-in-podman"),
		}
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              secret.Name,
			Namespace:         ps.namespace,
			CreationTimestamp: creationTime,
			Annotations: map[string]string{
				"podman.io/secret-id": secret.ID,
				"podman.io/driver":    secret.Driver,
				"podman.io/created":   secret.CreatedAt,
				"podman.io/updated":   secret.UpdatedAt,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: secretData,
	}
}

// ListSecrets returns a list of secrets from Podman
func (ps *PodStorage) ListSecrets(namespace string) (*corev1.SecretList, error) {
	// Filter by namespace if specified
	if namespace != "" && namespace != ps.namespace {
		return &corev1.SecretList{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SecretList",
				APIVersion: "v1",
			},
			Items: []corev1.Secret{},
		}, nil
	}

	// Get secrets from Podman
	secrets, err := ps.getPodmanSecrets()
	if err != nil {
		klog.Errorf("Failed to get Podman secrets: %v", err)
		return nil, fmt.Errorf("failed to get secrets: %v", err)
	}

	var k8sSecrets []corev1.Secret
	for _, secret := range secrets {
		k8sSecret := ps.podmanSecretToSecret(&secret)
		k8sSecrets = append(k8sSecrets, *k8sSecret)
	}

	return &corev1.SecretList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SecretList",
			APIVersion: "v1",
		},
		ListMeta: metav1.ListMeta{},
		Items:    k8sSecrets,
	}, nil
}

// GetSecret returns a specific secret by namespace and name
func (ps *PodStorage) GetSecret(namespace, name string) (*corev1.Secret, error) {
	// Only support our containers namespace
	if namespace != "" && namespace != ps.namespace {
		return nil, fmt.Errorf("secret %s/%s not found", namespace, name)
	}

	// Get specific secret by name
	secret, err := ps.getPodmanSecret(name)
	if err != nil {
		return nil, fmt.Errorf("secret %s/%s not found", namespace, name)
	}

	return ps.podmanSecretToSecret(secret), nil
}

// CreateSecret adds a new secret to storage by creating a Podman secret
func (ps *PodStorage) CreateSecret(secret *corev1.Secret) (*corev1.Secret, error) {
	// Validate namespace
	if secret.Namespace != ps.namespace {
		return nil, fmt.Errorf("secrets can only be created in namespace %s", ps.namespace)
	}

	// Check if secret already exists
	existing, err := ps.getPodmanSecret(secret.Name)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("secret %s/%s already exists", secret.Namespace, secret.Name)
	}

	// Create the Podman secret using CLI layer
	err = ps.createPodmanSecret(secret)
	if err != nil {
		return nil, err
	}

	// Get the created secret details and return as Secret
	createdSecret, err := ps.getPodmanSecret(secret.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get created secret: %v", err)
	}

	return ps.podmanSecretToSecret(createdSecret), nil
}

// DeleteSecret removes a secret from storage by removing the Podman secret
func (ps *PodStorage) DeleteSecret(namespace, name string) error {
	// Validate namespace
	if namespace != "" && namespace != ps.namespace {
		return fmt.Errorf("secret %s/%s not found", namespace, name)
	}

	// Check if secret exists
	_, err := ps.getPodmanSecret(name)
	if err != nil {
		return fmt.Errorf("secret %s/%s not found", namespace, name)
	}

	// Remove the secret using CLI layer
	err = ps.removePodmanSecret(name)
	if err != nil {
		return err
	}

	return nil
}
