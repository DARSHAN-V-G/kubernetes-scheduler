package metrics

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ContainerMetrics holds declarative resource requests/limits and actual runtime telemetry for a container.
type ContainerMetrics struct {
	Name                 string    `json:"name"`
	Image                string    `json:"image"`
	RequestedCPUMillis   int64     `json:"requestedCpuMillis"`
	LimitCPUMillis       int64     `json:"limitCpuMillis"`
	RequestedMemoryBytes int64     `json:"requestedMemoryBytes"`
	LimitMemoryBytes     int64     `json:"limitMemoryBytes"`
	UsageCPUMillicores   float64   `json:"usageCpuMillicores"`
	UsageMemoryBytes     int64     `json:"usageMemoryBytes"` // Working set (matches K8s OOM criterion)
	UsageRSSBytes        int64     `json:"usageRssBytes"`    // Resident set size
	TelemetryReady       bool      `json:"telemetryReady"`
	LastUpdated          time.Time `json:"lastUpdated"`
}

// Clone creates a deep copy of ContainerMetrics.
func (c *ContainerMetrics) Clone() *ContainerMetrics {
	if c == nil {
		return nil
	}
	copy := *c
	return &copy
}

// ReplicaInfo captures the quorum and replica health of the pod's owning controller.
type ReplicaInfo struct {
	OwnerKind         string `json:"ownerKind"` // Deployment, ReplicaSet, StatefulSet, etc.
	OwnerName         string `json:"ownerName"`
	DesiredReplicas   int32  `json:"desiredReplicas"`
	ReadyReplicas     int32  `json:"readyReplicas"`
	AvailableReplicas int32  `json:"availableReplicas"`
}

// Clone creates a deep copy of ReplicaInfo.
func (r *ReplicaInfo) Clone() *ReplicaInfo {
	if r == nil {
		return nil
	}
	copy := *r
	return &copy
}

// PodMetrics aggregates telemetry and control plane state for a single Pod.
type PodMetrics struct {
	Namespace          string                       `json:"namespace"`
	Name               string                       `json:"name"`
	UID                types.UID                    `json:"uid"`
	NodeName           string                       `json:"nodeName"`
	Phase              corev1.PodPhase              `json:"phase"`
	Labels             map[string]string            `json:"labels"`
	Annotations        map[string]string            `json:"annotations"`

	// K8s Control Plane State
	QoSClass           corev1.PodQOSClass           `json:"qosClass"`          // Guaranteed, Burstable, BestEffort
	Priority           int32                        `json:"priority"`          // Numeric priority
	PriorityClassName  string                       `json:"priorityClassName"` // PriorityClass name
	DisruptionsAllowed int32                        `json:"disruptionsAllowed"`// From matching PDB (-1 if no PDB)
	Replicas           *ReplicaInfo                 `json:"replicas,omitempty"`// Owning controller replica quorum

	// Container Telemetry & Specs
	Containers         map[string]*ContainerMetrics `json:"containers"`

	// Aggregated Resource Requests / Limits
	TotalRequestedCPUMillis int64                   `json:"totalRequestedCpuMillis"`
	TotalLimitCPUMillis     int64                   `json:"totalLimitCpuMillis"`
	TotalRequestedMemory    int64                   `json:"totalRequestedMemoryBytes"`
	TotalLimitMemory        int64                   `json:"totalLimitMemoryBytes"`

	// Aggregated Real Runtime Telemetry
	TotalUsageCPUMillicores float64                 `json:"totalUsageCpuMillicores"`
	TotalUsageMemoryBytes   int64                   `json:"totalUsageMemoryBytes"` // Sum of working set bytes
	TotalUsageRSSBytes      int64                   `json:"totalUsageRssBytes"`

	// Network I/O & Request/QPS
	NetworkRxBytesPerSec    float64                 `json:"networkRxBytesPerSec"`
	NetworkTxBytesPerSec    float64                 `json:"networkTxBytesPerSec"`
	TotalNetworkBytesSec    float64                 `json:"totalNetworkBytesSec"`
	RequestQPS              float64                 `json:"requestQps"` // HTTP QPS or packet rate fallback

	// Multi-Signal Idle Duration Tracking
	IdleDuration            time.Duration           `json:"idleDuration"`
	LastActiveTime          time.Time               `json:"lastActiveTime"`
	IsIdle                  bool                    `json:"isIdle"`

	// Telemetry Quality Metadata
	TelemetryReady          bool                    `json:"telemetryReady"`
	LastUpdated             time.Time               `json:"lastUpdated"`
}

// Clone creates a deep copy of PodMetrics.
func (p *PodMetrics) Clone() *PodMetrics {
	if p == nil {
		return nil
	}
	clone := *p

	if p.Labels != nil {
		clone.Labels = make(map[string]string, len(p.Labels))
		for k, v := range p.Labels {
			clone.Labels[k] = v
		}
	}
	if p.Annotations != nil {
		clone.Annotations = make(map[string]string, len(p.Annotations))
		for k, v := range p.Annotations {
			clone.Annotations[k] = v
		}
	}
	if p.Replicas != nil {
		clone.Replicas = p.Replicas.Clone()
	}
	if p.Containers != nil {
		clone.Containers = make(map[string]*ContainerMetrics, len(p.Containers))
		for k, v := range p.Containers {
			clone.Containers[k] = v.Clone()
		}
	}

	return &clone
}

