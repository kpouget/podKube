package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/httpstream/spdy"
	remotecommandconsts "k8s.io/apimachinery/pkg/util/remotecommand"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"

	"podman-k8s-adapter/pkg/storage"
)

// TerminalSize represents terminal dimensions
type TerminalSize struct {
	Width  uint16 `json:"width"`
	Height uint16 `json:"height"`
}


// Server represents our Kubernetes API server
type Server struct {
	host       string
	port       int
	httpServer *http.Server
	podStorage *storage.PodStorage
}

// New creates a new Kubernetes API server
func New(host string, port int) *Server {
	podStorage := storage.NewPodStorage()

	mux := http.NewServeMux()

	server := &Server{
		host:       host,
		port:       port,
		podStorage: podStorage,
		httpServer: &http.Server{
			Addr:    fmt.Sprintf("%s:%d", host, port),
			Handler: mux,
		},
	}

	// Register all API routes
	server.registerRoutes(mux)

	return server
}

// registerRoutes sets up all Kubernetes API endpoints
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Core API discovery endpoints (required by kubectl/oc)
	mux.HandleFunc("/api", s.handleAPIDiscovery)
	mux.HandleFunc("/apis", s.handleAPIsDiscovery)
	mux.HandleFunc("/api/v1", s.handleAPIV1Discovery)
	mux.HandleFunc("/apis/project.openshift.io/v1", s.handleProjectAPIDiscovery)

	// Namespace API endpoints
	mux.HandleFunc("/api/v1/namespaces", s.handleNamespaceList)

	// Project API endpoints (OpenShift compatibility)
	mux.HandleFunc("/apis/project.openshift.io/v1/projects/", s.handleProjectByName)
	mux.HandleFunc("/apis/project.openshift.io/v1/projects", s.handleProjectList)
	mux.HandleFunc("/oapi/v1/projects", s.handleProjectList) // Legacy OpenShift API

	// Pod API endpoints
	mux.HandleFunc("/api/v1/pods", s.handleClusterPods)
	mux.HandleFunc("/api/v1/namespaces/", s.handleNamespacedResources)

	// Secret API endpoints
	mux.HandleFunc("/api/v1/secrets", s.handleClusterSecrets)

	// Health and version endpoints
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleHealth)
	mux.HandleFunc("/livez", s.handleHealth)
	mux.HandleFunc("/version", s.handleVersion)

	klog.Infof("Registered API routes:")
	klog.Infof("  GET /api/v1/namespaces")
	klog.Infof("  GET /apis/project.openshift.io/v1/projects")
	klog.Infof("  GET /oapi/v1/projects")
	klog.Infof("  GET /api/v1/pods")
	klog.Infof("  GET /api/v1/namespaces/{namespace}/pods")
	klog.Infof("  GET /api/v1/namespaces/{namespace}/pods/{name}")
	klog.Infof("  GET /api/v1/namespaces/{namespace}/pods/{name}/log")
	klog.Infof("  POST /api/v1/namespaces/{namespace}/pods/{name}/exec")
	klog.Infof("  GET /api/v1/secrets")
	klog.Infof("  GET /api/v1/namespaces/{namespace}/secrets")
	klog.Infof("  GET /api/v1/namespaces/{namespace}/secrets/{name}")
	klog.Infof("  GET /healthz, /readyz, /livez")
	klog.Infof("  GET /version")
}

// handleAPIDiscovery returns core API group information
func (s *Server) handleAPIDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiVersions := &metav1.APIVersions{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIVersions",
			APIVersion: "v1",
		},
		Versions: []string{"v1"},
		ServerAddressByClientCIDRs: []metav1.ServerAddressByClientCIDR{
			{
				ClientCIDR:    "0.0.0.0/0",
				ServerAddress: fmt.Sprintf("%s:%d", s.host, s.port),
			},
		},
	}

	s.writeJSON(w, apiVersions)
}

// handleAPIsDiscovery returns available API groups (empty for core API only)
func (s *Server) handleAPIsDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiGroupList := &metav1.APIGroupList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIGroupList",
			APIVersion: "v1",
		},
		Groups: []metav1.APIGroup{
			{
				Name: "project.openshift.io",
				Versions: []metav1.GroupVersionForDiscovery{
					{
						GroupVersion: "project.openshift.io/v1",
						Version:      "v1",
					},
				},
				PreferredVersion: metav1.GroupVersionForDiscovery{
					GroupVersion: "project.openshift.io/v1",
					Version:      "v1",
				},
			},
		},
	}

	s.writeJSON(w, apiGroupList)
}

// handleAPIV1Discovery returns resources available in the v1 API
func (s *Server) handleAPIV1Discovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiResourceList := &metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{
				Name:         "namespaces",
				SingularName: "namespace",
				Namespaced:   false,
				Kind:         "Namespace",
				Verbs:        []string{"get", "list"},
				ShortNames:   []string{"ns"},
			},
			{
				Name:         "pods",
				SingularName: "pod",
				Namespaced:   true,
				Kind:         "Pod",
				Verbs:        []string{"get", "list", "create", "update", "patch", "delete", "deletecollection", "watch"},
				Categories:   []string{"all"},
			},
			{
				Name:         "pods/exec",
				SingularName: "",
				Namespaced:   true,
				Kind:         "PodExecOptions",
				Verbs:        []string{"create"},
			},
			{
				Name:         "pods/log",
				SingularName: "",
				Namespaced:   true,
				Kind:         "PodLogOptions",
				Verbs:        []string{"get"},
			},
			{
				Name:         "secrets",
				SingularName: "secret",
				Namespaced:   true,
				Kind:         "Secret",
				Verbs:        []string{"get", "list", "create", "delete"},
			},
		},
	}

	s.writeJSON(w, apiResourceList)
}

// handleProjectAPIDiscovery returns resources available in the project.openshift.io/v1 API
func (s *Server) handleProjectAPIDiscovery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	apiResourceList := &metav1.APIResourceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "APIResourceList",
			APIVersion: "v1",
		},
		GroupVersion: "project.openshift.io/v1",
		APIResources: []metav1.APIResource{
			{
				Name:         "projects",
				SingularName: "project",
				Namespaced:   false,
				Kind:         "Project",
				Verbs:        []string{"get", "list"},
			},
		},
	}

	s.writeJSON(w, apiResourceList)
}

// handleNamespaceList handles requests to /api/v1/namespaces
func (s *Server) handleNamespaceList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	namespaces := s.podStorage.ListNamespaces()

	// Create Kubernetes-compatible namespace objects
	var namespaceItems []corev1.Namespace
	for _, ns := range namespaces {
		namespaceItems = append(namespaceItems, corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Namespace",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
			},
		})
	}

	namespaceList := &corev1.NamespaceList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NamespaceList",
			APIVersion: "v1",
		},
		Items: namespaceItems,
	}

	s.writeJSON(w, namespaceList)
}

// handleProjectList handles requests to /apis/project.openshift.io/v1/projects and /oapi/v1/projects
func (s *Server) handleProjectList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	projectList := s.podStorage.ListProjects()
	s.writeJSON(w, projectList)
}

// handleProjectByName handles requests to /apis/project.openshift.io/v1/projects/{name}
func (s *Server) handleProjectByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse the project name from URL
	path := strings.TrimPrefix(r.URL.Path, "/apis/project.openshift.io/v1/projects/")
	projectName := strings.Split(path, "/")[0]

	if projectName == "" {
		http.Error(w, "Project name is required", http.StatusBadRequest)
		return
	}

	// Get the list of available namespaces
	namespaces := s.podStorage.ListNamespaces()

	// Check if the requested project exists
	projectExists := false
	for _, ns := range namespaces {
		if ns == projectName {
			projectExists = true
			break
		}
	}

	if !projectExists {
		http.Error(w, fmt.Sprintf(`projects.project.openshift.io "%s" not found`, projectName), http.StatusNotFound)
		return
	}

	// Return the specific project
	project := &storage.Project{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Project",
			APIVersion: "project.openshift.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: projectName,
			Annotations: map[string]string{
				"openshift.io/display-name": projectName,
				"openshift.io/description":  fmt.Sprintf("Project for %s", projectName),
			},
		},
		Spec: storage.ProjectSpec{
			Finalizers: []string{"kubernetes"},
		},
		Status: storage.ProjectStatus{
			Phase: "Active",
		},
	}

	s.writeJSON(w, project)
}

