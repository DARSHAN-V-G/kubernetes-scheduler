package metrics

import (
	"fmt"
	"sync"
	"time"
)

// MetricsCache is a thread-safe, high-performance in-memory cache that aggregates
// real-time telemetry from Prometheus and declarative state from Kubernetes Informers.
type MetricsCache struct {
	mu           sync.RWMutex
	windowSize   int
	podsByName   map[string]*PodMetrics            // Key: "namespace/name"
	podsByNode   map[string]map[string]*PodMetrics // Key: nodeName -> "namespace/name" -> PodMetrics
	nodesByName  map[string]*NodeMetrics           // Key: nodeName
	podWindows   map[string]*MetricWindow          // Key: "namespace/name" -> sliding window
}

// NewMetricsCache creates a newly initialized MetricsCache with the given window size.
func NewMetricsCache(windowSize int) *MetricsCache {
	if windowSize <= 0 {
		windowSize = 5
	}
	return &MetricsCache{
		windowSize:  windowSize,
		podsByName:  make(map[string]*PodMetrics),
		podsByNode:  make(map[string]map[string]*PodMetrics),
		nodesByName: make(map[string]*NodeMetrics),
		podWindows:  make(map[string]*MetricWindow),
	}
}

func podKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

// SetPod stores or updates a Pod's metrics and indexes it by node.
func (c *MetricsCache) SetPod(pod *PodMetrics) {
	if pod == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := podKey(pod.Namespace, pod.Name)
	existing, found := c.podsByName[key]

	// If node binding changed, clean up previous node index
	if found && existing.NodeName != "" && existing.NodeName != pod.NodeName {
		if nodeMap, exists := c.podsByNode[existing.NodeName]; exists {
			delete(nodeMap, key)
			if len(nodeMap) == 0 {
				delete(c.podsByNode, existing.NodeName)
			}
		}
	}

	// Clone to maintain cache isolation
	podClone := pod.Clone()
	c.podsByName[key] = podClone

	if pod.NodeName != "" {
		if _, exists := c.podsByNode[pod.NodeName]; !exists {
			c.podsByNode[pod.NodeName] = make(map[string]*PodMetrics)
		}
		c.podsByNode[pod.NodeName][key] = podClone
	}

	// Initialize window if absent
	if _, exists := c.podWindows[key]; !exists {
		c.podWindows[key] = NewMetricWindow(c.windowSize)
	}
}

// GetPod retrieves a cloned copy of the specified pod's metrics.
func (c *MetricsCache) GetPod(namespace, name string) (*PodMetrics, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := podKey(namespace, name)
	pod, found := c.podsByName[key]
	if !found {
		return nil, false
	}
	return pod.Clone(), true
}

// DeletePod removes a pod from the cache and its associated node index and history.
func (c *MetricsCache) DeletePod(namespace, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := podKey(namespace, name)
	if pod, found := c.podsByName[key]; found {
		if pod.NodeName != "" {
			if nodeMap, exists := c.podsByNode[pod.NodeName]; exists {
				delete(nodeMap, key)
				if len(nodeMap) == 0 {
					delete(c.podsByNode, pod.NodeName)
				}
			}
		}
		delete(c.podsByName, key)
		delete(c.podWindows, key)
	}
}

// GetPodsForNode returns all pods scheduled on the specified node.
func (c *MetricsCache) GetPodsForNode(nodeName string) []*PodMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	nodeMap, exists := c.podsByNode[nodeName]
	if !exists {
		return nil
	}

	result := make([]*PodMetrics, 0, len(nodeMap))
	for _, p := range nodeMap {
		result = append(result, p.Clone())
	}
	return result
}

// GetAllPods returns a slice of all pods currently in the cache.
func (c *MetricsCache) GetAllPods() []*PodMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*PodMetrics, 0, len(c.podsByName))
	for _, p := range c.podsByName {
		result = append(result, p.Clone())
	}
	return result
}

// SetNode stores or updates a Node's capacity and telemetry.
func (c *MetricsCache) SetNode(node *NodeMetrics) {
	if node == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.nodesByName[node.Name] = node.Clone()
}

