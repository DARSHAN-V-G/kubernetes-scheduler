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
	"k8s.io/apimachinery/pkg/types"
)

// CheckpointRecordWriter abstracts committing CheckpointRecord CRDs to Kubernetes.
type CheckpointRecordWriter interface {
	Create(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error)
	Delete(ctx context.Context, namespace, name string) error
}

type CheckpointStateStore interface {
	FindCheckpointed(ctx context.Context, namespace, podName string, podUID types.UID) (*v1alpha1.CheckpointRecord, error)
}

// ActionManager orchestrates execution of Full Reclaim, Soft Reclaim, and Keep decisions.
type ActionManager struct {
	kubeletClient KubeletClient
	validator     *CheckpointValidator
	evictor       PodEvictor
	softReclaimer *SoftReclaimer
	recordWriter  CheckpointRecordWriter
	graceful      *GracefulReclaimer
	logger        *zap.Logger
}

// SetGracefulReclaimer configures the fallback used by annotated network services.
func (m *ActionManager) SetGracefulReclaimer(reclaimer *GracefulReclaimer) {
	m.graceful = reclaimer
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

	if req.Decision.Action != decision.ActionKeep && req.Pod.Phase != "" && req.Pod.Phase != corev1.PodRunning {
		if stateStore, ok := m.recordWriter.(CheckpointStateStore); ok {
			record, err := stateStore.FindCheckpointed(ctx, req.Pod.Namespace, req.Pod.Name, req.Pod.UID)
			if err != nil {
				return m.failResult(result, start, fmt.Sprintf("lookup checkpoint state: %v", err))
			}
			if record != nil {
				result.Success = true
				result.CheckpointRecordName = record.Name
				result.CheckpointPath = record.Spec.CheckpointPath
				result.CheckpointSizeBytes = record.Spec.CheckpointSizeBytes
				result.Message = fmt.Sprintf("already checkpointed: %s", record.Spec.CheckpointPath)
				result.Duration = time.Since(start)
				return result, nil
			}
		}
		return m.failResult(result, start, fmt.Sprintf("pod is %s; restartPolicy=%s; containerExitCode=%d", req.Pod.Phase, req.Pod.RestartPolicy, req.Pod.ContainerExitCode))
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
	if m.kubeletClient == nil {
		return m.failResult(result, start, "full reclaim is unavailable: kubelet client is not configured")
	}
	if m.validator == nil {
		return m.failResult(result, start, "full reclaim is unavailable: checkpoint validator is not configured")
	}
	if m.evictor == nil {
		return m.failResult(result, start, "full reclaim is unavailable: pod evictor is not configured")
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
		if pod.Annotations["reclaim.io/graceful-redeploy"] == "true" && m.graceful != nil {
			m.logger.Warn("CRIU checkpoint failed; falling back to graceful network-service redeploy", zap.String("pod", pod.Name), zap.Error(err))
			return m.graceful.Reclaim(ctx, pod)
		}
		m.logger.Error("Full reclaim aborted: Kubelet checkpoint API failed; pod will NOT be evicted",
			zap.String("pod", pod.Name),
			zap.Error(err),
		)
		result.Error = fmt.Sprintf("checkpoint failed: %v", err)
		result.Duration = time.Since(start)
		return result, fmt.Errorf("kubelet checkpoint failed: %w", err)
	}

	// Step 2: Determine archive path and validate archive integrity
	archivePath := fmt.Sprintf("%s/checkpoint-%s_%s-%s.tar", CheckpointDirectory(), pod.Namespace, pod.Name, containerName)
	artifact := resp.Artifact()
	if artifact.Path != "" {
		archivePath = artifact.Path
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
	if artifact.SizeBytes > 0 && artifact.SizeBytes != archiveMeta.SizeBytes {
		err := fmt.Errorf("checkpoint archive size mismatch: kubelet reported %d bytes, found %d bytes", artifact.SizeBytes, archiveMeta.SizeBytes)
		return m.failResult(result, start, err.Error())
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
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.GroupVersion.String(),
			Kind:       "CheckpointRecord",
		},
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
			PodSpecSnapshot:     pod.PodSpecSnapshot,
		},
		Status: v1alpha1.CheckpointRecordStatus{
			Phase:   v1alpha1.CheckpointPhaseCheckpointed,
			Message: "Container state captured and verified; source pod lifecycle pending",
		},
	}

	if m.recordWriter == nil {
		return m.failResult(result, start, "checkpoint captured but cannot be evicted: record writer is not configured")
	}
	if _, err := m.recordWriter.Create(ctx, record); err != nil {
		m.logger.Error("Full reclaim aborted: failed writing CheckpointRecord CRD; pod will NOT be evicted", zap.Error(err))
		return m.failResult(result, start, fmt.Sprintf("checkpoint captured but record persistence failed: %v", err))
	}
	result.CheckpointRecordName = ckptRecordName

	if pod.Replicas != nil && pod.Replicas.OwnerKind != "" {
		result.Success = true
		result.Message = fmt.Sprintf("checkpointed; lifecycle release deferred for owner %s", pod.Replicas.OwnerKind)
		result.Duration = time.Since(start)
		return result, nil
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

func (m *ActionManager) failResult(result *ActionResult, start time.Time, message string) (*ActionResult, error) {
	result.Error = message
	result.Duration = time.Since(start)
	return result, fmt.Errorf("%s", message)
}

func (m *ActionManager) executeSoftReclaim(ctx context.Context, req ActionRequest, result *ActionResult, start time.Time) (*ActionResult, error) {
	if m.softReclaimer == nil {
		return m.failResult(result, start, "soft reclaim is unavailable: reclaimer is not configured")
	}
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