// NodeMetrics tracks node capacity, declarative reservations, and real physical telemetry.
type NodeMetrics struct {
	Name                     string                 `json:"name"`
	AllocatableCPUMillis     int64                  `json:"allocatableCpuMillis"`
	AllocatableMemoryBytes   int64                  `json:"allocatableMemoryBytes"`
	TotalCapacityCPUMillis   int64                  `json:"totalCapacityCpuMillis"`
	TotalCapacityMemoryBytes int64                  `json:"totalCapacityMemoryBytes"`

	// Declaratively scheduled requests (sum of pods on node)
	AllocatedRequestedCPU    int64                  `json:"allocatedRequestedCpuMillis"`
	AllocatedRequestedMem    int64                  `json:"allocatedRequestedMemBytes"`

	// Real physical telemetry (Prometheus / cAdvisor / NodeExporter)
	ActualUsageCPUMillicores float64                `json:"actualUsageCpuMillicores"`
	ActualUsageMemoryBytes   int64                  `json:"actualUsageMemoryBytes"`

	// Real Headroom for Custom Scheduler (Allocatable - ActualUsage)
	RealFreeCPUMillicores    float64                `json:"realFreeCpuMillicores"`
	RealFreeMemoryBytes      int64                  `json:"realFreeMemoryBytes"`

	// Node Health & Pressure Conditions
	Conditions               []corev1.NodeCondition `json:"conditions"`
	IsReady                  bool                   `json:"isReady"`
	HasMemoryPressure        bool                   `json:"hasMemoryPressure"`
	HasDiskPressure          bool                   `json:"hasDiskPressure"`
	HasPIDPressure           bool                   `json:"hasPidPressure"`
	PodCount                 int                    `json:"podCount"`
	LastUpdated              time.Time              `json:"lastUpdated"`
}

// Clone creates a deep copy of NodeMetrics.
func (n *NodeMetrics) Clone() *NodeMetrics {
	if n == nil {
		return nil
	}
	clone := *n
	if n.Conditions != nil {
		clone.Conditions = make([]corev1.NodeCondition, len(n.Conditions))
		copy(clone.Conditions, n.Conditions)
	}
	return &clone
}

// ClusterSnapshot is an immutable snapshot of cluster telemetry for atomic scheduler plugin evaluations.
type ClusterSnapshot struct {
	Nodes     map[string]*NodeMetrics `json:"nodes"`
	Pods      map[string]*PodMetrics  `json:"pods"` // Key: "namespace/podName"
	Timestamp time.Time               `json:"timestamp"`
}

// Clone creates a deep copy of ClusterSnapshot.
func (cs *ClusterSnapshot) Clone() *ClusterSnapshot {
	if cs == nil {
		return nil
	}
	snapshot := &ClusterSnapshot{
		Nodes:     make(map[string]*NodeMetrics, len(cs.Nodes)),
		Pods:      make(map[string]*PodMetrics, len(cs.Pods)),
		Timestamp: cs.Timestamp,
	}
	for k, v := range cs.Nodes {
		snapshot.Nodes[k] = v.Clone()
	}
	for k, v := range cs.Pods {
		snapshot.Pods[k] = v.Clone()
	}
	return snapshot
}

// MetricSample represents an instantaneous telemetry measurement for smoothing.
type MetricSample struct {
	Timestamp            time.Time
	CPUMillicores        float64
	MemoryWorkingSet     int64
	MemoryRSS            int64
	NetworkBytesPerSec   float64
	RequestQPS           float64
}

// CollectorConfig holds tuning and operational parameters for metrics collection.
type CollectorConfig struct {
	PrometheusURL       string        `json:"prometheusUrl"`
	ScrapeInterval      time.Duration `json:"scrapeInterval"`
	HTTPTimeout         time.Duration `json:"httpTimeout"`
	WindowSize          int           `json:"windowSize"`
	IdleCPUThreshold    float64       `json:"idleCpuThreshold"`    // Millicores (e.g. 20.0 = 0.02 cores)
	IdleNetThreshold    float64       `json:"idleNetThreshold"`    // Bytes/sec (e.g. 10240 = 10 KB/s)
	IdleQPSThreshold    float64       `json:"idleQpsThreshold"`    // Requests/sec (e.g. 0.1)
	IdleMinDuration     time.Duration `json:"idleMinDuration"`     // Minimum duration to qualify as idle
	HTTPPort            int           `json:"httpPort"`
}

// DefaultCollectorConfig returns standard production defaults.
func DefaultCollectorConfig() *CollectorConfig {
	return &CollectorConfig{
		PrometheusURL:    "http://127.0.0.1:9090",
		ScrapeInterval:   10 * time.Second,
		HTTPTimeout:      5 * time.Second,
		WindowSize:       5, // 5 samples * 10s = 50s sliding window
		IdleCPUThreshold: 20.0,
		IdleNetThreshold: 10240.0,
		IdleQPSThreshold: 0.1,
		IdleMinDuration:  60 * time.Second,
		HTTPPort:         8081,
	}
}

// MutexWrapper provides safe synchronization helpers.
type SafeMetricsMap struct {
	sync.RWMutex
	items map[string]interface{}
}
