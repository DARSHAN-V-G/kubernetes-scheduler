package simulation

import (
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/analyzer"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/decision"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/detector"
	"simulator/backend/conversion"
	"simulator/backend/models"
)

// PipelineRunner executes the real Go intelligence pipeline:
// pkg/analyzer -> pkg/detector -> pkg/decision
type PipelineRunner struct {
	analyzerCfg *analyzer.Config
	detectorCfg *detector.Config
	policy      *decision.Policy
	history     *decision.History
	engine      *decision.Engine
}

// NewPipelineRunner creates a runner with standard default configurations.
func NewPipelineRunner() *PipelineRunner {
	policy := decision.DefaultPolicy()
	return &PipelineRunner{
		analyzerCfg: analyzer.DefaultConfig(),
		detectorCfg: detector.DefaultConfig(),
		policy:      policy,
		history:     decision.NewHistory(),
		engine:      decision.NewEngine(policy),
	}
}

// Policy returns the active decision policy (used for inspectable weights and thresholds).
func (r *PipelineRunner) Policy() *decision.Policy {
	return r.policy
}

// ExecuteSingleWorkload executes the pipeline on a single synthetic workload.
func (r *PipelineRunner) ExecuteSingleWorkload(sw models.SyntheticWorkload) models.WorkloadSimulationResult {
	pod, window := conversion.ToPodMetricsAndWindow(sw)

	// Step 1: Real Analyzer
	profile := analyzer.Analyze(pod, window, r.analyzerCfg)

	// Step 2: Real Detector
	classification := detector.Classify(profile, r.detectorCfg)

	// Step 3: Real Decision Engine (only evaluated if workload is IDLE candidate)
	var dec decision.DecisionResult
	if classification.Class == detector.ClassIdle {
		dec = r.engine.Evaluate(profile, pod)
	} else {
		dec = decision.DecisionResult{
			PodNamespace:   pod.Namespace,
			PodName:        pod.Name,
			Classification: classification.Class.String(),
			Action:         decision.ActionKeep,
			Eligible:       false,
			Reasons: []string{
				"Workload classified as " + classification.Class.String() + " by Idle Detector — reclamation evaluation only applies to IDLE candidates (KEEP)",
			},
			DecidedAt: time.Now(),
		}
	}

	return models.WorkloadSimulationResult{
		Name:                     sw.Name,
		Namespace:                sw.Namespace,
		NodeName:                 sw.NodeName,
		OwnerKind:                sw.OwnerKind,
		OwnerName:                sw.OwnerName,
		AvgCPUMillicores:         profile.AvgCPUMillicores,
		PeakCPUMillicores:        profile.PeakCPUMillicores,
		CPUUtilization:           profile.CPUUtilization,
		CPUUtilStatus:            profile.CPUUtilStatus,
		AvgMemoryBytes:           profile.AvgMemoryBytes,
		PeakMemoryBytes:          profile.PeakMemoryBytes,
		MemoryUtilization:        profile.MemoryUtilization,
		MemoryUtilStatus:         profile.MemoryUtilStatus,
		CPUTrend:                 profile.CPUTrend,
		ReclaimableCPUMillicores: profile.ReclaimableCPUMillicores,
		ReclaimableMemoryBytes:   profile.ReclaimableMemoryBytes,
		IsConsistentlyIdle:       profile.IsConsistentlyIdle,
		Classification:           classification.Class,
		ClassificationReasons:    classification.Reasons,
		DetectedIdleDuration:     classification.IdleDuration,
		Action:                   dec.Action,
		Eligible:                 dec.Eligible,
		Score:                    dec.Score,
		Scores:                   dec.Scores,
		Capabilities:             dec.Capabilities,
		DecisionReasons:          dec.Reasons,
		RejectionReasons:         dec.RejectionReasons,
		DecidedAt:                dec.DecidedAt,
	}
}

