package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CheckpointPhase represents the current lifecycle phase of a CRIU checkpoint archive.
type CheckpointPhase string

const (
	// CheckpointPhasePending indicates the checkpoint command has been initiated but tarball verification is incomplete.
	CheckpointPhasePending CheckpointPhase = "Pending"

	// CheckpointPhaseReady indicates the checkpoint tarball exists, SHA256 verified, and is ready for restoration.
	CheckpointPhaseReady CheckpointPhase = "Ready"

	// CheckpointPhaseRestoring indicates a restore operation is actively in progress.
	CheckpointPhaseRestoring CheckpointPhase = "Restoring"

	// CheckpointPhaseRestored indicates the pod was successfully reconstituted and resumed from this checkpoint.
	CheckpointPhaseRestored CheckpointPhase = "Restored"

	// CheckpointPhaseFailed indicates checkpoint capture, integrity check, or restore failed.
	CheckpointPhaseFailed CheckpointPhase = "Failed"

	// CheckpointPhaseExpired indicates the checkpoint exceeded retention TTL and has been marked for garbage collection.
	CheckpointPhaseExpired CheckpointPhase = "Expired"
)

// CheckpointRecordSpec defines the metadata, storage paths, and resource state of a captured checkpoint.
type CheckpointRecordSpec struct {
	// SourcePodName is the name of the pod from which this checkpoint was captured.
	SourcePodName string `json:"sourcePodName"`

	// SourcePodUID is the unique identifier of the source pod.
	SourcePodUID string `json:"sourcePodUid,omitempty"`

	// NodeName is the Kubernetes node where the source container was running during checkpoint capture.
	NodeName string `json:"nodeName"`

	// ContainerName is the specific container within the pod that was checkpointed.
	ContainerName string `json:"containerName"`

	// ImageURI is the container image running when checkpointed.
	ImageURI string `json:"imageUri"`

	// CheckpointPath is the absolute filesystem path or URI where the CRIU checkpoint archive is stored.
	CheckpointPath string `json:"checkpointPath"`

	// CheckpointSizeBytes is the size of the checkpoint archive tarball in bytes.
	CheckpointSizeBytes int64 `json:"checkpointSizeBytes,omitempty"`

	// ChecksumSHA256 is the cryptographic SHA256 hash of the checkpoint tarball for integrity verification.
	ChecksumSHA256 string `json:"checksumSha256,omitempty"`

	// CapturedAt records the timestamp when the checkpoint was captured.
	CapturedAt metav1.Time `json:"capturedAt"`

	// OriginalRequests records the resource requests of the container/pod at the time of reclamation.
	OriginalRequests corev1.ResourceList `json:"originalRequests,omitempty"`

	// OriginalLimits records the resource limits of the container/pod at the time of reclamation.
	OriginalLimits corev1.ResourceList `json:"originalLimits,omitempty"`

	// PodSpecSnapshot is a serialized JSON copy of the original pod specification for exact reconstitution.
	PodSpecSnapshot string `json:"podSpecSnapshot,omitempty"`
}

// CheckpointRecordStatus defines the lifecycle status and restoration record of a checkpoint.
type CheckpointRecordStatus struct {
	// Phase is the current lifecycle state of the checkpoint archive.
	// +kubebuilder:default="Pending"
	Phase CheckpointPhase `json:"phase,omitempty"`

	// RestoredPodName is the name of the new pod created when this checkpoint was restored.
	RestoredPodName string `json:"restoredPodName,omitempty"`

	// RestoredAt records the timestamp when the checkpoint was restored.
	RestoredAt *metav1.Time `json:"restoredAt,omitempty"`

	// Message provides human-readable details about the current phase or errors.
	Message string `json:"message,omitempty"`

	// FailureReason records the error code or short string if the checkpoint or restore failed.
	FailureReason string `json:"failureReason,omitempty"`

	// Conditions represents observations of the checkpoint record's state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.phase",description="Checkpoint Lifecycle Phase"
// +kubebuilder:printcolumn:name="SourcePod",type="string",JSONPath=".spec.sourcePodName",description="Source Pod Name"
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName",description="Node where checkpoint occurred"
// +kubebuilder:printcolumn:name="Size",type="integer",JSONPath=".spec.checkpointSizeBytes",description="Archive Size (bytes)"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// CheckpointRecord is the Schema for the checkpointrecords API.
type CheckpointRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CheckpointRecordSpec   `json:"spec,omitempty"`
	Status CheckpointRecordStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// CheckpointRecordList contains a list of CheckpointRecord.
type CheckpointRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CheckpointRecord `json:"items"`
}
