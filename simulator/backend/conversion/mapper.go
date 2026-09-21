package conversion

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"simulator/backend/models"
)

// ToPodMetricsAndWindow converts a SyntheticWorkload into the authoritative
// *metrics.PodMetrics and populated *metrics.MetricWindow ring buffer.
func ToPodMetricsAndWindow(sw models.SyntheticWorkload) (*metrics.PodMetrics, *metrics.MetricWindow) {
	pod := &metrics.PodMetrics{
		Namespace:          sw.Namespace,
		Name:               sw.Name,
		UID:                types.UID(sw.Namespace + "/" + sw.Name),
		NodeName:           sw.NodeName,
		Phase:              sw.GetPodPhase(),
		QoSClass:           corev1.PodQOSClass(sw.QoSClass),
		Priority:           sw.Priority,
		PriorityClassName:  sw.PriorityClassName,
		DisruptionsAllowed: sw.DisruptionsAllowed,

		TotalRequestedCPUMillis: sw.RequestedCPUMillis,
		TotalLimitCPUMillis:     sw.LimitCPUMillis,
		TotalRequestedMemory:    sw.RequestedMemoryBytes,
		TotalLimitMemory:        sw.LimitMemoryBytes,

		TotalUsageCPUMillicores: sw.UsageCPUMillicores,
		TotalUsageMemoryBytes:   sw.UsageMemoryBytes,
		TotalUsageRSSBytes:      sw.UsageMemoryBytes, // default RSS to working set
		TotalNetworkBytesSec:    sw.NetworkBytesPerSec,
		RequestQPS:              sw.RequestQPS,

		IdleDuration:   time.Duration(sw.IdleDurationSeconds) * time.Second,
		IsIdle:         sw.IsIdle,
		TelemetryReady: true,
		LastUpdated:    time.Now(),
	}

	if len(sw.Labels) > 0 {
		pod.Labels = make(map[string]string, len(sw.Labels))
		for k, v := range sw.Labels {
			pod.Labels[k] = v
		}
	}

	if len(sw.Annotations) > 0 {
		pod.Annotations = make(map[string]string, len(sw.Annotations))
		for k, v := range sw.Annotations {
			pod.Annotations[k] = v
		}
	}

	if sw.OwnerKind != "" || sw.DesiredReplicas > 0 {
		pod.Replicas = &metrics.ReplicaInfo{
			OwnerKind:         sw.OwnerKind,
			OwnerName:         sw.OwnerName,
			DesiredReplicas:   sw.DesiredReplicas,
			ReadyReplicas:     sw.ReadyReplicas,
			AvailableReplicas: sw.AvailableReplicas,
		}
	}

	// Create sliding window ring buffer (5 samples default capacity)
	const windowCap = 5
	window := metrics.NewMetricWindow(windowCap)
	now := time.Now()

	// Fill window with samples representing the telemetry state
	for i := 0; i < windowCap; i++ {
		window.AddSample(metrics.MetricSample{
			Timestamp:          now.Add(-time.Duration(windowCap-1-i) * 10 * time.Second),
			CPUMillicores:      sw.UsageCPUMillicores,
			MemoryWorkingSet:   sw.UsageMemoryBytes,
			MemoryRSS:          sw.UsageMemoryBytes,
			NetworkBytesPerSec: sw.NetworkBytesPerSec,
			RequestQPS:         sw.RequestQPS,
		})
	}

	return pod, window
}

// ToNodeMetrics converts a SyntheticNode into *metrics.NodeMetrics.
func ToNodeMetrics(sn models.SyntheticNode) *metrics.NodeMetrics {
	return &metrics.NodeMetrics{
		Name:                     sn.Name,
		TotalCapacityCPUMillis:   sn.TotalCapacityCPUMillis,
		TotalCapacityMemoryBytes: sn.TotalCapacityMemoryBytes,
		AllocatableCPUMillis:     sn.AllocatableCPUMillis,
		AllocatableMemoryBytes:   sn.AllocatableMemoryBytes,
		ActualUsageCPUMillicores: sn.ActualUsageCPUMillicores,
		ActualUsageMemoryBytes:   sn.ActualUsageMemoryBytes,
		RealFreeCPUMillicores:    float64(sn.AllocatableCPUMillis) - sn.ActualUsageCPUMillicores,
		RealFreeMemoryBytes:      sn.AllocatableMemoryBytes - sn.ActualUsageMemoryBytes,
		IsReady:                  sn.IsReady,
		LastUpdated:              time.Now(),
	}
}
