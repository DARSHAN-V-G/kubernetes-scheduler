#!/usr/bin/env python3
"""
High-Frequency Per-Second Telemetry Collector
Scrapes Prometheus PromQL metrics every second and records to SQLite3 database.
Also maintains an in-memory ring-buffer for the real-time live dashboard.
"""

import os
import sys
import time
import json
import sqlite3
import urllib.parse
import urllib.request
from collections import deque
from threading import Lock

DB_PATH = os.path.join(os.path.dirname(__file__), "benchmark_telemetry.sqlite3")
PROMETHEUS_URL = os.environ.get("PROMETHEUS_URL", "http://127.0.0.1:9090")

# Thread-safe ring buffer for the last 600 seconds (10 minutes) of live data
live_buffer = deque(maxlen=600)
buffer_lock = Lock()

def init_database(db_path=DB_PATH, reset=False):
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()
    cur.execute("PRAGMA journal_mode=DELETE;")
    cur.execute("PRAGMA synchronous=NORMAL;")

    if reset:
        cur.execute("DROP TABLE IF EXISTS telemetry_samples;")
        cur.execute("DROP TABLE IF EXISTS benchmark_runs;")

    cur.execute("""
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
    """)

    cur.execute("""
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
    """)
    try:
        cur.execute("ALTER TABLE telemetry_samples ADD COLUMN run_id INTEGER DEFAULT 1;")
    except Exception:
        pass
    try:
        cur.execute("ALTER TABLE telemetry_samples ADD COLUMN def_node_mem_available_mb REAL DEFAULT 0;")
        cur.execute("ALTER TABLE telemetry_samples ADD COLUMN adp_node_mem_available_mb REAL DEFAULT 0;")
    except Exception:
        pass

    cur.execute("""
        CREATE TABLE IF NOT EXISTS checkpoint_events (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            run_id INTEGER DEFAULT 1,
            timestamp TEXT NOT NULL,
            pod_name TEXT NOT NULL,
            namespace TEXT NOT NULL,
            action TEXT NOT NULL,
            score REAL DEFAULT 0.0,
            memory_freed_mb REAL DEFAULT 0.0,
            checkpoint_size_mb REAL DEFAULT 0.0,
            checkpoint_path TEXT,
            FOREIGN KEY(run_id) REFERENCES benchmark_runs(run_id)
        );
    """)

    cur.execute("CREATE INDEX IF NOT EXISTS idx_telemetry_run ON telemetry_samples(run_id);")
    cur.execute("CREATE INDEX IF NOT EXISTS idx_telemetry_ts ON telemetry_samples(timestamp);")
    cur.execute("CREATE INDEX IF NOT EXISTS idx_checkpoint_run ON checkpoint_events(run_id);")
    conn.commit()
    conn.close()

def record_checkpoint_event(run_id: int, pod_name: str, namespace: str, action: str,
                            score: float = 0.0, memory_freed_mb: float = 0.0,
                            checkpoint_size_mb: float = 0.0, checkpoint_path: str = "",
                            db_path=DB_PATH):
    try:
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        ts = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        cur.execute("""
            INSERT INTO checkpoint_events (
                run_id, timestamp, pod_name, namespace, action,
                score, memory_freed_mb, checkpoint_size_mb, checkpoint_path
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
        """, (run_id, ts, pod_name, namespace, action, score, memory_freed_mb, checkpoint_size_mb, checkpoint_path))
        conn.commit()
        conn.close()
    except Exception as e:
        print(f"[WARN] Failed to record checkpoint event: {e}")

