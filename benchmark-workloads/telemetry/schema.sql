-- =============================================================================
-- Adaptive Kubernetes Scheduler Benchmark Telemetry Database Schema
-- Location: benchmark-workloads/telemetry/benchmark_telemetry.sqlite3
-- =============================================================================

PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 10000;

-- 1. Benchmark Execution Runs
CREATE TABLE IF NOT EXISTS benchmark_runs (
    run_id INTEGER PRIMARY KEY AUTOINCREMENT,
    start_time TEXT NOT NULL,
    end_time TEXT,
    mode TEXT DEFAULT 'timewarp',
    timewarp_factor INTEGER DEFAULT 48,
    duration_planned TEXT,
    total_samples INTEGER DEFAULT 0,
    status TEXT DEFAULT 'RUNNING'
);

-- 2. Dual-Cluster Time-Series Telemetry (Default vs Adaptive)
CREATE TABLE IF NOT EXISTS telemetry_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER DEFAULT 1,
    timestamp TEXT NOT NULL,
    elapsed_seconds REAL NOT NULL,
    node_mem_available_mb REAL NOT NULL,
    def_node_mem_available_mb REAL DEFAULT 0,
    adp_node_mem_available_mb REAL DEFAULT 0,
    def_mem_working_set_mb REAL NOT NULL,
    adp_mem_working_set_mb REAL NOT NULL,
    def_cpu_millicores REAL NOT NULL,
    adp_cpu_millicores REAL NOT NULL,
    saved_mem_mb REAL NOT NULL,
    def_replicas INTEGER NOT NULL,
    adp_replicas INTEGER NOT NULL,
    FOREIGN KEY(run_id) REFERENCES benchmark_runs(run_id)
);

-- 3. Checkpoint & Autonomous Reclamation Audit Log
CREATE TABLE IF NOT EXISTS checkpoint_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER DEFAULT 1,
    timestamp TEXT NOT NULL,
    pod_name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    action TEXT NOT NULL,
    status TEXT DEFAULT 'SUCCESS',
    status_message TEXT DEFAULT '',
    score REAL DEFAULT 0.0,
    memory_freed_mb REAL DEFAULT 0.0,
    checkpoint_size_mb REAL DEFAULT 0.0,
    checkpoint_path TEXT,
    FOREIGN KEY(run_id) REFERENCES benchmark_runs(run_id)
);

-- 4. Scheduler Evaluation Cycles & 9-Factor Scoring Details
CREATE TABLE IF NOT EXISTS scheduler_evaluation_cycles (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id INTEGER DEFAULT 1,
    cycle_number INTEGER DEFAULT 0,
    timestamp TEXT NOT NULL,
    elapsed_seconds REAL NOT NULL,
    pod_name TEXT NOT NULL,
    namespace TEXT NOT NULL,
    workload_kind TEXT DEFAULT 'Pod',
    phase TEXT DEFAULT 'Running',
    node_name TEXT DEFAULT '',
    in_cooldown INTEGER DEFAULT 0,
    cooldown_remaining_sec REAL DEFAULT 0.0,
    is_candidate INTEGER NOT NULL,
    stage1_classification TEXT NOT NULL,
    cpu_millicores REAL DEFAULT 0.0,
    cpu_threshold_millicores REAL DEFAULT 20.0,
    cpu_util_pct REAL DEFAULT 0.0,
    memory_working_set_mb REAL DEFAULT 0.0,
    memory_requested_mb REAL DEFAULT 0.0,
    memory_util_pct REAL DEFAULT 0.0,
    network_bytes_sec REAL DEFAULT 0.0,
    qps REAL DEFAULT 0.0,
    idle_duration_sec REAL DEFAULT 0.0,
    min_idle_duration_sec REAL DEFAULT 30.0,
    duration_satisfied INTEGER DEFAULT 0,
    sample_count INTEGER DEFAULT 0,
    min_samples INTEGER DEFAULT 3,
    samples_satisfied INTEGER DEFAULT 0,
    is_consistently_idle INTEGER DEFAULT 0,
    stage1_reasons TEXT,
    composite_score REAL DEFAULT 0.0,
    decision_action TEXT NOT NULL,
    soft_threshold REAL DEFAULT 0.3361,
    full_threshold REAL DEFAULT 0.5927,
    score_cpu REAL DEFAULT 0.0,
    score_mem REAL DEFAULT 0.0,
    score_benefit REAL DEFAULT 0.0,
    score_priority REAL DEFAULT 0.0,
    score_state REAL DEFAULT 0.0,
    score_replica REAL DEFAULT 0.0,
    full_reclaim_allowed INTEGER DEFAULT 1,
    soft_reclaim_allowed INTEGER DEFAULT 1,
    rejection_reasons TEXT,
    decision_reasons TEXT,
    FOREIGN KEY(run_id) REFERENCES benchmark_runs(run_id)
);

-- Performance Indexes
CREATE INDEX IF NOT EXISTS idx_telemetry_run ON telemetry_samples(run_id);
CREATE INDEX IF NOT EXISTS idx_telemetry_ts ON telemetry_samples(timestamp);
CREATE INDEX IF NOT EXISTS idx_checkpoint_run ON checkpoint_events(run_id);
CREATE INDEX IF NOT EXISTS idx_eval_run ON scheduler_evaluation_cycles(run_id);
CREATE INDEX IF NOT EXISTS idx_eval_pod ON scheduler_evaluation_cycles(pod_name);
CREATE INDEX IF NOT EXISTS idx_eval_ts ON scheduler_evaluation_cycles(timestamp);
