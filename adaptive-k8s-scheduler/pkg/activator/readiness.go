package activator

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ReadinessChecker abstracts verifying Kubernetes Service and workload readiness.
type ReadinessChecker interface {
	IsServiceReady(ctx context.Context, namespace, serviceName string) (bool, error)
	WaitUntilServiceReady(ctx context.Context, namespace, serviceName string, timeout time.Duration) error
	GetServiceTargetURL(namespace, serviceName string, defaultPort int) string
}

// K8sServiceReadinessChecker implements ReadinessChecker against live Kubernetes APIs.
type K8sServiceReadinessChecker struct {
	client    kubernetes.Interface
	logger    *zap.Logger
	targetEnv string // "cluster" (default) or "local" (for simulator/testing)
}

func NewK8sServiceReadinessChecker(client kubernetes.Interface, logger *zap.Logger) *K8sServiceReadinessChecker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &K8sServiceReadinessChecker{
		client:    client,
		logger:    logger,
		targetEnv: "cluster",
	}
}

func (k *K8sServiceReadinessChecker) SetTargetEnv(env string) {
	k.targetEnv = env
}

// IsServiceReady checks if the Kubernetes Service has at least one active, ready endpoint.
func (k *K8sServiceReadinessChecker) IsServiceReady(ctx context.Context, namespace, serviceName string) (bool, error) {
	if k.client == nil {
		return false, fmt.Errorf("kubernetes client is nil")
	}

	// 1. Primary check: discovery.k8s.io/v1 EndpointSlices
	labelSelector := fmt.Sprintf("kubernetes.io/service-name=%s", serviceName)
	slices, err := k.client.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err == nil && len(slices.Items) > 0 {
		for _, slice := range slices.Items {
			for _, ep := range slice.Endpoints {
				if ep.Conditions.Ready != nil && *ep.Conditions.Ready {
					return true, nil
				}
			}
		}
		return false, nil
	}

	// 2. Secondary check: corev1 Endpoints
	endpoints, err := k.client.CoreV1().Endpoints(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err == nil {
		for _, subset := range endpoints.Subsets {
			if len(subset.Addresses) > 0 {
				return true, nil
			}
		}
		return false, nil
	}

	// 3. Fallback: check matching running pods with app label
	pods, err := k.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=%s", serviceName),
	})
	if err == nil && len(pods.Items) > 0 {
		for _, pod := range pods.Items {
			if pod.Status.Phase == corev1.PodRunning {
				for _, cond := range pod.Status.Conditions {
					if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
						return true, nil
					}
				}
			}
		}
	}

	return false, nil
}

// WaitUntilServiceReady polls until the Service's endpoints become ready or timeout is reached.
func (k *K8sServiceReadinessChecker) WaitUntilServiceReady(ctx context.Context, namespace, serviceName string, timeout time.Duration) error {
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctxWithTimeout.Done():
			return fmt.Errorf("timeout waiting for service %s/%s readiness: %w", namespace, serviceName, ctxWithTimeout.Err())
		case <-ticker.C:
			ready, err := k.IsServiceReady(ctxWithTimeout, namespace, serviceName)
			if err != nil {
				k.logger.Debug("Checking service readiness encountered error, retrying", zap.Error(err))
				continue
			}
			if ready {
				k.logger.Info("Service readiness confirmed on EndpointSlice",
					zap.String("namespace", namespace),
					zap.String("service", serviceName),
				)
				return nil
			}
		}
	}
}

// GetServiceTargetURL returns the network address to reach the service.
func (k *K8sServiceReadinessChecker) GetServiceTargetURL(namespace, serviceName string, defaultPort int) string {
	if k.targetEnv == "local" {
		return fmt.Sprintf("http://127.0.0.1:%d", defaultPort)
	}
	// Kubernetes in-cluster Service DNS
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, namespace, defaultPort)
}
