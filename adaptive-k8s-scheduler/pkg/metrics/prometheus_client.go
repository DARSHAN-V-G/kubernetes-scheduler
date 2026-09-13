package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap"
)

// PrometheusQuerier is an interface abstraction over Prometheus v1 API for query execution and testing.
type PrometheusQuerier interface {
	Query(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error)
}

// PrometheusTelemetry contains the decoded vector metrics from a single scraping round.
type PrometheusTelemetry struct {
	Timestamp            time.Time
	ContainerCPU         map[string]float64 // Key: "namespace/pod/container" -> Millicores
	ContainerMemWorking  map[string]int64   // Key: "namespace/pod/container" -> Working Set Bytes
	ContainerMemRSS      map[string]int64   // Key: "namespace/pod/container" -> RSS Bytes
	PodNetworkRx         map[string]float64 // Key: "namespace/pod" -> Bytes/sec
	PodNetworkTx         map[string]float64 // Key: "namespace/pod" -> Bytes/sec
	PodQPS               map[string]float64 // Key: "namespace/pod" -> Requests or Packets/sec
	NodeCPU              map[string]float64 // Key: nodeName/instance -> Millicores
	NodeMem              map[string]int64   // Key: nodeName/instance -> Used Bytes
}

// PrometheusClient wraps Prometheus API calls with connection management and error handling.
type PrometheusClient struct {
	api    PrometheusQuerier
	logger *zap.Logger
}

