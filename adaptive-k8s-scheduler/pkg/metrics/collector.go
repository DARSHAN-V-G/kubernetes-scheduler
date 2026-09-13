package metrics

import (
	"context"
	"time"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
)

// MetricsCollector orchestrates telemetry scraping from Prometheus, informer updates from K8s API,
// idle duration tracking, and cache synchronization.
type MetricsCollector struct {
	config       *CollectorConfig
	cache        *MetricsCache
	promClient   *PrometheusClient
	informers    *K8sInformerManager
	logger       *zap.Logger
}

// NewMetricsCollector constructs a new collector instance.
func NewMetricsCollector(
	cfg *CollectorConfig,
	cache *MetricsCache,
	promClient *PrometheusClient,
	k8sClient kubernetes.Interface,
	logger *zap.Logger,
) *MetricsCollector {
	if cfg == nil {
		cfg = DefaultCollectorConfig()
	}
	if cache == nil {
		cache = NewMetricsCache(cfg.WindowSize)
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	informers := NewK8sInformerManager(k8sClient, cache, 30*time.Second, logger)

	return &MetricsCollector{
		config:     cfg,
		cache:      cache,
		promClient: promClient,
		informers:  informers,
		logger:     logger,
	}
}

// Start launches the informer managers and the background polling loop.
func (c *MetricsCollector) Start(ctx context.Context) error {
	c.logger.Info("Starting centralized metrics collector",
		zap.String("prometheusUrl", c.config.PrometheusURL),
		zap.Duration("scrapeInterval", c.config.ScrapeInterval),
		zap.Float64("idleCpuThresholdMillis", c.config.IdleCPUThreshold),
	)

	// Start informers and wait for initial sync
	if err := c.informers.Start(ctx); err != nil {
		return err
	}

	// Initial telemetry scrape
	c.collectTelemetry(ctx)

	ticker := time.NewTicker(c.config.ScrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping metrics collector loop...")
			return nil
		case <-ticker.C:
			c.collectTelemetry(ctx)
		}
	}
}

// Cache returns the thread-safe in-memory cache.
func (c *MetricsCollector) Cache() *MetricsCache {
	return c.cache
}

// GetSnapshot returns an immutable snapshot of all cluster metrics.
func (c *MetricsCollector) GetSnapshot() *ClusterSnapshot {
	return c.cache.GetSnapshot()
}