// GetNode retrieves a cloned copy of the specified node's metrics.
func (c *MetricsCache) GetNode(name string) (*NodeMetrics, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	node, found := c.nodesByName[name]
	if !found {
		return nil, false
	}
	return node.Clone(), true
}

// DeleteNode removes a node from the cache.
func (c *MetricsCache) DeleteNode(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.nodesByName, name)
	delete(c.podsByNode, name)
}

// GetAllNodes returns a slice of all nodes currently in the cache.
func (c *MetricsCache) GetAllNodes() []*NodeMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*NodeMetrics, 0, len(c.nodesByName))
	for _, n := range c.nodesByName {
		result = append(result, n.Clone())
	}
	return result
}

// AddSample records a new telemetry sample for a pod and updates its rolling window.
func (c *MetricsCache) AddSample(namespace, name string, sample MetricSample) {
	c.mu.Lock()
	key := podKey(namespace, name)
	win, exists := c.podWindows[key]
	if !exists {
		win = NewMetricWindow(c.windowSize)
		c.podWindows[key] = win
	}
	c.mu.Unlock()

	win.AddSample(sample)
}

// GetWindow retrieves the rolling window for a pod.
func (c *MetricsCache) GetWindow(namespace, name string) (*MetricWindow, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := podKey(namespace, name)
	win, found := c.podWindows[key]
	return win, found
}

// RecalculateNodeHeadroom updates the node's declarative allocations and real free headroom.
func (c *MetricsCache) RecalculateNodeHeadroom(nodeName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	node, found := c.nodesByName[nodeName]
	if !found {
		return
	}

	var totalReqCPU int64
	var totalReqMem int64
	var podCount int

	if nodeMap, exists := c.podsByNode[nodeName]; exists {
		podCount = len(nodeMap)
		for _, pod := range nodeMap {
			totalReqCPU += pod.TotalRequestedCPUMillis
			totalReqMem += pod.TotalRequestedMemory
		}
	}

	node.AllocatedRequestedCPU = totalReqCPU
	node.AllocatedRequestedMem = totalReqMem
	node.PodCount = podCount

	// Real Headroom = Allocatable - ActualUsage
	node.RealFreeCPUMillicores = float64(node.AllocatableCPUMillis) - node.ActualUsageCPUMillicores
	if node.RealFreeCPUMillicores < 0 {
		node.RealFreeCPUMillicores = 0
	}

	node.RealFreeMemoryBytes = node.AllocatableMemoryBytes - node.ActualUsageMemoryBytes
	if node.RealFreeMemoryBytes < 0 {
		node.RealFreeMemoryBytes = 0
	}

	node.LastUpdated = time.Now()
}

// GetSnapshot generates an immutable snapshot of all nodes and pods for scheduler plugin evaluations.
func (c *MetricsCache) GetSnapshot() *ClusterSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snapshot := &ClusterSnapshot{
		Nodes:     make(map[string]*NodeMetrics, len(c.nodesByName)),
		Pods:      make(map[string]*PodMetrics, len(c.podsByName)),
		Timestamp: time.Now(),
	}

	for k, v := range c.nodesByName {
		snapshot.Nodes[k] = v.Clone()
	}
	for k, v := range c.podsByName {
		snapshot.Pods[k] = v.Clone()
	}

	return snapshot
}

// PruneStale removes pods that have not been refreshed within the maxAge duration.
func (c *MetricsCache) PruneStale(maxAge time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	staleKeys := make([]string, 0)

	for key, pod := range c.podsByName {
		if pod.LastUpdated.Before(cutoff) {
			staleKeys = append(staleKeys, key)
		}
	}

	for _, key := range staleKeys {
		pod := c.podsByName[key]
		if pod != nil && pod.NodeName != "" {
			if nodeMap, exists := c.podsByNode[pod.NodeName]; exists {
				delete(nodeMap, key)
				if len(nodeMap) == 0 {
					delete(c.podsByNode, pod.NodeName)
				}
			}
		}
		delete(c.podsByName, key)
		delete(c.podWindows, key)
	}

	return len(staleKeys)
}