// NewPrometheusClient initializes a new client targeted at the given Prometheus URL (e.g. http://127.0.0.1:9090).
func NewPrometheusClient(prometheusURL string, logger *zap.Logger) (*PrometheusClient, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	client, err := api.NewClient(api.Config{
		Address: prometheusURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Prometheus HTTP client: %w", err)
	}

	v1api := promv1.NewAPI(client)
	return &PrometheusClient{
		api:    v1api,
		logger: logger,
	}, nil
}

// NewPrometheusClientWithQuerier allows injecting a custom or mock PromQL querier.
func NewPrometheusClientWithQuerier(querier PrometheusQuerier, logger *zap.Logger) *PrometheusClient {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PrometheusClient{
		api:    querier,
		logger: logger,
	}
}

// ContainerKey formats a lookup key for container-level maps.
func ContainerKey(namespace, pod, container string) string {
	return fmt.Sprintf("%s/%s/%s", namespace, pod, container)
}

// PodKey formats a lookup key for pod-level maps.
func PodKey(namespace, pod string) string {
	return fmt.Sprintf("%s/%s", namespace, pod)
}

// Queries defines the PromQL queries used for telemetry ingestion.
var Queries = struct {
	ContainerCPU        string
	ContainerMemWorking string
	ContainerMemRSS     string
	PodNetworkRx        string
	PodNetworkTx        string
	PodAppQPS           string
	PodPacketRate       string
	NodeCPU             string
	NodeMem             string
}{
	ContainerCPU:        `sum(rate(container_cpu_usage_seconds_total{container!="",container!="POD"}[2m])) by (namespace, pod, container) * 1000`,
	ContainerMemWorking: `sum(container_memory_working_set_bytes{container!="",container!="POD"}) by (namespace, pod, container)`,
	ContainerMemRSS:     `sum(container_memory_rss{container!="",container!="POD"}) by (namespace, pod, container)`,
	PodNetworkRx:        `sum(rate(container_network_receive_bytes_total[2m])) by (namespace, pod)`,
	PodNetworkTx:        `sum(rate(container_network_transmit_bytes_total[2m])) by (namespace, pod)`,
	PodAppQPS:           `sum(rate(http_requests_total[2m])) by (namespace, pod)`,
	PodPacketRate:       `sum(rate(container_network_receive_packets_total[2m]) + rate(container_network_transmit_packets_total[2m])) by (namespace, pod)`,
	NodeCPU:             `sum(rate(node_cpu_seconds_total{mode!="idle"}[2m])) by (instance, node) * 1000`,
	NodeMem:             `node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes`,
}

// FetchClusterTelemetry queries Prometheus concurrently for all resource telemetry.
func (p *PrometheusClient) FetchClusterTelemetry(ctx context.Context) (*PrometheusTelemetry, error) {
	telemetry := &PrometheusTelemetry{
		Timestamp:           time.Now(),
		ContainerCPU:        make(map[string]float64),
		ContainerMemWorking: make(map[string]int64),
		ContainerMemRSS:     make(map[string]int64),
		PodNetworkRx:        make(map[string]float64),
		PodNetworkTx:        make(map[string]float64),
		PodQPS:              make(map[string]float64),
		NodeCPU:             make(map[string]float64),
		NodeMem:             make(map[string]int64),
	}

	var mu sync.Mutex
	var wg sync.WaitGroup

	execute := func(name, query string, handler func(model.Vector)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			val, warnings, err := p.api.Query(ctx, query, time.Now())
			if err != nil {
				p.logger.Warn("Prometheus query failed", zap.String("metric", name), zap.Error(err))
				return
			}
			if len(warnings) > 0 {
				p.logger.Debug("Prometheus query returned warnings", zap.String("metric", name), zap.Strings("warnings", warnings))
			}

			vec, ok := val.(model.Vector)
			if !ok {
				p.logger.Debug("Prometheus query returned non-vector value", zap.String("metric", name))
				return
			}

			mu.Lock()
			defer mu.Unlock()
			handler(vec)
		}()
	}

	// 1. Container CPU Rate (millicores)
	execute("container_cpu", Queries.ContainerCPU, func(vec model.Vector) {
		for _, s := range vec {
			ns := string(s.Metric["namespace"])
			pod := string(s.Metric["pod"])
			container := string(s.Metric["container"])
			if ns != "" && pod != "" && container != "" {
				telemetry.ContainerCPU[ContainerKey(ns, pod, container)] = float64(s.Value)
			}
		}
	})

	// 2. Container Working Set Memory (bytes)
	execute("container_mem_working", Queries.ContainerMemWorking, func(vec model.Vector) {
		for _, s := range vec {
			ns := string(s.Metric["namespace"])
			pod := string(s.Metric["pod"])
			container := string(s.Metric["container"])
			if ns != "" && pod != "" && container != "" {
				telemetry.ContainerMemWorking[ContainerKey(ns, pod, container)] = int64(s.Value)
			}
		}
	})

	// 3. Container RSS Memory (bytes)
	execute("container_mem_rss", Queries.ContainerMemRSS, func(vec model.Vector) {
		for _, s := range vec {
			ns := string(s.Metric["namespace"])
			pod := string(s.Metric["pod"])
			container := string(s.Metric["container"])
			if ns != "" && pod != "" && container != "" {
				telemetry.ContainerMemRSS[ContainerKey(ns, pod, container)] = int64(s.Value)
			}
		}
	})

	// 4. Pod Network Rx (bytes/sec)
	execute("pod_network_rx", Queries.PodNetworkRx, func(vec model.Vector) {
		for _, s := range vec {
			ns := string(s.Metric["namespace"])
			pod := string(s.Metric["pod"])
			if ns != "" && pod != "" {
				telemetry.PodNetworkRx[PodKey(ns, pod)] = float64(s.Value)
			}
		}
	})

	// 5. Pod Network Tx (bytes/sec)
	execute("pod_network_tx", Queries.PodNetworkTx, func(vec model.Vector) {
		for _, s := range vec {
			ns := string(s.Metric["namespace"])
			pod := string(s.Metric["pod"])
			if ns != "" && pod != "" {
				telemetry.PodNetworkTx[PodKey(ns, pod)] = float64(s.Value)
			}
		}
	})

	// 6. Application HTTP QPS (primary) or Packet Rate (fallback)
	execute("pod_app_qps", Queries.PodAppQPS, func(vec model.Vector) {
		for _, s := range vec {
			ns := string(s.Metric["namespace"])
			pod := string(s.Metric["pod"])
			if ns != "" && pod != "" {
				telemetry.PodQPS[PodKey(ns, pod)] = float64(s.Value)
			}
		}
	})

	// 7. Node Physical CPU
	execute("node_cpu", Queries.NodeCPU, func(vec model.Vector) {
		for _, s := range vec {
			node := string(s.Metric["node"])
			if node == "" {
				node = string(s.Metric["instance"])
			}
			if node != "" {
				telemetry.NodeCPU[node] = float64(s.Value)
			}
		}
	})

	// 8. Node Physical Memory
	execute("node_mem", Queries.NodeMem, func(vec model.Vector) {
		for _, s := range vec {
			node := string(s.Metric["node"])
			if node == "" {
				node = string(s.Metric["instance"])
			}
			if node != "" {
				telemetry.NodeMem[node] = int64(s.Value)
			}
		}
	})

	wg.Wait()

	// If HTTP QPS was not found for some pods, populate packet rate as fallback
	if len(telemetry.PodQPS) == 0 {
		val, _, err := p.api.Query(ctx, Queries.PodPacketRate, time.Now())
		if err == nil {
			if vec, ok := val.(model.Vector); ok {
				for _, s := range vec {
					ns := string(s.Metric["namespace"])
					pod := string(s.Metric["pod"])
					if ns != "" && pod != "" {
						telemetry.PodQPS[PodKey(ns, pod)] = float64(s.Value)
					}
				}
			}
		}
	}

	return telemetry, nil
}