// collectTelemetry performs one synchronization cycle: Prometheus query -> Correlation -> Idle tracking -> Cache update.
func (c *MetricsCollector) collectTelemetry(ctx context.Context) {
	start := time.Now()

	queryCtx, cancel := context.WithTimeout(ctx, c.config.HTTPTimeout)
	defer cancel()

	telemetry, err := c.promClient.FetchClusterTelemetry(queryCtx)
	if err != nil {
		c.logger.Warn("Failed to fetch Prometheus telemetry; falling back to last known or requested resources", zap.Error(err))
	}

	now := time.Now()

	// 1. Correlate Pod & Container Telemetry
	allPods := c.cache.GetAllPods()
	for _, pod := range allPods {
		var podTotalCPU float64
		var podTotalWorkingMem int64
		var podTotalRSSMem int64
		var hasAnyTelemetry bool

		for cName, cMetric := range pod.Containers {
			cKey := ContainerKey(pod.Namespace, pod.Name, cName)

			var cpuUsed float64
			var memWorking int64
			var memRSS int64
			var found bool

			if telemetry != nil {
				if cpu, ok := telemetry.ContainerCPU[cKey]; ok {
					cpuUsed = cpu
					found = true
				}
				if mem, ok := telemetry.ContainerMemWorking[cKey]; ok {
					memWorking = mem
					found = true
				}
				if rss, ok := telemetry.ContainerMemRSS[cKey]; ok {
					memRSS = rss
					found = true
				}
			}

			if found {
				cMetric.UsageCPUMillicores = cpuUsed
				cMetric.UsageMemoryBytes = memWorking
				cMetric.UsageRSSBytes = memRSS
				cMetric.TelemetryReady = true
				hasAnyTelemetry = true
			} else if !cMetric.TelemetryReady {
				// Fallback baseline for containers that haven't been scraped yet
				cMetric.UsageCPUMillicores = float64(cMetric.RequestedCPUMillis)
				cMetric.UsageMemoryBytes = cMetric.RequestedMemoryBytes
				cMetric.UsageRSSBytes = 0
			}

			cMetric.LastUpdated = now
			podTotalCPU += cMetric.UsageCPUMillicores
			podTotalWorkingMem += cMetric.UsageMemoryBytes
			podTotalRSSMem += cMetric.UsageRSSBytes
		}

		pod.TotalUsageCPUMillicores = podTotalCPU
		pod.TotalUsageMemoryBytes = podTotalWorkingMem
		pod.TotalUsageRSSBytes = podTotalRSSMem
		pod.TelemetryReady = hasAnyTelemetry

		// Network & QPS correlation
		pKey := PodKey(pod.Namespace, pod.Name)
		if telemetry != nil {
			if rx, ok := telemetry.PodNetworkRx[pKey]; ok {
				pod.NetworkRxBytesPerSec = rx
			}
			if tx, ok := telemetry.PodNetworkTx[pKey]; ok {
				pod.NetworkTxBytesPerSec = tx
			}
			pod.TotalNetworkBytesSec = pod.NetworkRxBytesPerSec + pod.NetworkTxBytesPerSec

			if qps, ok := telemetry.PodQPS[pKey]; ok {
				pod.RequestQPS = qps
			}
		}

		// 2. Idle Duration Tracking
		// Evaluates multi-signal criteria: CPU <= threshold, Net <= threshold, QPS <= threshold
		isInactive := pod.TotalUsageCPUMillicores <= c.config.IdleCPUThreshold &&
			pod.TotalNetworkBytesSec <= c.config.IdleNetThreshold &&
			pod.RequestQPS <= c.config.IdleQPSThreshold

		if isInactive {
			if pod.LastActiveTime.IsZero() {
				pod.LastActiveTime = now
			}
			pod.IdleDuration = now.Sub(pod.LastActiveTime)
			if pod.IdleDuration >= c.config.IdleMinDuration {
				pod.IsIdle = true
			}
		} else {
			pod.LastActiveTime = now
			pod.IdleDuration = 0
			pod.IsIdle = false
		}

		pod.LastUpdated = now

		// Record sample into sliding window ring buffer
		sample := MetricSample{
			Timestamp:          now,
			CPUMillicores:      pod.TotalUsageCPUMillicores,
			MemoryWorkingSet:   pod.TotalUsageMemoryBytes,
			MemoryRSS:          pod.TotalUsageRSSBytes,
			NetworkBytesPerSec: pod.TotalNetworkBytesSec,
			RequestQPS:         pod.RequestQPS,
		}
		c.cache.AddSample(pod.Namespace, pod.Name, sample)
		c.cache.SetPod(pod)
	}

	// 3. Correlate Node Telemetry
	allNodes := c.cache.GetAllNodes()
	for _, node := range allNodes {
		var nodeUsedCPU float64
		var nodeUsedMem int64
		var found bool

		if telemetry != nil {
			if cpu, ok := telemetry.NodeCPU[node.Name]; ok {
				nodeUsedCPU = cpu
				found = true
			}
			if mem, ok := telemetry.NodeMem[node.Name]; ok {
				nodeUsedMem = mem
				found = true
			}
		}

		if found {
			node.ActualUsageCPUMillicores = nodeUsedCPU
			node.ActualUsageMemoryBytes = nodeUsedMem
		}

		c.cache.SetNode(node)
		c.cache.RecalculateNodeHeadroom(node.Name)
	}

	// 4. Prune stale pods (e.g. terminated pods not refreshed in 5 minutes)
	pruned := c.cache.PruneStale(5 * time.Minute)
	if pruned > 0 {
		c.logger.Debug("Pruned stale pods from metrics cache", zap.Int("prunedCount", pruned))
	}

	c.logger.Debug("Telemetry synchronization cycle completed",
		zap.Duration("duration", time.Since(start)),
		zap.Int("podsTracked", len(allPods)),
		zap.Int("nodesTracked", len(allNodes)),
	)
}
