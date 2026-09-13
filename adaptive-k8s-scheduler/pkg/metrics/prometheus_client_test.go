package metrics

import (
	"context"
	"testing"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap"
)

type mockQuerier struct {
	responses map[string]model.Value
}

func (m *mockQuerier) Query(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
	if val, ok := m.responses[query]; ok {
		return val, nil, nil
	}
	return model.Vector{}, nil, nil
}

func TestPrometheusClient_FetchClusterTelemetry(t *testing.T) {
	mock := &mockQuerier{
		responses: make(map[string]model.Value),
	}

	// Mock container CPU query response
	mock.responses[Queries.ContainerCPU] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"namespace": "ecommerce",
				"pod":       "user-service-abc",
				"container": "user-service",
			},
			Value:     125.5, // 125.5 millicores
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}

	// Mock container working set memory
	mock.responses[Queries.ContainerMemWorking] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"namespace": "ecommerce",
				"pod":       "user-service-abc",
				"container": "user-service",
			},
			Value:     52428800, // 50 MiB
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}

	// Mock container RSS memory
	mock.responses[Queries.ContainerMemRSS] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"namespace": "ecommerce",
				"pod":       "user-service-abc",
				"container": "user-service",
			},
			Value:     41943040, // 40 MiB
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}

	// Mock pod network Rx and Tx
	mock.responses[Queries.PodNetworkRx] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"namespace": "ecommerce",
				"pod":       "user-service-abc",
			},
			Value:     1500.0, // 1.5 KB/s
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}
	mock.responses[Queries.PodNetworkTx] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"namespace": "ecommerce",
				"pod":       "user-service-abc",
			},
			Value:     2500.0, // 2.5 KB/s
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}

	// Mock pod HTTP QPS
	mock.responses[Queries.PodAppQPS] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"namespace": "ecommerce",
				"pod":       "user-service-abc",
			},
			Value:     42.0, // 42 req/sec
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}

	// Mock node CPU and Memory
	mock.responses[Queries.NodeCPU] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"node": "worker-1",
			},
			Value:     650.0, // 650 millicores
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}
	mock.responses[Queries.NodeMem] = model.Vector{
		&model.Sample{
			Metric: model.Metric{
				"node": "worker-1",
			},
			Value:     2147483648, // 2 GiB
			Timestamp: model.TimeFromUnix(time.Now().Unix()),
		},
	}

	client := NewPrometheusClientWithQuerier(mock, zap.NewNop())
	telemetry, err := client.FetchClusterTelemetry(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cKey := ContainerKey("ecommerce", "user-service-abc", "user-service")
	if telemetry.ContainerCPU[cKey] != 125.5 {
		t.Fatalf("expected container CPU 125.5, got %f", telemetry.ContainerCPU[cKey])
	}
	if telemetry.ContainerMemWorking[cKey] != 52428800 {
		t.Fatalf("expected container working memory 52428800, got %d", telemetry.ContainerMemWorking[cKey])
	}
	if telemetry.ContainerMemRSS[cKey] != 41943040 {
		t.Fatalf("expected container RSS memory 41943040, got %d", telemetry.ContainerMemRSS[cKey])
	}

	pKey := PodKey("ecommerce", "user-service-abc")
	if telemetry.PodNetworkRx[pKey] != 1500.0 {
		t.Fatalf("expected network Rx 1500.0, got %f", telemetry.PodNetworkRx[pKey])
	}
	if telemetry.PodNetworkTx[pKey] != 2500.0 {
		t.Fatalf("expected network Tx 2500.0, got %f", telemetry.PodNetworkTx[pKey])
	}
	if telemetry.PodQPS[pKey] != 42.0 {
		t.Fatalf("expected QPS 42.0, got %f", telemetry.PodQPS[pKey])
	}

	if telemetry.NodeCPU["worker-1"] != 650.0 {
		t.Fatalf("expected node CPU 650.0, got %f", telemetry.NodeCPU["worker-1"])
	}
	if telemetry.NodeMem["worker-1"] != 2147483648 {
		t.Fatalf("expected node memory 2147483648, got %d", telemetry.NodeMem["worker-1"])
	}
}
