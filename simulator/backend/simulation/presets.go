package simulation

import (
	"fmt"
	"strings"

	"simulator/backend/models"
)

// PresetMetadata provides descriptive info about a scenario for the UI.
// Preserves all existing fields for backward compatibility while adding additive category and number fields.
type PresetMetadata struct {
	ID               string `json:"id"`
	Number           int    `json:"number"`
	Name             string `json:"name"`
	Category         string `json:"category"`
	CategoryName     string `json:"categoryName"`
	Description      string `json:"description"`
	Tag              string `json:"tag"`
	DesignedToTest   string `json:"designedToTest"`
	PrimaryCondition string `json:"primaryCondition"`
	NodeCount        int    `json:"nodeCount"`
	WorkloadCount    int    `json:"workloadCount"`
}

// PresetScenario encapsulates metadata and the synthetic cluster data for a scenario.
type PresetScenario struct {
	Metadata PresetMetadata      `json:"metadata"`
	Cluster  models.ClusterModel `json:"cluster"`
}

// GetAllPresetMetadata returns the list of all available preset scenario metadata.
func GetAllPresetMetadata() []PresetMetadata {
	presets := GetPresetScenarios()
	meta := make([]PresetMetadata, len(presets))
	for i, p := range presets {
		meta[i] = p.Metadata
	}
	return meta
}

// AllPresets is an alias for GetPresetScenarios for scenario integrity testing.
func AllPresets() []PresetScenario {
	return GetPresetScenarios()
}

// GetPresetByID returns a specific preset scenario by its ID (supports scenario-01..75 and legacy preset-a..g).
func GetPresetByID(id string) (*PresetScenario, error) {
	norm := strings.ToLower(strings.TrimSpace(id))

	// Normalize legacy aliases
	switch norm {
	case "preset-a":
		norm = "scenario-01"
	case "preset-b":
		norm = "scenario-02"
	case "preset-c":
		norm = "scenario-03"
	case "preset-d":
		norm = "scenario-18" // Protected Idle
	case "preset-e":
		norm = "scenario-19" // PDB Restricted
	case "preset-f":
		norm = "scenario-28" // Checkpoint Unsupported
	case "preset-g":
		norm = "scenario-67" // Mixed Cluster
	}

	for _, p := range GetPresetScenarios() {
		if strings.EqualFold(p.Metadata.ID, norm) {
			return &p, nil
		}
	}
	return nil, fmt.Errorf("preset scenario %q not found", id)
}

// ── Synthetic Object Builders (Eliminate repetitive boilerplate) ─────────────

// NodeOpts configures a synthetic node.
type NodeOpts struct {
	Name        string
	CPUCores    int64
	MemGiB      int64
	UsageCPU    float64
	UsageMemGiB float64
	IsReady     bool
}

func defaultNode(name string) models.SyntheticNode {
	return makeNode(NodeOpts{
		Name:        name,
		CPUCores:    8,
		MemGiB:      16,
		UsageCPU:    150.0,
		UsageMemGiB: 2.0,
		IsReady:     true,
	})
}

func makeNode(opts NodeOpts) models.SyntheticNode {
	if opts.CPUCores == 0 {
		opts.CPUCores = 8
	}
	if opts.MemGiB == 0 {
		opts.MemGiB = 16
	}
	isReady := true
	if !opts.IsReady && opts.Name != "" {
		isReady = opts.IsReady
	}
	cpuTotal := opts.CPUCores * 1000
	memTotal := opts.MemGiB * 1024 * 1024 * 1024
	return models.SyntheticNode{
		Name:                     opts.Name,
		TotalCapacityCPUMillis:   cpuTotal,
		TotalCapacityMemoryBytes: memTotal,
		AllocatableCPUMillis:     cpuTotal - 200,
		AllocatableMemoryBytes:   memTotal - (512 * 1024 * 1024),
		ActualUsageCPUMillicores: opts.UsageCPU,
		ActualUsageMemoryBytes:   int64(opts.UsageMemGiB * 1024 * 1024 * 1024),
		IsReady:                  isReady,
	}
}

// WorkloadOpts simplifies creation of synthetic workloads with sensible defaults.
type WorkloadOpts struct {
	Name               string
	Namespace          string
	NodeName           string
	Phase              string
	QoSClass           string
	Priority           int32
	PriorityClassName  string
	DisruptionsAllowed int32 // -1 = none, 0 = blocked, >0 = allowed
	OwnerKind          string
	OwnerName          string
	DesiredReplicas    int32
	ReadyReplicas      int32
	AvailableReplicas  int32
	ReqCPUMillis       int64
	LimitCPUMillis     int64
	ReqMemMiB          int64
	LimitMemMiB        int64
	UsageCPU           float64
	UsageMemMiB        int64
	NetworkBps         float64
	RequestQPS         float64
	IdleDurationSec    int64
	IsIdle             bool
	Checkpointable     bool
	Protected          bool
	Labels             map[string]string
	Annotations        map[string]string
}

func makeWorkload(opts WorkloadOpts) models.SyntheticWorkload {
	if opts.Namespace == "" {
		opts.Namespace = "default"
	}
	if opts.NodeName == "" {
		opts.NodeName = "node-compute-01"
	}
	if opts.Phase == "" {
		opts.Phase = "Running"
	}
	if opts.OwnerKind == "" {
		opts.OwnerKind = "Deployment"
	}
	if opts.OwnerName == "" {
		opts.OwnerName = opts.Name
	}
	if opts.DesiredReplicas == 0 && opts.AvailableReplicas == 0 {
		opts.DesiredReplicas = 3
		opts.ReadyReplicas = 3
		opts.AvailableReplicas = 3
	}
	if opts.QoSClass == "" {
		if opts.ReqCPUMillis > 0 && opts.ReqCPUMillis == opts.LimitCPUMillis &&
			opts.ReqMemMiB > 0 && opts.ReqMemMiB == opts.LimitMemMiB {
			opts.QoSClass = "Guaranteed"
		} else if opts.ReqCPUMillis == 0 && opts.ReqMemMiB == 0 {
			opts.QoSClass = "BestEffort"
		} else {
			opts.QoSClass = "Burstable"
		}
	}

	annos := make(map[string]string)
	for k, v := range opts.Annotations {
		annos[k] = v
	}
	if opts.Checkpointable {
		annos["reclaim.io/checkpointable"] = "true"
	}
	if opts.Protected {
		annos["reclaim.io/protected"] = "true"
	}

	labels := make(map[string]string)
	for k, v := range opts.Labels {
		labels[k] = v
	}
	if len(labels) == 0 {
		labels["app"] = opts.Name
	}

	return models.SyntheticWorkload{
		Name:                 opts.Name,
		Namespace:            opts.Namespace,
		NodeName:             opts.NodeName,
		Phase:                opts.Phase,
		QoSClass:             opts.QoSClass,
		Priority:             opts.Priority,
		PriorityClassName:    opts.PriorityClassName,
		DisruptionsAllowed:   opts.DisruptionsAllowed,
		OwnerKind:            opts.OwnerKind,
		OwnerName:            opts.OwnerName,
		DesiredReplicas:      opts.DesiredReplicas,
		ReadyReplicas:        opts.ReadyReplicas,
		AvailableReplicas:    opts.AvailableReplicas,
		RequestedCPUMillis:   opts.ReqCPUMillis,
		LimitCPUMillis:       opts.LimitCPUMillis,
		RequestedMemoryBytes: opts.ReqMemMiB * 1024 * 1024,
		LimitMemoryBytes:     opts.LimitMemMiB * 1024 * 1024,
		UsageCPUMillicores:   opts.UsageCPU,
		UsageMemoryBytes:     opts.UsageMemMiB * 1024 * 1024,
		NetworkBytesPerSec:   opts.NetworkBps,
		RequestQPS:           opts.RequestQPS,
		IdleDurationSeconds:  opts.IdleDurationSec,
		IsIdle:               opts.IsIdle,
		Labels:               labels,
		Annotations:          annos,
	}
}

