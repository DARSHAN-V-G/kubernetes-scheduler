// State management for the Simulator Dashboard
// Strictly holds presentation state — zero mathematical decision calculations.

class SimulatorState {
  constructor() {
    this.cluster = {
      nodes: [],
      workloads: [],
    };
    this.activePresetId = "scenario-10";
    this.activePresetName = "SCENARIO 10: Strong Full Reclaim Candidate";
    this.presets = [];
    this.simulationResult = null;
    this.policy = null;
    this.isSimulating = false;
    this.lastSimulationTimestamp = null;
    this.pipelineStatus = "ready"; // "ready" | "running" | "completed" | "error"
    this.pipelineErrorMessage = null;

    // View & UI selection
    this.activeView = "overview"; // "overview" | "lab" | "history"
    this.selectedWorkloadIndex = null; // Drawer opens when not null

    // Table Filtering & Sorting
    this.searchQuery = "";
    this.filterClassification = "ALL";
    this.filterAction = "ALL";
    this.filterNode = "ALL";
    this.filterKind = "ALL";
    this.sortField = "name";
    this.sortAsc = true;

    // Scenario Lab Filtering
    this.labCategoryFilter = "ALL";
    this.labSearchQuery = "";

    // Client-side session run history (observability only)
    this.history = [];

    this.subscribers = [];
  }

  subscribe(callback) {
    this.subscribers.push(callback);
  }

  notify() {
    for (const cb of this.subscribers) {
      cb(this);
    }
  }

  setPolicy(policy) {
    this.policy = policy;
    this.notify();
  }

  setPresets(presets) {
    this.presets = presets;
    this.notify();
  }

  setCluster(cluster, presetId = null, presetName = null) {
    this.cluster = JSON.parse(JSON.stringify(cluster));
    if (presetId) this.activePresetId = presetId;
    if (presetName) this.activePresetName = presetName;
    this.simulationResult = null;
    this.selectedWorkloadIndex = null;
    this.pipelineStatus = "ready";
    this.pipelineErrorMessage = null;
    this.notify();
  }

  setSimulationResult(result) {
    this.simulationResult = result;
    this.isSimulating = false;
    this.pipelineStatus = "completed";
    this.pipelineErrorMessage = null;
    this.lastSimulationTimestamp = new Date().toLocaleTimeString();

    // Record session history entry
    if (result && result.clusterSummary) {
      const s = result.clusterSummary;
      this.history.unshift({
        runId: this.history.length + 1,
        timestamp: this.lastSimulationTimestamp,
        scenarioId: this.activePresetId,
        scenarioName: this.activePresetName || this.activePresetId,
        totalWorkloads: s.totalWorkloads,
        countKeep: s.countKeep,
        countSoft: s.countSoftReclaim,
        countFull: s.countFullReclaim,
        countBlocked: s.countSafetyBlocked,
        clusterSnapshot: JSON.parse(JSON.stringify(this.cluster)),
        resultSnapshot: JSON.parse(JSON.stringify(result)),
      });
    }

    this.notify();
  }

  loadHistoryRun(runId) {
    const entry = this.history.find(h => h.runId === runId);
    if (!entry) return;
    if (entry.clusterSnapshot) {
      this.cluster = JSON.parse(JSON.stringify(entry.clusterSnapshot));
    }
    if (entry.resultSnapshot) {
      this.simulationResult = JSON.parse(JSON.stringify(entry.resultSnapshot));
    }
    this.activePresetId = entry.scenarioId;
    this.activePresetName = entry.scenarioName;
    this.activeView = "overview";
    this.pipelineStatus = "completed";
    this.notify();
  }

  setSimulating(loading) {
    this.isSimulating = loading;
    if (loading) {
      this.pipelineStatus = "running";
      this.pipelineErrorMessage = null;
    }
    this.notify();
  }

  setSimulationError(msg) {
    this.isSimulating = false;
    this.pipelineStatus = "error";
    this.pipelineErrorMessage = msg;
    this.notify();
  }

  setActiveView(view) {
    this.activeView = view;
    this.notify();
  }

  setSelectedWorkload(index) {
    this.selectedWorkloadIndex = index;
    this.notify();
  }

  setTableSearch(q) {
    this.searchQuery = q.toLowerCase();
    this.notify();
  }

  setFilterClassification(c) {
    this.filterClassification = c;
    this.notify();
  }

  setFilterAction(a) {
    this.filterAction = a;
    this.notify();
  }

  setFilterNode(n) {
    this.filterNode = n;
    this.notify();
  }

  setFilterKind(k) {
    this.filterKind = k;
    this.notify();
  }

  setLabCategoryFilter(cat) {
    this.labCategoryFilter = cat;
    this.notify();
  }

  setLabSearch(q) {
    this.labSearchQuery = q.toLowerCase();
    this.notify();
  }

  setSort(field) {
    if (this.sortField === field) {
      this.sortAsc = !this.sortAsc;
    } else {
      this.sortField = field;
      this.sortAsc = true;
    }
    this.notify();
  }

  addNode(node) {
    this.cluster.nodes.push(node);
    this.simulationResult = null;
    this.pipelineStatus = "ready";
    this.notify();
  }

  removeNode(name) {
    this.cluster.nodes = this.cluster.nodes.filter(n => n.name !== name);
    for (const w of this.cluster.workloads) {
      if (w.nodeName === name) {
        w.nodeName = this.cluster.nodes[0] ? this.cluster.nodes[0].name : "unassigned";
      }
    }
    this.simulationResult = null;
    this.pipelineStatus = "ready";
    this.notify();
  }

  addWorkload(workload) {
    this.cluster.workloads.push(workload);
    this.simulationResult = null;
    this.pipelineStatus = "ready";
    this.notify();
  }

  removeWorkload(name, namespace) {
    this.cluster.workloads = this.cluster.workloads.filter(
      w => !(w.name === name && w.namespace === namespace)
    );
    this.simulationResult = null;
    this.pipelineStatus = "ready";
    this.notify();
  }
}

export const state = new SimulatorState();
