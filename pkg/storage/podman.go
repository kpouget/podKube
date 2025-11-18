package storage

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
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

	// Convert Podman state to Kubernetes phase and container state
	var phase corev1.PodPhase
	var conditions []corev1.PodCondition
	var containerState corev1.ContainerState
	var ready bool = false
	var restartCount int32 = int32(container.Restarts)

	switch container.State {
	case "running":
		phase = corev1.PodRunning
		ready = true
		conditions = []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
		}
		containerState = corev1.ContainerState{
			Running: &corev1.ContainerStateRunning{
				StartedAt: metav1.NewTime(time.Unix(container.StartedAt, 0)),
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
		containerState = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				ExitCode:   int32(container.ExitCode),
				Reason:     "Completed",
				FinishedAt: metav1.NewTime(time.Unix(container.StartedAt, 0)),
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
		containerState = corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{
				Reason: "ContainerCreating",
			},
		}
	default:
		phase = corev1.PodUnknown
		conditions = []corev1.PodCondition{}
		containerState = corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{
				Reason: "Unknown",
			},
		}
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
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         podName,
					Image:        container.Image,
					ImageID:      container.ImageID,
					ContainerID:  fmt.Sprintf("podman://%s", container.Id),
					Ready:        ready,
					RestartCount: restartCount,
					State:        containerState,
				},
			},
		},
	}

	if creationTime != nil {
		pod.ObjectMeta.CreationTimestamp = *creationTime
	}

	return pod
}