package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"simulator/backend/models"
)

// Discoverer interacts with the live Kubernetes cluster to list nodes and active workloads.
type Discoverer struct {
	client     kubernetes.Interface
	restConfig *rest.Config
}

// NewDiscoverer initializes a Kubernetes client from kubeconfig or in-cluster credentials.
func NewDiscoverer(kubeconfigPath string) (*Discoverer, error) {
	restConfig, err := buildKubeConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	return &Discoverer{
		client:     clientset,
		restConfig: restConfig,
	}, nil
}

// Client returns the underlying kubernetes.Interface.
func (d *Discoverer) Client() kubernetes.Interface {
	return d.client
}

// RESTConfig returns the underlying *rest.Config.
func (d *Discoverer) RESTConfig() *rest.Config {
	return d.restConfig
}

// DiscoverNodes retrieves all live Kubernetes nodes and maps them to models.SyntheticNode.
func (d *Discoverer) DiscoverNodes(ctx context.Context) ([]models.SyntheticNode, error) {
	nodeList, err := d.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	nodes := make([]models.SyntheticNode, 0, len(nodeList.Items))
	for _, n := range nodeList.Items {
		var isReady bool
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				isReady = true
				break
			}
		}

		cpuCap := n.Status.Capacity.Cpu().MilliValue()
		memCap := n.Status.Capacity.Memory().Value()
		cpuAlloc := n.Status.Allocatable.Cpu().MilliValue()
		memAlloc := n.Status.Allocatable.Memory().Value()

		nodes = append(nodes, models.SyntheticNode{
			Name:                     n.Name,
			TotalCapacityCPUMillis:   cpuCap,
			TotalCapacityMemoryBytes: memCap,
			AllocatableCPUMillis:     cpuAlloc,
			AllocatableMemoryBytes:   memAlloc,
			ActualUsageCPUMillicores: 0,
			ActualUsageMemoryBytes:   0,
			IsReady:                  isReady,
		})
	}
	return nodes, nil
}

// DiscoverWorkloads retrieves pods across target namespaces and maps them into models.SyntheticWorkload structures.
func (d *Discoverer) DiscoverWorkloads(ctx context.Context, namespaceFilter string) ([]models.SyntheticWorkload, error) {
	podList, err := d.client.CoreV1().Pods(namespaceFilter).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	ownerPodCount := make(map[string]int32)
	for _, p := range podList.Items {
		if p.Status.Phase == corev1.PodRunning {
			k, n := resolveOwner(&p)
			if k != "Pod" {
				ownerPodCount[fmt.Sprintf("%s/%s/%s", p.Namespace, k, n)]++
			}
		}
	}

	workloads := make([]models.SyntheticWorkload, 0, len(podList.Items))
	for _, p := range podList.Items {
		if namespaceFilter == "" && isSystemNamespace(p.Namespace) {
			continue
		}

		var reqCPU, limCPU int64
		var reqMem, limMem int64
		for _, c := range p.Spec.Containers {
			reqCPU += c.Resources.Requests.Cpu().MilliValue()
			limCPU += c.Resources.Limits.Cpu().MilliValue()
			reqMem += c.Resources.Requests.Memory().Value()
			limMem += c.Resources.Limits.Memory().Value()
		}

		ownerKind, ownerName := resolveOwner(&p)
		priority := int32(0)
		if p.Spec.Priority != nil {
			priority = *p.Spec.Priority
		}

		replicas := int32(1)
		if count, ok := ownerPodCount[fmt.Sprintf("%s/%s/%s", p.Namespace, ownerKind, ownerName)]; ok && count > 1 {
			replicas = count
		}

		sw := models.SyntheticWorkload{
			Name:                 p.Name,
			Namespace:            p.Namespace,
			NodeName:             p.Spec.NodeName,
			Phase:                string(p.Status.Phase),
			QoSClass:             string(p.Status.QOSClass),
			Priority:             priority,
			PriorityClassName:    p.Spec.PriorityClassName,
			DisruptionsAllowed:   -1,
			OwnerKind:            ownerKind,
			OwnerName:            ownerName,
			DesiredReplicas:      replicas,
			ReadyReplicas:        replicas,
			AvailableReplicas:    replicas,
			RequestedCPUMillis:   reqCPU,
			LimitCPUMillis:       limCPU,
			RequestedMemoryBytes: reqMem,
			LimitMemoryBytes:     limMem,
			UsageCPUMillicores:   0,
			UsageMemoryBytes:     0,
			NetworkBytesPerSec:   0,
			RequestQPS:           0,
			IdleDurationSeconds:  0,
			IsIdle:               false,
			Labels:               p.Labels,
			Annotations:          p.Annotations,
		}

		workloads = append(workloads, sw)
	}

	return workloads, nil
}

func resolveOwner(pod *corev1.Pod) (string, string) {
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			kind := ref.Kind
			name := ref.Name
			return kind, name
		}
	}
	return "Pod", pod.Name
}

func isSystemNamespace(ns string) bool {
	switch ns {
	case "kube-system", "kube-public", "kube-node-lease", "local-path-storage":
		return true
	default:
		return false
	}
}

func buildKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}
	if inCluster, err := rest.InClusterConfig(); err == nil {
		return inCluster, nil
	}
	if envPath := os.Getenv("KUBECONFIG"); envPath != "" {
		return clientcmd.BuildConfigFromFlags("", envPath)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		defaultPath := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(defaultPath); err == nil {
			return clientcmd.BuildConfigFromFlags("", defaultPath)
		}
	}
	return nil, fmt.Errorf("unable to locate valid kubeconfig")
}

// GetClusterSummary returns quick cluster version and health metadata.
func (d *Discoverer) GetClusterSummary(ctx context.Context) (map[string]interface{}, error) {
	info, err := d.client.Discovery().ServerVersion()
	version := "unknown"
	if err == nil {
		version = info.GitVersion
	}

	nodes, err := d.DiscoverNodes(ctx)
	if err != nil {
		return nil, err
	}

	readyNodes := 0
	for _, n := range nodes {
		if n.IsReady {
			readyNodes++
		}
	}

	return map[string]interface{}{
		"status":        "CONNECTED",
		"serverVersion": version,
		"totalNodes":    len(nodes),
		"readyNodes":    readyNodes,
		"queriedAt":     time.Now().UTC().Format(time.RFC3339),
		"hostEndpoint":  strings.TrimPrefix(d.restConfig.Host, "https://"),
	}, nil
}
