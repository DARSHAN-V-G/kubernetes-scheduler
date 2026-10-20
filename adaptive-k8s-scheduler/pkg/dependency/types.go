package dependency

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	// AnnotationDependsOn specifies comma-separated dependencies: "svc-a,svc-b" or "ns/svc-a"
	AnnotationDependsOn = "reclaim.io/depends-on"
	// AnnotationApp groups workloads into an application boundary
	AnnotationApp = "reclaim.io/app"
	// AnnotationStartupOrder provides optional numeric startup ordering
	AnnotationStartupOrder = "reclaim.io/startup-order"
)

// WorkloadKey uniquely identifies a workload in the cluster.
type WorkloadKey struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (w WorkloadKey) String() string {
	if w.Namespace == "" {
		return w.Name
	}
	return fmt.Sprintf("%s/%s", w.Namespace, w.Name)
}

// WorkloadInfo holds the metadata and state of a workload for dependency resolution.
type WorkloadInfo struct {
	Key          WorkloadKey
	ServiceName  string
	Dependencies []WorkloadKey
	IsReady      bool
	IsHibernate  bool // Checkpointed/Reclaimed
	Labels       map[string]string
	Annotations  map[string]string
}

// DependencyNode represents a node in the dependency graph.
type DependencyNode struct {
	Info     WorkloadInfo
	Parents  map[WorkloadKey]*DependencyNode // Workloads that depend on this node
	Children map[WorkloadKey]*DependencyNode // Workloads that this node depends on
}

// RestorationPlan represents an ordered sequence of restoration stages.
// Workloads in the same stage can be restored concurrently in parallel.
type RestorationPlan struct {
	Target WorkloadKey
	Stages [][]WorkloadKey
}

// ParseDependencies extracts dependency keys from workload annotations.
func ParseDependencies(namespace string, annotations map[string]string) []WorkloadKey {
	if annotations == nil {
		return nil
	}
	raw := annotations[AnnotationDependsOn]
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	deps := make([]WorkloadKey, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "/") {
			subParts := strings.SplitN(trimmed, "/", 2)
			deps = append(deps, WorkloadKey{Namespace: subParts[0], Name: subParts[1]})
		} else {
			deps = append(deps, WorkloadKey{Namespace: namespace, Name: trimmed})
		}
	}
	return deps
}

// WorkloadInfoFromPod builds WorkloadInfo from a Kubernetes Pod object.
func WorkloadInfoFromPod(pod *corev1.Pod, isHibernated bool) WorkloadInfo {
	serviceName := pod.Labels["app"]
	if serviceName == "" {
		serviceName = pod.Name
	}

	isReady := false
	if !isHibernated && pod.Status.Phase == corev1.PodRunning {
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				isReady = true
				break
			}
		}
	}

	return WorkloadInfo{
		Key:          WorkloadKey{Namespace: pod.Namespace, Name: serviceName},
		ServiceName:  serviceName,
		Dependencies: ParseDependencies(pod.Namespace, pod.Annotations),
		IsReady:      isReady,
		IsHibernate:  isHibernated,
		Labels:       pod.Labels,
		Annotations:  pod.Annotations,
	}
}
