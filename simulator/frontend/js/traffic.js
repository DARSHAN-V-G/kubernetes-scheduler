// Live Traffic Simulation & Scheduler Decision Monitor Controller
// Connects to /api/traffic/* and /api/workloads to demonstrate live scheduler behavior

export class TrafficMonitorController {
  constructor() {
    this.status = null;
    this.workloads = [];
    this.pollTimer = null;
    this.customWindowSeconds = 30;
    this.isPolling = true;
  }

  async init() {
    this.bindEvents();
    await this.refresh();
  }

  bindEvents() {
    const btnStart = document.getElementById("btn-traffic-start");
    if (btnStart) {
      btnStart.addEventListener("click", () => this.startTraffic());
    }

    const btnStop = document.getElementById("btn-traffic-stop");
    if (btnStop) {
      btnStop.addEventListener("click", () => this.stopTraffic());
    }

    const btnBurst = document.getElementById("btn-traffic-burst");
    if (btnBurst) {
      btnBurst.addEventListener("click", () => this.burstTraffic(20));
    }

    const btnWake = document.getElementById("btn-traffic-wake");
    if (btnWake) {
      btnWake.addEventListener("click", () => this.wakeWorkload());
    }

    const btnRefresh = document.getElementById("btn-traffic-refresh");
    if (btnRefresh) {
      btnRefresh.addEventListener("click", () => this.refresh());
    }

    const autoPollCheck = document.getElementById("traffic-auto-poll");
    if (autoPollCheck) {
      autoPollCheck.addEventListener("change", (e) => {
        this.isPolling = e.target.checked;
        if (this.isPolling) this.startPolling();
        else this.stopPolling();
      });
    }

    const nsInput = document.getElementById("traffic-input-namespace");
    if (nsInput) {
      nsInput.addEventListener("change", () => this.refresh());
      nsInput.addEventListener("keyup", (e) => {
        if (e.key === "Enter") this.refresh();
      });
    }
  }

  startPolling() {
    this.stopPolling();
    this.pollTimer = setInterval(() => {
      this.refresh(true);
    }, 2000);
  }

  stopPolling() {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }

