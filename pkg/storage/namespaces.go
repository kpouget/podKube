package storage

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OpenShift Project types (simplified)
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ProjectSpec   `json:"spec,omitempty"`
	Status            ProjectStatus `json:"status,omitempty"`
}

type ProjectSpec struct {
	Finalizers []string `json:"finalizers,omitempty"`
}

type ProjectStatus struct {
	Phase string `json:"phase,omitempty"`
}

type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

// ListNamespaces returns the list of available namespaces
func (ps *PodStorage) ListNamespaces() []string {
	return []string{
		"containers",
		"containers-exited",
		"pods",
	}
}

// ListProjects returns the list of available namespaces as OpenShift projects
func (ps *PodStorage) ListProjects() *ProjectList {
	namespaces := ps.ListNamespaces()
	var projects []Project

	for _, ns := range namespaces {
		projects = append(projects, Project{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Project",
				APIVersion: "project.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
				Annotations: map[string]string{
					"openshift.io/display-name": ns,
					"openshift.io/description":  fmt.Sprintf("Project for %s", ns),
				},
			},
			Spec: ProjectSpec{
				Finalizers: []string{"kubernetes"},
			},
			Status: ProjectStatus{
				Phase: "Active",
			},
		})
	}

	return &ProjectList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ProjectList",
			APIVersion: "project.openshift.io/v1",
		},
		Items: projects,
	}
}