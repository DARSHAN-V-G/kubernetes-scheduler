package action

import (
	"context"
	"fmt"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CheckpointRecordWriter abstracts committing CheckpointRecord CRDs to Kubernetes.
type CheckpointRecordWriter interface {
	Create(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error)
}

// ActionManager orchestrates execution of Full Reclaim, Soft Reclaim, and Keep decisions.
type ActionManager struct {
	kubeletClient KubeletClient
	validator     *CheckpointValidator
	evictor       PodEvictor
	softReclaimer *SoftReclaimer
	recordWriter  CheckpointRecordWriter
	logger        *zap.Logger
}

// NewActionManager creates a fully configured ActionManager instance.
func NewActionManager(
	kubelet KubeletClient,
	validator *CheckpointValidator,
	evictor PodEvictor,
	softReclaimer *SoftReclaimer,
	recordWriter CheckpointRecordWriter,
	logger *zap.Logger,
) *ActionManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ActionManager{
		kubeletClient: kubelet,
		validator:     validator,
		evictor:       evictor,
		softReclaimer: softReclaimer,
		recordWriter:  recordWriter,
		logger:        logger,
	}
}

// Execute handles an ActionRequest and executes the corresponding reclamation action.
func (m *ActionManager) Execute(ctx context.Context, req ActionRequest) (*ActionResult, error) {
	start := time.Now()

	if req.Pod == nil {
		return nil, fmt.Errorf("ActionRequest.Pod cannot be nil")
	}

	result := &ActionResult{
		PodNamespace: req.Pod.Namespace,
		PodName:      req.Pod.Name,
		Action:       req.Decision.Action,
		ExecutedAt:   start,
	}

	switch req.Decision.Action {
	case decision.ActionKeep:
		result.Success = true
		result.Message = "Workload retained according to policy (ActionKeep)"
		result.Duration = time.Since(start)
		return result, nil

	case decision.ActionFullReclaim:
		return m.executeFullReclaim(ctx, req, result, start)

	case decision.ActionSoftReclaim:
		return m.executeSoftReclaim(ctx, req, result, start)

	default:
		return nil, fmt.Errorf("unknown decision action: %v", req.Decision.Action)
	}
}

