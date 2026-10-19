package action

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/storage"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// Mock Kubelet Client
type mockKubeletClient struct {
	checkpointFn func(ctx context.Context, nodeName, namespace, pod, container string) (*CheckpointResponse, error)
}

func (m *mockKubeletClient) Checkpoint(ctx context.Context, nodeName, namespace, pod, container string) (*CheckpointResponse, error) {
	if m.checkpointFn != nil {
		return m.checkpointFn(ctx, nodeName, namespace, pod, container)
	}
	return &CheckpointResponse{Items: []string{"/tmp/test-checkpoint.tar"}}, nil
}

// Mock Storage
type mockStorage struct {
	storage.CheckpointStorage
	existsFn func(path string) (bool, error)
	verifyFn func(path string) (*storage.ArchiveMetadata, error)
}

func (m *mockStorage) Exists(path string) (bool, error) {
	if m.existsFn != nil {
		return m.existsFn(path)
	}
	return true, nil
}

func (m *mockStorage) VerifyArchive(path string) (*storage.ArchiveMetadata, error) {
	if m.verifyFn != nil {
		return m.verifyFn(path)
	}
	return &storage.ArchiveMetadata{
		Path:        path,
		SizeBytes:   10240,
		ChecksumSHA: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		FileCount:   5,
		CreatedAt:   time.Now(),
	}, nil
}

// Mock Evictor
type mockEvictor struct {
	evictCalls int
	evictFn    func(ctx context.Context, namespace, podName string) error
}

func (m *mockEvictor) Evict(ctx context.Context, namespace, podName string) error {
	m.evictCalls++
	if m.evictFn != nil {
		return m.evictFn(ctx, namespace, podName)
	}
	return nil
}

// Mock CheckpointRecordWriter
type mockRecordWriter struct {
	savedRecord *v1alpha1.CheckpointRecord
}

func (m *mockRecordWriter) Create(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error) {
	m.savedRecord = record
	return record, nil
}

func (m *mockRecordWriter) Delete(ctx context.Context, namespace, name string) error {
	return nil
}

func createTestPodMetrics() *metrics.PodMetrics {
	return &metrics.PodMetrics{
		Namespace:               "ecommerce",
		Name:                    "analytics-worker-xyz",
		NodeName:                "kind-worker-1",
		TotalRequestedCPUMillis: 1000,
		TotalLimitCPUMillis:     2000,
		TotalRequestedMemory:    1024 * 1024 * 1024, // 1 GiB
		TotalLimitMemory:        2048 * 1024 * 1024,
		Containers: map[string]*metrics.ContainerMetrics{
			"worker": {
				Name:                 "worker",
				Image:                "worker:latest",
				RequestedCPUMillis:   1000,
				RequestedMemoryBytes: 1024 * 1024 * 1024,
				UsageCPUMillicores:   50.0,
				UsageMemoryBytes:     100 * 1024 * 1024,
			},
		},
	}
}

func TestExecuteActionKeep(t *testing.T) {
	mgr := NewActionManager(nil, nil, nil, nil, nil, nil)
	req := ActionRequest{
		Pod: createTestPodMetrics(),
		Decision: decision.DecisionResult{
			Action: decision.ActionKeep,
		},
	}

	res, err := mgr.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Unexpected error for ActionKeep: %v", err)
	}
	if !res.Success {
		t.Errorf("Expected success=true for ActionKeep")
	}
}

