package models

import (
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/detector"
)

// WorkloadSimulationResult packages the authoritative output from all three stages:
// Analyzer, Detector, and Decision Engine for a single workload.
type WorkloadSimulationResult struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	NodeName  string `json:"nodeName"`
	OwnerKind string `json:"ownerKind"`
	OwnerName string `json:"ownerName"`

	// Analyzer Output
	AvgCPUMillicores         float64                    `json:"avgCpuMillicores"`
	PeakCPUMillicores        float64                    `json:"peakCpuMillicores"`
	CPUUtilization           float64                    `json:"cpuUtilization"`
	CPUUtilStatus            analyzer.UtilizationStatus `json:"cpuUtilStatus"`
	AvgMemoryBytes           int64                      `json:"avgMemoryBytes"`
	PeakMemoryBytes          int64                      `json:"peakMemoryBytes"`
	MemoryUtilization        float64                    `json:"memoryUtilization"`
	MemoryUtilStatus         analyzer.UtilizationStatus `json:"memoryUtilStatus"`
	CPUTrend                 analyzer.Trend             `json:"cpuTrend"`
	ReclaimableCPUMillicores float64                    `json:"reclaimableCpuMillicores"`
	ReclaimableMemoryBytes   int64                      `json:"reclaimableMemoryBytes"`
	IsConsistentlyIdle       bool                       `json:"isConsistentlyIdle"`

	// Detector Output
	Classification        detector.WorkloadClass `json:"classification"`
	ClassificationReasons []string               `json:"classificationReasons"`
	DetectedIdleDuration  time.Duration          `json:"detectedIdleDuration"`

	// Decision Engine Output
	Action           decision.Action           `json:"action"`
	Eligible         bool                      `json:"eligible"`
	Score            float64                   `json:"score"`
	Scores           decision.IndividualScores `json:"scores"`
	Capabilities     decision.Capabilities     `json:"capabilities"`
	DecisionReasons  []string                  `json:"decisionReasons"`
	RejectionReasons []string                  `json:"rejectionReasons"`
	DecidedAt        time.Time                 `json:"decidedAt"`
}

// NodeSimulationSummary aggregates resource states and reclaimable capacities per node.
type NodeSimulationSummary struct {
	NodeName                 string  `json:"nodeName"`
	TotalCapacityCPUMillis   int64   `json:"totalCapacityCpuMillis"`
	TotalCapacityMemoryBytes int64   `json:"totalCapacityMemoryBytes"`
	AllocatedRequestedCPU    int64   `json:"allocatedRequestedCpuMillis"`
	AllocatedRequestedMem    int64   `json:"allocatedRequestedMemBytes"`
	ActualUsageCPUMillicores float64 `json:"actualUsageCpuMillicores"`
	ActualUsageMemoryBytes   int64   `json:"actualUsageMemoryBytes"`
	SimulatedReclaimableCPU  float64 `json:"simulatedReclaimableCpuMillicores"`
	SimulatedReclaimableMem  int64   `json:"simulatedReclaimableMemBytes"`
	PodCount                 int     `json:"podCount"`
}

// ClusterSimulationSummary aggregates cluster-wide statistics.
type ClusterSimulationSummary struct {
	TotalWorkloads     int `json:"totalWorkloads"`
	CountActive        int `json:"countActive"`
	CountLowUsage      int `json:"countLowUsage"`
	CountIdle          int `json:"countIdle"`
	CountKeep          int `json:"countKeep"`
	CountSoftReclaim   int `json:"countSoftReclaim"`
	CountFullReclaim   int `json:"countFullReclaim"`
	CountSafetyBlocked int `json:"countSafetyBlocked"`

	TotalReclaimableCPU float64 `json:"totalReclaimableCpuMillicores"`
	TotalReclaimableMem int64   `json:"totalReclaimableMemBytes"`

	TotalCapacityCPUMillis      int64   `json:"totalCapacityCpuMillis"`
	TotalCapacityMemoryBytes    int64   `json:"totalCapacityMemoryBytes"`
	TotalUsageCPUMillicores     float64 `json:"totalUsageCpuMillicores"`
	TotalUsageMemoryBytes       int64   `json:"totalUsageMemoryBytes"`
	ProjectedAvailableCPUMillis float64 `json:"projectedAvailableCpuMillis"`
	ProjectedAvailableMemBytes  int64   `json:"projectedAvailableMemBytes"`
}

// SimulationResponse is returned by POST /api/simulate.
type SimulationResponse struct {
	ClusterSummary ClusterSimulationSummary   `json:"clusterSummary"`
	NodeSummaries  []NodeSimulationSummary    `json:"nodeSummaries"`
	Workloads      []WorkloadSimulationResult `json:"workloads"`
	SimulatedAt    time.Time                  `json:"simulatedAt"`
}