// handleClusterPods handles requests to /api/v1/pods (cluster-wide pods)
func (s *Server) handleClusterPods(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listPods(w, r, "")
	case http.MethodPost:
		s.createPod(w, r, "")
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleClusterSecrets handles requests to /api/v1/secrets (cluster-wide secrets)
func (s *Server) handleClusterSecrets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listSecrets(w, r, "")
	case http.MethodPost:
		s.createSecret(w, r, "")
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleNamespacedResources handles requests to /api/v1/namespaces/{namespace}/...
func (s *Server) handleNamespacedResources(w http.ResponseWriter, r *http.Request) {
	// Parse the path: /api/v1/namespaces/{namespace}/{resource}[/{name}]
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/")
	parts := strings.Split(path, "/")

	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}

	namespace := parts[0]
	resource := parts[1]

	// Handle pods
	if resource == "pods" {
		// Handle pod logs requests: /api/v1/namespaces/{namespace}/pods/{name}/log
		if len(parts) == 4 && parts[3] == "log" {
			podName := parts[2]
			s.handlePodLogs(w, r, namespace, podName)
			return
		}

		// Handle pod exec requests: /api/v1/namespaces/{namespace}/pods/{name}/exec
		if len(parts) == 4 && parts[3] == "exec" {
			podName := parts[2]
			s.handlePodExec(w, r, namespace, podName)
			return
		}

		// Handle specific pod requests
		if len(parts) == 3 {
			podName := parts[2]
			s.handlePodByName(w, r, namespace, podName)
			return
		}

		// Handle pod list for namespace
		switch r.Method {
		case http.MethodGet:
			s.listPods(w, r, namespace)
		case http.MethodPost:
			s.createPod(w, r, namespace)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// Handle secrets
	if resource == "secrets" {
		// Handle specific secret requests
		if len(parts) == 3 {
			secretName := parts[2]
			s.handleSecretByName(w, r, namespace, secretName)
			return
		}

		// Handle secret list for namespace
		switch r.Method {
		case http.MethodGet:
			s.listSecrets(w, r, namespace)
		case http.MethodPost:
			s.createSecret(w, r, namespace)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	http.NotFound(w, r)
}

// handlePodByName handles requests for specific pods
func (s *Server) handlePodByName(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getPod(w, r, namespace, name)
	case http.MethodPut:
		s.updatePod(w, r, namespace, name)
	case http.MethodDelete:
		s.deletePod(w, r, namespace, name)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// listPods lists pods, optionally filtered by namespace
func (s *Server) listPods(w http.ResponseWriter, r *http.Request, namespace string) {
	labelSelector := r.URL.Query().Get("labelSelector")
	fieldSelector := r.URL.Query().Get("fieldSelector")
	watchParam := r.URL.Query().Get("watch")

	// Handle watch requests
	if watchParam == "true" {
		s.watchPods(w, r, namespace, labelSelector, fieldSelector)
		return
	}

	podList, err := s.podStorage.List(namespace, labelSelector, fieldSelector)
	if err != nil {
		klog.Errorf("Failed to list pods: %v", err)
		http.Error(w, fmt.Sprintf("Failed to list pods: %v", err), http.StatusInternalServerError)
		return
	}

	// Check if client wants table format (oc get pods uses this)
	acceptHeader := r.Header.Get("Accept")
	if strings.Contains(acceptHeader, "as=Table") {
		table := s.podListToTable(podList)
		s.writeJSON(w, table)
	} else {
		s.writeJSON(w, podList)
	}
}

// watchPods handles watch requests for pods
func (s *Server) watchPods(w http.ResponseWriter, r *http.Request, namespace, labelSelector, fieldSelector string) {
	klog.Infof("Starting watch for pods in namespace %q with fieldSelector=%q labelSelector=%q", namespace, fieldSelector, labelSelector)

	// Set headers for streaming (Kubernetes watch format)
	w.Header().Set("Content-Type", "application/json;stream=watch")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Check if we can flush responses
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Check if client wants table format
	acceptHeader := r.Header.Get("Accept")
	isTableFormat := strings.Contains(acceptHeader, "as=Table")

	// Write response header
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Get current pods and send them as ADDED events
	podList, err := s.podStorage.List(namespace, labelSelector, fieldSelector)
	if err != nil {
		klog.Errorf("Failed to list pods for watch: %v", err)
		return
	}

	klog.Infof("Watch DEBUG: Found %d pods matching filters", len(podList.Items))
	for i, pod := range podList.Items {
		klog.Infof("Watch DEBUG: Pod[%d]: name=%s namespace=%s phase=%s", i, pod.Name, pod.Namespace, pod.Status.Phase)
	}

	encoder := json.NewEncoder(w)

	// Send initial ADDED events for existing pods that match the selectors
	// This is correct Kubernetes watch behavior
	klog.Infof("Watch starting - sending initial ADDED events for %d existing pods", len(podList.Items))

	// Keep track of previous pods for change detection
	previousPods := make(map[string]*corev1.Pod)

	// Send initial ADDED events for existing pods
	for _, pod := range podList.Items {
		key := s.podKey(pod.Namespace, pod.Name)
		previousPods[key] = pod.DeepCopy()

		// Send ADDED event for this existing pod
		if isTableFormat {
			singlePodList := &corev1.PodList{
				Items: []corev1.Pod{pod},
			}
			table := s.podListToTable(singlePodList)
			event := &metav1.WatchEvent{
				Type:   string(watch.Added),
				Object: *s.tableRowToRawExtension(table, 0),
			}
			encoder.Encode(event)
			flusher.Flush()
		} else {
			event := &metav1.WatchEvent{
				Type:   string(watch.Added),
				Object: *s.podToRawExtension(&pod),
			}
			// Write JSON directly to ensure proper streaming
			if eventJSON, err := json.Marshal(event); err == nil {
				klog.Infof("Watch DEBUG: Sending event JSON: %s", string(eventJSON))
				w.Write(eventJSON)
				w.Write([]byte("\n"))
				flusher.Flush()
			} else {
				klog.Errorf("Failed to marshal watch event: %v", err)
			}
		}
		klog.Infof("Sent initial ADDED event for pod %s", key)
	}

	// Keep connection alive and watch for changes
	ticker := time.NewTicker(5 * time.Second) // Check more frequently for changes
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			klog.Infof("Watch connection closed by client")
			return
		case <-ticker.C:
			// Check for actual changes
			currentPods, err := s.podStorage.List(namespace, labelSelector, fieldSelector)
			if err != nil {
				klog.Errorf("Failed to refresh pods during watch: %v", err)
				continue
			}

			// Detect changes and send appropriate events
			changes := s.detectPodChanges(previousPods, currentPods.Items)

			if len(changes) > 0 {
				klog.V(2).Infof("Detected %d pod changes", len(changes))

				if isTableFormat {
					// Send table format events for changes only
					table := s.podListToTable(currentPods)
					podIndexMap := make(map[string]int)
					for i, pod := range currentPods.Items {
						key := s.podKey(pod.Namespace, pod.Name)
						podIndexMap[key] = i
					}

					for _, change := range changes {
						var event *metav1.WatchEvent

						switch change.Type {
						case string(watch.Added), string(watch.Modified):
							if idx, exists := podIndexMap[change.Key]; exists {
								event = &metav1.WatchEvent{
									Type:   change.Type,
									Object: *s.tableRowToRawExtension(table, idx),
								}
							}
						case string(watch.Deleted):
							// For deleted pods, create a minimal table row
							deletedTable := s.createDeletedPodTable(change.Pod)
							event = &metav1.WatchEvent{
								Type:   change.Type,
								Object: *s.tableRowToRawExtension(deletedTable, 0),
							}
						}

						if event != nil {
							if err := encoder.Encode(event); err != nil {
								klog.Errorf("Failed to encode watch event: %v", err)
								return
							}
							flusher.Flush()
						}
					}
				} else {
					// Send regular pod format events for changes only
					for _, change := range changes {
						event := &metav1.WatchEvent{
							Type:   change.Type,
							Object: *s.podToRawExtension(change.Pod),
						}
						if err := encoder.Encode(event); err != nil {
							klog.Errorf("Failed to encode watch event: %v", err)
							return
						}
						flusher.Flush()
					}
				}
			}

			// Update previous pods state
			previousPods = make(map[string]*corev1.Pod)
			for _, pod := range currentPods.Items {
				key := s.podKey(pod.Namespace, pod.Name)
				previousPods[key] = pod.DeepCopy()
			}
		}
	}
}

// PodChange represents a change detected in pod state
type PodChange struct {
	Type string
	Key  string
	Pod  *corev1.Pod
}

// podKey creates a unique key for a pod
func (s *Server) podKey(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

// detectPodChanges compares previous and current pods and returns changes
func (s *Server) detectPodChanges(previousPods map[string]*corev1.Pod, currentPods []corev1.Pod) []PodChange {
	var changes []PodChange
	currentPodMap := make(map[string]*corev1.Pod)

	// Build current pods map using each pod's actual namespace
	for i := range currentPods {
		key := s.podKey(currentPods[i].Namespace, currentPods[i].Name)
		currentPodMap[key] = &currentPods[i]
	}

	klog.V(2).Infof("Comparing pods: previous=%d, current=%d", len(previousPods), len(currentPodMap))

	// Check for new or modified pods
	for key, currentPod := range currentPodMap {
		if previousPod, existed := previousPods[key]; existed {
			// Check if pod actually changed
			if s.podChanged(previousPod, currentPod) {
				klog.V(2).Infof("Pod modified: %s", key)
				changes = append(changes, PodChange{
					Type: string(watch.Modified),
					Key:  key,
					Pod:  currentPod,
				})
			} else {
				klog.V(4).Infof("Pod unchanged: %s", key)
			}
		} else {
			klog.V(2).Infof("New pod detected: %s (this shouldn't happen on first poll)", key)
			changes = append(changes, PodChange{
				Type: string(watch.Added),
				Key:  key,
				Pod:  currentPod,
			})
		}
	}

	// Check for deleted pods
	for key, previousPod := range previousPods {
		if _, exists := currentPodMap[key]; !exists {
			klog.V(2).Infof("Pod deleted: %s", key)
			changes = append(changes, PodChange{
				Type: string(watch.Deleted),
				Key:  key,
				Pod:  previousPod,
			})
		}
	}

	return changes
}

// podChanged checks if a pod has actually changed in meaningful ways
func (s *Server) podChanged(previous, current *corev1.Pod) bool {
	// Don't check ResourceVersion - it changes too frequently for internal reasons

	// Check status phase
	if previous.Status.Phase != current.Status.Phase {
		klog.V(2).Infof("Pod %s: Phase changed %s -> %s", current.Name, previous.Status.Phase, current.Status.Phase)
		return true
	}

	// Check container statuses count
	if len(previous.Status.ContainerStatuses) != len(current.Status.ContainerStatuses) {
		klog.V(2).Infof("Pod %s: Container status count changed %d -> %d", current.Name, len(previous.Status.ContainerStatuses), len(current.Status.ContainerStatuses))
		return true
	}

	// Check meaningful container status changes
	for i, prevStatus := range previous.Status.ContainerStatuses {
		if i < len(current.Status.ContainerStatuses) {
			currStatus := current.Status.ContainerStatuses[i]
			if prevStatus.Ready != currStatus.Ready {
				klog.V(2).Infof("Pod %s container %d: Ready changed %t -> %t", current.Name, i, prevStatus.Ready, currStatus.Ready)
				return true
			}
			if prevStatus.RestartCount != currStatus.RestartCount {
				klog.V(2).Infof("Pod %s container %d: RestartCount changed %d -> %d", current.Name, i, prevStatus.RestartCount, currStatus.RestartCount)
				return true
			}
			// Don't check ContainerID changes - containers can be restarted with same restart count
		}
	}

	// Check if pod condition changed (like Ready condition)
	prevConditions := s.getPodConditionSummary(previous)
	currConditions := s.getPodConditionSummary(current)
	if prevConditions != currConditions {
		klog.V(2).Infof("Pod %s: Conditions changed %s -> %s", current.Name, prevConditions, currConditions)
		return true
	}

	return false
}

// getPodConditionSummary returns a summary string of pod conditions for comparison
func (s *Server) getPodConditionSummary(pod *corev1.Pod) string {
	var conditions []string
	for _, condition := range pod.Status.Conditions {
		if condition.Status == corev1.ConditionTrue {
			conditions = append(conditions, string(condition.Type))
		}
	}
	return strings.Join(conditions, ",")
}

// createDeletedPodTable creates a table representation for a deleted pod
func (s *Server) createDeletedPodTable(pod *corev1.Pod) *metav1.Table {
	table := &metav1.Table{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Table",
			APIVersion: "meta.k8s.io/v1",
		},
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Format: "name", Description: "Name must be unique within a namespace"},
			{Name: "Ready", Type: "string", Description: "The aggregate readiness state of this pod for accepting traffic"},
			{Name: "Status", Type: "string", Description: "The aggregate status of the containers in this pod"},
			{Name: "Restarts", Type: "integer", Description: "The number of times the containers in this pod have been restarted", Priority: 1},
			{Name: "Age", Type: "string", Description: "Time since the container started running"},
			{Name: "Created", Type: "string", Description: "When the container was created"},
			{Name: "Image", Type: "string", Description: "The image the container is running", Priority: 1},
			{Name: "Command", Type: "string", Description: "The command the container is running", Priority: 1},
			{Name: "Ports", Type: "string", Description: "The ports exposed by the container", Priority: 1},
			{Name: "Container-ID", Type: "string", Description: "Container ID", Priority: 1},
			{Name: "Labels", Type: "string", Description: "Labels assigned to the pod"},
		},
		Rows: []metav1.TableRow{
			{
				Cells: []interface{}{
					pod.Name,
					"0/0",
					"Terminating",
					0,
					"<unknown>",
					"<unknown>",
					"<none>",
					"<none>",
					"<none>",
					"<none>",
					s.formatLabelsForTable(pod.Labels),
				},
			},
		},
	}
	return table
}

// podToRawExtension converts a pod to a runtime.RawExtension for watch events
func (s *Server) podToRawExtension(pod *corev1.Pod) *runtime.RawExtension {
	// Ensure the pod has proper TypeMeta
	if pod.Kind == "" {
		pod.Kind = "Pod"
	}
	if pod.APIVersion == "" {
		pod.APIVersion = "v1"
	}

	// Convert pod to JSON
	podBytes, err := json.Marshal(pod)
	if err != nil {
		klog.Errorf("Failed to marshal pod for watch event: %v", err)
		return &runtime.RawExtension{}
	}

	return &runtime.RawExtension{
		Raw: podBytes,
	}
}

// tableRowToRawExtension converts a table row to a runtime.RawExtension for watch events
func (s *Server) tableRowToRawExtension(table *metav1.Table, rowIndex int) *runtime.RawExtension {
	if rowIndex >= len(table.Rows) {
		klog.Errorf("Row index %d out of bounds for table with %d rows", rowIndex, len(table.Rows))
		return &runtime.RawExtension{}
	}

	// Create a table with just this one row but same column definitions
	singleRowTable := &metav1.Table{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Table",
			APIVersion: "meta.k8s.io/v1",
		},
		ColumnDefinitions: table.ColumnDefinitions,
		Rows:              []metav1.TableRow{table.Rows[rowIndex]},
	}

	// Convert table to JSON
	tableBytes, err := json.Marshal(singleRowTable)
	if err != nil {
		klog.Errorf("Failed to marshal table row for watch event: %v", err)
		return &runtime.RawExtension{}
	}

	return &runtime.RawExtension{
		Raw: tableBytes,
	}
}