func TestExecuteFullReclaimSuccess(t *testing.T) {
	kubelet := &mockKubeletClient{
		checkpointFn: func(ctx context.Context, node, ns, pod, container string) (*CheckpointResponse, error) {
			return &CheckpointResponse{Items: []string{"/var/lib/kubelet/checkpoints/test.tar"}}, nil
		},
	}
	store := &mockStorage{}
	validator := NewCheckpointValidator(store, nil)
	evictor := &mockEvictor{}
	writer := &mockRecordWriter{}

	mgr := NewActionManager(kubelet, validator, evictor, nil, writer, nil)

	pod := createTestPodMetrics()
	pod.PodSpecSnapshot = `{"containers":[{"name":"worker","image":"worker:latest"}]}`
	req := ActionRequest{
		Pod: pod,
		Decision: decision.DecisionResult{
			Action: decision.ActionFullReclaim,
		},
		ContainerName: "worker",
	}

	res, err := mgr.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute FullReclaim failed: %v", err)
	}

	if !res.Success {
		t.Errorf("Expected res.Success=true")
	}
	if evictor.evictCalls != 1 {
		t.Errorf("Expected 1 eviction call, got %d", evictor.evictCalls)
	}
	if res.ChecksumSHA256 == "" {
		t.Errorf("Expected non-empty checksum")
	}
	if res.FreedCPUMillicores != 1000 {
		t.Errorf("Expected freed CPU 1000, got %f", res.FreedCPUMillicores)
	}
	if writer.savedRecord == nil {
		t.Errorf("Expected CheckpointRecord to be saved")
	} else if writer.savedRecord.Spec.SourcePodName != "analytics-worker-xyz" {
		t.Errorf("Saved record has incorrect pod name: %s", writer.savedRecord.Spec.SourcePodName)
	} else if writer.savedRecord.Spec.PodSpecSnapshot != pod.PodSpecSnapshot {
		t.Errorf("Saved record did not preserve pod spec snapshot")
	}
}

func TestExecuteFullReclaimRecordPersistenceFailureDoesNotEvict(t *testing.T) {
	kubelet := &mockKubeletClient{}
	validator := NewCheckpointValidator(&mockStorage{}, nil)
	evictor := &mockEvictor{}
	writer := &failingRecordWriter{}

	mgr := NewActionManager(kubelet, validator, evictor, nil, writer, nil)
	res, err := mgr.Execute(context.Background(), ActionRequest{
		Pod: createTestPodMetrics(),
		Decision: decision.DecisionResult{
			Action: decision.ActionFullReclaim,
		},
	})
	if err == nil {
		t.Fatal("expected record persistence failure")
	}
	if res.Success {
		t.Fatal("expected failed action result")
	}
	if evictor.evictCalls != 0 {
		t.Fatal("pod was evicted despite record persistence failure")
	}
}

type failingRecordWriter struct{}

func (w *failingRecordWriter) Create(context.Context, *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error) {
	return nil, fmt.Errorf("simulated API failure")
}

func (w *failingRecordWriter) Delete(context.Context, string, string) error {
	return nil
}

type stateAwareRecordWriter struct {
	record *v1alpha1.CheckpointRecord
}

func (w *stateAwareRecordWriter) Create(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error) {
	w.record = record
	return record, nil
}

func (w *stateAwareRecordWriter) Delete(ctx context.Context, namespace, name string) error {
	return nil
}

func (w *stateAwareRecordWriter) FindCheckpointed(ctx context.Context, namespace, podName string, podUID types.UID) (*v1alpha1.CheckpointRecord, error) {
	if w.record == nil || w.record.Namespace != namespace || w.record.Spec.SourcePodName != podName {
		return nil, nil
	}
	return w.record, nil
}

