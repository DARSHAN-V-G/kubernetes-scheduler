// Active Workload / Cluster Mode Controller
// Connects strictly to backend REST endpoints: /api/cluster/status, /api/workloads, /api/workloads/checkpoint, /api/workloads/restore, /api/reclaim/config

export class ClusterController {
  constructor() {
    this.status = null;
    this.workloads = [];
    this.nodes = [];
    this.popoverBound = false;
    this.customWindowSeconds = 60;
  }

  setCustomWindow(seconds) {
    seconds = parseInt(seconds, 10);
    if (isNaN(seconds) || seconds <= 0) seconds = 60;
    this.customWindowSeconds = seconds;

    const input = document.getElementById("input-custom-window");
    const unitSelect = document.getElementById("select-custom-window-unit");
    if (input) input.value = seconds;
    if (unitSelect) unitSelect.value = "s";

    [30, 60, 300, 900].forEach((s) => {
      const chip = document.getElementById(`chip-win-${s}`);
      if (chip) {
        if (s === seconds) chip.classList.add("active");
        else chip.classList.remove("active");
      }
    });

    this.loadWorkloads();
  }

  applyCustomWindowFromInput() {
    const input = document.getElementById("input-custom-window");
    const unitSelect = document.getElementById("select-custom-window-unit");
    let val = input ? parseFloat(input.value) : 60;
    if (isNaN(val) || val <= 0) val = 60;

    const unit = unitSelect ? unitSelect.value : "s";
    let seconds = Math.round(val);
    if (unit === "m") seconds = Math.round(val * 60);
    else if (unit === "h") seconds = Math.round(val * 3600);

    this.customWindowSeconds = seconds;

    [30, 60, 300, 900].forEach((s) => {
      const chip = document.getElementById(`chip-win-${s}`);
      if (chip) {
        if (s === seconds) chip.classList.add("active");
        else chip.classList.remove("active");
      }
    });

    this.loadWorkloads();
  }

  async init() {
    this.bindEvents();
    this.setupPopovers();
    await this.refresh();
  }

  bindEvents() {
    const refreshBtn = document.getElementById("btn-cluster-refresh");
    if (refreshBtn) {
      refreshBtn.addEventListener("click", () => this.refresh());
    }

    const configBtn = document.getElementById("btn-cluster-config");
    if (configBtn) {
      configBtn.addEventListener("click", () => this.openConfigModal());
    }

    const configForm = document.getElementById("form-reclaim-config");
    if (configForm) {
      configForm.addEventListener("submit", (e) => this.handleSaveConfig(e));
    }
  }

  showReasonPopup(title, body, hint) {
    let msg = `=== ${title.toUpperCase()} ===\n\n${body}`;
    if (hint) {
      msg += `\n\n💡 Diagnostic Hint:\n${hint}`;
    }
    alert(msg);
  }

  setupPopovers() {
    if (this.popoverBound) return;
    this.popoverBound = true;

    const portal = document.getElementById("cluster-popover-portal");
    const titleEl = document.getElementById("cluster-popover-title");
    const bodyEl = document.getElementById("cluster-popover-body");
    const hintEl = document.getElementById("cluster-popover-hint");
    if (!portal) return;

    let activePill = null;

    const showPopover = (pill) => {
      activePill = pill;
      const title = pill.getAttribute("data-popover-title") || "Information";
      const body = pill.getAttribute("data-popover-body") || "";
      const hint = pill.getAttribute("data-popover-hint") || "";
      const type = pill.getAttribute("data-popover-type") || "info";

      if (titleEl) titleEl.textContent = title;
      if (bodyEl) bodyEl.textContent = body;
      if (hintEl) {
        if (hint) {
          hintEl.textContent = hint;
          hintEl.style.display = "block";
        } else {
          hintEl.style.display = "none";
        }
      }

      portal.className = "cluster-popover-portal";
      if (type === "danger") portal.classList.add("danger");
      if (type === "success") portal.classList.add("success");

      portal.style.display = "block";

      const rect = pill.getBoundingClientRect();
      const pWidth = portal.offsetWidth;
      const pHeight = portal.offsetHeight;

      let top = rect.top - pHeight - 8;
      let left = rect.left + (rect.width / 2) - (pWidth / 2);

      // Boundary checks
      if (top < 10) {
        top = rect.bottom + 8;
      }
      if (left < 10) left = 10;
      if (left + pWidth > window.innerWidth - 10) {
        left = window.innerWidth - pWidth - 10;
      }

      portal.style.top = `${Math.round(top)}px`;
      portal.style.left = `${Math.round(left)}px`;
      portal.classList.add("visible");
    };

    const hidePopover = () => {
      activePill = null;
      portal.classList.remove("visible");
      portal.style.display = "none";
    };

    document.addEventListener("mouseover", (e) => {
      const pill = e.target.closest(".pop-pill, .btn-reason");
      if (pill) {
        showPopover(pill);
      }
    });

    document.addEventListener("mouseout", (e) => {
      const pill = e.target.closest(".pop-pill, .btn-reason");
      if (pill && pill === activePill) {
        hidePopover();
      }
    });
  }

