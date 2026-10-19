# ML Weight and Threshold Optimization Plan

This document outlines the strategy for utilizing the Google Borg production trace dataset (`dataset/borg_traces_data.csv`) to:
1. Derive objective scoring weights for **6 factors** using the **Entropy Weight Method (EWM)**.
2. Determine decision threshold ranges for **`KEEP`**, **`SOFT_RECLAIM`**, and **`FULL_RECLAIM`** using **K-Means Clustering**.

---

## 1. The 6 Scoring Factors (Mapped to Borg Dataset)

We focus on the 6 factors that map directly to columns in `dataset/borg_traces_data.csv`:

```text
Reclaim_Score = (W_CPU      * Score_CPU)
              + (W_Mem      * Score_Mem)
              + (W_Benefit  * Score_Benefit)
              + (W_Priority * Score_Priority)
              + (W_State    * Score_State)
              + (W_Replica  * Score_Replica)
```

### Dataset Column Mapping

| Factor Symbol | Factor Name | Raw Borg Column(s) | Extraction / Normalization Logic | Score Range | Description |
|---|---|---|---|---|---|
| `Score_CPU` | CPU Waste | `resource_request['cpus']`, `average_usage['cpus']` | `ratio = usage_cpu / max(req_cpu, 1e-9)`<br>`Score_CPU = clamp(1.0 - ratio, 0.0, 1.0)` | `[0.0, 1.0]` | Higher waste means more unutilized CPU headroom. |
| `Score_Mem` | Memory Waste | `resource_request['memory']`, `average_usage['memory']` | `ratio = usage_mem / max(req_mem, 1e-9)`<br>`Score_Mem = clamp(1.0 - ratio, 0.0, 1.0)` | `[0.0, 1.0]` | Higher waste means unallocated memory buffer. |
| `Score_Benefit` | Reclaimable Volume | `resource_request`, `average_usage` | `Diff_CPU = req_cpu - usage_cpu`<br>`Diff_Mem = req_mem - usage_mem`<br>`Score = 0.5*(Diff_CPU/P95_CPU) + 0.5*(Diff_Mem/P95_Mem)` | `[0.0, 1.0]` | Absolute CPU and Memory capacity recoverable. |
| `Score_Priority` | Workload Inversion | `priority` (0 to 450) | `Score_Priority = 1.0 - clamp(priority / 450.0, 0.0, 1.0)` | `[0.0, 1.0]` | Lower-priority batch jobs yield resources to high-priority services. |
| `Score_State` | Latency / Statefulness | `scheduling_class` (0 to 3) | Discrete mapping:<br>• Class 0 (Batch / non-prod) $\rightarrow 1.0$<br>• Class 1 (Internal service) $\rightarrow 0.7$<br>• Class 2 (Mid-tier service) $\rightarrow 0.4$<br>• Class 3 (User-facing latency-critical) $\rightarrow 0.1$ | `[0.0, 1.0]` | Stateless batch tasks tolerate disruption better than latency-critical services. |
| `Score_Replica` | Service Redundancy | `collection_id`, `instance_index` | Group by `collection_id`, count active instances:<br>• $\ge 4$ instances $\rightarrow 1.0$<br>• $3$ instances $\rightarrow 0.8$<br>• $2$ instances $\rightarrow 0.5$<br>• $\le 1$ instance $\rightarrow 0.0$ | `[0.0, 1.0]` | Higher redundancy means less risk to overall service availability. |

---

## 2. Weight Optimization: Correlation Analysis & Entropy Weight Method (EWM)

### Step 1: Correlation Analysis
* Compute the Pearson correlation matrix across the 6 extracted factors.
* Verify feature relationships and ensure no two factors are exact collinear duplicates.

### Step 2: Entropy Weight Method (EWM)
The Entropy Weight Method calculates the weight of each factor based on the information dispersion (entropy) across all rows in the dataset:

1. **Normalize Matrix ($m$ rows, $n=6$ columns):**
   $$p_{ij} = \frac{x_{ij} + \epsilon}{\sum_{i=1}^{m} (x_{ij} + \epsilon)}$$
   *(where $\epsilon = 10^{-12}$)*