// ExecuteSimulation runs the simulation over a full synthetic cluster and workloads.
func (r *PipelineRunner) ExecuteSimulation(cluster models.ClusterModel) *models.SimulationResponse {
	workloadResults := make([]models.WorkloadSimulationResult, 0, len(cluster.Workloads))

	var clusterSummary models.ClusterSimulationSummary
	clusterSummary.TotalWorkloads = len(cluster.Workloads)

	// Map to track per-node aggregations
	nodeSummaryMap := make(map[string]*models.NodeSimulationSummary)
	for _, n := range cluster.Nodes {
		nodeSummaryMap[n.Name] = &models.NodeSimulationSummary{
			NodeName:                 n.Name,
			TotalCapacityCPUMillis:   n.TotalCapacityCPUMillis,
			TotalCapacityMemoryBytes: n.TotalCapacityMemoryBytes,
			ActualUsageCPUMillicores: n.ActualUsageCPUMillicores,
			ActualUsageMemoryBytes:   n.ActualUsageMemoryBytes,
		}
	}

	for _, sw := range cluster.Workloads {
		res := r.ExecuteSingleWorkload(sw)
		workloadResults = append(workloadResults, res)

		// Classification counts
		switch res.Classification {
		case detector.ClassActive:
			clusterSummary.CountActive++
		case detector.ClassLowUsage:
			clusterSummary.CountLowUsage++
		case detector.ClassIdle:
			clusterSummary.CountIdle++
		}

		// Action counts
		switch res.Action {
		case decision.ActionKeep:
			clusterSummary.CountKeep++
		case decision.ActionSoftReclaim:
			clusterSummary.CountSoftReclaim++
		case decision.ActionFullReclaim:
			clusterSummary.CountFullReclaim++
		}

		// Safety blocked count (neither full nor soft allowed)
		if !res.Capabilities.FullReclaimAllowed && !res.Capabilities.SoftReclaimAllowed {
			clusterSummary.CountSafetyBlocked++
		}

		// Node-level aggregation
		if summary, ok := nodeSummaryMap[sw.NodeName]; ok {
			summary.PodCount++
			summary.AllocatedRequestedCPU += sw.RequestedCPUMillis
			summary.AllocatedRequestedMem += sw.RequestedMemoryBytes

			if res.Action != decision.ActionKeep {
				summary.SimulatedReclaimableCPU += res.ReclaimableCPUMillicores
				summary.SimulatedReclaimableMem += res.ReclaimableMemoryBytes
				clusterSummary.TotalReclaimableCPU += res.ReclaimableCPUMillicores
				clusterSummary.TotalReclaimableMem += res.ReclaimableMemoryBytes
			}
		}
	}

	// Preserve node order and aggregate node capacity/usage
	nodeSummaries := make([]models.NodeSimulationSummary, 0, len(cluster.Nodes))
	for _, n := range cluster.Nodes {
		if summary, ok := nodeSummaryMap[n.Name]; ok {
			nodeSummaries = append(nodeSummaries, *summary)
		}
		clusterSummary.TotalCapacityCPUMillis += n.TotalCapacityCPUMillis
		clusterSummary.TotalCapacityMemoryBytes += n.TotalCapacityMemoryBytes
		clusterSummary.TotalUsageCPUMillicores += n.ActualUsageCPUMillicores
		clusterSummary.TotalUsageMemoryBytes += n.ActualUsageMemoryBytes
	}

	// Compute theoretical projected available capacity
	projCPU := float64(clusterSummary.TotalCapacityCPUMillis) - (clusterSummary.TotalUsageCPUMillicores - clusterSummary.TotalReclaimableCPU)
	if projCPU < 0 {
		projCPU = 0
	}
	clusterSummary.ProjectedAvailableCPUMillis = projCPU

	projMem := clusterSummary.TotalCapacityMemoryBytes - (clusterSummary.TotalUsageMemoryBytes - clusterSummary.TotalReclaimableMem)
	if projMem < 0 {
		projMem = 0
	}
	clusterSummary.ProjectedAvailableMemBytes = projMem

	return &models.SimulationResponse{
		ClusterSummary: clusterSummary,
		NodeSummaries:  nodeSummaries,
		Workloads:      workloadResults,
		SimulatedAt:    time.Now(),
	}
}
