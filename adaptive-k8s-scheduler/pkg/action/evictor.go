package action

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodEvictor defines the interface for evicting pods via the Kubernetes Eviction API.
type PodEvictor interface {
	Evict(ctx context.Context, namespace, podName string) error
}

// K8sPodEvictor evicts pods gracefully using policyv1.Eviction while respecting PodDisruptionBudgets.
type K8sPodEvictor struct {
	client kubernetes.Interface
	logger *zap.Logger
}

// NewK8sPodEvictor creates a new Kubernetes pod evictor.
func NewK8sPodEvictor(client kubernetes.Interface, logger *zap.Logger) *K8sPodEvictor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &K8sPodEvictor{
		client: client,
		logger: logger,
	}
}

// Evict initiates graceful eviction of the specified pod.
func (e *K8sPodEvictor) Evict(ctx context.Context, namespace, podName string) error {
	if namespace == "" || podName == "" {
		return fmt.Errorf("namespace and podName must be non-empty")
	}

	e.logger.Info("Initiating pod eviction via Kubernetes Eviction API",
		zap.String("namespace", namespace),
		zap.String("pod", podName),
	)

	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
		DeleteOptions: &metav1.DeleteOptions{
			GracePeriodSeconds: new(int64), // 0 or default graceful termination
		},
	}
	*eviction.DeleteOptions.GracePeriodSeconds = 30

	err := e.client.PolicyV1().Evictions(namespace).Evict(ctx, eviction)
	if err != nil {
		if apierrors.IsTooManyRequests(err) {
			return fmt.Errorf("eviction rejected by PodDisruptionBudget (429 TooManyRequests): %w", err)
		}
		if apierrors.IsNotFound(err) {
			e.logger.Warn("Pod already deleted during eviction attempt", zap.String("pod", podName))
			return nil
		}
		return fmt.Errorf("failed to evict pod %s/%s: %w", namespace, podName, err)
	}

	e.logger.Info("Pod eviction successfully submitted",
		zap.String("namespace", namespace),
		zap.String("pod", podName),
		zap.Time("submittedAt", time.Now()),
	)
	return nil
}