// podListToTable converts a PodList to Table format with custom columns
func (s *Server) podListToTable(podList *corev1.PodList) *metav1.Table {
	table := &metav1.Table{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Table",
			APIVersion: "meta.k8s.io/v1",
		},
		ColumnDefinitions: []metav1.TableColumnDefinition{
			{Name: "Name", Type: "string", Format: "name", Description: "Name must be unique within a namespace"},
			{Name: "Ready", Type: "string", Description: "The aggregate readiness state of this pod for accepting traffic"},
			{Name: "Status", Type: "string", Description: "The aggregate status of the containers in this pod"},
			{Name: "Restarts", Type: "integer", Description: "The number of times the containers in this pod have been restarted", Priority: 1},
			{Name: "Age", Type: "string", Description: "Time since the container started running"},
			{Name: "Created", Type: "string", Description: "When the container was created"},
			{Name: "Image", Type: "string", Description: "The image the container is running", Priority: 1},
			{Name: "Command", Type: "string", Description: "The command the container is running", Priority: 1},
			{Name: "Ports", Type: "string", Description: "The ports exposed by the container", Priority: 1},
			{Name: "Container-ID", Type: "string", Description: "Container ID", Priority: 1},
		},
	}

	// Convert each pod to a table row
	for _, pod := range podList.Items {
		// Calculate age based on start time (when container actually started running)
		age := "<unknown>"
		if pod.Status.StartTime != nil && !pod.Status.StartTime.IsZero() {
			age = translateTimestampSince(*pod.Status.StartTime)
		}

		// Calculate created timestamp (relative time)
		created := "<unknown>"
		if !pod.CreationTimestamp.IsZero() {
			created = translateTimestampSinceCreated(pod.CreationTimestamp)
		}

		// Get container info
		image := "<none>"
		containerID := "<none>"
		command := "<none>"
		ports := "<none>"
		readyContainers := 0
		totalContainers := len(pod.Status.ContainerStatuses)
		restarts := int32(0)

		// Get command and ports from pod spec
		if len(pod.Spec.Containers) > 0 {
			container := pod.Spec.Containers[0]

			// Extract command
			if len(container.Command) > 0 {
				command = strings.Join(container.Command, " ")
			} else if len(container.Args) > 0 {
				command = strings.Join(container.Args, " ")
			}

			// Truncate command to 32 chars + "..." if necessary
			if len(command) > 32 {
				command = command[:32] + "..."
			}

			// Extract ports
			var portStrs []string
			for _, port := range container.Ports {
				portStr := fmt.Sprintf("%d", port.ContainerPort)
				if port.Protocol != "" && port.Protocol != "TCP" {
					portStr += "/" + string(port.Protocol)
				}
				if port.Name != "" {
					portStr += " (" + port.Name + ")"
				}
				portStrs = append(portStrs, portStr)
			}
			if len(portStrs) > 0 {
				ports = strings.Join(portStrs, ", ")
			}
		}

		if len(pod.Status.ContainerStatuses) > 0 {
			containerStatus := pod.Status.ContainerStatuses[0]
			image = containerStatus.Image
			if containerStatus.ContainerID != "" {
				// Extract short container ID
				fullID := containerStatus.ContainerID
				if strings.HasPrefix(fullID, "podman://") {
					shortID := strings.TrimPrefix(fullID, "podman://")
					if len(shortID) >= 12 {
						containerID = shortID[:12]
					} else {
						containerID = shortID
					}
				} else {
					containerID = fullID
				}
			}
			restarts = containerStatus.RestartCount
			if containerStatus.Ready {
				readyContainers++
			}
		}

		// Format ready status as "x/y"
		ready := fmt.Sprintf("%d/%d", readyContainers, totalContainers)

		// Create table row with Object field for --show-labels support
		podCopy := pod.DeepCopy()
		row := metav1.TableRow{
			Cells: []interface{}{
				pod.Name,
				ready,
				string(pod.Status.Phase),
				restarts,
				age,
				created,
				image,
				command,
				ports,
				containerID,
			},
			Object: runtime.RawExtension{
				Object: podCopy,
			},
		}
		table.Rows = append(table.Rows, row)
	}

	return table
}