func TestExecuteActionAlreadyCheckpointedPod(t *testing.T) {
	writer := &stateAwareRecordWriter{record: &v1alpha1.CheckpointRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ckpt-analytics-worker-xyz",
			Namespace: "ecommerce",
		},
		Spec: v1alpha1.CheckpointRecordSpec{
			SourcePodName:       "analytics-worker-xyz",
			SourcePodUID:        "uid-123",
			CheckpointPath:      "/var/lib/rancher/k3s/agent/kubelet/checkpoints/test.tar",
			CheckpointSizeBytes: 10240,
		},
		Status: v1alpha1.CheckpointRecordStatus{
			Phase: v1alpha1.CheckpointPhaseCheckpointed,
		},
	}}

	mgr := NewActionManager(nil, nil, nil, nil, writer, nil)
	pod := createTestPodMetrics()
	pod.Phase = "Succeeded"
	pod.UID = "uid-123"

	res, err := mgr.Execute(context.Background(), ActionRequest{
		Pod: pod,
		Decision: decision.DecisionResult{
			Action: decision.ActionFullReclaim,
		},
	})
	if err != nil {
		t.Fatalf("expected non-running checkpointed pod to be treated as already checkpointed: %v", err)
	}
	if !res.Success {
		t.Fatal("expected success when checkpoint record already exists for non-running pod")
	}
	if res.CheckpointRecordName != "ckpt-analytics-worker-xyz" {
		t.Fatalf("expected checkpoint record name to be preserved, got %q", res.CheckpointRecordName)
	}
	if res.Message == "" || res.CheckpointPath == "" {
		t.Fatal("expected checkpoint metadata in action result")
	}
}

func TestExecuteFullReclaimCheckpointFailure(t *testing.T) {
	kubelet := &mockKubeletClient{
		checkpointFn: func(ctx context.Context, node, ns, pod, container string) (*CheckpointResponse, error) {
			return nil, fmt.Errorf("simulated Kubelet 500 internal error")
		},
	}
	store := &mockStorage{}
	validator := NewCheckpointValidator(store, nil)
	evictor := &mockEvictor{}

	mgr := NewActionManager(kubelet, validator, evictor, nil, nil, nil)

	req := ActionRequest{
		Pod: createTestPodMetrics(),
		Decision: decision.DecisionResult{
			Action: decision.ActionFullReclaim,
		},
	}

	res, err := mgr.Execute(context.Background(), req)
	if err == nil {
		t.Fatalf("Expected error when Kubelet checkpoint fails, got nil")
	}

	// CRITICAL SAFETY CHECK: Eviction must NOT be called if checkpoint fails
	if evictor.evictCalls != 0 {
		t.Errorf("Pod was evicted even though checkpoint failed! Safety violation.")
	}
	if res.Success {
		t.Errorf("Expected res.Success=false on failure")
	}
}

func TestExecuteFullReclaimArchiveValidationFailure(t *testing.T) {
	kubelet := &mockKubeletClient{}
	store := &mockStorage{
		verifyFn: func(path string) (*storage.ArchiveMetadata, error) {
			return nil, fmt.Errorf("corrupted tar archive")
		},
	}
	validator := NewCheckpointValidator(store, nil)
	evictor := &mockEvictor{}

	mgr := NewActionManager(kubelet, validator, evictor, nil, nil, nil)

	req := ActionRequest{
		Pod: createTestPodMetrics(),
		Decision: decision.DecisionResult{
			Action: decision.ActionFullReclaim,
		},
	}

	_, err := mgr.Execute(context.Background(), req)
	if err == nil {
		t.Fatalf("Expected error when archive verification fails, got nil")
	}

	// CRITICAL SAFETY CHECK: Eviction must NOT be called if tarball is corrupted
	if evictor.evictCalls != 0 {
		t.Errorf("Pod was evicted despite corrupt archive! Safety violation.")
	}
}

func TestSoftReclaimRightSizing(t *testing.T) {
	cMetric := &metrics.ContainerMetrics{
		RequestedCPUMillis:   1000,
		RequestedMemoryBytes: 1024 * 1024 * 1024,
		UsageCPUMillicores:   100.0,
		UsageMemoryBytes:     200 * 1024 * 1024,
	}

	reclaimer := NewSoftReclaimer(nil, nil)
	rightSize := reclaimer.CalculateRightSizing(cMetric, 1.20)

	// 100 * 1.20 = 120m
	if rightSize.TargetCPUMillicores != 120 {
		t.Errorf("Expected target CPU 120m, got %d", rightSize.TargetCPUMillicores)
	}
	// Freed CPU = 1000 - 120 = 880m
	if rightSize.FreedCPUMillicores != 880 {
		t.Errorf("Expected freed CPU 880m, got %d", rightSize.FreedCPUMillicores)
	}
	// 200MB * 1.20 = 240MB
	expectedMem := int64(240 * 1024 * 1024)
	if rightSize.TargetMemoryBytes != expectedMem {
		t.Errorf("Expected target memory %d, got %d", expectedMem, rightSize.TargetMemoryBytes)
	}
}

