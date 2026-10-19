package action

import (
	"context"
	"fmt"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

var checkpointRecordResource = schema.GroupVersionResource{
	Group:    v1alpha1.GroupVersion.Group,
	Version:  v1alpha1.GroupVersion.Version,
	Resource: "checkpointrecords",
}

// DynamicCheckpointRecordWriter persists CheckpointRecord objects without requiring
// generated client code. It is suitable for the controller's narrow write path.
type DynamicCheckpointRecordWriter struct {
	client dynamic.Interface
}

func NewDynamicCheckpointRecordWriter(client dynamic.Interface) *DynamicCheckpointRecordWriter {
	return &DynamicCheckpointRecordWriter{client: client}
}

func (w *DynamicCheckpointRecordWriter) Create(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error) {
	if w == nil || w.client == nil {
		return nil, fmt.Errorf("checkpoint record dynamic client is not configured")
	}
	if record == nil {
		return nil, fmt.Errorf("checkpoint record cannot be nil")
	}

	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(record)
	if err != nil {
		return nil, fmt.Errorf("convert checkpoint record to unstructured: %w", err)
	}

	created, err := w.client.Resource(checkpointRecordResource).Namespace(record.Namespace).Create(
		ctx,
		&unstructured.Unstructured{Object: content},
		metav1.CreateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("create checkpoint record %s/%s: %w", record.Namespace, record.Name, err)
	}

	result := new(v1alpha1.CheckpointRecord)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(created.Object, result); err != nil {
		return nil, fmt.Errorf("convert created checkpoint record: %w", err)
	}
	result.Status = record.Status
	updated, err := w.updateStatus(ctx, result)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (w *DynamicCheckpointRecordWriter) Get(ctx context.Context, namespace, name string) (*v1alpha1.CheckpointRecord, error) {
	if w == nil || w.client == nil {
		return nil, fmt.Errorf("checkpoint record dynamic client is not configured")
	}
	object, err := w.client.Resource(checkpointRecordResource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get checkpoint record %s/%s: %w", namespace, name, err)
	}

	record := new(v1alpha1.CheckpointRecord)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, record); err != nil {
		return nil, fmt.Errorf("convert checkpoint record: %w", err)
	}
	return record, nil
}

func (w *DynamicCheckpointRecordWriter) ListReady(ctx context.Context, namespace string) ([]*v1alpha1.CheckpointRecord, error) {
	if w == nil || w.client == nil {
		return nil, fmt.Errorf("checkpoint record dynamic client is not configured")
	}
	objects, err := w.client.Resource(checkpointRecordResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list checkpoint records in %s: %w", namespace, err)
	}

	ready := make([]*v1alpha1.CheckpointRecord, 0, len(objects.Items))
	for index := range objects.Items {
		record := new(v1alpha1.CheckpointRecord)
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(objects.Items[index].Object, record); err != nil {
			return nil, fmt.Errorf("convert checkpoint record %s: %w", objects.Items[index].GetName(), err)
		}
		if record.Status.Phase == v1alpha1.CheckpointPhaseCheckpointed || record.Status.Phase == v1alpha1.CheckpointPhaseReady || record.Status.Phase == v1alpha1.CheckpointPhaseRestoring {
			ready = append(ready, record)
		}
	}
	return ready, nil
}

// FindCheckpointed returns a verified checkpoint for a source pod, if one exists.
func (w *DynamicCheckpointRecordWriter) FindCheckpointed(ctx context.Context, namespace, podName string, podUID types.UID) (*v1alpha1.CheckpointRecord, error) {
	if w == nil || w.client == nil {
		return nil, fmt.Errorf("checkpoint record dynamic client is not configured")
	}
	objects, err := w.client.Resource(checkpointRecordResource).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list checkpoint records in %s: %w", namespace, err)
	}
	for index := range objects.Items {
		record := new(v1alpha1.CheckpointRecord)
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(objects.Items[index].Object, record); err != nil {
			return nil, fmt.Errorf("convert checkpoint record %s: %w", objects.Items[index].GetName(), err)
		}
		if record.Status.Phase != v1alpha1.CheckpointPhaseCheckpointed && record.Status.Phase != v1alpha1.CheckpointPhaseReady {
			continue
		}
		if record.Spec.SourcePodName == podName && (podUID == "" || record.Spec.SourcePodUID == string(podUID)) {
			return record, nil
		}
	}
	return nil, nil
}

func (w *DynamicCheckpointRecordWriter) UpdateStatus(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error) {
	if w == nil || w.client == nil {
		return nil, fmt.Errorf("checkpoint record dynamic client is not configured")
	}
	return w.updateStatus(ctx, record)
}

// Delete removes a CheckpointRecord by namespace and name.
// Used by GracefulReclaimer to clean up a dangling Ready record when UpdateScale fails.
func (w *DynamicCheckpointRecordWriter) Delete(ctx context.Context, namespace, name string) error {
	if w == nil || w.client == nil {
		return fmt.Errorf("checkpoint record dynamic client is not configured")
	}
	err := w.client.Resource(checkpointRecordResource).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("delete checkpoint record %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (w *DynamicCheckpointRecordWriter) updateStatus(ctx context.Context, record *v1alpha1.CheckpointRecord) (*v1alpha1.CheckpointRecord, error) {
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(record)
	if err != nil {
		return nil, fmt.Errorf("convert checkpoint record status: %w", err)
	}
	updated, err := w.client.Resource(checkpointRecordResource).Namespace(record.Namespace).UpdateStatus(
		ctx,
		&unstructured.Unstructured{Object: content},
		metav1.UpdateOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("update checkpoint record status %s/%s: %w", record.Namespace, record.Name, err)
	}
	result := new(v1alpha1.CheckpointRecord)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(updated.Object, result); err != nil {
		return nil, fmt.Errorf("convert updated checkpoint record status: %w", err)
	}
	return result, nil
}