def get_or_create_active_run_id(db_path=DB_PATH, mode="timewarp", timewarp_factor=48, duration="30m") -> int:
    env_run_id = os.environ.get("BENCHMARK_RUN_ID")
    if env_run_id:
        try:
            return int(env_run_id)
        except ValueError:
            pass

    init_database(db_path)
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()
    cur.execute("SELECT run_id FROM benchmark_runs WHERE status = 'RUNNING' ORDER BY run_id DESC LIMIT 1;")
    row = cur.fetchone()
    if row:
        run_id = row[0]
    else:
        ts = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        cur.execute("""
            INSERT INTO benchmark_runs (start_time, mode, timewarp_factor, duration_planned, status)
            VALUES (?, ?, ?, ?, 'RUNNING')
        """, (ts, mode, timewarp_factor, duration))
        conn.commit()
        run_id = cur.lastrowid
    conn.close()
    return run_id

def finish_benchmark_run(run_id: int, db_path=DB_PATH, status="COMPLETED"):
    try:
        conn = sqlite3.connect(db_path)
        cur = conn.cursor()
        ts = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        cur.execute("SELECT COUNT(*) FROM telemetry_samples WHERE run_id = ?;", (run_id,))
        count = cur.fetchone()[0] or 0
        cur.execute("""
            UPDATE benchmark_runs
            SET end_time = ?, total_samples = ?, status = ?
            WHERE run_id = ?;
        """, (ts, count, status, run_id))
        conn.commit()
        conn.close()
    except Exception:
        pass

def query_prometheus(query: str, prom_url: str = PROMETHEUS_URL) -> float:
    try:
        url = f"{prom_url}/api/v1/query?query={urllib.parse.quote(query)}"
        req = urllib.request.Request(url, headers={"User-Agent": "BenchmarkTelemetry/1.0"})
        with urllib.request.urlopen(req, timeout=1.8) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            if data.get("status") == "success" and data.get("data", {}).get("result"):
                val = float(data["data"]["result"][0]["value"][1])
                return round(val, 2)
    except Exception:
        pass
    return 0.0

def query_replica_count(namespace: str, prom_url: str = PROMETHEUS_URL) -> int:
    q = f"sum(kube_deployment_status_replicas_ready{{namespace='{namespace}'}})"
    val = query_prometheus(q, prom_url)
    return int(val) if val > 0 else 0

def collect_sample(elapsed: float, prom_url: str = PROMETHEUS_URL) -> dict:
    ts = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())

    # 1. Base Host Physical Available Memory
    q_node_free = "node_memory_MemAvailable_bytes / (1024 * 1024)"
    base_free = query_prometheus(q_node_free, prom_url)
    if base_free <= 0.0:
        base_free = max(1000.0, 3800.0 - query_prometheus("sum(container_memory_working_set_bytes{container!='', container!='POD'}) / (1024*1024)", prom_url))

    # 2. Default Scheduler Workloads (running on worker-1: benchmark.role=default)
    q_def_mem = "sum(container_memory_working_set_bytes{namespace='test-application-default', container!='', container!='POD'}) / (1024 * 1024)"
    q_def_cpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application-default', container!='', container!='POD'}[30s])) * 1000"
    def_mem = query_prometheus(q_def_mem, prom_url)
    def_cpu = query_prometheus(q_def_cpu, prom_url)

    # 3. Adaptive Scheduler Workloads (running on worker-2: benchmark.role=adaptive)
    q_adp_mem = "sum(container_memory_working_set_bytes{namespace='test-application-adaptive', container!='', container!='POD'}) / (1024 * 1024)"
    q_adp_cpu = "sum(rate(container_cpu_usage_seconds_total{namespace='test-application-adaptive', container!='', container!='POD'}[30s])) * 1000"
    adp_mem = query_prometheus(q_adp_mem, prom_url)
    adp_cpu = query_prometheus(q_adp_cpu, prom_url)

    def_rep = query_replica_count("test-application-default", prom_url)
    adp_rep = query_replica_count("test-application-adaptive", prom_url)

    # 4. Two Distinct Node Free Memory Calculations:
    # Try querying direct physical worker node metrics from cAdvisor if available:
    q_def_direct = "(machine_memory_bytes{node=~'.*worker$|.*default.*'} - sum(container_memory_working_set_bytes{node=~'.*worker$|.*default.*', container!='', container!='POD'})) / (1024 * 1024)"
    q_adp_direct = "(machine_memory_bytes{node=~'.*worker2|.*adaptive.*'} - sum(container_memory_working_set_bytes{node=~'.*worker2|.*adaptive.*', container!='', container!='POD'})) / (1024 * 1024)"
    def_direct = query_prometheus(q_def_direct, prom_url)
    adp_direct = query_prometheus(q_adp_direct, prom_url)
    if def_direct > 0 and adp_direct > 0:
        def_node_free = round(def_direct, 2)
        adp_node_free = round(adp_direct, 2)
    else:
        # Derived from base host physical headroom and per-workload working sets
        def_node_free = round(base_free - def_mem, 2)
        adp_node_free = round(base_free - adp_mem, 2)

    # Instantaneous memory saved
    saved_mem_mb = max(0.0, round(def_mem - adp_mem, 2))

    sample = {
        "timestamp": ts,
        "elapsed_seconds": round(elapsed, 1),
        "node_mem_available_mb": round((def_node_free + adp_node_free) / 2.0, 2),
        "def_node_mem_available_mb": def_node_free,
        "adp_node_mem_available_mb": adp_node_free,
        "def_mem_working_set_mb": def_mem,
        "adp_mem_working_set_mb": adp_mem,
        "def_cpu_millicores": def_cpu,
        "adp_cpu_millicores": adp_cpu,
        "saved_mem_mb": saved_mem_mb,
        "def_replicas": def_rep,
        "adp_replicas": adp_rep
    }

    # Append to in-memory live buffer
    with buffer_lock:
        live_buffer.append(sample)

    return sample

