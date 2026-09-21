// Main UI Controller for the Simulator Dashboard
// Enterprise Observability Aesthetic — strictly consumes backend pipeline outputs.
// Pure presentation and interaction layer with zero decision formulas or threshold logic.

import { api } from "./api.js?v=2";
import { state } from "./state.js?v=2";

// ── Formatters (Presentation only) ──────────────────────────────────────────
function formatCPU(millicores) {
  if (millicores === undefined || millicores === null) return "-";
  if (millicores >= 1000) {
    return `${(millicores / 1000).toFixed(2)} cores`;
  }
  return `${Math.round(millicores)}m`;
}

function formatMemory(bytes) {
  if (bytes === undefined || bytes === null) return "-";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(2)} GiB`;
  const mb = bytes / (1024 * 1024);
  return `${Math.round(mb)} MiB`;
}

function formatDuration(seconds) {
  if (!seconds || seconds <= 0) return "0s";
  if (seconds < 60) return `${seconds}s`;
  const mins = Math.floor(seconds / 60);
  if (mins < 60) {
    const s = seconds % 60;
    return `${String(mins).padStart(2, "0")}m ${String(s).padStart(2, "0")}s`;
  }
  const hrs = Math.floor(seconds / 3600);
  const remMins = Math.floor((seconds % 3600) / 60);
  return `${String(hrs).padStart(2, "0")}h ${String(remMins).padStart(2, "0")}m`;
}

function formatScore(score) {
  if (score === undefined || score === null || isNaN(score)) return "-";
  return Number(score).toFixed(3);
}

// ── Global Sort Exposure for Inline HTML Handler ───────────────────────────
window.setSort = (field) => {
  state.setSort(field);
};

// Factor explanatory text (presentation metadata only)
const FACTOR_INFO = {
  CPU: { code: "R_CPU", title: "CPU Utilization", desc: "Lower utilization yields higher reclamation incentive" },
  Memory: { code: "R_Memory", title: "Memory Working Set", desc: "Lower memory consumption yields higher reclamation incentive" },
  Idle: { code: "R_Idle", title: "Idle Duration", desc: "Longer continuous inactivity increases confidence" },
  Benefit: { code: "R_Benefit", title: "Resource Benefit", desc: "Absolute volume of allocatable resources recovered" },
  Replica: { code: "R_Replica", title: "Replica Quorum", desc: "Higher available quorum allows safer disruption" },
  Priority: { code: "R_Priority", title: "Priority Class", desc: "Lower priority workloads are prioritized for reclamation" },
  PDB: { code: "R_PDB", title: "PDB Disruption Budget", desc: "Disruptions allowed by PodDisruptionBudget policy" },
  State: { code: "R_State", title: "Controller Lifecycle", desc: "Stateless Deployments score higher than stateful controllers" },
  Checkpoint: { code: "R_Checkpoint", title: "CRIU Compatibility", desc: "Explicit checkpoint annotation support enables stateful migration" },
};

// ── Application Lifecycle ───────────────────────────────────────────────────
document.addEventListener("DOMContentLoaded", async () => {
  try {
    // 1. Fetch system health and policy metadata from Go backend
    const health = await api.getHealth();
    if (health.policy) {
      state.setPolicy(health.policy);
    }

    // 2. Fetch all 75 preset scenarios from Go backend
    const presets = await api.getPresets();
    state.setPresets(presets);
    populateScenarioDropdown(presets);

    // 3. Load default scenario (scenario-10: Strong Full Reclaim, or first available)
    const defaultScenario = presets.find(p => p.id === "scenario-10") || presets[0];
    if (defaultScenario) {
      await loadScenario(defaultScenario.id, defaultScenario.name);
    }

    // 4. Register reactive render subscriber
    state.subscribe(renderDashboard);

    // 5. Setup event bindings
    bindEvents();

    // 6. Initial render
    renderDashboard(state);
  } catch (err) {
    console.error("Initialization error:", err);
    showErrorBanner(`Failed to connect to simulator backend on port 8082: ${err.message}`);
  }
});

function populateScenarioDropdown(presets) {
  const select = document.getElementById("select-scenario");
  if (!select) return;
  select.innerHTML = "";
  for (const p of presets) {
    const opt = document.createElement("option");
    opt.value = p.id;
    opt.textContent = p.name;
    if (p.id === state.activePresetId) opt.selected = true;
    select.appendChild(opt);
  }
}

async function loadScenario(id, name = null) {
  try {
    const scenario = await api.getPreset(id);
    state.setCluster(scenario.cluster, id, name || scenario.metadata.name);

    const select = document.getElementById("select-scenario");
    if (select) select.value = id;

    // Trigger simulation through real Go pipeline
    await executeSimulation();
  } catch (err) {
    console.error("Failed to load scenario:", err);
    showErrorBanner(`Error loading preset scenario ${id}: ${err.message}`);
  }
}

// ── Simulation Execution (Constraint 5: No fake timers) ────────────────────
async function executeSimulation() {
  if (state.isSimulating) return;

  const btnSim = document.getElementById("btn-run-sim");
  const btnText = document.getElementById("btn-run-sim-text");
  const pipelineContainer = document.getElementById("pipeline-strip-container");

  state.setSimulating(true);

  if (btnSim) btnSim.disabled = true;
  if (btnText) btnText.textContent = "RUNNING SIMULATION...";
  if (pipelineContainer) {
    pipelineContainer.className = "pipeline-strip-container running";
  }

  try {
    // Invoke authoritative Go pipeline (/api/simulate)
    const simFn = (typeof api.simulate === "function") 
      ? api.simulate.bind(api) 
      : (typeof api.runSimulation === "function" ? api.runSimulation.bind(api) : null);
    
    if (!simFn) {
      throw new Error("Neither api.simulate nor api.runSimulation is available");
    }
    const result = await simFn(state.cluster);
    state.setSimulationResult(result);

    if (pipelineContainer) {
      pipelineContainer.className = "pipeline-strip-container completed";
    }
  } catch (err) {
    console.error("Simulation failed:", err);
    state.setSimulationError(err.message);
    if (pipelineContainer) {
      pipelineContainer.className = "pipeline-strip-container error";
    }
    showErrorBanner(`Simulation execution failed: ${err.message}`);
  } finally {
    if (btnSim) btnSim.disabled = false;
    if (btnText) btnText.textContent = "RUN SIMULATION";
  }
}

// ── Event Bindings ─────────────────────────────────────────────────────────
function bindEvents() {
  // Navigation Tabs
  document.querySelectorAll(".nav-tab-btn").forEach(btn => {
    btn.addEventListener("click", () => {
      const targetView = btn.getAttribute("data-view");
      state.setActiveView(targetView);
    });
  });

  // Scenario Dropdown Change
  const selectScenario = document.getElementById("select-scenario");
  if (selectScenario) {
    selectScenario.addEventListener("change", (e) => {
      const selected = state.presets.find(p => p.id === e.target.value);
      if (selected) {
        loadScenario(selected.id, selected.name);
      }
    });
  }

  // Run Simulation Button
  const btnRun = document.getElementById("btn-run-sim");
  if (btnRun) {
    btnRun.addEventListener("click", () => {
      executeSimulation();
    });
  }

  // Table Search Input
  const tableSearch = document.getElementById("table-search");
  if (tableSearch) {
    tableSearch.addEventListener("input", (e) => {
      state.setTableSearch(e.target.value);
    });
  }

  // Table Filter Selects
  const filterClass = document.getElementById("filter-classification");
  if (filterClass) {
    filterClass.addEventListener("change", (e) => {
      state.setFilterClassification(e.target.value);
    });
  }

  const filterAct = document.getElementById("filter-action");
  if (filterAct) {
    filterAct.addEventListener("change", (e) => {
      state.setFilterAction(e.target.value);
    });
  }

  const filterNode = document.getElementById("filter-node");
  if (filterNode) {
    filterNode.addEventListener("change", (e) => {
      state.setFilterNode(e.target.value);
    });
  }

  const filterKind = document.getElementById("filter-kind");
  if (filterKind) {
    filterKind.addEventListener("change", (e) => {
      state.setFilterKind(e.target.value);
    });
  }

  // Scenario Lab Category Pills
  document.querySelectorAll(".category-pill").forEach(pill => {
    pill.addEventListener("click", () => {
      document.querySelectorAll(".category-pill").forEach(p => p.classList.remove("active"));
      pill.classList.add("active");
      state.setLabCategoryFilter(pill.getAttribute("data-category"));
    });
  });

  // Scenario Lab Search Input
  const labSearch = document.getElementById("lab-search");
  if (labSearch) {
    labSearch.addEventListener("input", (e) => {
      state.setLabSearch(e.target.value);
    });
  }

  // Drawer Close Button & Backdrop
  const btnCloseDrawer = document.getElementById("btn-close-drawer");
  const drawerBackdrop = document.getElementById("drawer-backdrop");
  if (btnCloseDrawer) {
    btnCloseDrawer.addEventListener("click", closeDrawer);
  }
  if (drawerBackdrop) {
    drawerBackdrop.addEventListener("click", closeDrawer);
  }

  // Add Node Modal
  const btnAddNode = document.getElementById("btn-modal-add-node");
  const modalNode = document.getElementById("modal-add-node");
  if (btnAddNode && modalNode) {
    btnAddNode.addEventListener("click", () => openModal(modalNode));
  }

  // Add Workload Modal
  const btnAddWorkload = document.getElementById("btn-modal-add-workload");
  const modalWorkload = document.getElementById("modal-add-workload");
  if (btnAddWorkload && modalWorkload) {
    btnAddWorkload.addEventListener("click", () => {
      populateModalNodeSelect();
      openModal(modalWorkload);
    });
  }

  // Custom Scenario Builder Modal
  const btnCustomScenario = document.getElementById("btn-modal-custom-scenario");
  const modalCustom = document.getElementById("modal-custom-scenario");
  if (btnCustomScenario && modalCustom) {
    btnCustomScenario.addEventListener("click", () => {
      populateBuilderNodeSelect();
      openModal(modalCustom);
    });
  }

  // Modal Cancel & Close Buttons
  document.querySelectorAll(".modal-close, .btn-modal-cancel").forEach(btn => {
    btn.addEventListener("click", () => {
      const modal = btn.closest(".modal-overlay");
      if (modal) closeModal(modal);
    });
  });

  // Add Node Form Submit
  const formAddNode = document.getElementById("form-add-node");
  if (formAddNode) {
    formAddNode.addEventListener("submit", (e) => {
      e.preventDefault();
      const fd = new FormData(formAddNode);
      const name = fd.get("name").trim();
      const cpu = parseInt(fd.get("cpu"), 10);
      const memGiB = parseInt(fd.get("mem"), 10);

      state.addNode({
        name,
        totalCapacityCpuMillis: cpu,
        totalCapacityMemoryBytes: memGiB * 1024 * 1024 * 1024,
        allocatableCpuMillis: cpu - 200,
        allocatableMemoryBytes: (memGiB * 1024 * 1024 * 1024) - (512 * 1024 * 1024),
        actualUsageCpuMillicores: 100.0,
        actualUsageMemoryBytes: 1024 * 1024 * 1024,
        isReady: true,
      });

      formAddNode.reset();
      closeModal(modalNode);
      executeSimulation();
    });
  }

  // Add Workload Form Submit
  const formAddWorkload = document.getElementById("form-add-workload");
  if (formAddWorkload) {
    formAddWorkload.addEventListener("submit", (e) => {
      e.preventDefault();
      const fd = new FormData(formAddWorkload);

      const isCheckpointable = fd.get("isCheckpointable") === "on";
      const isProtected = fd.get("isProtected") === "on";

      const annotations = {};
      if (isCheckpointable) annotations["reclaim.io/checkpointable"] = "true";
      if (isProtected) annotations["reclaim.io/protected"] = "true";

      const reqCpu = parseInt(fd.get("reqCpu"), 10);
      const usageCpu = parseFloat(fd.get("usageCpu"));
      const reqMemMiB = parseInt(fd.get("reqMem"), 10);
      const usageMemMiB = parseInt(fd.get("usageMem"), 10);
      const idleDuration = parseInt(fd.get("idleDuration"), 10);
      const qps = parseFloat(fd.get("qps") || 0);
      const network = parseFloat(fd.get("network") || 0);
      const replicas = parseInt(fd.get("replicas") || 3, 10);
      const pdb = parseInt(fd.get("pdb") || 1, 10);
      const priority = parseInt(fd.get("priority") || 100, 10);

      state.addWorkload({
        name: fd.get("name").trim(),
        namespace: fd.get("namespace").trim(),
        nodeName: fd.get("nodeName"),
        ownerKind: fd.get("ownerKind"),
        ownerName: fd.get("name").trim(),
        phase: "Running",
        qosClass: reqCpu > 0 && reqMemMiB > 0 ? "Burstable" : "BestEffort",
        priority,
        priorityClassName: priority >= 1000 ? "high-priority" : "default",
        disruptionsAllowed: pdb,
        desiredReplicas: replicas,
        readyReplicas: replicas,
        availableReplicas: replicas,
        requestedCpuMillis: reqCpu,
        limitCpuMillis: reqCpu,
        requestedMemoryBytes: reqMemMiB * 1024 * 1024,
        limitMemoryBytes: reqMemMiB * 1024 * 1024,
        usageCpuMillicores: usageCpu,
        usageMemoryBytes: usageMemMiB * 1024 * 1024,
        networkBytesPerSec: network,
        requestQps: qps,
        idleDurationSeconds: idleDuration,
        isIdle: idleDuration > 60 && usageCpu < 100,
        labels: { app: fd.get("name").trim() },
        annotations,
      });

      formAddWorkload.reset();
      closeModal(modalWorkload);
      executeSimulation();
    });
  }

  // Custom Scenario Builder Form Submit
  const formCustomScenario = document.getElementById("form-custom-scenario");
  if (formCustomScenario) {
    formCustomScenario.addEventListener("submit", (e) => {
      e.preventDefault();
      const fd = new FormData(formCustomScenario);

      const isCheckpointable = fd.get("b_checkpointable") === "on";
      const isProtected = fd.get("b_protected") === "on";

      const annotations = {};
      if (isCheckpointable) annotations["reclaim.io/checkpointable"] = "true";
      if (isProtected) annotations["reclaim.io/protected"] = "true";

      const reqCpu = parseInt(fd.get("b_reqCpu") || 0, 10);
      const limitCpu = parseInt(fd.get("b_limitCpu") || reqCpu, 10);
      const reqMemMiB = parseInt(fd.get("b_reqMem") || 0, 10);
      const limitMemMiB = parseInt(fd.get("b_limitMem") || reqMemMiB, 10);
      const usageCpu = parseFloat(fd.get("b_usageCpu") || 0);
      const usageMemMiB = parseInt(fd.get("b_usageMem") || 0, 10);
      const network = parseFloat(fd.get("b_network") || 0);
      const qps = parseFloat(fd.get("b_qps") || 0);
      const idleDuration = parseInt(fd.get("b_idleDuration") || 0, 10);
      const desiredReplicas = parseInt(fd.get("b_desiredReplicas") || 3, 10);
      const readyReplicas = parseInt(fd.get("b_readyReplicas") || desiredReplicas, 10);
      const availableReplicas = parseInt(fd.get("b_availableReplicas") || desiredReplicas, 10);
      const pdb = parseInt(fd.get("b_pdb") || 1, 10);
      const priority = parseInt(fd.get("b_priority") || 100, 10);
      const priorityClass = fd.get("b_priorityClass") || (priority >= 1000 ? "high-priority" : "default");
      const phase = fd.get("b_phase") || "Running";
      const name = fd.get("b_name").trim();
      const namespace = fd.get("b_namespace").trim();
      const ownerKind = fd.get("b_ownerKind");
      const targetNode = fd.get("b_nodeName") || (state.cluster.nodes[0] ? state.cluster.nodes[0].name : "node-compute-01");

      state.addWorkload({
        name,
        namespace,
        nodeName: targetNode,
        ownerKind,
        ownerName: name,
        phase,
        qosClass: reqCpu > 0 && reqMemMiB > 0 ? "Burstable" : "BestEffort",
        priority,
        priorityClassName: priorityClass,
        disruptionsAllowed: pdb,
        desiredReplicas,
        readyReplicas,
        availableReplicas,
        requestedCpuMillis: reqCpu,
        limitCpuMillis: limitCpu,
        requestedMemoryBytes: reqMemMiB * 1024 * 1024,
        limitMemoryBytes: limitMemMiB * 1024 * 1024,
        usageCpuMillicores: usageCpu,
        usageMemoryBytes: usageMemMiB * 1024 * 1024,
        networkBytesPerSec: network,
        requestQps: qps,
        idleDurationSeconds: idleDuration,
        isIdle: idleDuration > 60 && usageCpu < 100,
        labels: { app: name },
        annotations,
      });

      closeModal(modalCustom);
      executeSimulation();
    });
  }
}

function openModal(modal) {
  if (!modal) return;
  modal.classList.add("active");
}

function closeModal(modal) {
  if (!modal) return;
  modal.classList.remove("active");
}

function populateModalNodeSelect() {
  const select = document.querySelector("#modal-add-workload select[name='nodeName']");
  if (!select) return;
  select.innerHTML = "";
  for (const n of state.cluster.nodes) {
    const opt = document.createElement("option");
    opt.value = n.name;
    opt.textContent = n.name;
    select.appendChild(opt);
  }
}

function populateBuilderNodeSelect() {
  const select = document.querySelector("#modal-custom-scenario select[name='b_nodeName']");
  if (!select) return;
  select.innerHTML = "";
  for (const n of state.cluster.nodes) {
    const opt = document.createElement("option");
    opt.value = n.name;
    opt.textContent = n.name;
    select.appendChild(opt);
  }
}

function closeDrawer() {
  const drawer = document.getElementById("drawer-panel");
  const backdrop = document.getElementById("drawer-backdrop");
  if (drawer) drawer.classList.remove("open");
  if (backdrop) backdrop.classList.remove("active");
  state.setSelectedWorkload(null);
}

function openDrawer(index) {
  state.setSelectedWorkload(index);
  const drawer = document.getElementById("drawer-panel");
  const backdrop = document.getElementById("drawer-backdrop");
  if (drawer) drawer.classList.add("open");
  if (backdrop) backdrop.classList.add("active");
}

// ── Master Render Dispatcher ───────────────────────────────────────────────
function renderDashboard(currentState) {
  // Update view navigation tabs
  document.querySelectorAll(".nav-tab-btn").forEach(btn => {
    btn.classList.toggle("active", btn.getAttribute("data-view") === currentState.activeView);
  });

  document.querySelectorAll(".view-panel").forEach(panel => {
    panel.classList.toggle("active", panel.getAttribute("data-view-panel") === currentState.activeView);
  });

  // Render System Status Strip
  renderSystemStatus(currentState);

  // Render Cluster Overview (View 1)
  renderExecutiveMetrics(currentState);
  renderResourceProjection(currentState);
  renderTopologyTree(currentState);
  renderWorkloadTable(currentState);

  // Render Scenario Lab (View 2)
  renderScenarioLab(currentState);

  // Render History (View 3)
  renderHistoryTable(currentState);

  // Render Drawer if a workload is selected
  if (currentState.selectedWorkloadIndex !== null && currentState.simulationResult) {
    renderDrawer(currentState);
  }
}

// ── Render Components ──────────────────────────────────────────────────────

function renderSystemStatus(currentState) {
  const simState = document.getElementById("status-sim-state");
  const nodeCount = document.getElementById("status-node-count");
  const wlCount = document.getElementById("status-workload-count");
  const lastRun = document.getElementById("status-last-run");

  if (simState) {
    if (currentState.isSimulating) {
      simState.textContent = "RUNNING...";
      simState.className = "status-item-value mono";
      simState.style.color = "var(--color-brand)";
    } else if (currentState.simulationResult) {
      simState.textContent = "EVALUATED";
      simState.className = "status-item-value mono highlight";
      simState.style.color = "var(--color-idle)";
    } else {
      simState.textContent = "READY";
      simState.className = "status-item-value mono";
      simState.style.color = "var(--text-muted)";
    }
  }

  if (nodeCount) nodeCount.textContent = currentState.cluster.nodes.length;
  if (wlCount) wlCount.textContent = currentState.cluster.workloads.length;
  if (lastRun) lastRun.textContent = currentState.lastSimulationTimestamp || "Not Evaluated";

  // Also update Overview cards
  const ovNodes = document.getElementById("ov-node-count");
  const ovWls = document.getElementById("ov-workload-count");
  if (ovNodes) ovNodes.textContent = currentState.cluster.nodes.length;
  if (ovWls) ovWls.textContent = currentState.cluster.workloads.length;
}

function renderExecutiveMetrics(currentState) {
  const summary = currentState.simulationResult ? currentState.simulationResult.clusterSummary : null;

  const setVal = (id, val) => {
    const el = document.getElementById(id);
    if (el) el.textContent = val !== null && val !== undefined ? val : "-";
  };

  if (summary) {
    setVal("stat-val-active", summary.countActive);
    setVal("stat-val-low", summary.countLowUsage);
    setVal("stat-val-idle", summary.countIdle);

    setVal("stat-val-keep", summary.countKeep);
    setVal("stat-val-soft", summary.countSoftReclaim);
    setVal("stat-val-full", summary.countFullReclaim);

    setVal("stat-val-passed", summary.totalWorkloads - summary.countSafetyBlocked);
    setVal("stat-val-blocked", summary.countSafetyBlocked);

    setVal("stat-val-reclaim-cpu", formatCPU(summary.totalReclaimableCpuMillicores));
    setVal("stat-val-reclaim-mem", formatMemory(summary.totalReclaimableMemBytes));

    setVal("ov-cpu-cap", formatCPU(summary.totalCapacityCpuMillis));
    setVal("ov-mem-cap", formatMemory(summary.totalCapacityMemoryBytes));
  } else {
    // Zero-state values when not simulated
    setVal("stat-val-active", "-");
    setVal("stat-val-low", "-");
    setVal("stat-val-idle", "-");
    setVal("stat-val-keep", "-");
    setVal("stat-val-soft", "-");
    setVal("stat-val-full", "-");
    setVal("stat-val-passed", "-");
    setVal("stat-val-blocked", "-");
    setVal("stat-val-reclaim-cpu", "-");
    setVal("stat-val-reclaim-mem", "-");
    setVal("ov-cpu-cap", "-");
    setVal("ov-mem-cap", "-");
  }
}

// Constraint 4: Theoretical resource impact values directly from Go backend summary
function renderResourceProjection(currentState) {
  const summary = currentState.simulationResult ? currentState.simulationResult.clusterSummary : null;

  const setVal = (id, val) => {
    const el = document.getElementById(id);
    if (el) el.textContent = val;
  };

  if (summary) {
    setVal("proj-cap-cpu", formatCPU(summary.totalCapacityCpuMillis));
    setVal("proj-cap-mem", formatMemory(summary.totalCapacityMemoryBytes));

    setVal("proj-used-cpu", formatCPU(summary.totalUsageCpuMillicores));
    setVal("proj-used-mem", formatMemory(summary.totalUsageMemoryBytes));

    setVal("proj-reclaim-cpu", formatCPU(summary.totalReclaimableCpuMillicores));
    setVal("proj-reclaim-mem", formatMemory(summary.totalReclaimableMemBytes));

    setVal("proj-post-cpu", formatCPU(summary.projectedAvailableCpuMillis));
    setVal("proj-post-mem", formatMemory(summary.projectedAvailableMemBytes));
  } else {
    setVal("proj-cap-cpu", "-");
    setVal("proj-cap-mem", "-");
    setVal("proj-used-cpu", "-");
    setVal("proj-used-mem", "-");
    setVal("proj-reclaim-cpu", "-");
    setVal("proj-reclaim-mem", "-");
    setVal("proj-post-cpu", "-");
    setVal("proj-post-mem", "-");
  }
}

// Cluster Topology View (Part 8)
function renderTopologyTree(currentState) {
  const container = document.getElementById("topology-tree-grid");
  if (!container) return;
  container.innerHTML = "";

  const nodeSummaries = currentState.simulationResult ? currentState.simulationResult.nodeSummaries : [];
  const workloads = currentState.simulationResult ? currentState.simulationResult.workloads : [];

  for (const node of currentState.cluster.nodes) {
    const summary = nodeSummaries.find(s => s.nodeName === node.name);
    const nodeWorkloads = workloads.filter(w => w.nodeName === node.name);

    const card = document.createElement("div");
    card.className = "node-topology-card";

    // Usage percentages
    const totalCPU = node.totalCapacityCpuMillis || 8000;
    const usedCPU = node.actualUsageCpuMillicores || 150;
    const cpuPct = Math.min(100, Math.round((usedCPU / totalCPU) * 100));

    const totalMem = node.totalCapacityMemoryBytes || (16 * 1024 * 1024 * 1024);
    const usedMem = node.actualUsageMemoryBytes || (2 * 1024 * 1024 * 1024);
    const memPct = Math.min(100, Math.round((usedMem / totalMem) * 100));

    const reclaimCPU = summary ? summary.simulatedReclaimableCpuMillicores : 0;
    const reclaimMem = summary ? summary.simulatedReclaimableMemBytes : 0;

    let workloadsListHTML = "";
    if (nodeWorkloads.length === 0) {
      workloadsListHTML = `<div style="font-size: 11px; color: var(--text-dim); padding: 0.25rem 0;">No workloads scheduled on this node</div>`;
    } else {
      for (const w of nodeWorkloads) {
        const actionBadgeClass = w.action === "FULL_RECLAIM" ? "badge-full" : (w.action === "SOFT_RECLAIM" ? "badge-soft" : "badge-keep");
        const classBadgeClass = w.classification === "ACTIVE" ? "badge-active" : (w.classification === "LOW_USAGE" ? "badge-low" : "badge-idle");

        workloadsListHTML += `
          <div class="node-workload-row">
            <div style="display: flex; align-items: center; gap: 0.4rem; min-width: 0;">
              <span style="font-size: 9px; color: var(--text-dim); text-transform: uppercase;">${w.ownerKind || "Pod"}</span>
              <span class="workload-tree-name" title="${w.name}">${w.name}</span>
            </div>
            <div class="workload-tree-badges">
              <span class="status-badge ${classBadgeClass}">${w.classification}</span>
              <span class="status-badge ${actionBadgeClass}">${w.action}</span>
              <span class="mono" style="font-size: 10px; color: var(--color-brand);">${formatScore(w.score)}</span>
            </div>
          </div>
        `;
      }
    }

    card.innerHTML = `
      <div class="node-card-header">
        <div class="node-title-ident">
          <svg class="icon-svg sm" viewBox="0 0 24 24" style="color: var(--text-dim);"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"></rect><rect x="2" y="14" width="20" height="8" rx="2" ry="2"></rect><line x1="6" y1="6" x2="6.01" y2="6"></line><line x1="6" y1="18" x2="6.01" y2="18"></line></svg>
          <span class="node-ident-name">${node.name}</span>
        </div>
        <span class="node-ready-pill">${node.isReady ? "READY" : "NOT READY"}</span>
      </div>

      <div class="node-meta-gauges">
        <div class="gauge-row">
          <span style="color: var(--text-dim);">CPU: <strong class="mono" style="color: var(--text-main);">${formatCPU(usedCPU)} / ${formatCPU(totalCPU)}</strong></span>
          <span class="mono" style="font-weight: 700; color: var(--color-brand);">${cpuPct}%</span>
        </div>
        <div class="gauge-track">
          <div class="gauge-fill-cpu" style="width: ${cpuPct}%;"></div>
        </div>

        <div class="gauge-row" style="margin-top: 0.2rem;">
          <span style="color: var(--text-dim);">Memory: <strong class="mono" style="color: var(--text-main);">${formatMemory(usedMem)} / ${formatMemory(totalMem)}</strong></span>
          <span class="mono" style="font-weight: 700; color: var(--color-purple);">${memPct}%</span>
        </div>
        <div class="gauge-track">
          <div class="gauge-fill-mem" style="width: ${memPct}%;"></div>
        </div>

        <div style="display: flex; justify-content: space-between; font-size: 10px; color: var(--text-dim); margin-top: 0.25rem;">
          <span>Pods: <strong class="mono" style="color: var(--text-main);">${nodeWorkloads.length}</strong></span>
          <span>Reclaimable: <strong class="mono" style="color: var(--color-idle);">${formatCPU(reclaimCPU)} / ${formatMemory(reclaimMem)}</strong></span>
        </div>
      </div>

      <div class="node-workloads-tree">
        <div style="font-size: 9px; font-weight: 700; text-transform: uppercase; color: var(--text-dim); margin-bottom: 0.2rem;">Child Workloads</div>
        ${workloadsListHTML}
      </div>
    `;

    container.appendChild(card);
  }
}

// Workload Decision Matrix Table (Part 9)
function renderWorkloadTable(currentState) {
  const tbody = document.getElementById("workload-tbody");
  const countTag = document.getElementById("table-filtered-count");
  if (!tbody) return;
  tbody.innerHTML = "";

  const workloads = currentState.simulationResult ? currentState.simulationResult.workloads : [];

  // Populate node filter dropdown dynamically
  const filterNode = document.getElementById("filter-node");
  if (filterNode) {
    const currentVal = filterNode.value;
    const existingOptions = Array.from(filterNode.options).map(o => o.value);
    for (const n of currentState.cluster.nodes) {
      if (!existingOptions.includes(n.name)) {
        const opt = document.createElement("option");
        opt.value = n.name;
        opt.textContent = n.name;
        filterNode.appendChild(opt);
      }
    }
    filterNode.value = currentVal;
  }

  // Filter workloads
  let filtered = workloads.filter(w => {
    if (currentState.filterClassification !== "ALL" && w.classification !== currentState.filterClassification) {
      return false;
    }
    if (currentState.filterAction !== "ALL" && w.action !== currentState.filterAction) {
      return false;
    }
    if (currentState.filterNode !== "ALL" && w.nodeName !== currentState.filterNode) {
      return false;
    }
    if (currentState.filterKind !== "ALL" && w.ownerKind !== currentState.filterKind) {
      return false;
    }
    if (currentState.searchQuery) {
      const q = currentState.searchQuery;
      const matchName = w.name && w.name.toLowerCase().includes(q);
      const matchNs = w.namespace && w.namespace.toLowerCase().includes(q);
      const matchNode = w.nodeName && w.nodeName.toLowerCase().includes(q);
      if (!matchName && !matchNs && !matchNode) return false;
    }
    return true;
  });

  // Sort workloads
  filtered.sort((a, b) => {
    let vA, vB;
    switch (currentState.sortField) {
      case "name":
        vA = a.name.toLowerCase();
        vB = b.name.toLowerCase();
        break;
      case "cpu":
        vA = a.avgCpuMillicores || 0;
        vB = b.avgCpuMillicores || 0;
        break;
      case "mem":
        vA = a.avgMemoryBytes || 0;
        vB = b.avgMemoryBytes || 0;
        break;
      case "idle":
        vA = a.detectedIdleDuration || 0;
        vB = b.detectedIdleDuration || 0;
        break;
      case "score":
        vA = a.score || 0;
        vB = b.score || 0;
        break;
      default:
        vA = a.name;
        vB = b.name;
    }
    if (vA < vB) return currentState.sortAsc ? -1 : 1;
    if (vA > vB) return currentState.sortAsc ? 1 : -1;
    return 0;
  });

  if (countTag) {
    countTag.textContent = `Showing ${filtered.length} of ${workloads.length} workloads`;
  }

  if (filtered.length === 0) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td colspan="11" style="text-align: center; color: var(--text-dim); padding: 2rem;">No workloads matching current filters</td>`;
    tbody.appendChild(tr);
    return;
  }

  filtered.forEach((w) => {
    const rawIndex = workloads.indexOf(w);
    const tr = document.createElement("tr");
    if (currentState.selectedWorkloadIndex === rawIndex) {
      tr.classList.add("selected");
    }

    const actionBadgeClass = w.action === "FULL_RECLAIM" ? "badge-full" : (w.action === "SOFT_RECLAIM" ? "badge-soft" : "badge-keep");
    const classBadgeClass = w.classification === "ACTIVE" ? "badge-active" : (w.classification === "LOW_USAGE" ? "badge-low" : "badge-idle");

    const safetyBlocked = !w.capabilities.FullReclaimAllowed && !w.capabilities.SoftReclaimAllowed;
    const safetyBadge = safetyBlocked
      ? `<span class="badge-safety-block">BLOCKED</span>`
      : `<span class="badge-safety-pass">PASSED</span>`;

    const scorePct = Math.min(100, Math.round((w.score || 0) * 100));
    const scoreColor = w.action === "FULL_RECLAIM" ? "var(--color-full)" : (w.action === "SOFT_RECLAIM" ? "var(--color-soft)" : "var(--color-keep)");

    // Find original synthetic workload requests
    const orig = currentState.cluster.workloads.find(sw => sw.name === w.name) || {};
    const reqCPU = orig.requestedCpuMillis || 0;
    const reqMem = orig.requestedMemoryBytes || 0;

    tr.innerHTML = `
      <td>
        <div style="display: flex; flex-direction: column;">
          <strong class="mono" style="color: var(--text-main); font-size: 12px;">${w.name}</strong>
          <span style="font-size: 10px; color: var(--text-dim);">${w.namespace}</span>
        </div>
      </td>
      <td><span style="font-size: 11px; color: var(--text-muted);">${w.ownerKind || "Pod"}</span></td>
      <td><span class="mono" style="font-size: 11px; color: var(--text-muted);">${w.nodeName}</span></td>
      <td>
        <span class="mono" style="font-size: 11px;">${formatCPU(w.avgCpuMillicores)} / <span style="color: var(--text-dim);">${formatCPU(reqCPU)}</span></span>
      </td>
      <td>
        <span class="mono" style="font-size: 11px;">${formatMemory(w.avgMemoryBytes)} / <span style="color: var(--text-dim);">${formatMemory(reqMem)}</span></span>
      </td>
      <td>
        <span class="mono" style="font-size: 11px; color: var(--text-dim);">${formatDuration(w.detectedIdleDuration ? w.detectedIdleDuration / 1000000000 : 0)}</span>
      </td>
      <td><span class="status-badge ${classBadgeClass}">${w.classification}</span></td>
      <td>
        <div class="table-score-gauge">
          <span class="table-score-val" style="color: ${scoreColor};">${formatScore(w.score)}</span>
          <div class="table-score-track">
            <div class="table-score-fill" style="width: ${scorePct}%; background-color: ${scoreColor};"></div>
          </div>
        </div>
      </td>
      <td><span class="status-badge ${actionBadgeClass}">${w.action}</span></td>
      <td>${safetyBadge}</td>
      <td style="text-align: right;">
        <button class="btn btn-subtle btn-details" data-idx="${rawIndex}" title="Open 9-factor score breakdown">
          <svg class="icon-svg sm" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line><polyline points="10 9 9 9 8 9"></polyline></svg>
          Analysis
        </button>
      </td>
    `;

    tr.addEventListener("click", (e) => {
      if (e.target.closest(".btn-details")) {
        openDrawer(rawIndex);
      } else {
        openDrawer(rawIndex);
      }
    });

    tbody.appendChild(tr);
  });
}