func TestRestoreEngineSpecReconstitution(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	restoreEngine := NewRestoreEngine(fakeClient, nil)

	record := &v1alpha1.CheckpointRecord{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ckpt-worker-123",
			Namespace: "ecommerce",
		},
		Spec: v1alpha1.CheckpointRecordSpec{
			SourcePodName:  "worker-pod",
			ContainerName:  "worker",
			ImageURI:       "worker:v1",
			CheckpointPath: "/var/lib/kubelet/checkpoints/worker.tar",
			ChecksumSHA256: "abcd1234efgh5678",
		},
	}

	restoredPod, err := restoreEngine.BuildRestoredPodSpec(record)
	if err != nil {
		t.Fatalf("BuildRestoredPodSpec failed: %v", err)
	}

	if restoredPod.Namespace != "ecommerce" {
		t.Errorf("Expected namespace ecommerce, got %s", restoredPod.Namespace)
	}
	if len(restoredPod.Spec.Containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(restoredPod.Spec.Containers))
	}
	c := restoredPod.Spec.Containers[0]
	if c.Name != "worker" || c.Image != "worker:v1" {
		t.Errorf("Container specs mismatch: %s / %s", c.Name, c.Image)
	}
	if restoredPod.Annotations["reclaim.io/checkpoint-sha256"] != "abcd1234efgh5678" {
		t.Errorf("Missing checkpoint sha annotation on restored pod")
	}
}

func TestGracefulReclaimer_RecordNameLengthAndReplicaValidation(t *testing.T) {
	pod := createTestPodMetrics()
	pod.Name = "a-very-long-pod-name-that-would-exceed-the-limit-if-not-properly-truncated"
	pod.Replicas = &metrics.ReplicaInfo{
		OwnerKind: "Deployment",
		OwnerName: "test-deployment",
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deployment",
			Namespace: "ecommerce",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: func() *int32 { r := int32(2); return &r }(),
		},
	}

	fakeClient := fake.NewSimpleClientset(deployment)
	mockWriter := &mockRecordWriter{}
	reclaimer := NewGracefulReclaimer(fakeClient, mockWriter, nil)

	// Should fail because replicas == 2 (> 1)
	res, err := reclaimer.Reclaim(context.Background(), pod)
	if err == nil {
		t.Fatalf("expected error when replicas > 1, got result: %v", res)
	}

	// Verify recordName was generated correctly (<= 63 chars and starts with graceful-)
	if mockWriter.savedRecord == nil {
		t.Fatalf("expected record to have been created before validation")
	}
	if len(mockWriter.savedRecord.Name) > 63 {
		t.Errorf("record name exceeds 63 characters: %d (%s)", len(mockWriter.savedRecord.Name), mockWriter.savedRecord.Name)
	}
	if !strings.HasPrefix(mockWriter.savedRecord.Name, "graceful-") {
		t.Errorf("record name missing graceful- prefix: %s", mockWriter.savedRecord.Name)
	}

	// Now set replicas to 1 and verify success
	*deployment.Spec.Replicas = 1
	fakeClient = fake.NewSimpleClientset(deployment)
	mockWriter = &mockRecordWriter{}
	reclaimer = NewGracefulReclaimer(fakeClient, mockWriter, nil)

	res, err = reclaimer.Reclaim(context.Background(), pod)
	if err != nil {
		t.Fatalf("expected success with 1 replica, got error: %v", err)
	}
	if !res.Success {
		t.Errorf("expected Success=true in result")
	}
}