// buildScenario packages metadata and cluster into a PresetScenario.
func buildScenario(
	number int,
	name string,
	category string,
	categoryName string,
	desc string,
	tag string,
	designedToTest string,
	primaryCond string,
	nodes []models.SyntheticNode,
	workloads []models.SyntheticWorkload,
) PresetScenario {
	id := fmt.Sprintf("scenario-%02d", number)
	return PresetScenario{
		Metadata: PresetMetadata{
			ID:               id,
			Number:           number,
			Name:             fmt.Sprintf("SCENARIO %02d: %s", number, name),
			Category:         category,
			CategoryName:     categoryName,
			Description:      desc,
			Tag:              tag,
			DesignedToTest:   designedToTest,
			PrimaryCondition: primaryCond,
			NodeCount:        len(nodes),
			WorkloadCount:    len(workloads),
		},
		Cluster: models.ClusterModel{
			Nodes:     nodes,
			Workloads: workloads,
		},
	}
}

// ── Master Preset List: Exactly 75 Scenarios Across Categories A–J ───────────

// GetPresetScenarios returns the complete suite of 75 predefined test scenarios.
func GetPresetScenarios() []PresetScenario {
	scenarios := make([]PresetScenario, 0, 75)

	n1 := []models.SyntheticNode{defaultNode("node-compute-01")}
	twoNodes := []models.SyntheticNode{
		defaultNode("node-compute-01"),
		makeNode(NodeOpts{Name: "node-compute-02", CPUCores: 8, MemGiB: 16, UsageCPU: 200.0, UsageMemGiB: 3.0, IsReady: true}),
	}
	fourNodes := []models.SyntheticNode{
		defaultNode("node-compute-01"),
		makeNode(NodeOpts{Name: "node-compute-02", CPUCores: 8, MemGiB: 16, UsageCPU: 300.0, UsageMemGiB: 4.0, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-03", CPUCores: 16, MemGiB: 32, UsageCPU: 500.0, UsageMemGiB: 6.0, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-04", CPUCores: 16, MemGiB: 32, UsageCPU: 400.0, UsageMemGiB: 5.0, IsReady: true}),
	}
	eightNodes := []models.SyntheticNode{
		defaultNode("node-compute-01"),
		makeNode(NodeOpts{Name: "node-compute-02", CPUCores: 8, MemGiB: 16, UsageCPU: 200.0, UsageMemGiB: 2.0, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-03", CPUCores: 8, MemGiB: 16, UsageCPU: 350.0, UsageMemGiB: 3.5, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-04", CPUCores: 8, MemGiB: 16, UsageCPU: 400.0, UsageMemGiB: 4.0, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-05", CPUCores: 16, MemGiB: 32, UsageCPU: 600.0, UsageMemGiB: 8.0, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-06", CPUCores: 16, MemGiB: 32, UsageCPU: 750.0, UsageMemGiB: 9.0, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-07", CPUCores: 16, MemGiB: 32, UsageCPU: 500.0, UsageMemGiB: 7.0, IsReady: true}),
		makeNode(NodeOpts{Name: "node-compute-08", CPUCores: 16, MemGiB: 32, UsageCPU: 800.0, UsageMemGiB: 10.0, IsReady: true}),
	}

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY A — BASIC WORKLOAD STATES (01–09)
	// ══════════════════════════════════════════════════════════════════════════
	catA := "workload-state"
	catAName := "Basic Workload States"

	// 01 Active Workload (High CPU, high QPS, active)
	scenarios = append(scenarios, buildScenario(
		1, "Active Workload", catA, catAName,
		"High CPU consumption and continuous incoming request traffic. Validates that active workloads are retained without disruption.",
		"Active", "Workload activity detection and retention", "CPU 85%, 180 QPS, 0s idle duration",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "payment-gateway", Namespace: "production", ReqCPUMillis: 1000, LimitCPUMillis: 2000,
				ReqMemMiB: 1024, LimitMemMiB: 2048, UsageCPU: 850.0, UsageMemMiB: 600,
				NetworkBps: 5242880, RequestQPS: 180.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 02 Low Usage Workload (Underutilized but idle duration not met)
	scenarios = append(scenarios, buildScenario(
		2, "Low Usage Workload", catA, catAName,
		"Workload is underutilized with low CPU usage, but idle duration is under threshold (15s < 30s min). Classified as LOW_USAGE and kept.",
		"Low Usage", "Transient low-usage vs genuine idleness classification", "CPU 8%, 15s idle duration",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "catalog-read-replica", Namespace: "production", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 80.0, UsageMemMiB: 50,
				NetworkBps: 1024, RequestQPS: 0.0, IdleDurationSec: 15, IsIdle: false,
			}),
		},
	))

	// 03 Idle Workload (Strong Full Reclaim Candidate: Preserves legacy scenario-03)
	scenarios = append(scenarios, buildScenario(
		3, "Idle Workload", catA, catAName,
		"Workload exhibits prolonged inactivity (>2 hours) and minimal resource draw with complete checkpoint support and quorum.",
		"Idle", "Full reclamation capability under safe quorum conditions", "Idle 7200s, checkpointable, healthy quorum, PDB=2",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "analytics-batch-reporter", Namespace: "analytics", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 4096, LimitMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50,
				NetworkBps: 20.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				Priority: 50, PriorityClassName: "batch-low", DisruptionsAllowed: 2,
				DesiredReplicas: 4, ReadyReplicas: 4, AvailableReplicas: 4, Checkpointable: true,
			}),
		},
	))

	// 04 Recently Active Workload
	scenarios = append(scenarios, buildScenario(
		4, "Recently Active Workload", catA, catAName,
		"Low current utilization with recent incoming traffic history. Short idle duration prevents premature reclamation.",
		"Activity", "Recent activity telemetry discrimination", "Low CPU/Mem, idle duration 15s",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "search-indexer-worker", Namespace: "search", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 40.0, UsageMemMiB: 48,
				NetworkBps: 2048, RequestQPS: 2.0, IdleDurationSec: 15, IsIdle: false,
			}),
		},
	))

	// 05 Bursty Workload
	scenarios = append(scenarios, buildScenario(
		5, "Bursty Workload", catA, catAName,
		"Intermittent telemetry spikes and fluctuating request rates prevent steady idle classification.",
		"Bursty", "Intermittent load pattern handling", "Fluctuating telemetry, 20s idle duration",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "webhook-event-receiver", Namespace: "integrations", ReqCPUMillis: 1500, LimitCPUMillis: 2000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 300.0, UsageMemMiB: 200,
				NetworkBps: 1048576, RequestQPS: 25.0, IdleDurationSec: 20, IsIdle: false,
			}),
		},
	))

	// 06 Sustained Low CPU
	scenarios = append(scenarios, buildScenario(
		6, "Sustained Low CPU", catA, catAName,
		"Continuous minimal CPU consumption (20m) with normal memory footprint and extended duration.",
		"Low CPU", "Low CPU profile stability evaluation", "CPU 20m, idle duration 180s",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "background-cron-poller", Namespace: "core", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 20.0, UsageMemMiB: 400,
				NetworkBps: 100, RequestQPS: 0.0, IdleDurationSec: 180, IsIdle: true,
			}),
		},
	))

	// 07 Sustained Low Memory
	scenarios = append(scenarios, buildScenario(
		7, "Sustained Low Memory", catA, catAName,
		"Minimal memory working set (25MiB) with moderate CPU utilization.",
		"Low Memory", "Low memory working set evaluation", "Memory 25MiB, idle duration 120s",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "udp-stream-relayer", Namespace: "streaming", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 2048, LimitMemMiB: 2048, UsageCPU: 250.0, UsageMemMiB: 25,
				NetworkBps: 512000, RequestQPS: 10.0, IdleDurationSec: 120, IsIdle: false,
			}),
		},
	))

	// 08 CPU-Heavy Workload
	scenarios = append(scenarios, buildScenario(
		8, "CPU-Heavy Workload", catA, catAName,
		"Computationally intensive workload saturating multi-core allocation. Retained with zero reclamation eligibility.",
		"Active", "High-compute saturation handling", "CPU 3800m / 4000m (95%)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "ml-inference-engine", Namespace: "ai", ReqCPUMillis: 4000, LimitCPUMillis: 4000,
				ReqMemMiB: 4096, LimitMemMiB: 4096, UsageCPU: 3800.0, UsageMemMiB: 2048,
				NetworkBps: 2097152, RequestQPS: 50.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 09 Memory-Heavy Workload
	scenarios = append(scenarios, buildScenario(
		9, "Memory-Heavy Workload", catA, catAName,
		"In-memory store consuming large heap resident set. Protected from disruption due to high stateful memory retention.",
		"Active", "In-memory caching behavior verification", "Memory 15.0 GiB / 16.0 GiB (94%)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "redis-cache-cluster", Namespace: "cache", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 16384, LimitMemMiB: 16384, UsageCPU: 50.0, UsageMemMiB: 15360,
				NetworkBps: 4194304, RequestQPS: 120.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY B — RECLAMATION CANDIDATES (10–17)
	// ══════════════════════════════════════════════════════════════════════════
	catB := "reclamation"
	catBName := "Reclamation Candidates"

	// 10 Strong Full Reclaim Candidate
	scenarios = append(scenarios, buildScenario(
		10, "Strong Full Reclaim Candidate", catB, catBName,
		"Highly idle workload with extensive idle duration, safe replica availability, PDB allowance, and verified CRIU checkpoint support.",
		"Reclaim", "Full CRIU suspend-and-resume workflow validation", "Idle 7200s, checkpointable, PDB=2, replicas=4",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "batch-reporting-worker", Namespace: "analytics", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 4096, LimitMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				Priority: 50, PriorityClassName: "batch-low", DisruptionsAllowed: 2,
				DesiredReplicas: 4, ReadyReplicas: 4, AvailableReplicas: 4, Checkpointable: true,
			}),
		},
	))

	// 11 Strong Soft Reclaim Candidate
	scenarios = append(scenarios, buildScenario(
		11, "Strong Soft Reclaim Candidate", catB, catBName,
		"Idle Deployment without explicit CRIU checkpoint support. Full reclaim is unavailable; decision engine selects in-place soft reclamation.",
		"Soft Reclaim", "Graceful fallback from Full to Soft reclamation", "Idle 3600s, non-checkpointable Deployment",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "doc-converter-service", Namespace: "media", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 4096, LimitMemMiB: 4096, UsageCPU: 8.0, UsageMemMiB: 60,
				NetworkBps: 15.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				Priority: 100, DisruptionsAllowed: 1, DesiredReplicas: 3, AvailableReplicas: 3,
				Checkpointable: false,
			}),
		},
	))

	// 12 High CPU Reclaim Potential
	scenarios = append(scenarios, buildScenario(
		12, "High CPU Reclaim Potential", catB, catBName,
		"Idle workload holding 8 CPU cores with negligible utilization (<15m). Yields maximum benefit score for CPU recovery.",
		"High Benefit", "Benefit score scaling for massive CPU allocation", "CPU request 8000m, usage 15m (99.8% headroom)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "overprovisioned-cpu-worker", Namespace: "compute", ReqCPUMillis: 8000, LimitCPUMillis: 8000,
				ReqMemMiB: 4096, LimitMemMiB: 4096, UsageCPU: 15.0, UsageMemMiB: 100,
				NetworkBps: 20.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				DisruptionsAllowed: 2, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 13 High Memory Reclaim Potential
	scenarios = append(scenarios, buildScenario(
		13, "High Memory Reclaim Potential", catB, catBName,
		"Idle workload holding 32 GiB memory reservation with nominal 200 MiB footprint.",
		"High Benefit", "Benefit score scaling for massive memory allocation", "Memory request 32 GiB, usage 200 MiB",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "overprovisioned-mem-store", Namespace: "analytics", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 32768, LimitMemMiB: 32768, UsageCPU: 10.0, UsageMemMiB: 200,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 14 High Combined Reclaim Potential
	scenarios = append(scenarios, buildScenario(
		14, "High Combined Reclaim Potential", catB, catBName,
		"Workload over-allocated on both CPU (16 cores) and memory (64 GiB) idling for hours.",
		"High Benefit", "Maximum dual-resource reclamation incentive", "16 cores + 64 GiB requested, <0.5% used",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "idle-training-job", Namespace: "ai", ReqCPUMillis: 16000, LimitCPUMillis: 16000,
				ReqMemMiB: 65536, LimitMemMiB: 65536, UsageCPU: 20.0, UsageMemMiB: 300,
				NetworkBps: 30.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				DisruptionsAllowed: 2, AvailableReplicas: 4, Checkpointable: true,
			}),
		},
	))

	// 15 Long-Term Idle Workload
	scenarios = append(scenarios, buildScenario(
		15, "Long-Term Idle Workload", catB, catBName,
		"Workload dormant for 24+ hours (86,400s). High idle duration score guarantees high reclamation confidence.",
		"Long Idle", "Idle duration factor saturation", "Idle duration = 86,400s (24 hours)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "dormant-test-runner", Namespace: "testing", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 2.0, UsageMemMiB: 30,
				NetworkBps: 5.0, RequestQPS: 0.0, IdleDurationSec: 86400, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 16 Moderately Idle Workload
	scenarios = append(scenarios, buildScenario(
		16, "Moderately Idle Workload", catB, catBName,
		"Workload idle for 10 minutes (600s). Confirms classification as IDLE and evaluates proportional idle factor.",
		"Moderate Idle", "Proportional idle duration curve assessment", "Idle duration = 600s (10 minutes)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "staging-api-service", Namespace: "staging", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 10.0, UsageMemMiB: 50,
				NetworkBps: 15.0, RequestQPS: 0.0, IdleDurationSec: 600, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 17 Borderline Reclaim Candidate
	scenarios = append(scenarios, buildScenario(
		17, "Borderline Reclaim Candidate", catB, catBName,
		"Moderate utilization (~25%) and intermediate idle duration (180s). Tests policy evaluation near decision boundary.",
		"Boundary", "Policy threshold sensitivity analysis", "CPU 25%, idle 180s, score near 0.50 boundary",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "borderline-worker", Namespace: "staging", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 25.0, UsageMemMiB: 200,
				NetworkBps: 50.0, RequestQPS: 0.0, IdleDurationSec: 180, IsIdle: true,
				Priority: 800, DisruptionsAllowed: 1, DesiredReplicas: 2, AvailableReplicas: 2,
				Checkpointable: false,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY C — SAFETY CONSTRAINTS (18–26)
	// ══════════════════════════════════════════════════════════════════════════
	catC := "safety"
	catCName := "Safety Constraints"

	// 18 Protected Idle Workload (Preserves legacy scenario-04)
	scenarios = append(scenarios, buildScenario(
		18, "Protected Idle Workload", catC, catCName,
		"Zero usage and prolonged idleness, but carries the protected annotation. Verifies that the hard safety gate overrides high scores.",
		"Safety Gate", "Unconditional protection annotation safety gate", "reclaim.io/protected=true",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "disaster-recovery-standby", Namespace: "ops", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 4096, LimitMemMiB: 4096, UsageCPU: 2.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				Priority: 100, DisruptionsAllowed: 2, DesiredReplicas: 3, AvailableReplicas: 3,
				Checkpointable: true, Protected: true,
			}),
		},
	))

	// 19 PDB Disruptions = 0 (Preserves legacy scenario-05)
	scenarios = append(scenarios, buildScenario(
		19, "PDB Disruptions = 0", catC, catCName,
		"Idle workload subject to PodDisruptionBudget with DisruptionsAllowed=0. Verifies PDB safety gate blocks reclamation.",
		"PDB Gate", "Disruption budget exhaustion safety gate", "PDB disruptionsAllowed = 0",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "core-identity-service", Namespace: "auth", ReqCPUMillis: 1500, LimitCPUMillis: 1500,
				ReqMemMiB: 2048, LimitMemMiB: 2048, UsageCPU: 8.0, UsageMemMiB: 40,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 5400, IsIdle: true,
				Priority: 100, DisruptionsAllowed: 0, DesiredReplicas: 3, AvailableReplicas: 3,
				Checkpointable: true,
			}),
		},
	))

	// 20 PDB Disruptions = 1
	scenarios = append(scenarios, buildScenario(
		20, "PDB Disruptions = 1", catC, catCName,
		"Idle workload with exactly 1 disruption permitted by PodDisruptionBudget. Passes PDB safety gate.",
		"PDB Pass", "Single disruption budget permission verification", "PDB disruptionsAllowed = 1",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "session-handler", Namespace: "auth", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 40,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 21 Insufficient Replica Availability (Preserves legacy scenario-08)
	scenarios = append(scenarios, buildScenario(
		21, "Insufficient Replica Availability", catC, catCName,
		"Workload configured with 4 desired replicas, but only 1 is available (below min safe quorum). Safety gate blocks disruption.",
		"Quorum Gate", "Replica quorum safety protection", "Desired: 4, Available: 1 (quorum deficit)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "unhealthy-cluster-pod", Namespace: "production", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 2048, LimitMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				Priority: 100, DisruptionsAllowed: 1, DesiredReplicas: 4, ReadyReplicas: 1, AvailableReplicas: 1,
				Checkpointable: true,
			}),
		},
	))

	// 22 Single Replica Workload
	scenarios = append(scenarios, buildScenario(
		22, "Single Replica Workload", catC, catCName,
		"Standalone deployment with desired=1, available=1. Hard safety gate blocks disruption to prevent service outage.",
		"Replica Gate", "Single-replica service outage prevention", "Single replica workload (desired=1, available=1)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "singleton-cache", Namespace: "production", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				DisruptionsAllowed: 1, DesiredReplicas: 1, AvailableReplicas: 1, Checkpointable: true,
			}),
		},
	))

	// 23 High Priority Workload (Preserves legacy scenario-09)
	scenarios = append(scenarios, buildScenario(
		23, "High Priority Workload", catC, catCName,
		"Prolonged idle duration, but assigned high numeric priority (150,000). Priority safety gate unconditionally blocks disruption.",
		"Priority Gate", "High numeric priority safety override", "Priority: 150000 (critical threshold exceeded)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "emergency-failover-router", Namespace: "routing", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 2048, LimitMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				Priority: 150000, PriorityClassName: "high-priority", DisruptionsAllowed: 2,
				DesiredReplicas: 3, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 24 Critical Priority Workload
	scenarios = append(scenarios, buildScenario(
		24, "Critical Priority Workload", catC, catCName,
		"System-level critical priority class. Blocked from all disruption actions by authoritative safety gate.",
		"Priority Gate", "System critical priority class gating", "system-cluster-critical priority class",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "cluster-dns-resolver", Namespace: "kube-system", ReqCPUMillis: 500, LimitCPUMillis: 500,
				ReqMemMiB: 512, LimitMemMiB: 512, UsageCPU: 5.0, UsageMemMiB: 20,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				Priority: 2000000000, PriorityClassName: "system-cluster-critical", DisruptionsAllowed: 1,
				DesiredReplicas: 3, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 25 Non-Running Workload (Preserves legacy scenario-12)
	scenarios = append(scenarios, buildScenario(
		25, "Non-Running Workload", catC, catCName,
		"Pod in Pending phase. Phase safety gate prevents reclamation actions on pods that are not actively running.",
		"Phase Gate", "Pod lifecycle phase safety gate", "Phase: Pending (non-running)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "unscheduled-batch-job", Namespace: "batch", Phase: "Pending",
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 0.0, UsageMemMiB: 0, NetworkBps: 0.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 26 Unsupported Workload State
	scenarios = append(scenarios, buildScenario(
		26, "Unsupported Workload State", catC, catCName,
		"Pod in Failed phase. State gate blocks reclamation evaluation on terminated or failed pods.",
		"Phase Gate", "Failed pod state gating", "Phase: Failed (terminated)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "crashed-worker", Namespace: "batch", Phase: "Failed",
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 0.0, UsageMemMiB: 0, NetworkBps: 0.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY D — CHECKPOINT / CAPABILITY (27–32)
	// ══════════════════════════════════════════════════════════════════════════
	catD := "checkpoint"
	catDName := "Checkpoint / Capability"

	// 27 Checkpointable Deployment
	scenarios = append(scenarios, buildScenario(
		27, "Checkpointable Deployment", catD, catDName,
		"Standard Deployment carrying verified CRIU checkpoint annotation. Full checkpoint-and-suspend capability active.",
		"Capability", "Deployment CRIU checkpoint evaluation", "Deployment with checkpointable=true",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "state-sync-worker", Namespace: "sync", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 2048, LimitMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 28 Checkpoint Unsupported (Preserves legacy scenario-06)
	scenarios = append(scenarios, buildScenario(
		28, "Checkpoint Unsupported", catD, catDName,
		"Idle StatefulSet without checkpoint capability annotation. Engine falls back to in-place Soft Reclaim.",
		"Capability", "StatefulSet checkpoint capability gating", "StatefulSet without checkpoint annotation",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "postgres-read-replica", Namespace: "database", OwnerKind: "StatefulSet",
				ReqCPUMillis: 2000, LimitCPUMillis: 2000, ReqMemMiB: 4096, LimitMemMiB: 4096,
				UsageCPU: 5.0, UsageMemMiB: 60, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1,
				DesiredReplicas: 3, AvailableReplicas: 3, Checkpointable: false,
			}),
		},
	))

	// 29 Checkpointable StatefulSet (Preserves legacy scenario-07)
	scenarios = append(scenarios, buildScenario(
		29, "Checkpointable StatefulSet", catD, catDName,
		"StatefulSet with verified CRIU checkpoint annotation. Enables Full Reclaim without blocking on controller kind.",
		"Capability", "StatefulSet checkpoint support validation", "StatefulSet with checkpointable=true",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "cassandra-coordinator", Namespace: "database", OwnerKind: "StatefulSet",
				ReqCPUMillis: 2000, LimitCPUMillis: 2000, ReqMemMiB: 4096, LimitMemMiB: 4096,
				UsageCPU: 6.0, UsageMemMiB: 50, NetworkBps: 15.0, RequestQPS: 0.0,
				IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1,
				DesiredReplicas: 3, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 30 Non-Checkpointable StatefulSet
	scenarios = append(scenarios, buildScenario(
		30, "Non-Checkpointable StatefulSet", catD, catDName,
		"StatefulSet lacking CRIU checkpoint support. Full reclaim blocked; falls back to soft reclaim.",
		"Capability", "StatefulSet full reclaim restriction", "StatefulSet checkpointing unavailable",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "kafka-broker-replica", Namespace: "event-stream", OwnerKind: "StatefulSet",
				ReqCPUMillis: 2000, LimitCPUMillis: 2000, ReqMemMiB: 4096, LimitMemMiB: 4096,
				UsageCPU: 8.0, UsageMemMiB: 70, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1,
				DesiredReplicas: 3, AvailableReplicas: 3, Checkpointable: false,
			}),
		},
	))

	// 31 StatefulSet With Safe Replicas
	scenarios = append(scenarios, buildScenario(
		31, "StatefulSet With Safe Replicas", catD, catDName,
		"StatefulSet with 5 healthy available replicas and checkpoint support. Evaluates high quorum score.",
		"Replica Quorum", "Stateful quorum scaling", "StatefulSet with 5 available replicas",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "zookeeper-node", Namespace: "cluster-coordination", OwnerKind: "StatefulSet",
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 2048, LimitMemMiB: 2048,
				UsageCPU: 5.0, UsageMemMiB: 40, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 2,
				DesiredReplicas: 5, ReadyReplicas: 5, AvailableReplicas: 5, Checkpointable: true,
			}),
		},
	))

	// 32 StatefulSet With Replica Constraint
	scenarios = append(scenarios, buildScenario(
		32, "StatefulSet With Replica Constraint", catD, catDName,
		"StatefulSet with only 1 available replica. Hard safety gate blocks disruption despite checkpoint support.",
		"Replica Gate", "Stateful replica safety boundary", "StatefulSet available replicas: 1",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "redis-master", Namespace: "database", OwnerKind: "StatefulSet",
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 2048, LimitMemMiB: 2048,
				UsageCPU: 5.0, UsageMemMiB: 40, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1,
				DesiredReplicas: 3, ReadyReplicas: 1, AvailableReplicas: 1, Checkpointable: true,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY E — ACTIVITY SIGNALS (33–38)
	// ══════════════════════════════════════════════════════════════════════════
	catE := "activity"
	catEName := "Activity Signals"

	// 33 Network Active / CPU Idle (Preserves legacy scenario-11)
	scenarios = append(scenarios, buildScenario(
		33, "Network Active / CPU Idle", catE, catEName,
		"Minimal CPU and memory consumption, but sustained 12 MB/s network throughput. Demonstrates multi-signal detector checks.",
		"Activity Gate", "Multi-signal idle detection (Network override)", "CPU: 2%, Network: 12 MB/s (ACTIVE)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "reverse-proxy-gateway", Namespace: "edge", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 20.0, UsageMemMiB: 40,
				NetworkBps: 12582912, RequestQPS: 0.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 34 QPS Active / CPU Idle
	scenarios = append(scenarios, buildScenario(
		34, "QPS Active / CPU Idle", catE, catEName,
		"Minimal CPU consumption (15m) but handling 250 QPS. Detector classifies as ACTIVE based on request rate.",
		"Activity Gate", "Multi-signal idle detection (QPS override)", "CPU: 1.5%, QPS: 250.0 (ACTIVE)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "fast-auth-validator", Namespace: "edge", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 512, LimitMemMiB: 512, UsageCPU: 15.0, UsageMemMiB: 30,
				NetworkBps: 50000, RequestQPS: 250.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 35 Network + QPS Active
	scenarios = append(scenarios, buildScenario(
		35, "Network + QPS Active", catE, catEName,
		"Both high network I/O (15 MB/s) and high request throughput (300 QPS). Unambiguously active workload.",
		"Activity Gate", "Combined telemetry activity confirmation", "Network: 15 MB/s, QPS: 300.0",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "api-gateway-core", Namespace: "edge", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 2048, LimitMemMiB: 2048, UsageCPU: 120.0, UsageMemMiB: 300,
				NetworkBps: 15728640, RequestQPS: 300.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 36 Zero Activity
	scenarios = append(scenarios, buildScenario(
		36, "Zero Activity", catE, catEName,
		"Absolute zero CPU, memory, QPS, and network traffic for hours. Pure idle profile.",
		"Zero Activity", "Zero-activity telemetry profile", "Zero CPU/Mem/QPS/Net, 14,400s idle",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "dormant-replica", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 0.0, UsageMemMiB: 10,
				NetworkBps: 0.0, RequestQPS: 0.0, IdleDurationSec: 14400, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 37 Sporadic Activity
	scenarios = append(scenarios, buildScenario(
		37, "Sporadic Activity", catE, catEName,
		"Workload experiences brief bursts separated by short pauses (45s idle). Idle duration resets prevent false idleness.",
		"Activity", "Sporadic burst pattern evaluation", "Bursts reset idle window (45s idle)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "event-subscriber", Namespace: "events", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 50.0, UsageMemMiB: 100,
				NetworkBps: 10240, RequestQPS: 5.0, IdleDurationSec: 45, IsIdle: false,
			}),
		},
	))

	// 38 Recently Active Network Workload
	scenarios = append(scenarios, buildScenario(
		38, "Recently Active Network Workload", catE, catEName,
		"Network I/O ceased only 15 seconds ago. Idle duration requirement prevents premature action.",
		"Activity", "Network cessation transient duration", "Network dropped, idle 15s < 30s min",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "download-manager", Namespace: "batch", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 10.0, UsageMemMiB: 50,
				NetworkBps: 100.0, RequestQPS: 0.0, IdleDurationSec: 15, IsIdle: false,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY F — RESOURCE EDGE CASES (39–46)
	// ══════════════════════════════════════════════════════════════════════════
	catF := "resources"
	catFName := "Resource Edge Cases"

	// 39 Zero CPU Request (Preserves legacy scenario-13)
	scenarios = append(scenarios, buildScenario(
		39, "Zero CPU Request", catF, catFName,
		"Workload configured without CPU request (BestEffort CPU). Pipeline utilizes absolute usage and headroom without division-by-zero.",
		"Edge Case", "Zero resource request handling (BestEffort)", "CPU request = 0m (BestEffort QoS)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "best-effort-scraper", Namespace: "batch", ReqCPUMillis: 0, LimitCPUMillis: 0,
				ReqMemMiB: 512, LimitMemMiB: 512, UsageCPU: 5.0, UsageMemMiB: 40,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 40 Zero Memory Request
	scenarios = append(scenarios, buildScenario(
		40, "Zero Memory Request", catF, catFName,
		"Workload with zero memory request. Headroom computation handles unbounded memory requests cleanly.",
		"Edge Case", "Zero memory request handling", "Memory request = 0 MiB",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "zero-mem-worker", Namespace: "batch", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 0, LimitMemMiB: 0, UsageCPU: 5.0, UsageMemMiB: 80,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 41 Zero CPU + Memory Request
	scenarios = append(scenarios, buildScenario(
		41, "Zero CPU + Memory Request", catF, catFName,
		"Pure BestEffort workload with neither CPU nor memory requests specified.",
		"Edge Case", "Dual zero request handling", "CPU = 0m, Memory = 0 MiB (BestEffort)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "pure-best-effort", Namespace: "batch", ReqCPUMillis: 0, LimitCPUMillis: 0,
				ReqMemMiB: 0, LimitMemMiB: 0, UsageCPU: 5.0, UsageMemMiB: 50,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 42 BestEffort-style Workload
	scenarios = append(scenarios, buildScenario(
		42, "BestEffort-style Workload", catF, catFName,
		"Workload running under BestEffort QoS with variable background footprint.",
		"Edge Case", "BestEffort QoS classification", "QoSClass: BestEffort",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "background-log-trimmer", Namespace: "maintenance", QoSClass: "BestEffort",
				ReqCPUMillis: 0, LimitCPUMillis: 0, ReqMemMiB: 0, LimitMemMiB: 0,
				UsageCPU: 10.0, UsageMemMiB: 30, NetworkBps: 100.0, RequestQPS: 0.0,
				IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 43 Very Small Resource Request
	scenarios = append(scenarios, buildScenario(
		43, "Very Small Resource Request", catF, catFName,
		"Micro-workload requesting 10m CPU and 16 MiB memory. Tests precision of score and benefit calculations.",
		"Edge Case", "Micro-allocation benefit scoring", "Req: 10m CPU, 16 MiB Memory",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "micro-probe", Namespace: "monitoring", ReqCPUMillis: 10, LimitCPUMillis: 10,
				ReqMemMiB: 16, LimitMemMiB: 16, UsageCPU: 0.5, UsageMemMiB: 4,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 44 Very Large Resource Request
	scenarios = append(scenarios, buildScenario(
		44, "Very Large Resource Request", catF, catFName,
		"Colossal allocation requesting 64 CPU cores and 256 GiB memory. Tests scaling limits of benefit functions.",
		"Edge Case", "Colossal allocation benefit scoring", "Req: 64,000m CPU, 256 GiB Memory",
		[]models.SyntheticNode{makeNode(NodeOpts{Name: "node-giant", CPUCores: 128, MemGiB: 512, UsageCPU: 50.0, UsageMemGiB: 5.0, IsReady: true})},
		[]models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "titan-compute-job", Namespace: "hpc", NodeName: "node-giant",
				ReqCPUMillis: 64000, LimitCPUMillis: 64000, ReqMemMiB: 262144, LimitMemMiB: 262144,
				UsageCPU: 20.0, UsageMemMiB: 1024, NetworkBps: 100.0, RequestQPS: 0.0,
				IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 4, Checkpointable: true,
			}),
		},
	))

	// 45 Extremely Low CPU Usage
	scenarios = append(scenarios, buildScenario(
		45, "Extremely Low CPU Usage", catF, catFName,
		"Workload drawing 0.01 millicores of CPU. Verifies floating-point stability near zero.",
		"Edge Case", "Sub-millicore precision evaluation", "CPU Usage: 0.01m",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "nanoworker", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 0.01, UsageMemMiB: 20,
				NetworkBps: 0.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 46 Extremely Low Memory Usage
	scenarios = append(scenarios, buildScenario(
		46, "Extremely Low Memory Usage", catF, catFName,
		"Workload consuming exactly 1 MiB resident memory. Tests memory utilization calculation precision.",
		"Edge Case", "Minimal resident memory precision", "Memory Usage: 1 MiB",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "tiny-agent", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 1,
				NetworkBps: 0.0, RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY G — PRIORITY / POLICY (47–51)
	// ══════════════════════════════════════════════════════════════════════════
	catG := "priority"
	catGName := "Priority / Policy"

	// 47 Normal Priority
	scenarios = append(scenarios, buildScenario(
		47, "Normal Priority", catG, catGName,
		"Default priority (0) workload. Receives normal priority factor score without penalty.",
		"Priority", "Default priority factor baseline", "Priority: 0 (default)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "standard-web-app", Namespace: "web", Priority: 0,
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 40, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 48 Medium Priority
	scenarios = append(scenarios, buildScenario(
		48, "Medium Priority", catG, catGName,
		"Medium priority (500) workload. Moderate priority factor score reduction.",
		"Priority", "Medium priority factor scaling", "Priority: 500",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "data-sync-pipeline", Namespace: "data", Priority: 500,
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 40, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 49 High Priority
	scenarios = append(scenarios, buildScenario(
		49, "High Priority", catG, catGName,
		"High priority (10,000) workload. Significant priority penalty applied to reclamation score.",
		"Priority", "High priority factor penalty", "Priority: 10000",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "important-service", Namespace: "production", Priority: 10000,
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 40, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 50 Critical Priority
	scenarios = append(scenarios, buildScenario(
		50, "Critical Priority", catG, catGName,
		"Critical priority (1,000,000) workload. Safety gate blocks all disruption.",
		"Priority", "Critical priority safety exclusion", "Priority: 1000000",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "critical-cluster-agent", Namespace: "kube-system", Priority: 1000000,
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 40, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 51 Priority Boundary Case
	scenarios = append(scenarios, buildScenario(
		51, "Priority Boundary Case", catG, catGName,
		"Workload with priority set exactly at policy threshold boundary (1000).",
		"Priority", "Priority policy threshold boundary evaluation", "Priority: 1000 (boundary value)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "boundary-priority-job", Namespace: "default", Priority: 1000,
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 40, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY H — REPLICA / AVAILABILITY (52–57)
	// ══════════════════════════════════════════════════════════════════════════
	catH := "replicas"
	catHName := "Replica / Availability"

	// 52 Single Replica
	scenarios = append(scenarios, buildScenario(
		52, "Single Replica", catH, catHName,
		"Workload configured with a single replica (desired=1, available=1). Outage prevention blocks disruption.",
		"Replica", "Single replica outage prevention", "Replicas: 1 (desired=1, available=1)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "solo-service", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, DesiredReplicas: 1, AvailableReplicas: 1, Checkpointable: true,
			}),
		},
	))

	// 53 Two Replicas
	scenarios = append(scenarios, buildScenario(
		53, "Two Replicas", catH, catHName,
		"Workload with 2 replicas available. Evaluates minimum viable quorum score.",
		"Replica", "Two-replica quorum evaluation", "Replicas: 2 (desired=2, available=2)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "pair-service", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, DesiredReplicas: 2, AvailableReplicas: 2, Checkpointable: true,
			}),
		},
	))

	// 54 Three Replicas
	scenarios = append(scenarios, buildScenario(
		54, "Three Replicas", catH, catHName,
		"Standard production 3-replica deployment. Safe quorum yields healthy replica factor score.",
		"Replica", "Three-replica standard quorum", "Replicas: 3 (desired=3, available=3)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "triplet-service", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, DesiredReplicas: 3, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 55 Highly Replicated Workload
	scenarios = append(scenarios, buildScenario(
		55, "Highly Replicated Workload", catH, catHName,
		"Large scale deployment with 10 available replicas. High replica availability maximizes quorum score.",
		"Replica", "Large quorum replica score saturation", "Replicas: 10 (desired=10, available=10)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "high-scale-deployment", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 3, DesiredReplicas: 10, AvailableReplicas: 10, Checkpointable: true,
			}),
		},
	))

	// 56 Replica Availability Constraint
	scenarios = append(scenarios, buildScenario(
		56, "Replica Availability Constraint", catH, catHName,
		"Deployment with 5 desired replicas but only 1 ready/available. Quorum deficit triggers safety block.",
		"Replica Gate", "Replica degradation safety block", "Desired: 5, Available: 1",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "degraded-cluster-pod", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 1, DesiredReplicas: 5, ReadyReplicas: 1, AvailableReplicas: 1, Checkpointable: true,
			}),
		},
	))

	// 57 Replica Availability Permissive
	scenarios = append(scenarios, buildScenario(
		57, "Replica Availability Permissive", catH, catHName,
		"Workload with 5 desired and 5 available replicas. Full redundancy enables safe reclamation.",
		"Replica", "Permissive quorum verification", "Desired: 5, Available: 5 (100% healthy)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "resilient-worker", Namespace: "default", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true,
				DisruptionsAllowed: 2, DesiredReplicas: 5, ReadyReplicas: 5, AvailableReplicas: 5, Checkpointable: true,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY I — MIXED SIGNALS (58–66)
	// ══════════════════════════════════════════════════════════════════════════
	catI := "mixed"
	catIName := "Mixed Signals"

	// 58 Low CPU + High Network
	scenarios = append(scenarios, buildScenario(
		58, "Low CPU + High Network", catI, catIName,
		"Workload uses only 10m CPU but sustains 8 MB/s network throughput. Network activity overrides low CPU.",
		"Conflict", "Network activity overrides low CPU", "CPU: 10m, Network: 8 MB/s",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "proxy-node", Namespace: "routing", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 10.0, UsageMemMiB: 50,
				NetworkBps: 8388608, RequestQPS: 0.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 59 Low CPU + High QPS
	scenarios = append(scenarios, buildScenario(
		59, "Low CPU + High QPS", catI, catIName,
		"Workload uses only 10m CPU but processes 200 incoming requests/second. QPS signal overrides low CPU.",
		"Conflict", "Request throughput overrides low CPU", "CPU: 10m, QPS: 200.0",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "lightweight-router", Namespace: "routing", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 512, LimitMemMiB: 512, UsageCPU: 10.0, UsageMemMiB: 40,
				NetworkBps: 100000, RequestQPS: 200.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 60 Low Memory + Active Network
	scenarios = append(scenarios, buildScenario(
		60, "Low Memory + Active Network", catI, catIName,
		"Workload uses only 30 MiB memory but handles active network streaming (10 MB/s). Classified as ACTIVE.",
		"Conflict", "Network activity overrides low memory", "Memory: 30 MiB, Network: 10 MB/s",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "stream-relayer", Namespace: "streaming", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 100.0, UsageMemMiB: 30,
				NetworkBps: 10485760, RequestQPS: 0.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 61 High CPU + Long Idle Metadata
	scenarios = append(scenarios, buildScenario(
		61, "High CPU + Long Idle Metadata", catI, catIName,
		"Telemetry window records high CPU (1500m) despite stale idle metadata. Real CPU telemetry overrides stale duration.",
		"Conflict", "Active CPU telemetry overrides idle duration", "CPU: 1500m (active override)",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "awoken-worker", Namespace: "batch", ReqCPUMillis: 2000, LimitCPUMillis: 2000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 1500.0, UsageMemMiB: 300,
				NetworkBps: 1048576, RequestQPS: 10.0, IdleDurationSec: 0, IsIdle: false,
			}),
		},
	))

	// 62 Low CPU + Short Idle Duration
	scenarios = append(scenarios, buildScenario(
		62, "Low CPU + Short Idle Duration", catI, catIName,
		"Low CPU usage (15m) but only 10 seconds of idle duration. Kept as LOW_USAGE because duration < 30s min.",
		"Conflict", "Minimum idle duration requirement", "CPU: 15m, Idle duration: 10s < 30s",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "briefly-quiet-worker", Namespace: "batch", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 15.0, UsageMemMiB: 40,
				NetworkBps: 10.0, RequestQPS: 0.0, IdleDurationSec: 10, IsIdle: false,
			}),
		},
	))

	// 63 Low CPU + Low Memory + Recent Activity
	scenarios = append(scenarios, buildScenario(
		63, "Low CPU + Low Memory + Recent Activity", catI, catIName,
		"Low resource draw (5m CPU, 30 MiB mem) but recent request activity recorded 20s ago.",
		"Conflict", "Recent activity discrimination", "CPU: 5m, Mem: 30 MiB, Recent activity: 20s",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "just-finished-task", Namespace: "batch", ReqCPUMillis: 1000, LimitCPUMillis: 1000,
				ReqMemMiB: 1024, LimitMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30,
				NetworkBps: 50.0, RequestQPS: 1.0, IdleDurationSec: 20, IsIdle: false,
			}),
		},
	))

	// 64 Low CPU + Low Memory + High Priority
	scenarios = append(scenarios, buildScenario(
		64, "Low CPU + Low Memory + High Priority", catI, catIName,
		"Completely idle resource profile but carries priority 50,000. Priority penalty suppresses reclamation score.",
		"Conflict", "Priority penalty on idle workload", "Idle profile + Priority: 50000",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "vip-idle-node", Namespace: "production", Priority: 50000,
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 30, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 65 Low CPU + Low Memory + PDB Block
	scenarios = append(scenarios, buildScenario(
		65, "Low CPU + Low Memory + PDB Block", catI, catIName,
		"Idle resource profile but PDB disruptionsAllowed=0. Policy prevents disruption.",
		"Conflict", "PDB blocking on idle workload", "Idle profile + PDB DisruptionsAllowed: 0",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "pdb-locked-idle", Namespace: "production",
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 30, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 0, AvailableReplicas: 3, Checkpointable: true,
			}),
		},
	))

	// 66 Low CPU + Low Memory + StatefulSet
	scenarios = append(scenarios, buildScenario(
		66, "Low CPU + Low Memory + StatefulSet", catI, catIName,
		"Idle resource profile under StatefulSet controller without checkpoint support. Full reclaim blocked; falls back to soft reclaim.",
		"Conflict", "Stateful controller capability fallback", "Idle StatefulSet without checkpoint annotation",
		n1, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{
				Name: "idle-db-slave", Namespace: "database", OwnerKind: "StatefulSet",
				ReqCPUMillis: 1000, LimitCPUMillis: 1000, ReqMemMiB: 1024, LimitMemMiB: 1024,
				UsageCPU: 5.0, UsageMemMiB: 30, NetworkBps: 10.0, RequestQPS: 0.0,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: false,
			}),
		},
	))

	// ══════════════════════════════════════════════════════════════════════════
	// CATEGORY J — COMPLEX MULTI-WORKLOAD CLUSTERS (67–75)
	// ══════════════════════════════════════════════════════════════════════════
	catJ := "complex-clusters"
	catJName := "Complex Multi-Workload Clusters"

	// 67 Small Mixed Cluster (Preserves legacy scenario-15 / preset-g)
	scenarios = append(scenarios, buildScenario(
		67, "Small Mixed Cluster", catJ, catJName,
		"2 nodes running 6 workloads spanning active, low usage, idle reclaim, protected, and PDB-blocked states.",
		"Cluster", "Heterogeneous cluster scheduling matrix", "2 nodes, 6 heterogeneous workloads",
		twoNodes, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{Name: "api-server", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 1800.0, UsageMemMiB: 1500, RequestQPS: 120.0, IdleDurationSec: 0, IsIdle: false}),
			makeWorkload(WorkloadOpts{Name: "worker-active", NodeName: "node-compute-01", ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 800.0, UsageMemMiB: 600, RequestQPS: 20.0, IdleDurationSec: 0, IsIdle: false}),
			makeWorkload(WorkloadOpts{Name: "batch-reporter", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 4, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "protected-standby", NodeName: "node-compute-02", ReqCPUMillis: 1000, ReqMemMiB: 2048, UsageCPU: 2.0, UsageMemMiB: 30, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true, Protected: true}),
			makeWorkload(WorkloadOpts{Name: "pdb-locked-service", NodeName: "node-compute-02", ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 0, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "stateful-replica", NodeName: "node-compute-02", OwnerKind: "StatefulSet", ReqCPUMillis: 1500, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 60, IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: false}),
		},
	))

	// 68 Reclamation Pressure Cluster
	scenarios = append(scenarios, buildScenario(
		68, "Reclamation Pressure Cluster", catJ, catJName,
		"Nodes operating near capacity threshold. Multiple idle candidates present significant recovery headroom.",
		"Pressure", "High resource pressure reclamation recovery", "2 nodes at high allocation, 6 workloads",
		twoNodes, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{Name: "heavy-web", NodeName: "node-compute-01", ReqCPUMillis: 3000, ReqMemMiB: 4096, UsageCPU: 2800.0, UsageMemMiB: 3500, RequestQPS: 150.0, IdleDurationSec: 0, IsIdle: false}),
			makeWorkload(WorkloadOpts{Name: "idle-batch-1", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "idle-batch-2", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "heavy-db", NodeName: "node-compute-02", ReqCPUMillis: 3000, ReqMemMiB: 6144, UsageCPU: 2700.0, UsageMemMiB: 5000, RequestQPS: 100.0, IdleDurationSec: 0, IsIdle: false}),
			makeWorkload(WorkloadOpts{Name: "idle-cache-1", NodeName: "node-compute-02", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "idle-cache-2", NodeName: "node-compute-02", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
		},
	))

	// 69 Safety-Heavy Cluster
	scenarios = append(scenarios, buildScenario(
		69, "Safety-Heavy Cluster", catJ, catJName,
		"Cluster dominated by protected, critical-priority, and PDB-restricted workloads. Demonstrates safety gate dominance.",
		"Safety Cluster", "Multi-workload safety gating prevalence", "2 nodes, 6 safety-restricted workloads",
		twoNodes, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{Name: "protected-job-1", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, Protected: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "protected-job-2", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, Protected: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "pdb-blocked-1", NodeName: "node-compute-01", ReqCPUMillis: 1500, ReqMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 0, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "pdb-blocked-2", NodeName: "node-compute-02", ReqCPUMillis: 1500, ReqMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 0, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "critical-prio-1", NodeName: "node-compute-02", Priority: 200000, PriorityClassName: "system-cluster-critical", ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "singleton-1", NodeName: "node-compute-02", DesiredReplicas: 1, AvailableReplicas: 1, ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 5.0, UsageMemMiB: 30, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, Checkpointable: true}),
		},
	))

	// 70 Mixed Deployment + StatefulSet Cluster
	scenarios = append(scenarios, buildScenario(
		70, "Mixed Deployment + StatefulSet Cluster", catJ, catJName,
		"Even blend of Deployments and StatefulSets evaluating differing controller state factor scores.",
		"Cluster", "Controller kind scoring matrix", "2 nodes, 6 workloads (Deployments & StatefulSets)",
		twoNodes, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{Name: "deploy-idle-checkpoint", OwnerKind: "Deployment", NodeName: "node-compute-01", ReqCPUMillis: 1500, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "deploy-idle-nocheck", OwnerKind: "Deployment", NodeName: "node-compute-01", ReqCPUMillis: 1500, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: false}),
			makeWorkload(WorkloadOpts{Name: "deploy-active", OwnerKind: "Deployment", NodeName: "node-compute-01", ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 750.0, UsageMemMiB: 600, RequestQPS: 50.0, IdleDurationSec: 0, IsIdle: false}),
			makeWorkload(WorkloadOpts{Name: "stateful-idle-checkpoint", OwnerKind: "StatefulSet", NodeName: "node-compute-02", ReqCPUMillis: 1500, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "stateful-idle-nocheck", OwnerKind: "StatefulSet", NodeName: "node-compute-02", ReqCPUMillis: 1500, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: false}),
			makeWorkload(WorkloadOpts{Name: "stateful-active", OwnerKind: "StatefulSet", NodeName: "node-compute-02", ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 800.0, UsageMemMiB: 600, RequestQPS: 40.0, IdleDurationSec: 0, IsIdle: false}),
		},
	))

	// 71 High Idle Capacity Cluster
	scenarios = append(scenarios, buildScenario(
		71, "High Idle Capacity Cluster", catJ, catJName,
		"Cluster hosting multiple large batch analytics tasks that have completed their execution windows.",
		"High Reclaim", "Multi-workload high capacity recovery", "2 nodes, 4 large idle workloads holding 16 cores",
		twoNodes, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{Name: "hadoop-worker-1", NodeName: "node-compute-01", ReqCPUMillis: 4000, ReqMemMiB: 8192, UsageCPU: 10.0, UsageMemMiB: 100, IdleDurationSec: 14400, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "hadoop-worker-2", NodeName: "node-compute-01", ReqCPUMillis: 4000, ReqMemMiB: 8192, UsageCPU: 10.0, UsageMemMiB: 100, IdleDurationSec: 14400, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "spark-worker-1", NodeName: "node-compute-02", ReqCPUMillis: 4000, ReqMemMiB: 8192, UsageCPU: 10.0, UsageMemMiB: 100, IdleDurationSec: 14400, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "spark-worker-2", NodeName: "node-compute-02", ReqCPUMillis: 4000, ReqMemMiB: 8192, UsageCPU: 10.0, UsageMemMiB: 100, IdleDurationSec: 14400, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 3, Checkpointable: true}),
		},
	))

	// 72 Multiple Reclaim Candidates
	scenarios = append(scenarios, buildScenario(
		72, "Multiple Reclaim Candidates", catJ, catJName,
		"3 nodes hosting a wide spectrum of idle workloads with varying scores and controller types.",
		"Cluster", "Multi-candidate decision matrix", "3 nodes, 9 diverse workloads",
		fourNodes[:3], []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{Name: "cand-full-1", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-soft-1", NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: false}),
			makeWorkload(WorkloadOpts{Name: "cand-active-1", NodeName: "node-compute-01", ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 800.0, UsageMemMiB: 500, RequestQPS: 50.0, IdleDurationSec: 0, IsIdle: false}),
			makeWorkload(WorkloadOpts{Name: "cand-full-2", NodeName: "node-compute-02", ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-soft-2", NodeName: "node-compute-02", ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: false}),
			makeWorkload(WorkloadOpts{Name: "cand-active-2", NodeName: "node-compute-02", ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 800.0, UsageMemMiB: 500, RequestQPS: 50.0, IdleDurationSec: 0, IsIdle: false}),
			makeWorkload(WorkloadOpts{Name: "cand-full-3", NodeName: "node-compute-03", ReqCPUMillis: 4000, ReqMemMiB: 8192, UsageCPU: 5.0, UsageMemMiB: 80, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 4, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-blocked-1", NodeName: "node-compute-03", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 40, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 0, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-active-3", NodeName: "node-compute-03", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 1800.0, UsageMemMiB: 2000, RequestQPS: 100.0, IdleDurationSec: 0, IsIdle: false}),
		},
	))

	// 73 Competing Reclamation Candidates
	scenarios = append(scenarios, buildScenario(
		73, "Competing Reclamation Candidates", catJ, catJName,
		"Multiple workloads competing for prioritization with different sizes, idle durations, and priorities.",
		"Competition", "Prioritization ranking among candidates", "2 nodes, 6 competing candidates",
		twoNodes, []models.SyntheticWorkload{
			makeWorkload(WorkloadOpts{Name: "cand-high-priority", Priority: 5000, NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-low-priority", Priority: 10, NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-short-idle", Priority: 100, NodeName: "node-compute-01", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 120, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-large-size", Priority: 100, NodeName: "node-compute-02", ReqCPUMillis: 6000, ReqMemMiB: 12288, UsageCPU: 5.0, UsageMemMiB: 100, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-small-size", Priority: 100, NodeName: "node-compute-02", ReqCPUMillis: 500, ReqMemMiB: 512, UsageCPU: 2.0, UsageMemMiB: 20, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
			makeWorkload(WorkloadOpts{Name: "cand-stateful", OwnerKind: "StatefulSet", Priority: 100, NodeName: "node-compute-02", ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 50, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true}),
		},
	))

	// 74 Large Synthetic Cluster
	largeWorkloads := make([]models.SyntheticWorkload, 0, 20)
	nodeNames := []string{"node-compute-01", "node-compute-02", "node-compute-03", "node-compute-04"}
	for i := 1; i <= 20; i++ {
		targetNode := nodeNames[(i-1)%len(nodeNames)]
		switch i % 5 {
		case 0: // Active
			largeWorkloads = append(largeWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("web-service-%02d", i), Namespace: "prod", NodeName: targetNode,
				ReqCPUMillis: 1500, ReqMemMiB: 1024, UsageCPU: 1200.0, UsageMemMiB: 800,
				RequestQPS: 60.0, IdleDurationSec: 0, IsIdle: false,
			}))
		case 1: // Low Usage
			largeWorkloads = append(largeWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("api-replica-%02d", i), Namespace: "prod", NodeName: targetNode,
				ReqCPUMillis: 1000, ReqMemMiB: 1024, UsageCPU: 50.0, UsageMemMiB: 80,
				RequestQPS: 0.0, IdleDurationSec: 15, IsIdle: false,
			}))
		case 2: // Full Reclaim
			largeWorkloads = append(largeWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("batch-worker-%02d", i), Namespace: "analytics", NodeName: targetNode,
				ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 40,
				RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 2,
				AvailableReplicas: 3, Checkpointable: true,
			}))
		case 3: // Soft Reclaim
			largeWorkloads = append(largeWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("cache-worker-%02d", i), Namespace: "cache", NodeName: targetNode,
				ReqCPUMillis: 2000, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 50,
				RequestQPS: 0.0, IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1,
				AvailableReplicas: 3, Checkpointable: false,
			}))
		case 4: // Protected
			largeWorkloads = append(largeWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("db-standby-%02d", i), Namespace: "database", NodeName: targetNode,
				ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 60,
				RequestQPS: 0.0, IdleDurationSec: 7200, IsIdle: true, Protected: true,
				DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: true,
			}))
		}
	}
	scenarios = append(scenarios, buildScenario(
		74, "Large Synthetic Cluster", catJ, catJName,
		"4 nodes and 20 heterogeneous workloads demonstrating balanced cluster decision matrix.",
		"Large Cluster", "Full cluster decision matrix & capacity reclamation", "4 nodes, 20 heterogeneous workloads",
		fourNodes, largeWorkloads,
	))

	// 75 Stress Scenario
	stressWorkloads := make([]models.SyntheticWorkload, 0, 40)
	for i := 1; i <= 40; i++ {
		targetNode := fmt.Sprintf("node-compute-%02d", (i%8)+1)
		switch i % 4 {
		case 0:
			stressWorkloads = append(stressWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("stress-active-%02d", i), Namespace: "prod", NodeName: targetNode,
				ReqCPUMillis: 1500, ReqMemMiB: 1024, UsageCPU: 1300.0, UsageMemMiB: 700,
				RequestQPS: 80.0, IdleDurationSec: 0, IsIdle: false,
			}))
		case 1:
			stressWorkloads = append(stressWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("stress-idle-full-%02d", i), Namespace: "batch", NodeName: targetNode,
				ReqCPUMillis: 2000, ReqMemMiB: 4096, UsageCPU: 5.0, UsageMemMiB: 40,
				IdleDurationSec: 7200, IsIdle: true, DisruptionsAllowed: 2, AvailableReplicas: 4, Checkpointable: true,
			}))
		case 2:
			stressWorkloads = append(stressWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("stress-idle-soft-%02d", i), Namespace: "data", NodeName: targetNode,
				ReqCPUMillis: 1500, ReqMemMiB: 2048, UsageCPU: 5.0, UsageMemMiB: 50,
				IdleDurationSec: 3600, IsIdle: true, DisruptionsAllowed: 1, AvailableReplicas: 3, Checkpointable: false,
			}))
		case 3:
			stressWorkloads = append(stressWorkloads, makeWorkload(WorkloadOpts{
				Name: fmt.Sprintf("stress-blocked-%02d", i), Namespace: "ops", NodeName: targetNode,
				ReqCPUMillis: 1000, ReqMemMiB: 2048, UsageCPU: 2.0, UsageMemMiB: 30,
				IdleDurationSec: 7200, IsIdle: true, Protected: true, DisruptionsAllowed: 0, AvailableReplicas: 2,
			}))
		}
	}
	scenarios = append(scenarios, buildScenario(
		75, "Stress Scenario", catJ, catJName,
		"8 nodes hosting 40 workloads under continuous scheduling and reclamation evaluation.",
		"Stress", "Large scale multi-node decision stress testing", "8 nodes, 40 workloads across all kinds",
		eightNodes, stressWorkloads,
	))

	return scenarios
}