func (m *ActionManager) executeFullReclaim(ctx context.Context, req ActionRequest, result *ActionResult, start time.Time) (*ActionResult, error) {
	pod := req.Pod
	containerName := req.ContainerName

	// If containerName not specified, pick the first container
	if containerName == "" {
		for cName := range pod.Containers {
			containerName = cName
			break
		}
	}

	if containerName == "" {
		err := fmt.Errorf("pod %s/%s contains no containers to checkpoint", pod.Namespace, pod.Name)
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result, err
	}

	cMetric := pod.Containers[containerName]

	m.logger.Info("Starting FULL_RECLAIM workflow",
		zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
		zap.String("container", containerName),
		zap.String("node", pod.NodeName),
	)

	// Step 1: Trigger Kubelet checkpoint
	resp, err := m.kubeletClient.Checkpoint(ctx, pod.NodeName, pod.Namespace, pod.Name, containerName)
	if err != nil {
		m.logger.Error("Full reclaim aborted: Kubelet checkpoint API failed; pod will NOT be evicted",
			zap.String("pod", pod.Name),
			zap.Error(err),
		)
		result.Error = fmt.Sprintf("checkpoint failed: %v", err)
		result.Duration = time.Since(start)
		return result, fmt.Errorf("kubelet checkpoint failed: %w", err)
	}

	// Step 2: Determine archive path and validate archive integrity
	archivePath := fmt.Sprintf("/var/lib/kubelet/checkpoints/checkpoint-%s_%s-%s.tar", pod.Namespace, pod.Name, containerName)
	if resp != nil && len(resp.Items) > 0 {
		archivePath = resp.Items[0]
	}

	archiveMeta, err := m.validator.ValidateArchive(archivePath)
	if err != nil {
		m.logger.Error("Full reclaim aborted: Archive verification failed; pod will NOT be evicted",
			zap.String("path", archivePath),
			zap.Error(err),
		)
		result.Error = fmt.Sprintf("archive verification failed: %v", err)
		result.Duration = time.Since(start)
		return result, fmt.Errorf("archive validation failed: %w", err)
	}

	result.CheckpointPath = archiveMeta.Path
	result.CheckpointSizeBytes = archiveMeta.SizeBytes
	result.ChecksumSHA256 = archiveMeta.ChecksumSHA

	// Step 3: Create CheckpointRecord CRD
	ckptRecordName := fmt.Sprintf("ckpt-%s-%d", pod.Name, time.Now().Unix())
	if len(ckptRecordName) > 63 {
		ckptRecordName = ckptRecordName[:63]
	}

	var imageURI string
	if cMetric != nil {
		imageURI = cMetric.Image
	}

	origRequests := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(pod.TotalRequestedCPUMillis, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(pod.TotalRequestedMemory, resource.BinarySI),
	}
	origLimits := corev1.ResourceList{
		corev1.ResourceCPU:    *resource.NewMilliQuantity(pod.TotalLimitCPUMillis, resource.DecimalSI),
		corev1.ResourceMemory: *resource.NewQuantity(pod.TotalLimitMemory, resource.BinarySI),
	}

	record := &v1alpha1.CheckpointRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ckptRecordName,
			Namespace: pod.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "adaptive-scheduler",
				"reclaim.io/source-pod":     pod.Name,
				"reclaim.io/source-node":    pod.NodeName,
			},
		},
		Spec: v1alpha1.CheckpointRecordSpec{
			SourcePodName:       pod.Name,
			SourcePodUID:        string(pod.UID),
			NodeName:            pod.NodeName,
			ContainerName:       containerName,
			ImageURI:            imageURI,
			CheckpointPath:      archiveMeta.Path,
			CheckpointSizeBytes: archiveMeta.SizeBytes,
			ChecksumSHA256:      archiveMeta.ChecksumSHA,
			CapturedAt:          metav1.Now(),
			OriginalRequests:    origRequests,
			OriginalLimits:      origLimits,
		},
		Status: v1alpha1.CheckpointRecordStatus{
			Phase:   v1alpha1.CheckpointPhaseReady,
			Message: "Container state captured and verified",
		},
	}

	if m.recordWriter != nil {
		if _, err := m.recordWriter.Create(ctx, record); err != nil {
			m.logger.Warn("Failed writing CheckpointRecord CRD", zap.Error(err))
		} else {
			result.CheckpointRecordName = ckptRecordName
		}
	}

	// Step 4: Evict the pod to release physical & declarative quota
	if err := m.evictor.Evict(ctx, pod.Namespace, pod.Name); err != nil {
		m.logger.Error("Checkpoint captured but pod eviction failed", zap.Error(err))
		result.Error = fmt.Sprintf("checkpoint succeeded but eviction failed: %v", err)
		result.Duration = time.Since(start)
		return result, fmt.Errorf("eviction failed: %w", err)
	}

	result.Success = true
	result.FreedCPUMillicores = float64(pod.TotalRequestedCPUMillis)
	result.FreedMemoryBytes = pod.TotalRequestedMemory
	result.Message = fmt.Sprintf("Pod successfully checkpointed and evicted; freed %.0fm CPU and %d bytes RAM",
		result.FreedCPUMillicores, result.FreedMemoryBytes)
	result.Duration = time.Since(start)

	m.logger.Info("FULL_RECLAIM workflow completed successfully",
		zap.String("pod", pod.Name),
		zap.String("checkpointRecord", ckptRecordName),
		zap.Float64("freedCpuMillis", result.FreedCPUMillicores),
		zap.Int64("freedMemBytes", result.FreedMemoryBytes),
	)

	return result, nil
}

func (m *ActionManager) executeSoftReclaim(ctx context.Context, req ActionRequest, result *ActionResult, start time.Time) (*ActionResult, error) {
	pod := req.Pod
	containerName := req.ContainerName

	if containerName == "" {
		for cName := range pod.Containers {
			containerName = cName
			break
		}
	}

	cMetric := pod.Containers[containerName]
	if cMetric == nil {
		err := fmt.Errorf("container %s not found in pod %s", containerName, pod.Name)
		result.Error = err.Error()
		result.Duration = time.Since(start)
		return result, err
	}

	m.logger.Info("Starting SOFT_RECLAIM workflow",
		zap.String("pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)),
		zap.String("container", containerName),
	)

	// Step 1: Calculate conservative right-sizing
	rightSize := m.softReclaimer.CalculateRightSizing(cMetric, 1.20)

	// Step 2: Apply in-place resize patch
	if err := m.softReclaimer.ApplyInPlaceResize(ctx, pod.Namespace, pod.Name, containerName, rightSize); err != nil {
		result.Error = fmt.Sprintf("in-place resize failed: %v", err)
		result.Duration = time.Since(start)
		return result, err
	}

	result.Success = true
	result.FreedCPUMillicores = float64(rightSize.FreedCPUMillicores)
	result.FreedMemoryBytes = rightSize.FreedMemoryBytes
	result.Message = fmt.Sprintf("Pod successfully right-sized in-place; freed %dm CPU and %d bytes RAM",
		rightSize.FreedCPUMillicores, rightSize.FreedMemoryBytes)
	result.Duration = time.Since(start)

	return result, nil
}
