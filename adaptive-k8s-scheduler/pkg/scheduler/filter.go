package scheduler

import (
	"fmt"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	corev1 "k8s.io/api/core/v1"
)

// HeadroomFilter evaluates whether a node possesses sufficient physical and declarative headroom.
type HeadroomFilter struct {
	config *SchedulerConfig
}

// NewHeadroomFilter creates a new headroom filter.
func NewHeadroomFilter(cfg *SchedulerConfig) *HeadroomFilter {
	if cfg == nil {
		cfg = DefaultSchedulerConfig()
	}
	return &HeadroomFilter{
		config: cfg,
	}
}

// PodRequests extracts total CPU (millicores) and Memory (bytes) requested across all containers in a pod.
func PodRequests(pod *corev1.Pod) (reqCPU int64, reqMem int64) {
	if pod == nil {
		return 0, 0
	}

	for _, c := range pod.Spec.Containers {
		reqCPU += c.Resources.Requests.Cpu().MilliValue()
		reqMem += c.Resources.Requests.Memory().Value()
	}

	// For init containers, take the max requested by any single init container
	var maxInitCPU int64
	var maxInitMem int64
	for _, ic := range pod.Spec.InitContainers {
		icCPU := ic.Resources.Requests.Cpu().MilliValue()
		icMem := ic.Resources.Requests.Memory().Value()
		if icCPU > maxInitCPU {
			maxInitCPU = icCPU
		}
		if icMem > maxInitMem {
			maxInitMem = icMem
		}
	}

	if maxInitCPU > reqCPU {
		reqCPU = maxInitCPU
	}
	if maxInitMem > reqMem {
		reqMem = maxInitMem
	}

	return reqCPU, reqMem
}

// Filter evaluates a node against the pod resource requirements and real-time physical headroom.
func (f *HeadroomFilter) Filter(pod *corev1.Pod, node *metrics.NodeMetrics) FilterResult {
	if node == nil {
		return FilterResult{
			Eligible: false,
			Reason:   "node metrics are nil",
		}
	}

	res := FilterResult{
		NodeName: node.Name,
		Eligible: true,
	}

	// 1. Basic Node Health Conditions
	if !node.IsReady {
		res.Eligible = false
		res.Reason = "node is not in Ready state"
		return res
	}
	if node.HasMemoryPressure {
		res.Eligible = false
		res.Reason = "node has MemoryPressure condition"
		return res
	}
	if node.HasDiskPressure {
		res.Eligible = false
		res.Reason = "node has DiskPressure condition"
		return res
	}
	if node.HasPIDPressure {
		res.Eligible = false
		res.Reason = "node has PIDPressure condition"
		return res
	}

	reqCPU, reqMem := PodRequests(pod)

	// 2. Declarative Allocatable Headroom Check
	if (node.AllocatedRequestedCPU + reqCPU) > node.AllocatableCPUMillis {
		res.Eligible = false
		res.Reason = fmt.Sprintf(
			"insufficient declarative allocatable CPU (requested: %dm, available: %dm)",
			reqCPU, node.AllocatableCPUMillis-node.AllocatedRequestedCPU,
		)
		return res
	}
	if (node.AllocatedRequestedMem + reqMem) > node.AllocatableMemoryBytes {
		res.Eligible = false
		res.Reason = fmt.Sprintf(
			"insufficient declarative allocatable Memory (requested: %d bytes, available: %d bytes)",
			reqMem, node.AllocatableMemoryBytes-node.AllocatedRequestedMem,
		)
		return res
	}

	// 3. Real Physical Headroom Check (Adaptive Scheduler Core Innovation)
	// Prevents noisy neighbors and OOM kills by inspecting actual physical usage
	buffer := f.config.HeadroomSafetyBuffer
	if buffer < 1.0 {
		buffer = 1.0
	}

	requiredCPU := float64(reqCPU) * buffer
	requiredMem := float64(reqMem) * buffer

	if node.RealFreeCPUMillicores < requiredCPU {
		res.Eligible = false
		res.Reason = fmt.Sprintf(
			"insufficient real physical CPU headroom (required with %.0f%% buffer: %.1fm, real free: %.1fm)",
			(buffer-1.0)*100, requiredCPU, node.RealFreeCPUMillicores,
		)
		return res
	}

	if float64(node.RealFreeMemoryBytes) < requiredMem {
		res.Eligible = false
		res.Reason = fmt.Sprintf(
			"insufficient real physical Memory headroom (required with %.0f%% buffer: %.0f bytes, real free: %d bytes)",
			(buffer-1.0)*100, requiredMem, node.RealFreeMemoryBytes,
		)
		return res
	}

	return res
}