2. **Compute Shannon's Information Entropy ($e_j$):**
   $$e_j = -\frac{1}{\ln(m)} \sum_{i=1}^{m} p_{ij} \ln(p_{ij})$$

3. **Compute Information Utility ($d_j$):**
   $$d_j = 1.0 - e_j$$
   Factors with greater variability across workloads have higher information utility ($d_j$).

4. **Calculate Final Normalized Weights ($W_j$):**
   $$W_j = \frac{d_j}{\sum_{k=1}^{6} d_k}$$
   Guarantees: $W_j \ge 0$ and $\sum_{j=1}^{6} W_j = 1.0$.

---

## 3. Threshold Optimization: K-Means Clustering for Decision Ranges

Instead of hardcoding static threshold cutoffs (like 0.50 and 0.75), we derive the boundaries using **unsupervised K-Means clustering** on the scores:

1. Compute the composite reclamation score $S_i$ for all workloads in the dataset using the derived EWM weights:
   $$S_i = \sum_{j=1}^{6} W_j \cdot x_{ij}$$

2. Fit **K-Means Clustering** with $K = 3$ clusters on the distribution of $S$:
   * **Cluster 0:** Low score centroid ($C_0$) $\rightarrow$ **`KEEP`**
   * **Cluster 1:** Medium score centroid ($C_1$) $\rightarrow$ **`SOFT_RECLAIM`**
   * **Cluster 2:** High score centroid ($C_2$) $\rightarrow$ **`FULL_RECLAIM`**

3. **Derive the Decision Ranges:**
   * Boundary between `KEEP` and `SOFT_RECLAIM`:
     $$\text{SoftReclaimScoreThreshold} = \frac{C_0 + C_1}{2}$$
   * Boundary between `SOFT_RECLAIM` and `FULL_RECLAIM`:
     $$\text{FullReclaimScoreThreshold} = \frac{C_1 + C_2}{2}$$

---

---

## 4. Empirical Training Results (Trained on 405,120 Borg Workloads)

Running the automated training pipeline (`ml/run_training.py`) on Google Borg production trace data (`dataset/borg_traces_data.csv`, 405,120 rows) produced the following objective mathematical weights and decision thresholds:

### 4.1. Shannon Entropy Weights (EWM)

| Factor | Description | Entropy ($e_j$) | Divergence ($d_j$) | Derived Weight ($W_j$) | Percentage |
|---|---|---|---|---|---|
| `WeightBenefit` | Absolute Reclaimable Volume (CPU+RAM) | 0.985694 | 0.014306 | **0.409373** | **40.94%** |
| `WeightMemory` | Memory Waste Ratio (Req vs Avg) | 0.992109 | 0.007891 | **0.225841** | **22.58%** |
| `WeightCPU` | CPU Waste Ratio (Req vs Avg) | 0.993021 | 0.006979 | **0.199702** | **19.97%** |
| `WeightState` | Scheduling Class (Disruption Tolerance) | 0.996561 | 0.003439 | **0.098443** | **9.84%** |
| `WeightPriority` | Workload Priority Inversion | 0.997766 | 0.002234 | **0.063951** | **6.40%** |
| `WeightReplica` | Collection Redundancy Level | 0.999906 | 0.000094 | **0.002690** | **0.27%** |
| **Total** | — | — | **0.034943** | **1.000000** | **100.00%** |

#### Why this makes physical engineering sense:
1. **Benefit & Waste Dominate (~83.5% combined)**: The primary goal of resource reclamation is recovering high-volume wasted capacity. Workloads that hold gigabytes of idle RAM or cores have high variance and high information utility.
2. **State & Priority Provide Safety Context (~16.2% combined)**: Differentiates batch jobs from user-facing latency-critical services without subjective human guesswork.
3. **Zero Human Bias / No Circular Targets**: Unlike supervised regression models (e.g. Ridge or XGBoost) which require hand-crafting an artificial target $y = \text{gain} \times \text{safety}$ (which is circular tautology), EWM derives importance purely from natural statistical variance (the Information Theory Contrast Rule).