// Scenario Lab View (Part 12, 13, 14, 15)
function renderScenarioLab(currentState) {
  const container = document.getElementById("scenario-cards-container");
  const countBadge = document.getElementById("lab-search-count");
  if (!container) return;
  container.innerHTML = "";

  const categoryFilter = currentState.labCategoryFilter;
  const search = currentState.labSearchQuery;

  let filtered = currentState.presets.filter(p => {
    if (categoryFilter !== "ALL" && p.category !== categoryFilter) {
      return false;
    }
    if (search) {
      const matchName = p.name && p.name.toLowerCase().includes(search);
      const matchDesc = p.description && p.description.toLowerCase().includes(search);
      const matchCond = p.primaryCondition && p.primaryCondition.toLowerCase().includes(search);
      const matchCat = p.categoryName && p.categoryName.toLowerCase().includes(search);
      if (!matchName && !matchDesc && !matchCond && !matchCat) return false;
    }
    return true;
  });

  if (countBadge) {
    countBadge.textContent = `${filtered.length} of ${currentState.presets.length} scenarios`;
  }

  for (const scenario of filtered) {
    const card = document.createElement("div");
    card.className = "scenario-card";

    card.innerHTML = `
      <div>
        <div class="scenario-card-header">
          <div class="scenario-name">${scenario.name}</div>
          <span class="scenario-tag">${scenario.tag || scenario.category || "Test"}</span>
        </div>
        <p class="scenario-desc" style="margin-top: 0.45rem;">${scenario.description}</p>
        <div style="font-size: 10px; color: var(--text-dim); margin-top: 0.45rem;">
          Condition: <strong class="mono" style="color: var(--text-muted);">${scenario.primaryCondition || "-"}</strong>
        </div>
      </div>

      <div>
        <div class="scenario-meta-row" style="margin-bottom: 0.65rem;">
          <span>${scenario.nodeCount || 1} Node${(scenario.nodeCount || 1) > 1 ? "s" : ""}</span>
          <span>&bull;</span>
          <span>${scenario.workloadCount || 1} Workload${(scenario.workloadCount || 1) > 1 ? "s" : ""}</span>
          <span>&bull;</span>
          <span style="color: var(--color-brand);">${scenario.categoryName || "Scenario"}</span>
        </div>

        <div class="scenario-footer">
          <span class="scenario-test-target">Target: ${scenario.designedToTest || "Pipeline"}</span>
          <button class="btn btn-primary btn-run-scenario" data-id="${scenario.id}">
            <svg class="icon-svg sm" viewBox="0 0 24 24"><polygon points="5 3 19 12 5 21 5 3"></polygon></svg>
            Run Scenario
          </button>
        </div>
      </div>
    `;

    const btn = card.querySelector(".btn-run-scenario");
    if (btn) {
      btn.addEventListener("click", () => {
        loadScenario(scenario.id, scenario.name);
        state.setActiveView("overview");
      });
    }

    container.appendChild(card);
  }
}