  async startTraffic() {
    const btn = document.getElementById("btn-traffic-start");
    if (btn) btn.classList.add("loading");

    try {
      const resp = await fetch("/api/traffic/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          targetUrl: "http://localhost:8088",
          concurrency: 4,
          intervalMs: 600,
        }),
      });
      if (resp.ok) {
        await this.refresh();
      }
    } catch (e) {
      console.error("Failed to start traffic:", e);
    } finally {
      if (btn) btn.classList.remove("loading");
    }
  }

  async stopTraffic() {
    const btn = document.getElementById("btn-traffic-stop");
    if (btn) btn.classList.add("loading");

    try {
      const resp = await fetch("/api/traffic/stop", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
      });
      if (resp.ok) {
        await this.refresh();
      }
    } catch (e) {
      console.error("Failed to stop traffic:", e);
    } finally {
      if (btn) btn.classList.remove("loading");
    }
  }

  async burstTraffic(count = 20) {
    const btn = document.getElementById("btn-traffic-burst");
    if (btn) btn.classList.add("loading");

    try {
      await fetch("/api/traffic/burst", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          targetUrl: "http://localhost:8088",
          count: count,
        }),
      });
      await this.refresh();
    } catch (e) {
      console.error("Failed to burst traffic:", e);
    } finally {
      if (btn) btn.classList.remove("loading");
    }
  }

  async wakeWorkload() {
    const btn = document.getElementById("btn-traffic-wake");
    if (btn) btn.classList.add("loading");

    try {
      const resp = await fetch("/api/traffic/wake", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          activatorUrl: "http://localhost:8085",
          targetService: "ecommerce/backend-api:3000",
        }),
      });
      const data = await resp.json();
      if (data.success) {
        alert(`Workload Reconstituted Successfully!\n\nDuration: ${data.durationSec || "0.2s"}\nStatus: ${data.response?.status || "UP"}`);
      } else {
        alert(`Workload Wakeup: ${data.error || "Completed"}`);
      }
      await this.refresh();
    } catch (e) {
      console.error("Failed to wake workload:", e);
    } finally {
      if (btn) btn.classList.remove("loading");
    }
  }

  async refresh(silent = false) {
    const btnRefresh = document.getElementById("btn-traffic-refresh");
    if (!silent && btnRefresh) btnRefresh.classList.add("loading");

    try {
      await Promise.all([this.loadTrafficStatus(), this.loadWorkloads()]);
    } catch (e) {
      console.error("Traffic monitor refresh failed:", e);
    } finally {
      if (!silent && btnRefresh) btnRefresh.classList.remove("loading");
    }
  }

  async loadTrafficStatus() {
    try {
      const resp = await fetch("/api/traffic/status");
      if (!resp.ok) return;
      const s = await resp.json();
      this.status = s;

      const pill = document.getElementById("traffic-status-indicator");
      const text = document.getElementById("traffic-status-text");
      if (pill && text) {
        if (s.running) {
          pill.className = "traffic-status-pill active";
          text.textContent = `TRAFFIC: GENERATING (${s.currentQps || 0} req/s)`;
        } else {
          pill.className = "traffic-status-pill idle";
          text.textContent = "TRAFFIC: IDLE (STOPPED)";
        }
      }

      const elTarget = document.getElementById("traffic-target-url");
      if (elTarget) elTarget.textContent = s.targetUrl || "http://localhost:8088";

      const elSent = document.getElementById("traffic-stat-sent");
      if (elSent) elSent.textContent = (s.requestsSent || 0).toLocaleString();

      const elSuccess = document.getElementById("traffic-stat-success");
      if (elSuccess) elSuccess.textContent = (s.successCount || 0).toLocaleString();

      const elErrors = document.getElementById("traffic-stat-errors");
      if (elErrors) elErrors.textContent = (s.errorCount || 0).toLocaleString();

      const elQps = document.getElementById("traffic-stat-qps");
      if (elQps) elQps.textContent = s.currentQps || "0.0";
    } catch (e) {
      console.warn("Error fetching traffic status:", e);
    }
  }

  async loadWorkloads() {
    const tbody = document.getElementById("traffic-workloads-tbody");
    try {
      const nsInput = document.getElementById("traffic-input-namespace");
      const ns = nsInput ? nsInput.value.trim() : "test-application";
      const nsParam = ns ? `&namespace=${encodeURIComponent(ns)}` : "";
      const resp = await fetch(`/api/workloads?window=${this.customWindowSeconds}${nsParam}`);
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

        const scoreVal = (life.score !== undefined && life.score !== null) ? life.score : (sim.score || 0);

        return {
          name: sim.name || life.name,
          namespace: sim.namespace || life.namespace,
          phase: sim.phase || "Unknown",
          avgCpuMillicores: sim.avgCpuMillicores || 0,
          requestedCpuMillis: sim.requestedCpuMillis || sim.requestedCPUMillis || 100,
          avgMemoryBytes: sim.avgMemoryBytes || 0,
          requestedMemoryBytes: sim.requestedMemoryBytes || (64 * 1024 * 1024),
          detectedIdleDuration: sim.detectedIdleDuration || 0,
          qps: sim.qps || 0,
          classification: clsStr,
          score: scoreVal,
          action: actionStr,
          lifecycleState: life.state || (sim.phase === "Reclaimed" ? "RECLAIMED" : "RUNNING"),
          decisionReasons: sim.decisionReasons || life.decisionReasons || [],
          rawItem: item,
        };
      });

      // Update fully reclaimed counter in stats strip
      const reclaimedCount = this.workloads.filter(w => w.lifecycleState === "RECLAIMED" || w.phase === "Reclaimed").length;
      const elReclaimed = document.getElementById("traffic-stat-reclaimed");
      if (elReclaimed) elReclaimed.textContent = reclaimedCount.toString();

      this.renderTable();
    } catch (e) {
      if (tbody) {
        tbody.innerHTML = `<tr><td colspan="11" style="text-align:center; padding: 24px; color: var(--color-red);">Error fetching workloads: ${e.message}</td></tr>`;
      }
    }
  }

  renderTable() {
    const tbody = document.getElementById("traffic-workloads-tbody");
    if (!tbody) return;

    if (!this.workloads || this.workloads.length === 0) {
      tbody.innerHTML = `<tr><td colspan="11" style="text-align:center; padding: 24px; color: var(--text-dim);">No active workloads discovered in namespace.</td></tr>`;
      return;
    }

    tbody.innerHTML = this.workloads.map((w, idx) => {
      const isReclaimed = (w.lifecycleState === "RECLAIMED" || w.lifecycleState === "CHECKPOINTED" || w.phase === "Reclaimed");

      // 1. Status Badge: (reclaimed, idle, or active)
      let statusBadge = "";
      if (isReclaimed) {
        statusBadge = `<span class="badge-status-pill reclaimed" style="background: rgba(168, 85, 247, 0.22); color: #c084fc; border: 1px solid rgba(168, 85, 247, 0.5); font-weight:700;">● RECLAIMED (DORMANT)</span>`;
      } else if (w.classification === "IDLE") {
        const idleSec = Math.round(w.detectedIdleDuration / 1e9) || 0;
        const idleLabel = idleSec > 0 ? `IDLE (${idleSec}s)` : "IDLE";
        statusBadge = `<span class="badge-status-pill idle" style="background: rgba(245, 158, 11, 0.18); color: #fbbf24; border: 1px solid rgba(245, 158, 11, 0.4); font-weight:700;">● ${idleLabel}</span>`;
      } else {
        statusBadge = `<span class="badge-status-pill pass" style="background: rgba(16, 185, 129, 0.18); color: #34d399; border: 1px solid rgba(16, 185, 129, 0.4); font-weight:700;">● ACTIVE</span>`;
      }

      // 2. Scheduler Decision: (full reclaim, soft reclaim, no action / keep)
      let decisionBadge = "";
      if (isReclaimed || w.action === "FULL_RECLAIM") {
        decisionBadge = `<span class="badge-status-pill blocked" style="background: rgba(168, 85, 247, 0.25); color: #c084fc; border: 1px solid rgba(168, 85, 247, 0.6); font-weight:700;">FULL RECLAIM</span>`;
      } else if (w.action === "SOFT_RECLAIM") {
        decisionBadge = `<span class="badge-status-pill soft" style="background: rgba(245, 158, 11, 0.2); color: #f59e0b; border: 1px solid rgba(245, 158, 11, 0.4); font-weight:700;">SOFT RECLAIM</span>`;
      } else {
        decisionBadge = `<span class="badge-status-pill pass" style="background: rgba(56, 189, 248, 0.15); color: #38bdf8; border: 1px solid rgba(56, 189, 248, 0.35); font-weight:700;">NO ACTION (KEEP)</span>`;
      }

      // Reclaim Score Bar
      const scoreNum = typeof w.score === "number" ? w.score : 0;
      const scorePct = Math.min(100, Math.max(0, Math.round(scoreNum * 100)));
      const scoreColor = isReclaimed ? "#c084fc" : "var(--color-brand)";
      const scoreBar = `
        <div style="display:flex; align-items:center; gap:8px;">
          <div style="width:60px; height:6px; background:rgba(255,255,255,0.08); border-radius:3px; overflow:hidden;">
            <div style="width:${scorePct}%; height:100%; background:${scoreColor}; border-radius:3px;"></div>
          </div>
          <span class="mono bold" style="font-size:12px; ${isReclaimed ? 'color:#c084fc;' : ''}">${scoreNum.toFixed(4)}</span>
        </div>`;

      // CPU usage
      const cpuUsed = w.avgCpuMillicores.toFixed(1);
      const cpuReq = w.requestedCpuMillis;
      const cpuPct = cpuReq > 0 ? Math.round((w.avgCpuMillicores / cpuReq) * 100) : 0;
      let cpuColHtml = "";
      if (isReclaimed) {
        cpuColHtml = `
          <div class="mono" style="font-size:11px; color:#c084fc;">0m / ${cpuReq}m</div>
          <div style="font-size:10px; color:#a78bfa;">${cpuReq}m freed</div>`;
      } else {
        cpuColHtml = `
          <div class="mono" style="font-size:11px;">${cpuUsed}m / ${cpuReq}m</div>
          <div style="font-size:10px; color:var(--text-dim);">${cpuPct}% req</div>`;
      }

      // Memory usage
      const memMb = (w.avgMemoryBytes / (1024 * 1024)).toFixed(1);
      const memReqMb = Math.round(w.requestedMemoryBytes / (1024 * 1024));
      let memColHtml = "";
      if (isReclaimed) {
        memColHtml = `
          <div class="mono" style="font-size:11px; color:#c084fc;">0M / ${memReqMb}M</div>
          <div style="font-size:10px; color:#a78bfa;">${memReqMb}M freed</div>`;
      } else {
        memColHtml = `
          <div class="mono" style="font-size:11px;">${memMb}M / ${memReqMb}M</div>`;
      }

      // QPS
      const qpsVal = (w.qps || 0).toFixed(2);

      // Idle Duration
      const idleSecTotal = Math.round(w.detectedIdleDuration / 1e9);
      const idleStr = isReclaimed ? "Dormant" : (idleSecTotal > 0 ? `${idleSecTotal}s` : "0s");

      // Lifecycle State
      let statePill = `<span class="badge-status-pill pass" style="font-size:11px;">${w.lifecycleState}</span>`;
      if (isReclaimed) {
        statePill = `<span class="badge-status-pill reclaimed" style="font-size:11px; background: rgba(168, 85, 247, 0.22); color: #c084fc; border: 1px solid rgba(168, 85, 247, 0.5); font-weight:700;">RECLAIMED</span>`;
      } else if (w.lifecycleState === "CANDIDATE") {
        statePill = `<span class="badge-status-pill soft" style="font-size:11px;">CANDIDATE</span>`;
      } else if (w.lifecycleState === "CHECKPOINTED" || w.lifecycleState === "RESTORED") {
        statePill = `<span class="badge-status-pill blocked" style="font-size:11px; color:#c084fc; border-color:#c084fc;">${w.lifecycleState}</span>`;
      }

      // Actions Column
      let actionsHtml = "";
      if (isReclaimed) {
        actionsHtml = `
          <button class="btn btn-restore-traffic" data-ns="${this.escape(w.namespace)}" data-name="${this.escape(w.name)}" style="background:#8b5cf6; color:#fff; border:1px solid #a78bfa; padding:2px 8px; font-size:11px; font-weight:600; border-radius:4px; cursor:pointer;" title="Reconstitute reclaimed workload">
            Restore
          </button>
          <button class="btn btn-subtle btn-inspect-traffic" data-idx="${idx}" style="padding: 2px 8px; font-size:11px;" title="Inspect 9-Factor Decision Breakdown">
            Inspect &rarr;
          </button>`;
      } else if (w.action === "FULL_RECLAIM" || w.action === "SOFT_RECLAIM" || w.classification === "IDLE") {
        actionsHtml = `
          <button class="btn btn-reclaim-traffic" data-ns="${this.escape(w.namespace)}" data-name="${this.escape(w.name)}" style="background:rgba(239, 68, 68, 0.15); color:#f87171; border:1px solid rgba(239, 68, 68, 0.45); padding:2px 8px; font-size:11px; font-weight:600; border-radius:4px; cursor:pointer;" title="Checkpoint and fully reclaim pod">
            Reclaim
          </button>
          <button class="btn btn-subtle btn-inspect-traffic" data-idx="${idx}" style="padding: 2px 8px; font-size:11px;" title="Inspect 9-Factor Decision Breakdown">
            Inspect &rarr;
          </button>`;
      } else {
        actionsHtml = `
          <button class="btn btn-subtle btn-inspect-traffic" data-idx="${idx}" style="padding: 2px 8px; font-size:11px;" title="Inspect 9-Factor Decision Breakdown">
            Inspect &rarr;
          </button>`;
      }

      return `
        <tr style="${isReclaimed ? 'background: rgba(168, 85, 247, 0.04);' : ''}">
          <td>
            <div class="bold text-main" style="${isReclaimed ? 'color:#c084fc;' : ''}">${this.escape(w.name)}</div>
            <div style="font-size:11px; color:${isReclaimed ? '#a78bfa' : 'var(--text-dim)'};">${this.escape(w.phase)}</div>
          </td>
          <td><span class="mono" style="font-size:11px; color:var(--text-muted);">${this.escape(w.namespace)}</span></td>
          <td>${statusBadge}</td>
          <td>${decisionBadge}</td>
          <td>${scoreBar}</td>
          <td>${cpuColHtml}</td>
          <td>${memColHtml}</td>
          <td><span class="mono bold ${isReclaimed ? '' : 'highlight'}">${qpsVal}</span></td>
          <td><span class="mono text-muted">${idleStr}</span></td>
          <td>${statePill}</td>
          <td style="text-align: right; white-space: nowrap;">
            <div style="display:inline-flex; align-items:center; gap:6px;">
              ${actionsHtml}
            </div>
          </td>
        </tr>`;
    }).join("");

    // Bind inspect buttons
    tbody.querySelectorAll(".btn-inspect-traffic").forEach(btn => {
      btn.addEventListener("click", () => {
        const idx = parseInt(btn.getAttribute("data-idx"), 10);
        const item = this.workloads[idx];
        if (item && window.clusterCtrl) {
          window.clusterCtrl.openScoreDrawer(item.rawItem);
        }
      });
    });

    // Bind restore buttons
    tbody.querySelectorAll(".btn-restore-traffic").forEach(btn => {
      btn.addEventListener("click", () => {
        const ns = btn.getAttribute("data-ns");
        const name = btn.getAttribute("data-name");
        this.restoreWorkload(ns, name);
      });
    });

    // Bind reclaim buttons
    tbody.querySelectorAll(".btn-reclaim-traffic").forEach(btn => {
      btn.addEventListener("click", () => {
        const ns = btn.getAttribute("data-ns");
        const name = btn.getAttribute("data-name");
        this.checkpointWorkload(ns, name);
      });
    });
  }

  async checkpointWorkload(ns, name) {
    if (!confirm(`Trigger full reclamation for workload ${ns}/${name}?`)) return;
    try {
      const resp = await fetch("/api/workloads/checkpoint", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ namespace: ns, name: name }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        alert("Reclamation failed: " + (data.error || "unknown error"));
      } else {
        await this.refresh(false);
      }
    } catch (e) {
      alert("Reclamation request error: " + e.message);
    }
  }

  async restoreWorkload(ns, name) {
    try {
      const resp = await fetch("/api/workloads/restore", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ namespace: ns, name: name, resolveDependencies: true }),
      });
      const data = await resp.json();
      if (!resp.ok) {
        alert("Restoration failed: " + (data.error || "unknown error"));
      } else {
        await this.refresh(false);
      }
    } catch (e) {
      alert("Restoration request error: " + e.message);
    }
  }

  escape(str) {
    if (!str) return "";
    return String(str)
      .replace(/&/g, "&amp;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }
}