def get_live_history(limit: int = 120):
    with buffer_lock:
        data = list(live_buffer)
        if len(data) > limit:
            return data[-limit:]
        return data

def run_collector_loop(db_path=DB_PATH, prom_url=PROMETHEUS_URL, interval=1.0):
    run_id = get_or_create_active_run_id(db_path)
    conn = sqlite3.connect(db_path)
    cur = conn.cursor()

    print(f"[*] Telemetry Collector started. PromQL Target: {prom_url}")
    print(f"[*] SQLite Database: {db_path} (High-freq 1s resolution, Run ID: {run_id})")

    start_time = time.time()
    batch_count = 0

    try:
        while True:
            t0 = time.time()
            elapsed = t0 - start_time
            s = collect_sample(elapsed, prom_url)

            cur.execute("""
                INSERT INTO telemetry_samples (
                    run_id, timestamp, elapsed_seconds, node_mem_available_mb,
                    def_node_mem_available_mb, adp_node_mem_available_mb,
                    def_mem_working_set_mb, adp_mem_working_set_mb,
                    def_cpu_millicores, adp_cpu_millicores,
                    saved_mem_mb, def_replicas, adp_replicas
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            """, (
                run_id, s["timestamp"], s["elapsed_seconds"], s["node_mem_available_mb"],
                s["def_node_mem_available_mb"], s["adp_node_mem_available_mb"],
                s["def_mem_working_set_mb"], s["adp_mem_working_set_mb"],
                s["def_cpu_millicores"], s["adp_cpu_millicores"],
                s["saved_mem_mb"], s["def_replicas"], s["adp_replicas"]
            ))

            batch_count += 1
            if batch_count >= 5: # Commit every 5 seconds to reduce disk I/O
                conn.commit()
                batch_count = 0

            # Dynamic sleep to maintain precise 1-second cadence
            proc_time = time.time() - t0
            sleep_time = max(0.05, interval - proc_time)
            time.sleep(sleep_time)

    except KeyboardInterrupt:
        print("\nStopping collector...")
        conn.commit()
        conn.close()

if __name__ == "__main__":
    run_collector_loop()
