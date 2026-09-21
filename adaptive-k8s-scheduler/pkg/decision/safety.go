package decision

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// evaluateCapabilities runs all safety checks against the pod and returns
// capability flags describing which reclamation actions are permitted.
//
// Safety gate rule: hard constraints come first, before any score is computed.
// Each check is independent. Results are combined to set FullReclaimAllowed
// and SoftReclaimAllowed.
//
// Key distinction:
//   - "Protected" workload  → blocks BOTH full and soft reclamation.
//   - "Not checkpointable"  → blocks ONLY full reclamation; soft may remain available.
func evaluateCapabilities(pod *metrics.PodMetrics, policy *Policy) Capabilities {
	caps := Capabilities{
		FullReclaimAllowed: true,
		SoftReclaimAllowed: true,
	}

	type checkFn func(*metrics.PodMetrics, *Policy) (blockFull, blockSoft bool, reason string)

	checks := []checkFn{
		checkProtected,
		checkPriorityClass,
		checkPDB,
		checkReplicaAvailability,
		checkPodPhase,
		checkCheckpointAnnotation,
		checkStatefulCompatibility,
	}

	for _, check := range checks {
		blockFull, blockSoft, reason := check(pod, policy)
		if blockFull {
			caps.FullReclaimAllowed = false
		}
		if blockSoft {
			caps.SoftReclaimAllowed = false
		}
		if reason != "" && (blockFull || blockSoft) {
			caps.Reasons = append(caps.Reasons, reason)
		}
	}

	return caps
}

// checkProtected blocks ALL reclamation when the workload carries the protected annotation.
// This is a hard override that cannot be overridden by any score.
// "Protected" is semantically distinct from "not checkpointable".
func checkProtected(pod *metrics.PodMetrics, policy *Policy) (blockFull, blockSoft bool, reason string) {
	if pod.Annotations[policy.ProtectedAnnotation] == "true" {
		return true, true, fmt.Sprintf(
			"workload is protected (%s=true) — all reclamation blocked",
			policy.ProtectedAnnotation,
		)
	}
	return false, false, ""
}

// checkPriorityClass blocks ALL reclamation for system-critical pods or pods whose
// numeric priority equals or exceeds MaxPriorityForReclaim.
func checkPriorityClass(pod *metrics.PodMetrics, policy *Policy) (blockFull, blockSoft bool, reason string) {
	systemCritical := map[string]bool{
		"system-cluster-critical": true,
		"system-node-critical":    true,
	}
	if systemCritical[pod.PriorityClassName] {
		return true, true, fmt.Sprintf(
			"priority class %q is system-critical — all reclamation blocked",
			pod.PriorityClassName,
		)
	}
	if pod.Priority >= policy.MaxPriorityForReclaim {
		return true, true, fmt.Sprintf(
			"pod priority %d >= MaxPriorityForReclaim %d — all reclamation blocked",
			pod.Priority, policy.MaxPriorityForReclaim,
		)
	}
	return false, false, ""
}

// checkPDB blocks ALL reclamation when the pod's matching PodDisruptionBudget
// has DisruptionsAllowed == 0.
// DisruptionsAllowed == -1 means no PDB applies (safe to proceed).
func checkPDB(pod *metrics.PodMetrics, policy *Policy) (blockFull, blockSoft bool, reason string) {
	if pod.DisruptionsAllowed == 0 {
		return true, true, "PDB does not allow any disruptions (DisruptionsAllowed=0)"
	}
	return false, false, ""
}

// checkReplicaAvailability blocks ALL reclamation when removing this pod would
// drop available replicas below the minimum required threshold.
// Standalone pods (nil Replicas) are not constrained by this check.
func checkReplicaAvailability(pod *metrics.PodMetrics, policy *Policy) (blockFull, blockSoft bool, reason string) {
	if pod.Replicas == nil {
		return false, false, ""
	}
	afterRemoval := pod.Replicas.AvailableReplicas - 1
	if afterRemoval < policy.MinReplicasRequired {
		return true, true, fmt.Sprintf(
			"removing pod would leave %d available replicas (< minimum %d) for %s/%s",
			afterRemoval, policy.MinReplicasRequired,
			pod.Replicas.OwnerKind, pod.Replicas.OwnerName,
		)
	}
	return false, false, ""
}

// checkPodPhase blocks ALL reclamation when the pod is not in the Running phase.
// Acting on a Pending, Succeeded, or Failed pod is unsafe and undefined.
func checkPodPhase(pod *metrics.PodMetrics, policy *Policy) (blockFull, blockSoft bool, reason string) {
	if pod.Phase != corev1.PodRunning {
		return true, true, fmt.Sprintf(
			"pod phase is %q (must be Running for reclamation)",
			pod.Phase,
		)
	}
	return false, false, ""
}

// checkCheckpointAnnotation blocks FULL RECLAIM ONLY when the workload explicitly
// declares it does not support CRIU checkpointing.
//
// reclaim.io/checkpointable=false → FullReclaimAllowed=false, SoftReclaimAllowed unchanged
// reclaim.io/checkpointable=true  → no restriction
// annotation absent               → no restriction (neutral)
//
// NOTE: "not checkpointable" ≠ "protected". A not-checkpointable workload may still
// be soft-reclaimed if all other conditions allow it.
func checkCheckpointAnnotation(pod *metrics.PodMetrics, policy *Policy) (blockFull, blockSoft bool, reason string) {
	val, exists := pod.Annotations[policy.CheckpointableAnnotation]
	if exists && val == "false" {
		return true, false, fmt.Sprintf(
			"%s=false: CRIU checkpoint not supported; full reclaim blocked, soft reclaim still available",
			policy.CheckpointableAnnotation,
		)
	}
	return false, false, ""
}

// checkStatefulCompatibility blocks FULL RECLAIM ONLY for StatefulSet-owned pods
// that have not explicitly declared CRIU checkpoint support.
// StatefulSets often maintain persistent identities or storage that make
// checkpoint-and-suspend semantically unsafe without explicit opt-in.
// Soft reclaim (resource right-sizing) remains available.
func checkStatefulCompatibility(pod *metrics.PodMetrics, policy *Policy) (blockFull, blockSoft bool, reason string) {
	if pod.Replicas == nil || pod.Replicas.OwnerKind != "StatefulSet" {
		return false, false, ""
	}
	if pod.Annotations[policy.CheckpointableAnnotation] != "true" {
		return true, false, fmt.Sprintf(
			"StatefulSet pod without %s=true: full reclaim blocked (CRIU unsafe); soft reclaim available",
			policy.CheckpointableAnnotation,
		)
	}
	return false, false, ""
}
