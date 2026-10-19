package action

import (
	"context"
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"go.uber.org/zap"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// GracefulReclaimer replaces a network service without attempting a CRIU dump.
// The resulting Ready record is consumed by the Activator on the next request.
type GracefulReclaimer struct {
	client       kubernetes.Interface
	recordWriter CheckpointRecordWriter
	logger       *zap.Logger
}

func NewGracefulReclaimer(client kubernetes.Interface, recordWriter CheckpointRecordWriter, logger *zap.Logger) *GracefulReclaimer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GracefulReclaimer{client: client, recordWriter: recordWriter, logger: logger}
}

func (g *GracefulReclaimer) Reclaim(ctx context.Context, pod *metrics.PodMetrics) (*ActionResult, error) {
	if g == nil || g.client == nil || g.recordWriter == nil {
		return nil, fmt.Errorf("graceful redeploy is not configured")
	}
	if pod.Replicas == nil || pod.Replicas.OwnerKind != "Deployment" || pod.Replicas.OwnerName == "" {
		return nil, fmt.Errorf("graceful redeploy requires a Deployment owner")
	}

	recordName := fmt.Sprintf("graceful-%s-%d", pod.Name, time.Now().Unix())
	if len(recordName) > 63 {
		recordName = recordName[:63]
	}
	record := &v1alpha1.CheckpointRecord{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "CheckpointRecord"},
		ObjectMeta: metav1.ObjectMeta{Name: recordName, Namespace: pod.Namespace},
		Spec: v1alpha1.CheckpointRecordSpec{
			SourcePodName:     pod.Name,
			SourcePodUID:      string(pod.UID),
			NodeName:          pod.NodeName,
			OwnerKind:         pod.Replicas.OwnerKind,
			OwnerName:         pod.Replicas.OwnerName,
			ContainerName:     firstContainer(pod),
			ImageURI:          firstImage(pod),
			CheckpointPath:    fmt.Sprintf("graceful://%s/%s", pod.Namespace, pod.Name),
			CapturedAt:        metav1.Now(),
			OriginalRequests:  resourceListCPUAndMemory(pod.TotalRequestedCPUMillis, pod.TotalRequestedMemory),
			OriginalLimits:    resourceListCPUAndMemory(pod.TotalLimitCPUMillis, pod.TotalLimitMemory),
			PodSpecSnapshot:   pod.PodSpecSnapshot,
			PodLabelsSnapshot: pod.Labels,
		},
		Status: v1alpha1.CheckpointRecordStatus{Phase: v1alpha1.CheckpointPhaseReady, Message: "Network service saved for graceful redeployment"},
	}
	if _, err := g.recordWriter.Create(ctx, record); err != nil {
		return nil, fmt.Errorf("create graceful redeploy record: %w", err)
	}

	dep, err := g.client.AppsV1().Deployments(pod.Namespace).Get(ctx, pod.Replicas.OwnerName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get Deployment %s/%s: %w", pod.Namespace, pod.Replicas.OwnerName, err)
	}
	if dep.Spec.Replicas != nil && *dep.Spec.Replicas > 1 {
		return nil, fmt.Errorf("graceful redeploy only supports single-replica Deployments (current replicas: %d)", *dep.Spec.Replicas)
	}

	zero := int32(0)
	if _, err := g.client.AppsV1().Deployments(pod.Namespace).UpdateScale(ctx, pod.Replicas.OwnerName, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: pod.Replicas.OwnerName, Namespace: pod.Namespace}, Spec: autoscalingv1.ScaleSpec{Replicas: zero}}, metav1.UpdateOptions{}); err != nil {
		return nil, fmt.Errorf("scale Deployment %s/%s to zero: %w", pod.Namespace, pod.Replicas.OwnerName, err)
	}

	g.logger.Info("Network service gracefully reclaimed", zap.String("pod", pod.Namespace+"/"+pod.Name), zap.String("record", recordName))
	return &ActionResult{
		PodNamespace: pod.Namespace, PodName: pod.Name, Action: decision.ActionFullReclaim, Success: true,
		CheckpointRecordName: recordName, CheckpointPath: record.Spec.CheckpointPath,
		FreedCPUMillicores: float64(pod.TotalRequestedCPUMillis), FreedMemoryBytes: pod.TotalRequestedMemory,
		ExecutedAt: time.Now(), Message: "Network service scaled to zero; Activator will redeploy it on request",
	}, nil
}

func firstContainer(pod *metrics.PodMetrics) string {
	for name := range pod.Containers {
		return name
	}
	return ""
}

func firstImage(pod *metrics.PodMetrics) string {
	for _, container := range pod.Containers {
		return container.Image
	}
	return ""
}

func resourceListCPUAndMemory(cpuMillis, memoryBytes int64) corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMillis, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(memoryBytes, resource.BinarySI),
	}
}