// ── Legacy Helper Wrappers for Backward Compatibility with Existing Tests ───

func scenario01Active() PresetScenario {
	p, _ := GetPresetByID("scenario-01")
	return *p
}

func scenario02LowUsage() PresetScenario {
	p, _ := GetPresetByID("scenario-02")
	return *p
}

func scenario03StrongFullReclaim() PresetScenario {
	p, _ := GetPresetByID("scenario-03")
	return *p
}

func scenario04ProtectedIdle() PresetScenario {
	p, _ := GetPresetByID("scenario-18")
	return *p
}

func scenario05PDBBlocked() PresetScenario {
	p, _ := GetPresetByID("scenario-19")
	return *p
}

func scenario06CheckpointUnsupported() PresetScenario {
	p, _ := GetPresetByID("scenario-28")
	return *p
}

func scenario07StatefulCheckpointable() PresetScenario {
	p, _ := GetPresetByID("scenario-29")
	return *p
}

func scenario08ReplicaSafety() PresetScenario {
	p, _ := GetPresetByID("scenario-21")
	return *p
}

func scenario09HighPriority() PresetScenario {
	p, _ := GetPresetByID("scenario-23")
	return *p
}

func scenario10RecentlyActive() PresetScenario {
	p, _ := GetPresetByID("scenario-04")
	return *p
}

func scenario11NetworkActive() PresetScenario {
	p, _ := GetPresetByID("scenario-33")
	return *p
}

func scenario12NonRunning() PresetScenario {
	p, _ := GetPresetByID("scenario-25")
	return *p
}

func scenario13ZeroResourceRequest() PresetScenario {
	p, _ := GetPresetByID("scenario-39")
	return *p
}

func scenario14Borderline() PresetScenario {
	p, _ := GetPresetByID("scenario-17")
	return *p
}

func scenario15MixedCluster() PresetScenario {
	p, _ := GetPresetByID("scenario-67")
	return *p
}
