// API Client for the Kubernetes Intelligence Simulator Backend

const API_BASE = window.location.origin;

export const api = {
  // Check backend health and pipeline status
  async getHealth() {
    const res = await fetch(`${API_BASE}/api/health`);
    if (!res.ok) throw new Error(`Health check failed: ${res.status}`);
    return res.json();
  },

  // Get list of preset scenario metadata
  async getPresets() {
    const res = await fetch(`${API_BASE}/api/presets`);
    if (!res.ok) throw new Error(`Failed to fetch presets: ${res.status}`);
    return res.json();
  },

  // Get a full scenario by ID
  async getPreset(id) {
    const res = await fetch(`${API_BASE}/api/presets/${id}`);
    if (!res.ok) throw new Error(`Failed to fetch preset ${id}: ${res.status}`);
    return res.json();
  },

  // Run full simulation on cluster model
  async simulate(cluster) {
    const res = await fetch(`${API_BASE}/api/simulate`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(cluster),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(err.error || `Simulation failed: ${res.status}`);
    }
    return res.json();
  },

  // Alias for backward compatibility
  async runSimulation(cluster) {
    return this.simulate(cluster);
  },

  // Run ad-hoc simulation on a single workload
  async simulateSingleWorkload(workload) {
    const res = await fetch(`${API_BASE}/api/simulate/workload`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(workload),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(err.error || `Single simulation failed: ${res.status}`);
    }
    return res.json();
  },
};