  escapeAttr(str) {
    if (!str) return "";
    return String(str)
      .replace(/&/g, "&amp;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }

  async refresh() {
    const refreshBtn = document.getElementById("btn-cluster-refresh");
    if (refreshBtn) refreshBtn.classList.add("loading");

    try {
      await Promise.all([this.loadStatus(), this.loadWorkloads()]);
    } catch (err) {
      console.error("Cluster refresh failed:", err);
    } finally {
      if (refreshBtn) refreshBtn.classList.remove("loading");
    }
  }

  async loadStatus() {
    try {
      const resp = await fetch("/api/cluster/status");
      if (!resp.ok) throw new Error("HTTP " + resp.status);
      this.status = await resp.json();
      this.renderStatusCards();
    } catch (e) {
      console.warn("Failed fetching cluster status:", e);
    }
  }

  async loadWorkloads() {
    const tbody = document.getElementById("cluster-workloads-tbody");
    if (tbody) {
      tbody.innerHTML = `<tr><td colspan="13" style="text-align:center; padding: 24px; color: var(--text-dim);">Querying Kubernetes pods and physical Prometheus telemetry (Window: ${this.customWindowSeconds}s)...</td></tr>`;
    }

    try {
      const nsInput = document.getElementById("traffic-input-namespace");
      const ns = nsInput ? nsInput.value.trim() : "test-application";
      const nsParam = ns ? `&namespace=${encodeURIComponent(ns)}` : "";
      const winParam = this.customWindowSeconds ? `window=${this.customWindowSeconds}` : "window=60";
      const resp = await fetch(`/api/workloads?${winParam}${nsParam}`);
      if (!resp.ok) throw new Error("HTTP " + resp.status);
      const raw = await resp.json();
      const rawList = Array.isArray(raw) ? raw : (raw.workloads || []);

      this.workloads = rawList.map((item) => {
        const sim = item.simulation || item;
        const life = item.lifecycle || {};

        let clsStr = "ACTIVE";
        if (sim.classification === 2 || sim.classification === "IDLE" || sim.classification === "ClassIdle") {
          clsStr = "IDLE";
        } else if (sim.classification === 1 || sim.classification === "LOW_USAGE" || sim.classification === "ClassLowUsage") {
          clsStr = "LOW_USAGE";
        }

        let actionStr = life.action;
        if (!actionStr) {
          if (sim.action === 2 || sim.action === "FULL_RECLAIM") actionStr = "FULL_RECLAIM";
          else if (sim.action === 1 || sim.action === "SOFT_RECLAIM") actionStr = "SOFT_RECLAIM";
          else actionStr = "KEEP";
        }

        const scoreVal = (life.score !== undefined && life.score !== null) ? life.score : sim.score;

        return {
          name: sim.name || life.name,
          namespace: sim.namespace || life.namespace,
          phase: sim.phase || "Unknown",
          checkpointable: sim.checkpointable === true,
          nodeName: sim.nodeName || "adaptive-cluster-control-plane",
          avgCpuMillicores: sim.avgCpuMillicores || 0,
          requestedCpuMillis: sim.requestedCpuMillis || sim.requestedCPUMillis || 100,
          avgMemoryBytes: sim.avgMemoryBytes || 0,
          requestedMemoryBytes: sim.requestedMemoryBytes || (64 * 1024 * 1024),
          detectedIdleDuration: sim.detectedIdleDuration || 0,
          windowDuration: sim.windowDuration || 0,
          sampleCount: sim.sampleCount || 0,
          isConsistentlyIdle: sim.isConsistentlyIdle === true,
          classification: clsStr,
          score: scoreVal,
          action: actionStr,
          capabilities: sim.capabilities || {},
          decisionReasons: sim.decisionReasons || life.decisionReasons || [],
          rejectionReasons: sim.rejectionReasons || [],
          lifecycleState: life.state || (sim.phase === "Reclaimed" ? "RECLAIMED" : "RUNNING"),
          stateDetail: life.stateDetail || "",
          lastError: life.lastError || "",
          checkpointPath: life.checkpointPath || "",
        };
      });

      this.renderWorkloadsTable();
    } catch (e) {
      console.error("Failed fetching live workloads:", e);
      if (tbody) {
        tbody.innerHTML = `<tr><td colspan="13" style="text-align:center; padding: 24px; color: var(--color-red);">Error discovering workloads: ${e.message}</td></tr>`;
      }
    }
  }

  renderStatusCards() {
    if (!this.status) return;
    const s = this.status;
    const criu = s.criu || {};
    const prom = s.prometheus || {};

    const elK8sVer = document.getElementById("cl-stat-k8s-version");
    if (elK8sVer) elK8sVer.textContent = s.serverVersion || "v1.37.0";

    const elNodes = document.getElementById("cl-stat-ready-nodes");
    if (elNodes) elNodes.textContent = `${s.readyNodes ?? 1} / ${s.totalNodes ?? 1}`;

    const elCriu = document.getElementById("cl-stat-criu-status");
    if (elCriu) {
      if (criu.checkOk || criu.available) {
        elCriu.innerHTML = `<span class="badge-status-pill pass">ACTIVE (${criu.version || "v4.1.1"})</span>`;
      } else {
        elCriu.innerHTML = `<span class="badge-status-pill blocked">UNAVAILABLE</span>`;
      }
    }

    const elProm = document.getElementById("cl-stat-prom-status");
    if (elProm) {
      if (prom.connected) {
        elProm.innerHTML = `<span class="badge-status-pill pass">CONNECTED (${prom.trackedMetrics || 0} metrics &bull; Click to View)</span>`;
      } else {
        elProm.innerHTML = `<span class="badge-status-pill blocked">DISCONNECTED</span>`;
      }
    }
  }

  openPrometheusModal() {
    const modal = document.getElementById("modal-prometheus-details");
    if (!modal) return;

    const prom = (this.status && this.status.prometheus) || {};
    const details = prom.metricsDetails || {};

    const epEl = document.getElementById("prom-modal-endpoint");
    if (epEl) epEl.textContent = prom.endpoint || "http://127.0.0.1:9090";

    const timeEl = document.getElementById("prom-modal-timestamp");
    if (timeEl) timeEl.textContent = prom.lastScraped || new Date().toISOString();

    const container = document.getElementById("prom-modal-content");
    if (!container) return;

    let html = `
      <div style="margin-bottom: 12px; font-size: 12px; color: var(--text-dim);">
        Real-time physical telemetry scraped concurrently via cAdvisor and container network collectors.
        Total tracked streams: <strong class="mono" style="color:var(--text-bright);">${prom.trackedMetrics || 0}</strong>
      </div>
    `;

    // 1. Container CPU Rate Streams
    const cpuEntries = Object.entries(details.containerCPU || {});
    html += `
      <div class="prom-section-title">
        <span>⚡ Container CPU Usage Rates (container_cpu_usage_seconds_total [2m])</span>
        <span class="mono" style="font-size:11px; color:var(--text-dim); font-weight:normal;">(${cpuEntries.length} streams)</span>
      </div>
      <table class="prom-metric-table">
        <thead><tr><th>Namespace / Pod / Container</th><th style="text-align:right;">Usage Rate</th></tr></thead>
        <tbody>
    `;
    if (cpuEntries.length === 0) {
      html += `<tr><td colspan="2" style="color:var(--text-dim);">No CPU streams recorded</td></tr>`;
    } else {
      cpuEntries.forEach(([key, val]) => {
        const isEcom = key.startsWith("ecommerce/");
        html += `
          <tr ${isEcom ? 'style="background:rgba(59,130,246,0.08);"' : ""}>
            <td class="mono ${isEcom ? "bold" : ""}">${key}</td>
            <td class="mono ${isEcom ? "bold text-success" : ""}" style="text-align:right;">${val.toFixed(2)}m</td>
          </tr>
        `;
      });
    }
    html += `</tbody></table>`;

    // 2. Container Memory Working Set Streams
    const memEntries = Object.entries(details.containerMemWorking || {});
    html += `
      <div class="prom-section-title">
        <span>💾 Container Memory Working Set (container_memory_working_set_bytes)</span>
        <span class="mono" style="font-size:11px; color:var(--text-dim); font-weight:normal;">(${memEntries.length} streams)</span>
      </div>
      <table class="prom-metric-table">
        <thead><tr><th>Namespace / Pod / Container</th><th style="text-align:right;">Working Set</th></tr></thead>
        <tbody>
    `;
    if (memEntries.length === 0) {
      html += `<tr><td colspan="2" style="color:var(--text-dim);">No memory streams recorded</td></tr>`;
    } else {
      memEntries.forEach(([key, val]) => {
        const isEcom = key.startsWith("ecommerce/");
        const mb = (val / (1024 * 1024)).toFixed(2);
        html += `
          <tr ${isEcom ? 'style="background:rgba(59,130,246,0.08);"' : ""}>
            <td class="mono ${isEcom ? "bold" : ""}">${key}</td>
            <td class="mono ${isEcom ? "bold text-warning" : ""}" style="text-align:right;">${mb} MiB</td>
          </tr>
        `;
      });
    }
    html += `</tbody></table>`;

    // 3. Network Streams
    const netEntries = Object.entries(details.podNetworkRx || {});
    html += `
      <div class="prom-section-title">
        <span>🌐 Pod Network Rx &amp; Traffic (container_network_receive_bytes_total)</span>
        <span class="mono" style="font-size:11px; color:var(--text-dim); font-weight:normal;">(${netEntries.length} streams)</span>
      </div>
      <table class="prom-metric-table">
        <thead><tr><th>Namespace / Pod</th><th style="text-align:right;">Rx Rate</th><th style="text-align:right;">Tx Rate</th></tr></thead>
        <tbody>
    `;
    if (netEntries.length === 0) {
      html += `<tr><td colspan="3" style="color:var(--text-dim);">No network streams recorded</td></tr>`;
    } else {
      netEntries.forEach(([key, rxVal]) => {
        const isEcom = key.startsWith("ecommerce/");
        const txVal = (details.podNetworkTx && details.podNetworkTx[key]) || 0;
        html += `
          <tr ${isEcom ? 'style="background:rgba(59,130,246,0.08);"' : ""}>
            <td class="mono ${isEcom ? "bold" : ""}">${key}</td>
            <td class="mono" style="text-align:right;">${rxVal.toFixed(1)} B/s</td>
            <td class="mono" style="text-align:right;">${txVal.toFixed(1)} B/s</td>
          </tr>
        `;
      });
    }
    html += `</tbody></table>`;

    container.innerHTML = html;
    modal.classList.add("active");
  }

  renderWorkloadsTable() {
    const tbody = document.getElementById("cluster-workloads-tbody");
    if (!tbody) return;

    if (!this.workloads || this.workloads.length === 0) {
      tbody.innerHTML = `<tr><td colspan="13" style="text-align:center; padding: 24px; color: var(--text-dim);">No active workloads found in target namespaces.</td></tr>`;
      return;
    }

    tbody.innerHTML = this.workloads.map((w) => {
      const stateBadge = this.formatLifecycleBadge(w.lifecycleState, w.stateDetail, w.lastError);
      const actionBadge = this.formatActionBadge(w.action);
      const scoreFmt = (w.score !== undefined && w.score !== null) ? Number(w.score).toFixed(3) : "-";
      const isCandidate = (w.lifecycleState === "CANDIDATE" || w.action === "FULL_RECLAIM" || w.action === "SOFT_RECLAIM") && w.lifecycleState !== "RECLAIMED";
      const isReclaimed = (w.lifecycleState === "RECLAIMED" || w.lifecycleState === "CHECKPOINTED" || w.phase === "Reclaimed");
      const isRunning = w.phase === "Running";
      const canCheckpoint = w.checkpointable;

      let actionButtons = "";
      if (isReclaimed) {
        actionButtons = `<button class="btn btn-sm btn-restore" style="background:#8b5cf6; color:#fff; border:1px solid #a78bfa;" onclick="window.clusterCtrl.restoreWorkload('${w.namespace}', '${w.name}')">Restore</button>`;
      } else if (w.lifecycleState === "CHECKPOINTING") {
        actionButtons = `<span class="mono" style="font-size: 11px; color: var(--color-brand);">CHECKPOINTING...</span>`;
      } else if (w.lifecycleState === "RECLAMATION_FAILED" && isRunning && canCheckpoint) {
        actionButtons = `<button class="btn btn-sm btn-retry" onclick="window.clusterCtrl.checkpointWorkload('${w.namespace}', '${w.name}')">Retry Checkpoint</button>`;
      } else if (!isRunning) {
        actionButtons = `<span class="mono" style="font-size: 11px; color: var(--text-dim);" title="Checkpoint requires a Running pod">Unavailable: ${w.phase}</span>`;
      } else if (!canCheckpoint) {
        actionButtons = `<button class="btn btn-sm btn-checkpoint" onclick="window.clusterCtrl.checkpointWorkload('${w.namespace}', '${w.name}')" title="Execute Graceful Reclaim (Scale to 0)">Graceful Reclaim</button>`;
      } else {
        actionButtons = `<button class="btn btn-sm btn-checkpoint" onclick="window.clusterCtrl.checkpointWorkload('${w.namespace}', '${w.name}')">Checkpoint &amp; Reclaim</button>`;
      }

      // Decision button / trigger
      let decisionBtnHtml = "";
      if (w.rejectionReasons && w.rejectionReasons.length > 0) {
        const title = "Reclamation Blocked (Safety Filter)";
        const body = w.rejectionReasons.join("; ");
        decisionBtnHtml = `
          <button type="button" class="pop-pill btn-reason danger" 
            onclick="event.stopPropagation(); window.clusterCtrl.showReasonPopup('${this.escapeAttr(title)}', '${this.escapeAttr(body)}', '')"
            data-popover-title="${this.escapeAttr(title)}" 
            data-popover-body="${this.escapeAttr(body)}" 
            data-popover-type="danger"
            title="${this.escapeAttr(title)}: ${this.escapeAttr(body)}">
            <svg class="icon-svg xs" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>
            <span>Blocked Reason</span>
          </button>
        `;
      } else if (w.decisionReasons && w.decisionReasons.length > 0) {
        const isReclaim = (w.action === "FULL_RECLAIM" || w.action === "SOFT_RECLAIM");
        const title = isReclaim ? "Reclamation Rationale" : "Evaluation Rationale";
        const body = w.decisionReasons.join("; ");
        const type = isReclaim ? "success" : "info";
        const pillCls = isReclaim ? "success" : "";
        decisionBtnHtml = `
          <button type="button" class="pop-pill btn-reason ${pillCls}" 
            onclick="event.stopPropagation(); window.clusterCtrl.showReasonPopup('${this.escapeAttr(title)}', '${this.escapeAttr(body)}', '')"
            data-popover-title="${this.escapeAttr(title)}" 
            data-popover-body="${this.escapeAttr(body)}" 
            data-popover-type="${type}"
            title="${this.escapeAttr(title)}: ${this.escapeAttr(body)}">
            <svg class="icon-svg xs" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
            <span>Why ${w.action}?</span>
          </button>
        `;
      }

      // Workload state detail & failure explanation button
      let stateBtnHtml = "";
      if (w.lastError) {
        const title = "Reclamation / CRIU Dump Error";
        const body = w.lastError;
        const hint = "Active Prometheus or network services have locked sockets/threads that CRIU cannot dump. For a clean working demo, use 'idle-checkpoint-demo'.";
        stateBtnHtml = `
          <button type="button" class="pop-pill btn-reason danger" 
            onclick="event.stopPropagation(); window.clusterCtrl.showReasonPopup('${this.escapeAttr(title)}', '${this.escapeAttr(body)}', '${this.escapeAttr(hint)}')"
            data-popover-title="${this.escapeAttr(title)}" 
            data-popover-body="${this.escapeAttr(body)}" 
            data-popover-hint="${this.escapeAttr(hint)}" 
            data-popover-type="danger"
            title="${this.escapeAttr(title)}: ${this.escapeAttr(body)}">
            <svg class="icon-svg xs" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>
            <span>Failure Info</span>
          </button>
        `;
      } else if (w.stateDetail) {
        const title = "Lifecycle State Details";
        const body = w.stateDetail;
        stateBtnHtml = `
          <button type="button" class="pop-pill btn-reason" 
            onclick="event.stopPropagation(); window.clusterCtrl.showReasonPopup('${this.escapeAttr(title)}', '${this.escapeAttr(body)}', '')"
            data-popover-title="${this.escapeAttr(title)}" 
            data-popover-body="${this.escapeAttr(body)}" 
            data-popover-type="info"
            title="${this.escapeAttr(title)}: ${this.escapeAttr(body)}">
            <svg class="icon-svg xs" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
            <span>Details</span>
          </button>
        `;
      }

      const cpuText = `${Math.round(w.avgCpuMillicores || 0)}m / ${w.requestedCpuMillis || 0}m`;
      const memMb = Math.round((w.avgMemoryBytes || 0) / (1024 * 1024));
      const reqMemMb = Math.round((w.requestedMemoryBytes || 0) / (1024 * 1024));
      const memText = `${memMb}M / ${reqMemMb}M`;
      const winSec = w.windowDuration ? Math.round(w.windowDuration / 1e9) : 40;
      const sampleCnt = w.sampleCount || 5;
      const windowText = `${winSec}s (${sampleCnt} smp)`;
      const idleText = (w.detectedIdleDuration > 0) ? `${Math.round(w.detectedIdleDuration / 1e9)}s` : "0s";
      const chkCompatible = (w.capabilities && (w.capabilities.fullReclaimAllowed !== false)) ? `<span style="color:var(--color-green);">YES</span>` : `<span style="color:var(--text-dim);">NO</span>`;

      return `
        <tr class="workload-row ${isCandidate ? "candidate-highlight" : ""}">
          <td>
            <div class="pod-name-block">
              <span class="mono bold">${w.name}</span>
              <span class="pod-ns-label mono">${w.namespace}</span>
            </div>
          </td>
          <td><span class="mono">${w.nodeName || "-"}</span></td>
          <td><span class="mono">${cpuText}</span></td>
          <td><span class="mono">${memText}</span></td>
          <td><span class="mono badge-window" title="Telemetry sliding window considered: ${winSec} seconds across ${sampleCnt} samples">${windowText}</span></td>
          <td><span class="mono">${idleText}</span></td>
          <td><span class="mono">${w.classification || "ACTIVE"}</span></td>
          <td><span class="mono bold score-highlight">${scoreFmt}</span></td>
          <td>
            <div class="cell-pill-wrap">
              ${actionBadge}
              ${decisionBtnHtml}
            </div>
          </td>
          <td>${chkCompatible}</td>
          <td>
            <div class="cell-pill-wrap">
              ${stateBadge}
              ${stateBtnHtml}
            </div>
          </td>
          <td>
            <div class="checkpoint-info-cell">
              ${w.checkpointPath ? `<span class="mono chk-path" title="${w.checkpointPath}">${this.truncatePath(w.checkpointPath)}</span>` : `<span style="color:var(--text-dim);">-</span>`}
            </div>
          </td>
          <td style="text-align: right;">
            ${actionButtons}
          </td>
        </tr>
      `;
    }).join("");
  }

  truncatePath(p) {
    if (!p) return "";
    const parts = p.split("/");
    return parts[parts.length - 1];
  }

  formatLifecycleBadge(st, detail, err) {
    st = st || "RUNNING";
    let cls = "pass";
    if (st === "CHECKPOINTING" || st === "RESTORING") cls = "warn";
    if (st === "RECLAMATION_FAILED") cls = "blocked";
    if (st === "CANDIDATE") cls = "highlight";
    if (st === "RECLAIMED") cls = "purple";

    return `
      <div class="lifecycle-badge-wrap">
        <span class="badge-status-pill ${cls}">${st}</span>
      </div>
    `;
  }

  formatActionBadge(act) {
    if (act === "FULL_RECLAIM") return `<span class="badge-action badge-full">FULL_RECLAIM</span>`;
    if (act === "SOFT_RECLAIM") return `<span class="badge-action badge-soft">SOFT_RECLAIM</span>`;
    return `<span class="badge-action badge-keep">KEEP</span>`;
  }

  async checkpointWorkload(ns, name) {
    if (!confirm(`Trigger real CRIU checkpoint and reclaim resources for pod ${ns}/${name}?`)) return;

    try {
      const resp = await fetch("/api/workloads/checkpoint", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ namespace: ns, name: name }),
      });
      const res = await resp.json();
      if (!resp.ok) {
        const rawErr = res.error || res.message || "Unknown runtime error";
        let reasonNotice = "";
        if (name.includes("prometheus")) {
          reasonNotice = "\n\n💡 Why this failed: Active Prometheus services hold open TCP sockets and TSDB file locks that CRIU cannot dump. To test a verified successful CRIU checkpoint, click Checkpoint on 'idle-checkpoint-demo'.";
        }
        alert("Checkpoint Failed:\n\n" + rawErr + reasonNotice);
      } else {
        const archive = (res.state && res.state.checkpointPath) || (res.items && res.items[0]) || "Saved to /var/lib/kubelet/checkpoints/";
        alert("Checkpoint Succeeded!\n\nArchive Created:\n" + archive + "\n\nPod resources have been safely reclaimed.");
      }
      await this.refresh();
    } catch (e) {
      alert("Request Error:\n\n" + e.message);
      await this.refresh();
    }
  }

  async restoreWorkload(ns, name) {
    if (!confirm(`Restore workload ${ns}/${name} from checkpoint into cluster?`)) return;

    try {
      const resp = await fetch("/api/workloads/restore", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ namespace: ns, name: name }),
      });
      const res = await resp.json();
      if (!resp.ok) {
        alert("Restore Failed:\n\n" + (res.error || res.message));
      } else {
        const restoredPodName = (res.state && res.state.name) || (res.restoredPod) || name;
        alert("Workload Restored Successfully!\n\nPod: " + restoredPodName + "\nStatus: Running on cluster node.");
      }
      await this.refresh();
    } catch (e) {
      alert("Request Error:\n\n" + e.message);
      await this.refresh();
    }
  }

  openScoreDrawer(rawItem) {
    if (!rawItem) return;
    const sim = rawItem.simulation || rawItem;
    const life = rawItem.lifecycle || {};

    const podName = document.getElementById("drawer-pod-name");
    const podMeta = document.getElementById("drawer-pod-meta");
    const vClass = document.getElementById("drawer-verdict-class");
    const vScore = document.getElementById("drawer-verdict-score");
    const vAction = document.getElementById("drawer-verdict-action");
    const vSafety = document.getElementById("drawer-verdict-safety");
    const capFull = document.getElementById("drawer-cap-full");
    const capSoft = document.getElementById("drawer-cap-soft");

    const name = sim.name || life.name || "Unknown";
    const ns = sim.namespace || life.namespace || "ecommerce";
    const node = sim.nodeName || "adaptive-cluster-control-plane";
    const kind = sim.ownerKind || "Deployment";

    if (podName) podName.textContent = name;
    if (podMeta) podMeta.innerHTML = `Namespace: ${ns} &bull; Node: ${node} &bull; Kind: ${kind}`;

    let clsStr = "ACTIVE";
    if (sim.classification === 2 || sim.classification === "IDLE" || sim.classification === "ClassIdle") clsStr = "IDLE";
    else if (sim.classification === 1 || sim.classification === "LOW_USAGE" || sim.classification === "ClassLowUsage") clsStr = "LOW_USAGE";
    const classBadgeClass = clsStr === "ACTIVE" ? "badge-active" : (clsStr === "LOW_USAGE" ? "badge-low" : "badge-idle");

    let actionStr = life.action;
    if (!actionStr) {
      if (sim.action === 2 || sim.action === "FULL_RECLAIM") actionStr = "FULL_RECLAIM";
      else if (sim.action === 1 || sim.action === "SOFT_RECLAIM") actionStr = "SOFT_RECLAIM";
      else actionStr = "KEEP";
    }
    const actionBadgeClass = actionStr === "FULL_RECLAIM" ? "badge-full" : (actionStr === "SOFT_RECLAIM" ? "badge-soft" : "badge-keep");

    if (vClass) vClass.innerHTML = `<span class="status-badge ${classBadgeClass}">${clsStr}</span>`;
    const scoreVal = (life.score !== undefined && life.score !== null) ? life.score : (sim.score || 0);
    if (vScore) vScore.textContent = Number(scoreVal).toFixed(4);
    if (vAction) vAction.innerHTML = `<span class="status-badge ${actionBadgeClass}">${actionStr}</span>`;

    const caps = sim.capabilities || {};
    const fullAllowed = caps.fullReclaimAllowed !== false;
    const softAllowed = caps.softReclaimAllowed !== false;
    if (vSafety) {
      vSafety.innerHTML = (fullAllowed || softAllowed)
        ? `<span class="badge-safety-pass">PASSED</span>`
        : `<span class="badge-safety-block">BLOCKED</span>`;
    }
    if (capFull) {
      capFull.textContent = fullAllowed ? "AVAILABLE" : "BLOCKED";
      capFull.className = fullAllowed ? "badge-safety-pass" : "badge-safety-block";
    }
    if (capSoft) {
      capSoft.textContent = softAllowed ? "AVAILABLE" : "BLOCKED";
      capSoft.className = softAllowed ? "badge-safety-pass" : "badge-safety-block";
    }

    // Populate decision rationale lists
    const ulDecision = document.getElementById("drawer-reasons-decision");
    if (ulDecision) {
      ulDecision.innerHTML = "";
      const reasons = sim.decisionReasons || life.decisionReasons || [];
      if (reasons.length === 0) {
        ulDecision.innerHTML = `<li class="reason-li info">Workload operating under normal load &mdash; no reclaim required</li>`;
      } else {
        for (const r of reasons) {
          ulDecision.innerHTML += `<li class="reason-li positive"><svg class="icon-svg sm" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"></polyline></svg><span>${r}</span></li>`;
        }
      }
    }

    const ulRejection = document.getElementById("drawer-reasons-rejection");
    if (ulRejection) {
      ulRejection.innerHTML = "";
      const rejections = sim.rejectionReasons || [];
      if (rejections.length === 0) {
        ulRejection.innerHTML = `<li class="reason-li info"><svg class="icon-svg sm" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"></polyline></svg><span>Safety criteria passed &mdash; zero gating restrictions active</span></li>`;
      } else {
        for (const r of rejections) {
          ulRejection.innerHTML += `<li class="reason-li negative"><svg class="icon-svg sm" viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg><span>${r}</span></li>`;
        }
      }
    }

    const drawer = document.getElementById("drawer-panel");
    const backdrop = document.getElementById("drawer-backdrop");
    if (drawer) drawer.classList.add("open");
    if (backdrop) backdrop.classList.add("active");
  }

  async openConfigModal() {
    const modal = document.getElementById("modal-reclaim-config");
    if (!modal) return;
    try {
      const resp = await fetch("/api/reclaim/config");
      const cfg = await resp.json();
      this.currentConfig = cfg;

      const setVal = (id, val) => {
        const el = document.getElementById(id);
        if (el) el.value = val !== undefined && val !== null ? val : "";
      };

      if (cfg.thresholds) {
        setVal("cfg-full-reclaim", cfg.thresholds.full_reclaim);
        setVal("cfg-soft-reclaim", cfg.thresholds.soft_reclaim);
      }

      if (cfg.weights) {
        setVal("cfg-w-cpu", cfg.weights.cpu);
        setVal("cfg-w-mem", cfg.weights.memory);
        setVal("cfg-w-idle", cfg.weights.idle);
        setVal("cfg-w-benefit", cfg.weights.benefit);
        setVal("cfg-w-replica", cfg.weights.replica);
        setVal("cfg-w-priority", cfg.weights.priority);
        setVal("cfg-w-pdb", cfg.weights.pdb);
        setVal("cfg-w-state", cfg.weights.state);
        
        const chkInput = document.getElementById("cfg-w-checkpoint") || document.getElementById("cfg-w-chk");
        if (chkInput) chkInput.value = cfg.weights.checkpoint;
      }

      modal.classList.add("active");
    } catch (e) {
      console.error("Failed loading reclaim config:", e);
    }
  }

  handleSaveConfig(e) {
    if (e) e.preventDefault();
    const modal = document.getElementById("modal-reclaim-config");
    if (modal) modal.classList.remove("active");
  }
}