### 4.2. K-Means Thresholds ($K = 3$)

Applying 1D K-Means clustering to the 405,120 composite scores yielded three natural clusters:
- **Cluster 0 Centroid ($C_0$, Low Score)**: `0.1974` $\rightarrow$ Active, low-waste, or critical workloads.
- **Cluster 1 Centroid ($C_1$, Medium Score)**: `0.4748` $\rightarrow$ Moderate waste candidates.
- **Cluster 2 Centroid ($C_2$, High Score)**: `0.7105` $\rightarrow$ Severe waste, batch/low-priority candidates.

#### Derived Decision Thresholds:
$$\text{SoftReclaimScoreThreshold} = \frac{C_0 + C_1}{2} = \frac{0.1974 + 0.4748}{2} = \mathbf{0.3361}$$
$$\text{FullReclaimScoreThreshold} = \frac{C_1 + C_2}{2} = \frac{0.4748 + 0.7105}{2} = \mathbf{0.5927}$$

#### Workload Population Distribution Across Clusters:
- **`KEEP`** ($S < 0.3361$): **138,477 workloads (34.18%)**
- **`SOFT_RECLAIM`** ($0.3361 \le S < 0.5927$): **168,145 workloads (41.51%)**
- **`FULL_RECLAIM`** ($S \ge 0.5927$): **98,498 workloads (24.31%)**

---

## 5. Architectural Separation: Scoring vs. Safety Gates

It is critical to distinguish between **statistical scoring factors** and **binary safety constraints**:
- **Why PDB and CRIU are NOT weighted factors**:
  - `PodDisruptionBudget` (`DisruptionsAllowed == 0`) and CRIU Checkpointing compatibility are **hard binary constraints** dictated by Kubernetes runtime cluster state.
  - If a PDB has `DisruptionsAllowed == 0`, evicting the pod violates cluster SLOs regardless of how high its CPU/Memory waste is.
  - Therefore, PDB and CRIU are evaluated as **Stage 1 Safety Gates** in `safety.go`, before any composite score is calculated, while the Borg ML pipeline handles the 6 physical resource/workload dimensions.

---

## 6. Implementation Workflow & Deliverables

```text
+-------------------------------------------------------------+
|                 dataset/borg_traces_data.csv                |
|                 (405,120 production workloads)              |
+-------------------------------------------------------------+
                               |
                               v  (ml/extract.py)
+-------------------------------------------------------------+
|  1. Vectorized Feature Extraction                           |
|     - Score_CPU, Score_Mem, Benefit, Priority, State, Replica|
+-------------------------------------------------------------+
                               |
                               v  (ml/train.py)
+-------------------------------------------------------------+
|  2. Statistical & Information Theory Training:              |
|     - Pearson Correlation Heatmap & Verification            |
|     - Shannon Entropy Weight Calculation (W_Benefit, etc.)  |
|     - Composite Score Computation S                         |
|     - K-Means (K=3) Clustering & Decision Zone Derivation   |
+-------------------------------------------------------------+
                               |
                               v  (ml/export.py)
+-------------------------------------------------------------+
|  3. Export ml/artifacts/trained_weights.json                |
|     - weights: {WeightBenefit: 0.409373, ...}               |
|     - thresholds: {Soft: 0.3361, Full: 0.5927}              |
+-------------------------------------------------------------+
                               |
                               v
+-------------------------------------------------------------+
|  4. Go Reclamation Decision Engine Integration:             |
|     - pkg/decision/scoring.go (6-factor score computation)  |
|     - pkg/decision/policy.go (Defaults & thresholds)        |
|     - pkg/decision/weights.go (JSON artifact loader)        |
+-------------------------------------------------------------+
```

### Reproducibility Command:
```bash
python ml/run_training.py
```
This runs the entire end-to-end pipeline in under 10 seconds, regenerates visual artifacts (`correlation_heatmap.png`, `entropy_weights.png`, `kmeans_thresholds.png`), and writes `ml/artifacts/trained_weights.json`.
