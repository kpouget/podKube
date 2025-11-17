package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"podman-k8s-adapter/pkg/storage"
)

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

	// Only handle pods for now
	if resource != "pods" {
		http.NotFound(w, r)
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

	podList, err := s.podStorage.List(namespace, labelSelector, fieldSelector)
	if err != nil {
		klog.Errorf("Failed to list pods: %v", err)
		http.Error(w, fmt.Sprintf("Failed to list pods: %v", err), http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, podList)
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

	s.writeJSON(w, pod)
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

	// Return success status with empty response body (standard K8s behavior)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
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