// formatLabelsForTable formats pod labels for table display
func (s *Server) formatLabelsForTable(labels map[string]string) string {
	if len(labels) == 0 {
		return "<none>"
	}

	// Create key=value pairs
	var labelPairs []string
	for key, value := range labels {
		labelPairs = append(labelPairs, fmt.Sprintf("%s=%s", key, value))
	}

	// Join with commas, but truncate if too long for table display
	result := strings.Join(labelPairs, ",")
	if len(result) > 60 { // Limit to 60 characters for table readability
		result = result[:57] + "..."
	}

	return result
}

// translateTimestampSince returns the elapsed time since timestamp in podman ps format
func translateTimestampSince(timestamp metav1.Time) string {
	if timestamp.IsZero() {
		return "<unknown>"
	}

	elapsed := time.Since(timestamp.Time)

	// Convert to podman ps format like "Up 4 days"
	if elapsed < time.Minute {
		seconds := int(elapsed.Seconds())
		if seconds == 1 {
			return "Up 1 second"
		}
		return fmt.Sprintf("Up %d seconds", seconds)
	} else if elapsed < time.Hour {
		minutes := int(elapsed.Minutes())
		if minutes == 1 {
			return "Up 1 minute"
		}
		return fmt.Sprintf("Up %d minutes", minutes)
	} else if elapsed < 24*time.Hour {
		hours := int(elapsed.Hours())
		if hours == 1 {
			return "Up 1 hour"
		}
		return fmt.Sprintf("Up %d hours", hours)
	} else {
		days := int(elapsed.Hours() / 24)
		if days == 1 {
			return "Up 1 day"
		}
		return fmt.Sprintf("Up %d days", days)
	}
}