// Simulation Session History (Part 19)
function renderHistoryTable(currentState) {
  const tbody = document.getElementById("history-tbody");
  if (!tbody) return;
  tbody.innerHTML = "";

  if (currentState.history.length === 0) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td colspan="9" style="text-align: center; color: var(--text-dim); padding: 2rem;">No simulation runs recorded in current session</td>`;
    tbody.appendChild(tr);
    return;
  }

  for (const h of currentState.history) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td><span class="mono" style="color: var(--text-dim);">#${h.runId}</span></td>
      <td><span class="mono" style="font-size: 11px;">${h.timestamp}</span></td>
      <td><strong style="color: var(--text-main); font-size: 12px;">${h.scenarioName}</strong></td>
      <td><span class="mono">${h.totalWorkloads}</span></td>
      <td><span class="status-badge badge-keep">${h.countKeep}</span></td>
      <td><span class="status-badge badge-soft">${h.countSoft}</span></td>
      <td><span class="status-badge badge-full">${h.countFull}</span></td>
      <td><span class="status-badge ${h.countBlocked > 0 ? "badge-blocked" : "badge-keep"}">${h.countBlocked}</span></td>
      <td style="text-align: right;">
        <button class="btn btn-subtle btn-load-history" data-runid="${h.runId}">
          View Run
        </button>
      </td>
    `;

    const btn = tr.querySelector(".btn-load-history");
    if (btn) {
      btn.addEventListener("click", () => {
        state.loadHistoryRun(h.runId);
      });
    }

    tbody.appendChild(tr);
  }
}

