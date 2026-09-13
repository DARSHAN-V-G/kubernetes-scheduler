package metrics

import (
	"fmt"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestMetricsCache_ConcurrencyStress(t *testing.T) {
	cache := NewMetricsCache(5)
	const numGoroutines = 50
	const numIterations = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Writers
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				podName := fmt.Sprintf("pod-%d-%d", workerID, j)
				nodeName := fmt.Sprintf("node-%d", workerID%3)

				pod := &PodMetrics{
					Namespace:               "default",
					Name:                    podName,
					UID:                     types.UID(podName),
					NodeName:                nodeName,
					Phase:                   corev1.PodRunning,
					TotalRequestedCPUMillis: 100,
					TotalRequestedMemory:    1024 * 1024,
					TotalUsageCPUMillicores: float64(50 + j),
					TotalUsageMemoryBytes:   int64(512 * 1024),
					LastUpdated:             time.Now(),
				}
				cache.SetPod(pod)

				cache.AddSample("default", podName, MetricSample{
					Timestamp:     time.Now(),
					CPUMillicores: float64(50 + j),
				})

				node := &NodeMetrics{
					Name:                     nodeName,
					AllocatableCPUMillis:     4000,
					AllocatableMemoryBytes:   8 * 1024 * 1024 * 1024,
					ActualUsageCPUMillicores: float64(200 + j),
					ActualUsageMemoryBytes:   2 * 1024 * 1024 * 1024,
					LastUpdated:              time.Now(),
				}
				cache.SetNode(node)
				cache.RecalculateNodeHeadroom(nodeName)
			}
		}(i)
	}

	// Readers
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				podName := fmt.Sprintf("pod-%d-%d", workerID, j)
				cache.GetPod("default", podName)

				nodeName := fmt.Sprintf("node-%d", workerID%3)
				cache.GetNode(nodeName)
				cache.GetPodsForNode(nodeName)

				_ = cache.GetSnapshot()
			}
		}(i)
	}

	wg.Wait()
}

func TestMetricsCache_NodeHeadroomCalculation(t *testing.T) {
	cache := NewMetricsCache(5)
	nodeName := "worker-1"

	node := &NodeMetrics{
		Name:                     nodeName,
		AllocatableCPUMillis:     2000,                  // 2 CPUs
		AllocatableMemoryBytes:   4 * 1024 * 1024 * 1024, // 4 GiB
		ActualUsageCPUMillicores: 350.0,                 // 350 millicores physically used
		ActualUsageMemoryBytes:   1024 * 1024 * 1024,    // 1 GiB physically used
	}
	cache.SetNode(node)

	// Add 2 pods scheduled on this node
	pod1 := &PodMetrics{
		Namespace:               "ecommerce",
		Name:                    "user-service-1",
		NodeName:                nodeName,
		TotalRequestedCPUMillis: 500,
		TotalRequestedMemory:    512 * 1024 * 1024,
	}
	pod2 := &PodMetrics{
		Namespace:               "ecommerce",
		Name:                    "order-service-1",
		NodeName:                nodeName,
		TotalRequestedCPUMillis: 300,
		TotalRequestedMemory:    256 * 1024 * 1024,
	}
	cache.SetPod(pod1)
	cache.SetPod(pod2)

	cache.RecalculateNodeHeadroom(nodeName)

	retrieved, ok := cache.GetNode(nodeName)
	if !ok {
		t.Fatalf("expected node %s to exist", nodeName)
	}

	if retrieved.PodCount != 2 {
		t.Fatalf("expected 2 pods on node, got %d", retrieved.PodCount)
	}
	if retrieved.AllocatedRequestedCPU != 800 {
		t.Fatalf("expected 800m allocated requested CPU, got %d", retrieved.AllocatedRequestedCPU)
	}
	if retrieved.AllocatedRequestedMem != 768*1024*1024 {
		t.Fatalf("expected 768MiB allocated requested memory, got %d", retrieved.AllocatedRequestedMem)
	}

	// Real Free = Allocatable - Actual Physical Usage
	expectedFreeCPU := 2000.0 - 350.0 // 1650m
	if retrieved.RealFreeCPUMillicores != expectedFreeCPU {
		t.Fatalf("expected real free CPU %f, got %f", expectedFreeCPU, retrieved.RealFreeCPUMillicores)
	}

	expectedFreeMem := (4 * 1024 * 1024 * 1024) - (1024 * 1024 * 1024) // 3 GiB
	if retrieved.RealFreeMemoryBytes != int64(expectedFreeMem) {
		t.Fatalf("expected real free memory %d, got %d", expectedFreeMem, retrieved.RealFreeMemoryBytes)
	}
}

func TestMetricsCache_SnapshotIsolation(t *testing.T) {
	cache := NewMetricsCache(5)
	pod := &PodMetrics{
		Namespace:               "default",
		Name:                    "app-1",
		TotalUsageCPUMillicores: 100,
	}
	cache.SetPod(pod)

	snapshot1 := cache.GetSnapshot()
	if snapshot1.Pods["default/app-1"].TotalUsageCPUMillicores != 100 {
		t.Fatalf("expected 100 in snapshot1")
	}

	// Mutate pod in cache
	pod.TotalUsageCPUMillicores = 250
	cache.SetPod(pod)

	// snapshot1 must remain unchanged (deep copy isolation)
	if snapshot1.Pods["default/app-1"].TotalUsageCPUMillicores != 100 {
		t.Fatalf("snapshot isolation violated: got %f, expected 100", snapshot1.Pods["default/app-1"].TotalUsageCPUMillicores)
	}

	snapshot2 := cache.GetSnapshot()
	if snapshot2.Pods["default/app-1"].TotalUsageCPUMillicores != 250 {
		t.Fatalf("expected 250 in snapshot2, got %f", snapshot2.Pods["default/app-1"].TotalUsageCPUMillicores)
	}
}