// translateTimestampSinceCreated returns the elapsed time since timestamp in "X ago" format
func translateTimestampSinceCreated(timestamp metav1.Time) string {
	if timestamp.IsZero() {
		return "<unknown>"
	}

	elapsed := time.Since(timestamp.Time)

	// Convert to "X ago" format
	if elapsed < time.Minute {
		seconds := int(elapsed.Seconds())
		if seconds == 1 {
			return "1 second ago"
		}
		return fmt.Sprintf("%d seconds ago", seconds)
	} else if elapsed < time.Hour {
		minutes := int(elapsed.Minutes())
		if minutes == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	} else if elapsed < 24*time.Hour {
		hours := int(elapsed.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	} else {
		days := int(elapsed.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// getPod retrieves a specific pod
func (s *Server) getPod(w http.ResponseWriter, r *http.Request, namespace, name string) {
	pod, err := s.podStorage.Get(namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(`pods "%s" not found`, name), http.StatusNotFound)
		} else {
			klog.Errorf("Failed to get pod %s/%s: %v", namespace, name, err)
			http.Error(w, fmt.Sprintf("Failed to get pod: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Check if client wants table format (oc get pod uses this)
	acceptHeader := r.Header.Get("Accept")
	if strings.Contains(acceptHeader, "as=Table") {
		podList := &corev1.PodList{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PodList",
				APIVersion: "v1",
			},
			Items: []corev1.Pod{*pod},
		}
		table := s.podListToTable(podList)
		s.writeJSON(w, table)
	} else {
		s.writeJSON(w, pod)
	}
}

// createPod creates a new pod
func (s *Server) createPod(w http.ResponseWriter, r *http.Request, namespace string) {
	var pod corev1.Pod
	if err := json.NewDecoder(r.Body).Decode(&pod); err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode pod: %v", err), http.StatusBadRequest)
		return
	}

	// Set namespace from URL if not specified in the pod
	if pod.Namespace == "" {
		pod.Namespace = namespace
	}

	// Validate namespace matches URL
	if namespace != "" && pod.Namespace != namespace {
		http.Error(w, "Pod namespace does not match URL namespace", http.StatusBadRequest)
		return
	}

	createdPod, err := s.podStorage.Create(&pod)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			http.Error(w, err.Error(), http.StatusConflict)
		} else {
			klog.Errorf("Failed to create pod: %v", err)
			http.Error(w, fmt.Sprintf("Failed to create pod: %v", err), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(createdPod); err != nil {
		klog.Errorf("Failed to encode created pod: %v", err)
	}
}

// updatePod updates an existing pod
func (s *Server) updatePod(w http.ResponseWriter, r *http.Request, namespace, name string) {
	var pod corev1.Pod
	if err := json.NewDecoder(r.Body).Decode(&pod); err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode pod: %v", err), http.StatusBadRequest)
		return
	}

	// Validate pod name and namespace match URL
	if pod.Name != name {
		http.Error(w, "Pod name does not match URL", http.StatusBadRequest)
		return
	}
	if pod.Namespace != namespace {
		http.Error(w, "Pod namespace does not match URL", http.StatusBadRequest)
		return
	}

	updatedPod, err := s.podStorage.Update(&pod)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(`pods "%s" not found`, name), http.StatusNotFound)
		} else {
			klog.Errorf("Failed to update pod %s/%s: %v", namespace, name, err)
			http.Error(w, fmt.Sprintf("Failed to update pod: %v", err), http.StatusInternalServerError)
		}
		return
	}

	s.writeJSON(w, updatedPod)
}

// deletePod deletes a pod
func (s *Server) deletePod(w http.ResponseWriter, r *http.Request, namespace, name string) {
	err := s.podStorage.Delete(namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(`pods "%s" not found`, name), http.StatusNotFound)
		} else {
			klog.Errorf("Failed to delete pod %s/%s: %v", namespace, name, err)
			http.Error(w, fmt.Sprintf("Failed to delete pod: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Return success status with proper Kubernetes Status object
	status := &metav1.Status{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Status",
			APIVersion: "v1",
		},
		Status:  "Success",
		Code:    200,
		Message: fmt.Sprintf(`pod "%s" deleted`, name),
	}

	s.writeJSON(w, status)
}

// handlePodLogs handles requests for pod logs: /api/v1/namespaces/{namespace}/pods/{name}/log
func (s *Server) handlePodLogs(w http.ResponseWriter, r *http.Request, namespace, name string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate that the pod exists first
	_, err := s.podStorage.Get(namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(`pods "%s" not found`, name), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Failed to get pod: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Parse query parameters for logs options
	query := r.URL.Query()
	follow := query.Get("follow") == "true"
	timestamps := query.Get("timestamps") == "true"
	previous := query.Get("previous") == "true"
	sinceSeconds := query.Get("sinceSeconds")
	tailLines := query.Get("tailLines")

	// Build podman logs command
	args := []string{"logs"}

	if follow {
		args = append(args, "--follow")
	}
	if timestamps {
		args = append(args, "--timestamps")
	}
	if previous {
		args = append(args, "--latest")
	}
	if sinceSeconds != "" {
		args = append(args, "--since", sinceSeconds+"s")
	}
	if tailLines != "" {
		args = append(args, "--tail", tailLines)
	}

	// Add the container name
	args = append(args, name)

	klog.Infof("Executing: podman %v", strings.Join(args, " "))

	// Execute podman logs command
	cmd := exec.Command("podman", args...)

	if follow {
		// For follow mode, we need to stream the output
		s.streamPodmanLogs(w, r, cmd)
	} else {
		// For non-follow mode, get all output and return it
		output, err := cmd.CombinedOutput()
		if err != nil {
			klog.Errorf("Failed to get logs for pod %s/%s: %v, output: %s", namespace, name, err, string(output))
			http.Error(w, fmt.Sprintf("Failed to get logs: %v", err), http.StatusInternalServerError)
			return
		}

		klog.Infof("Got %d bytes of log output for pod %s/%s", len(output), namespace, name)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		if len(output) > 0 {
			n, err := w.Write(output)
			if err != nil {
				klog.Errorf("Failed to write logs response: %v", err)
			} else {
				klog.Infof("Successfully wrote %d bytes to response", n)
			}
		} else {
			klog.Infof("No log output for pod %s/%s", namespace, name)
		}
	}
}

// streamPodmanLogs handles streaming logs for follow mode
func (s *Server) streamPodmanLogs(w http.ResponseWriter, r *http.Request, cmd *exec.Cmd) {
	// Set headers for streaming
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Get stdout pipe
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create pipe: %v", err), http.StatusInternalServerError)
		return
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to start logs command: %v", err), http.StatusInternalServerError)
		return
	}

	// Make sure we can flush the response
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Write initial response
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Copy output to response writer
	// Note: This will block until the command finishes or the client disconnects
	buffer := make([]byte, 4096)
	for {
		klog.V(4).Infof("Reading Stdout ...")
		n, err := stdout.Read(buffer)
		klog.V(4).Infof("Reading Stdout ...")
		if n > 0 {
			w.Write(buffer[:n])
			flusher.Flush()
		}
		if err != nil {
			break
		}
		// Check if client disconnected
		if r.Context().Done() != nil {
			select {
			case <-r.Context().Done():
				cmd.Process.Kill()
				return
			default:
			}
		}
	}

	// Wait for command to finish
	cmd.Wait()
}

// handlePodExec handles requests for pod exec: /api/v1/namespaces/{namespace}/pods/{name}/exec
func (s *Server) handlePodExec(w http.ResponseWriter, r *http.Request, namespace, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Validate that the pod exists first
	_, err := s.podStorage.Get(namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(`pods "%s" not found`, name), http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf("Failed to get pod: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Parse query parameters for exec options
	query := r.URL.Query()
	command := query["command"] // Array of command parts
	stdin := query.Get("stdin") == "true"
	stdout := query.Get("stdout") == "true"
	stderr := query.Get("stderr") == "true"
	tty := query.Get("tty") == "true"

	// Debug logging for request details
	klog.Infof("Full URL: %s", r.URL.String())
	klog.Infof("Raw Query: %s", r.URL.RawQuery)
	klog.Infof("Query params: stdin=%s(%t), stdout=%s(%t), stderr=%s(%t), tty=%s(%t)",
		query.Get("stdin"), stdin,
		query.Get("stdout"), stdout,
		query.Get("stderr"), stderr,
		query.Get("tty"), tty)

	// Check if TTY info might be in headers
	klog.Infof("Request headers: %+v", r.Header)

	// Check if TTY info is inferred from stream setup - in kubelet, TTY might be detected
	// by whether stderr is disabled when stdin+stdout are present
	inferredTTY := stdin && stdout && !stderr
	klog.Infof("Inferred TTY from streams: %t (stdin=%t && stdout=%t && !stderr=%t)",
		inferredTTY, stdin, stdout, stderr)

	// Default to stdout if nothing specified
	if !stdin && !stdout && !stderr {
		stdout = true
	}

	// Validate command
	if len(command) == 0 {
		http.Error(w, "No command specified", http.StatusBadRequest)
		return
	}

	klog.Infof("Executing command in pod %s/%s: %v", namespace, name, command)

	// Build podman exec command
	args := []string{"exec"}
	if tty {
		args = append(args, "-t")
	}
	if stdin {
		args = append(args, "-i")
	}

	// Add the container name and command
	args = append(args, name)
	args = append(args, command...)

	klog.Infof("Executing: podman %v", strings.Join(args, " "))

	// Check if this is an upgrade request (WebSocket or SPDY)
	klog.Infof("Checking for protocol upgrade. Connection: %s, Upgrade: %s", r.Header.Get("Connection"), r.Header.Get("Upgrade"))
	if isUpgradeRequest(r) {
		upgrade := strings.ToLower(r.Header.Get("Upgrade"))
		if strings.HasPrefix(upgrade, "spdy") {
			klog.Infof("Handling SPDY exec request")
			s.handleSPDYExec(w, r, args, stdin, stdout, stderr, tty)
		} else if upgrade == "websocket" {
			klog.Infof("Handling WebSocket exec request")
			s.handleWebSocketExec(w, r, args, stdin, stdout, stderr, tty)
		}
		return
	}
	klog.Infof("Using HTTP streaming exec mode")

	// Handle different streaming modes for HTTP
	if stdin && (stdout || stderr) {
		// Interactive mode - bidirectional streaming
		s.handleInteractiveExec(w, r, args, tty)
	} else {
		// Simple exec mode - just run command and return output
		s.handleSimpleExec(w, r, args)
	}
}

// handleSimpleExec executes a command and returns the output
func (s *Server) handleSimpleExec(w http.ResponseWriter, r *http.Request, args []string) {
	cmd := exec.Command("podman", args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		klog.Errorf("Failed to exec command: %v, output: %s", err, string(output))
		http.Error(w, fmt.Sprintf("Failed to exec: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(output)
}

// handleInteractiveExec handles interactive exec with bidirectional streaming
func (s *Server) handleInteractiveExec(w http.ResponseWriter, r *http.Request, args []string, tty bool) {
	// Set headers for streaming
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Check if we can flush the response
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Create the command
	cmd := exec.Command("podman", args...)

	// Set up pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create stdin pipe: %v", err), http.StatusInternalServerError)
		return
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create stdout pipe: %v", err), http.StatusInternalServerError)
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create stderr pipe: %v", err), http.StatusInternalServerError)
		return
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to start command: %v", err), http.StatusInternalServerError)
		return
	}

	// Write initial response
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Handle stdout in a goroutine
	go func() {
		defer stdout.Close()
		buffer := make([]byte, 1024)
		for {
			klog.V(4).Infof("Reading Stdout(2) ...")
			n, err := stdout.Read(buffer)
			klog.V(4).Infof("Reading Stdout(2) ... DONE")
			if n > 0 {
				w.Write(buffer[:n])
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
	}()

	// Handle stderr in a goroutine
	go func() {
		defer stderr.Close()
		buffer := make([]byte, 1024)
		for {
			klog.V(4).Infof("Reading Stderr ...")
			n, err := stderr.Read(buffer)
			klog.V(4).Infof("Reading Stderr ... DONE")
			if n > 0 {
				w.Write(buffer[:n])
				flusher.Flush()
			}
			if err != nil {
				break
			}
		}
	}()

	// Handle stdin from request body
	if r.Body != nil {
		go func() {
			defer stdin.Close()
			io.Copy(stdin, r.Body)
		}()
	}

	// Wait for command to finish
	cmd.Wait()
}

// isUpgradeRequest checks if the request is asking for a protocol upgrade
func isUpgradeRequest(r *http.Request) bool {
	connectionHeaders := r.Header["Connection"]
	for _, header := range connectionHeaders {
		if strings.Contains(strings.ToLower(header), "upgrade") {
			return true
		}
	}
	return false
}

// ExecOptions contains details about which streams are required for remote command execution
type ExecOptions struct {
	Stdin  bool
	Stdout bool
	Stderr bool
	TTY    bool
}

// connectionContext contains the connection and streams used when forwarding an exec session
type connectionContext struct {
	conn         io.Closer
	stdinStream  io.ReadCloser
	stdoutStream io.WriteCloser
	stderrStream io.WriteCloser
	writeStatus  func(status *apierrors.StatusError) error
	resizeStream io.ReadCloser
	resizeChan   chan TerminalSize
	tty          bool
}

// streamAndReply holds both a Stream and a channel that is closed when the stream's reply frame is enqueued
type streamAndReply struct {
	httpstream.Stream
	replySent <-chan struct{}
}

// handleSPDYExec handles SPDY-based exec requests following kubelet patterns
func (s *Server) handleSPDYExec(w http.ResponseWriter, r *http.Request, args []string, stdin, stdout, stderr, tty bool) {
	klog.Infof("Kubelet-style SPDY exec session starting tty=%v", tty)

	// Parse options from request parameters (kubelet style)
	opts := &ExecOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		TTY:    tty,
	}

	// Supported stream protocols (latest to oldest)
	supportedStreamProtocols := []string{
		remotecommandconsts.StreamProtocolV4Name,
		remotecommandconsts.StreamProtocolV3Name,
		remotecommandconsts.StreamProtocolV2Name,
		remotecommandconsts.StreamProtocolV1Name,
	}

	// Create streaming context using kubelet patterns
	ctx, ok := s.createStreams(r, w, opts, supportedStreamProtocols, 30*time.Second, 10*time.Second)
	if !ok {
		// error is handled by createStreams
		return
	}
	defer ctx.conn.Close()

	// Execute the command with established streams
	klog.V(4).Infof("About to call execInContainer with tty=%t", tty)
	err := s.execInContainer(args, ctx.stdinStream, ctx.stdoutStream, ctx.stderrStream, tty, ctx.resizeChan)
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ProcessState != nil {
			rc := exitErr.ProcessState.ExitCode()
			ctx.writeStatus(&apierrors.StatusError{ErrStatus: metav1.Status{
				Status: metav1.StatusFailure,
				Reason: remotecommandconsts.NonZeroExitCodeReason,
				Details: &metav1.StatusDetails{
					Causes: []metav1.StatusCause{
						{
							Type:    remotecommandconsts.ExitCodeCauseType,
							Message: fmt.Sprintf("%d", rc),
						},
					},
				},
				Message: fmt.Sprintf("command terminated with non-zero exit code: %v", exitErr),
			}})
		} else {
			err = fmt.Errorf("error executing command in container: %v", err)
			klog.Errorf("%v", err)
			ctx.writeStatus(apierrors.NewInternalError(err))
		}
	} else {
		ctx.writeStatus(&apierrors.StatusError{ErrStatus: metav1.Status{
			Status: metav1.StatusSuccess,
		}})
	}

	klog.Infof("Kubelet-style SPDY exec session completed")
}

// createStreams creates the SPDY connection and waits for client streams (kubelet pattern)
func (s *Server) createStreams(req *http.Request, w http.ResponseWriter, opts *ExecOptions, supportedStreamProtocols []string, idleTimeout, streamCreationTimeout time.Duration) (*connectionContext, bool) {
	// Perform protocol handshake
	protocol, err := httpstream.Handshake(req, w, supportedStreamProtocols)
	if err != nil {
		klog.Errorf("Failed to perform protocol handshake: %v", err)
		return nil, false
	}

	klog.V(4).Infof("Negotiated protocol: %s for TTY=%t, Stdin=%t, Stdout=%t, Stderr=%t",
		protocol, opts.TTY, opts.Stdin, opts.Stdout, opts.Stderr)

	streamCh := make(chan streamAndReply)

	upgrader := spdy.NewResponseUpgrader()
	conn := upgrader.UpgradeResponse(w, req, func(stream httpstream.Stream, replySent <-chan struct{}) error {
		streamCh <- streamAndReply{Stream: stream, replySent: replySent}
		return nil
	})

	if conn == nil {
		klog.Errorf("Failed to upgrade connection to SPDY")
		return nil, false
	}

	conn.SetIdleTimeout(idleTimeout)

	// Count expected streams (error stream + requested streams + resize stream for TTY)
	expectedStreams := 1 // error stream is always expected
	if opts.Stdin {
		expectedStreams++
	}
	if opts.Stdout {
		expectedStreams++
	}
	if opts.Stderr {
		expectedStreams++
	}
	if opts.TTY {
		expectedStreams++ // resize stream for TTY mode
	}

	klog.Infof("Waiting for %d streams from client", expectedStreams)

	// Wait for client to create all expected streams
	ctx, err := s.waitForStreams(streamCh, expectedStreams, streamCreationTimeout, protocol)
	if err != nil {
		klog.Errorf("Failed to wait for streams: %v", err)
		conn.Close()
		return nil, false
	}

	ctx.conn = conn

	// Set up resize channel for TTY mode (following kubelet pattern)
	if ctx.resizeStream != nil {
		ctx.resizeChan = make(chan TerminalSize)
		go s.handleResizeEvents(req.Context(), ctx.resizeStream, ctx.resizeChan)
	}

	return ctx, true
}

// waitForStreams waits for the client to create the expected number of streams
func (s *Server) waitForStreams(streams <-chan streamAndReply, expectedStreams int, timeout time.Duration, protocol string) (*connectionContext, error) {
	ctx := &connectionContext{}
	receivedStreams := 0
	replyChan := make(chan struct{})
	expired := time.NewTimer(timeout)
	defer expired.Stop()

	for {
		select {
		case stream := <-streams:
			streamType := stream.Headers().Get(corev1.StreamType)
			klog.Infof("Received stream type: %s", streamType)

			switch streamType {
			case corev1.StreamTypeError:
				ctx.writeStatus = s.createWriteStatusFunc(stream, protocol)
				go s.waitStreamReply(stream.replySent, replyChan)
			case corev1.StreamTypeStdin:
				ctx.stdinStream = stream
				go s.waitStreamReply(stream.replySent, replyChan)
			case corev1.StreamTypeStdout:
				ctx.stdoutStream = stream
				go s.waitStreamReply(stream.replySent, replyChan)
			case corev1.StreamTypeStderr:
				ctx.stderrStream = stream
				go s.waitStreamReply(stream.replySent, replyChan)
			case corev1.StreamTypeResize:
				ctx.resizeStream = stream
				go s.waitStreamReply(stream.replySent, replyChan)
			default:
				klog.Errorf("Unexpected stream type: %q", streamType)
			}

		case <-replyChan:
			receivedStreams++
			klog.Infof("Received stream reply %d/%d", receivedStreams, expectedStreams)
			if receivedStreams == expectedStreams {
				klog.Infof("All expected streams received")
				return ctx, nil
			}

		case <-expired.C:
			return nil, fmt.Errorf("timed out waiting for client to create streams")
		}
	}
}

// waitStreamReply waits for a stream reply and signals completion
func (s *Server) waitStreamReply(replySent <-chan struct{}, notify chan<- struct{}) {
	<-replySent
	notify <- struct{}{}
}

// createWriteStatusFunc creates a status writing function based on protocol version
func (s *Server) createWriteStatusFunc(stream httpstream.Stream, protocol string) func(status *apierrors.StatusError) error {
	return func(status *apierrors.StatusError) error {
		defer func() {
			klog.V(4).Infof("Closing error stream after status write")
			stream.Close()
		}()

		if status.Status().Status == metav1.StatusSuccess {
			klog.V(4).Infof("Writing success status with protocol: %s", protocol)
		} else {
			klog.V(4).Infof("Writing error status: %s with protocol: %s", status.Error(), protocol)
		}

		// For v4+ protocols, write JSON status
		if protocol == remotecommandconsts.StreamProtocolV4Name {
			klog.V(4).Infof("Using v4 status writing")
			return s.writeV4Status(stream, status)
		} else {
			klog.V(4).Infof("Using v1 status writing")
			// For older protocols, write simple status
			return s.writeV1Status(stream, status)
		}
	}
}

// writeV4Status writes status in v4 protocol format (JSON)
func (s *Server) writeV4Status(stream httpstream.Stream, status *apierrors.StatusError) error {
	statusBytes, err := json.Marshal(status.Status())
	if err != nil {
		klog.Errorf("=== EXEC DEBUG: v4 Failed to marshal status: %v", err)
		return err
	}
	klog.V(4).Infof("v4 Writing JSON status to stream: %s", string(statusBytes))
	_, err = stream.Write(statusBytes)
	return err
}

// writeV1Status writes status in v1 protocol format (exit code)
func (s *Server) writeV1Status(stream httpstream.Stream, status *apierrors.StatusError) error {
	if status.Status().Status != metav1.StatusSuccess {
		klog.V(4).Infof("v1 Writing error to stream: %s", status.Error())
		_, err := stream.Write([]byte(status.Error()))
		return err
	}
	klog.V(4).Infof("v1 Success status - not writing anything to error stream")
	return nil
}

// execInContainer executes the command using the established streams (kubelet-style async stream handling)
func (s *Server) execInContainer(args []string, stdin io.ReadCloser, stdout, stderr io.WriteCloser, tty bool, resizeChan <-chan TerminalSize) error {
	klog.V(4).Infof("Starting execInContainer with args: %v", args)
	klog.V(4).Infof("Stream setup - stdin: %t, stdout: %t, stderr: %t, tty: %t, resize: %t",
		stdin != nil, stdout != nil, stderr != nil, tty, resizeChan != nil)

	// Create context to cancel goroutines when command completes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.Command("podman", args...)
	var cmdPid int // Store the podman exec process PID for resize handling
	var ptyFile *os.File // Store PTY file for resize operations

	// For TTY mode, use a real PTY; otherwise use pipes
	if tty {
		klog.V(4).Infof("Creating real PTY for TTY mode")

		// Start the command with a PTY
		var err error
		ptyFile, err = pty.Start(cmd)
		if err != nil {
			klog.Errorf("=== EXEC DEBUG: Failed to start command with PTY: %v", err)
			return fmt.Errorf("failed to start command with PTY: %v", err)
		}
		cmdPid = cmd.Process.Pid
		klog.V(4).Infof("Podman exec started with PTY, PID: %d", cmdPid)

		// Note: Initial PTY size will be set by resize events from the client
		klog.V(4).Infof("PTY created, waiting for initial resize event from client")
	} else {
		klog.V(4).Infof("Using pipes for non-TTY mode")

		// Set up pipes for all streams
		var stdinPipe io.WriteCloser
		var stdoutPipe io.ReadCloser
		var stderrPipe io.ReadCloser
		var err error

		if stdin != nil {
			klog.V(4).Infof("Creating stdin pipe")
			stdinPipe, err = cmd.StdinPipe()
			if err != nil {
				klog.Errorf("=== EXEC DEBUG: Failed to create stdin pipe: %v", err)
				return fmt.Errorf("failed to create stdin pipe: %v", err)
			}
			klog.V(4).Infof("Stdin pipe created successfully")
		}

		if stdout != nil {
			klog.V(4).Infof("Creating stdout pipe")
			stdoutPipe, err = cmd.StdoutPipe()
			if err != nil {
				klog.Errorf("=== EXEC DEBUG: Failed to create stdout pipe: %v", err)
				return fmt.Errorf("failed to create stdout pipe: %v", err)
			}
			klog.V(4).Infof("Stdout pipe created successfully")
		}

		if stderr != nil {
			klog.V(4).Infof("Creating stderr pipe")
			stderrPipe, err = cmd.StderrPipe()
			if err != nil {
				klog.Errorf("=== EXEC DEBUG: Failed to create stderr pipe: %v", err)
				return fmt.Errorf("failed to create stderr pipe: %v", err)
			}
			klog.V(4).Infof("Stderr pipe created successfully")
		}

		// Start the command
		klog.V(4).Infof("Starting podman command")
		err = cmd.Start()
		if err != nil {
			klog.Errorf("=== EXEC DEBUG: Failed to start command: %v", err)
			return err
		}
		cmdPid = cmd.Process.Pid
		klog.V(4).Infof("Podman exec command started successfully, PID: %d", cmdPid)

		// Handle pipe-based streams asynchronously
		if stdinPipe != nil && stdin != nil {
			go func() {
				defer stdinPipe.Close()
				io.Copy(stdinPipe, stdin)
			}()
		}

		if stdoutPipe != nil && stdout != nil {
			go func() {
				defer stdoutPipe.Close()
				io.Copy(stdout, stdoutPipe)
			}()
		}

		if stderrPipe != nil && stderr != nil {
			go func() {
				defer stderrPipe.Close()
				io.Copy(stderr, stderrPipe)
			}()
		}
	}

	// Handle streams asynchronously (kubelet pattern)
	var wg sync.WaitGroup
	streamCount := 0

	// For TTY mode, handle PTY streams
	if tty && ptyFile != nil {
		// PTY handles both stdin and stdout through the same file
		if stdin != nil || stdout != nil {
			streamCount++
			wg.Add(1)
			klog.V(4).Infof("Starting PTY stream goroutine (%d)", streamCount)
			go func() {
				defer wg.Done()
				defer ptyFile.Close()
				klog.V(4).Infof("PTY stream goroutine: Starting bidirectional copy")

				// Handle bidirectional copy between PTY and streams
				done := make(chan struct{})

				// Copy stdin to PTY
				if stdin != nil {
					go func() {
						bytes, err := io.Copy(ptyFile, stdin)
						klog.V(4).Infof("PTY stdin copy completed: %d bytes, error: %v", bytes, err)
						done <- struct{}{}
					}()
				}

				// Copy PTY to stdout
				if stdout != nil {
					go func() {
						bytes, err := io.Copy(stdout, ptyFile)
						klog.V(4).Infof("PTY stdout copy completed: %d bytes, error: %v", bytes, err)
						done <- struct{}{}
					}()
				}

				// Wait for either copy completion or context cancellation
				select {
				case <-done:
					klog.V(4).Infof("PTY stream copy completed")
				case <-ctx.Done():
					klog.V(4).Infof("PTY stream goroutine: Cancelled by context")
				}
				klog.V(4).Infof("PTY stream goroutine: Exiting")
			}()
		}
	}

	// Handle terminal resize events (for TTY mode)
	if resizeChan != nil {
		streamCount++
		wg.Add(1)
		klog.V(4).Infof("Starting resize goroutine (%d)", streamCount)
		go func() {
			defer wg.Done()
			defer func() {
				klog.V(4).Infof("Resize goroutine: Exiting")
			}()

			klog.V(4).Infof("Resize goroutine: Starting to listen for resize events")
			for {
				select {
				case size, ok := <-resizeChan:
					if !ok {
						klog.V(4).Infof("Resize channel closed")
						return
					}
					klog.V(4).Infof("Processing resize event: %dx%d", size.Width, size.Height)

					if tty && ptyFile != nil {
						// For TTY mode, resize the PTY directly
						winsize := &pty.Winsize{
							Rows: uint16(size.Height),
							Cols: uint16(size.Width),
						}
						if err := pty.Setsize(ptyFile, winsize); err != nil {
							klog.Errorf("=== EXEC DEBUG: Failed to resize PTY: %v", err)
						} else {
							klog.V(4).Infof("Successfully resized PTY to %dx%d", size.Width, size.Height)

							// Send SIGWINCH to podman exec so it detects the resize and propagates to container
							if cmdPid > 0 {
								if process, err := os.FindProcess(cmdPid); err != nil {
									klog.Errorf("=== EXEC DEBUG: Failed to find podman exec process %d: %v", cmdPid, err)
								} else if err := process.Signal(syscall.SIGWINCH); err != nil {
									klog.Errorf("=== EXEC DEBUG: Failed to send SIGWINCH to podman exec %d: %v", cmdPid, err)
								} else {
									klog.V(4).Infof("Sent SIGWINCH to podman exec process %d", cmdPid)
								}
							}
						}
					} else {
						klog.V(4).Infof("Skipping resize - not in TTY mode or no PTY file")
					}
				case <-ctx.Done():
					klog.V(4).Infof("Resize goroutine: Cancelled by context")
					return
				}
			}
		}()
	}

	klog.V(4).Infof("Started %d stream goroutines, waiting for command to complete", streamCount)

	// Wait for command to complete
	klog.V(4).Infof("Waiting for command to finish")
	cmdErr := cmd.Wait()
	klog.V(4).Infof("Command finished with error: %v", cmdErr)

	// Cancel context to signal goroutines to finish
	klog.V(4).Infof("Cancelling context to signal goroutines to finish")
	cancel()

	// Wait for all stream copying to complete
	klog.V(4).Infof("Waiting for %d stream goroutines to complete", streamCount)
	wg.Wait()
	klog.V(4).Infof("All stream copying completed")

	return cmdErr
}

// handleResizeEvents handles terminal resize events (kubelet pattern)
func (s *Server) handleResizeEvents(ctx context.Context, stream io.Reader, resizeChan chan<- TerminalSize) {
	defer close(resizeChan)

	decoder := json.NewDecoder(stream)
	for {
		var size TerminalSize
		if err := decoder.Decode(&size); err != nil {
			klog.V(4).Infof("Resize event decode error (expected at end): %v", err)
			break
		}
		klog.V(4).Infof("Received terminal resize: %dx%d", size.Width, size.Height)

		select {
		case resizeChan <- size:
			klog.V(4).Infof("Sent resize event to channel")
		case <-ctx.Done():
			klog.V(4).Infof("Resize handler cancelled by context")
			return
		}
	}
	klog.V(4).Infof("Resize event handler completed")
}


// handleWebSocketExec handles WebSocket-based exec requests (placeholder for now)
func (s *Server) handleWebSocketExec(w http.ResponseWriter, r *http.Request, args []string, stdin, stdout, stderr, tty bool) {
	klog.Infof("WebSocket exec not fully implemented yet, falling back to simple exec")

	// For now, fall back to simple exec
	cmd := exec.Command("podman", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		klog.Errorf("Failed to exec command: %v", err)
		http.Error(w, fmt.Sprintf("Failed to exec: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(output)
}

// handleSecretByName handles requests for specific secrets
func (s *Server) handleSecretByName(w http.ResponseWriter, r *http.Request, namespace, name string) {
	switch r.Method {
	case http.MethodGet:
		s.getSecret(w, r, namespace, name)
	case http.MethodDelete:
		s.deleteSecret(w, r, namespace, name)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// listSecrets lists secrets, optionally filtered by namespace
func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request, namespace string) {
	secretList, err := s.podStorage.ListSecrets(namespace)
	if err != nil {
		klog.Errorf("Failed to list secrets: %v", err)
		http.Error(w, fmt.Sprintf("Failed to list secrets: %v", err), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, secretList)
}

// getSecret retrieves a specific secret
func (s *Server) getSecret(w http.ResponseWriter, r *http.Request, namespace, name string) {
	secret, err := s.podStorage.GetSecret(namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(`secrets "%s" not found`, name), http.StatusNotFound)
		} else {
			klog.Errorf("Failed to get secret %s/%s: %v", namespace, name, err)
			http.Error(w, fmt.Sprintf("Failed to get secret: %v", err), http.StatusInternalServerError)
		}
		return
	}

	s.writeJSON(w, secret)
}

// createSecret creates a new secret
func (s *Server) createSecret(w http.ResponseWriter, r *http.Request, namespace string) {
	var secret corev1.Secret
	if err := json.NewDecoder(r.Body).Decode(&secret); err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode secret: %v", err), http.StatusBadRequest)
		return
	}

	// Set namespace from URL if not specified in the secret
	if secret.Namespace == "" {
		secret.Namespace = namespace
	}

	// Validate namespace matches URL
	if namespace != "" && secret.Namespace != namespace {
		http.Error(w, "Secret namespace does not match URL namespace", http.StatusBadRequest)
		return
	}

	createdSecret, err := s.podStorage.CreateSecret(&secret)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			http.Error(w, err.Error(), http.StatusConflict)
		} else {
			klog.Errorf("Failed to create secret: %v", err)
			http.Error(w, fmt.Sprintf("Failed to create secret: %v", err), http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	if err := json.NewEncoder(w).Encode(createdSecret); err != nil {
		klog.Errorf("Failed to encode created secret: %v", err)
	}
}

// deleteSecret deletes a secret
func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request, namespace, name string) {
	err := s.podStorage.DeleteSecret(namespace, name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, fmt.Sprintf(`secrets "%s" not found`, name), http.StatusNotFound)
		} else {
			klog.Errorf("Failed to delete secret %s/%s: %v", namespace, name, err)
			http.Error(w, fmt.Sprintf("Failed to delete secret: %v", err), http.StatusInternalServerError)
		}
		return
	}

	// Return success status with proper Kubernetes Status object
	status := &metav1.Status{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Status",
			APIVersion: "v1",
		},
		Status:  "Success",
		Code:    200,
		Message: fmt.Sprintf(`secret "%s" deleted`, name),
	}

	s.writeJSON(w, status)
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// handleVersion handles version requests
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	version := map[string]interface{}{
		"major":        "1",
		"minor":        "29",
		"gitVersion":   "v1.29.0-podman-adapter",
		"gitCommit":    "podman-adapter",
		"gitTreeState": "clean",
		"buildDate":    time.Now().Format(time.RFC3339),
		"goVersion":    "go1.24.0",
		"compiler":     "gc",
		"platform":     "linux/amd64",
	}

	s.writeJSON(w, version)
}

// writeJSON writes a JSON response
func (s *Server) writeJSON(w http.ResponseWriter, obj interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(obj); err != nil {
		klog.Errorf("Failed to encode JSON response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// ListenAndServeTLSWithSelfSigned starts the server with a self-signed certificate
func (s *Server) ListenAndServeTLSWithSelfSigned() error {
	cert, err := s.generateSelfSignedCert()
	if err != nil {
		return fmt.Errorf("failed to generate self-signed certificate: %v", err)
	}

	s.httpServer.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	klog.Infof("Starting HTTPS server with self-signed certificate")
	klog.Infof("Use: oc get pods --server=https://%s:%d --insecure-skip-tls-verify", s.host, s.port)

	return s.httpServer.ListenAndServeTLS("", "")
}

// ListenAndServeTLS starts the server with provided certificates
func (s *Server) ListenAndServeTLS(certFile, keyFile string) error {
	klog.Infof("Starting HTTPS server with provided certificate")
	return s.httpServer.ListenAndServeTLS(certFile, keyFile)
}

// generateSelfSignedCert creates a self-signed certificate
func (s *Server) generateSelfSignedCert() (tls.Certificate, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Podman-K8s-Adapter"},
			Country:      []string{"US"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
		DNSNames: []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	return tls.X509KeyPair(certPEM, keyPEM)
}