// ── Score Breakdown & Explainability Drawer (Part 10 & 11) ──────────────────
function renderDrawer(currentState) {
  const index = currentState.selectedWorkloadIndex;
  const workloads = currentState.simulationResult ? currentState.simulationResult.workloads : [];
  const w = workloads[index];
  if (!w) return;

  const podName = document.getElementById("drawer-pod-name");
  const podMeta = document.getElementById("drawer-pod-meta");
  const vClass = document.getElementById("drawer-verdict-class");
  const vScore = document.getElementById("drawer-verdict-score");
  const vAction = document.getElementById("drawer-verdict-action");
  const vSafety = document.getElementById("drawer-verdict-safety");
  const capFull = document.getElementById("drawer-cap-full");
  const capSoft = document.getElementById("drawer-cap-soft");

  if (podName) podName.textContent = w.name;
  if (podMeta) podMeta.textContent = `Namespace: ${w.namespace} &bull; Node: ${w.nodeName} &bull; Kind: ${w.ownerKind || "Deployment"}`;

  const classBadgeClass = w.classification === "ACTIVE" ? "badge-active" : (w.classification === "LOW_USAGE" ? "badge-low" : "badge-idle");
  const actionBadgeClass = w.action === "FULL_RECLAIM" ? "badge-full" : (w.action === "SOFT_RECLAIM" ? "badge-soft" : "badge-keep");

  if (vClass) vClass.innerHTML = `<span class="status-badge ${classBadgeClass}">${w.classification}</span>`;
  if (vScore) vScore.textContent = formatScore(w.score);
  if (vAction) vAction.innerHTML = `<span class="status-badge ${actionBadgeClass}">${w.action}</span>`;

  const safetyBlocked = !w.capabilities.FullReclaimAllowed && !w.capabilities.SoftReclaimAllowed;
  if (vSafety) {
    vSafety.innerHTML = safetyBlocked
      ? `<span class="badge-safety-block">BLOCKED</span>`
      : `<span class="badge-safety-pass">PASSED</span>`;
  }

  if (capFull) {
    capFull.textContent = w.capabilities.FullReclaimAllowed ? "AVAILABLE" : "UNAVAILABLE";
    capFull.className = w.capabilities.FullReclaimAllowed ? "badge-safety-pass" : "badge-safety-block";
  }

  if (capSoft) {
    capSoft.textContent = w.capabilities.SoftReclaimAllowed ? "AVAILABLE" : "UNAVAILABLE";
    capSoft.className = w.capabilities.SoftReclaimAllowed ? "badge-safety-pass" : "badge-safety-block";
  }

  // Render 9 Factor Cards
  const factorsContainer = document.getElementById("drawer-factors-list");
  if (factorsContainer) {
    factorsContainer.innerHTML = "";
    const factorsList = ["CPU", "Memory", "Idle", "Benefit", "Replica", "Priority", "PDB", "State", "Checkpoint"];
    const scores = w.scores || {};
    const policyWeights = currentState.policy ? currentState.policy.weights : {};

    for (const factorKey of factorsList) {
      const info = FACTOR_INFO[factorKey] || { code: factorKey, title: factorKey, desc: "" };
      const scoreVal = scores[factorKey] !== undefined ? scores[factorKey] : 0;
      const weightVal = policyWeights[factorKey] !== undefined ? policyWeights[factorKey] : null;
      const pct = Math.min(100, Math.round(scoreVal * 100));

      const card = document.createElement("div");
      card.className = "factor-card";
      card.innerHTML = `
        <div class="factor-top-line">
          <div class="factor-label-group">
            <span class="factor-code">${info.code}</span>
            <span class="factor-title">${info.title}</span>
            ${weightVal !== null ? `<span class="factor-weight-tag">(Weight: ${Number(weightVal).toFixed(2)})</span>` : ""}
          </div>
          <span class="factor-score-num">${formatScore(scoreVal)}</span>
        </div>
        <div class="factor-bar-bg">
          <div class="factor-bar-val" style="width: ${pct}%;"></div>
        </div>
        <div class="factor-expl">${info.desc}</div>
      `;
      factorsContainer.appendChild(card);
    }
  }

  // Render Decision Reasons (Positive)
  const ulDecision = document.getElementById("drawer-reasons-decision");
  if (ulDecision) {
    ulDecision.innerHTML = "";
    const reasons = w.decisionReasons || [];
    if (reasons.length === 0) {
      ulDecision.innerHTML = `<li class="reason-li info">No specific decision contribution notes populated</li>`;
    } else {
      for (const r of reasons) {
        ulDecision.innerHTML += `<li class="reason-li positive"><svg class="icon-svg sm" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"></polyline></svg><span>${r}</span></li>`;
      }
    }
  }

  // Render Rejection & Safety Reasons (Negative/Blocked)
  const ulRejection = document.getElementById("drawer-reasons-rejection");
  if (ulRejection) {
    ulRejection.innerHTML = "";
    const rejections = w.rejectionReasons || [];
    if (rejections.length === 0) {
      ulRejection.innerHTML = `<li class="reason-li info"><svg class="icon-svg sm" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"></polyline></svg><span>Safety criteria passed &mdash; zero gating restrictions active</span></li>`;
    } else {
      for (const r of rejections) {
        ulRejection.innerHTML += `<li class="reason-li negative"><svg class="icon-svg sm" viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg><span>${r}</span></li>`;
      }
    }
  }

  // Render Detector Signal Reasons
  const ulDetector = document.getElementById("drawer-reasons-detector");
  if (ulDetector) {
    ulDetector.innerHTML = "";
    const classReasons = w.classificationReasons || [];
    if (classReasons.length === 0) {
      ulDetector.innerHTML = `<li class="reason-li info">No detector signal reasons populated</li>`;
    } else {
      for (const r of classReasons) {
        ulDetector.innerHTML += `<li class="reason-li info"><svg class="icon-svg sm" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg><span>${r}</span></li>`;
      }
    }
  }
}

// ── Error Banner Helper ────────────────────────────────────────────────────
function showErrorBanner(message) {
  let banner = document.getElementById("error-banner");
  if (!banner) {
    banner = document.createElement("div");
    banner.id = "error-banner";
    banner.style.cssText = `
      background: #7f1d1d;
      color: #fecaca;
      border: 1px solid #ef4444;
      padding: 0.6rem 1.25rem;
      font-size: 12px;
      font-family: var(--font-mono);
      display: flex;
      align-items: center;
      justify-content: space-between;
      position: sticky;
      top: 48px;
      z-index: 45;
    `;
    document.body.prepend(banner);
  }
  banner.innerHTML = `
    <span>${message}</span>
    <button onclick="this.parentElement.remove()" style="background: transparent; border: none; color: #fecaca; cursor: pointer; font-size: 14px; font-weight: 700;">&times;</button>
  `;
}
