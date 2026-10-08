package cluster

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
	"simulator/backend/models"
)

// MetricsBridge wraps the existing PrometheusClient to enrich live Kubernetes workloads
// with real physical telemetry.
type MetricsBridge struct {
	client        *metrics.PrometheusClient
	prometheusURL string
	logger        *zap.Logger
}

// NewMetricsBridge initializes the bridge pointing at the Prometheus endpoint.
func NewMetricsBridge(prometheusURL string, logger *zap.Logger) (*MetricsBridge, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if prometheusURL == "" {
		prometheusURL = "http://127.0.0.1:9090"
	}

	client, err := metrics.NewPrometheusClient(prometheusURL, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus client: %w", err)
	}

	return &MetricsBridge{
		client:        client,
		prometheusURL: prometheusURL,
		logger:        logger,
	}, nil
}

// Status returns Prometheus connectivity information.
func (mb *MetricsBridge) Status(ctx context.Context) map[string]interface{} {
	telemetry, err := mb.client.FetchClusterTelemetry(ctx)
	connected := err == nil
	lastScrape := ""
	if connected && !telemetry.Timestamp.IsZero() {
		lastScrape = telemetry.Timestamp.UTC().Format(time.RFC3339)
	}

	return map[string]interface{}{
		"endpoint":       mb.prometheusURL,
		"connected":      connected,
		"lastScraped":    lastScrape,
		"trackedMetrics": len(telemetry.ContainerCPU) + len(telemetry.ContainerMemWorking),
	}
}

// EnrichWorkloads fetches real Prometheus telemetry and merges it into discovered workloads.
func (mb *MetricsBridge) EnrichWorkloads(ctx context.Context, workloads []models.SyntheticWorkload) ([]models.SyntheticWorkload, error) {
	telemetry, err := mb.client.FetchClusterTelemetry(ctx)
	if err != nil {
		mb.logger.Warn("Prometheus scrape failed during enrichment, using zero/fallback usage", zap.Error(err))
		return workloads, nil
	}

	enriched := make([]models.SyntheticWorkload, len(workloads))
	copy(enriched, workloads)

	for i := range enriched {
		w := &enriched[i]
		podKey := metrics.PodKey(w.Namespace, w.Name)

		// 1. CPU Usage: sum containers
		var totalCPU float64
		for k, cpu := range telemetry.ContainerCPU {
			if matchContainerPrefix(k, w.Namespace, w.Name) {
				totalCPU += cpu
			}
		}
		w.UsageCPUMillicores = totalCPU

		// 2. Memory Working Set: sum containers
		var totalMem int64
		for k, mem := range telemetry.ContainerMemWorking {
			if matchContainerPrefix(k, w.Namespace, w.Name) {
				totalMem += mem
			}
		}
		w.UsageMemoryBytes = totalMem

		// 3. Network I/O
		if rx, ok := telemetry.PodNetworkRx[podKey]; ok {
			tx := telemetry.PodNetworkTx[podKey]
			w.NetworkBytesPerSec = rx + tx
		}

		// 4. Request QPS
		if qps, ok := telemetry.PodQPS[podKey]; ok {
			w.RequestQPS = qps
		}

		// 5. Idle evaluation heuristics from live telemetry
		// Uses calibrated noise-filtering thresholds: QPS <= 2.0 (probe noise), Net < 15KB/s, CPU < 50m
		isCurrentlyIdle := (w.UsageCPUMillicores < 50.0) && (w.NetworkBytesPerSec < 15360) && (w.RequestQPS <= 2.0)
		w.IsIdle = isCurrentlyIdle
		if isCurrentlyIdle {
			if w.IdleDurationSeconds == 0 {
				w.IdleDurationSeconds = 300
			}
		} else {
			w.IdleDurationSeconds = 0
		}
	}

	return enriched, nil
}

func matchContainerPrefix(key, namespace, pod string) bool {
	prefix := fmt.Sprintf("%s/%s/", namespace, pod)
	return len(key) > len(prefix) && key[:len(prefix)] == prefix
}
