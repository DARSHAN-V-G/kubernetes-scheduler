package metrics

import (
	"sort"
	"sync"
	"time"
)

// MetricWindow implements a thread-safe sliding window ring buffer for time-series telemetry samples.
type MetricWindow struct {
	mu       sync.RWMutex
	capacity int
	samples  []MetricSample
	head     int
	count    int
}

// NewMetricWindow initializes a new ring buffer with the specified sample capacity.
func NewMetricWindow(capacity int) *MetricWindow {
	if capacity <= 0 {
		capacity = 5
	}
	return &MetricWindow{
		capacity: capacity,
		samples:  make([]MetricSample, capacity),
		head:     0,
		count:    0,
	}
}

// AddSample inserts a new sample into the ring buffer.
func (w *MetricWindow) AddSample(sample MetricSample) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.samples[w.head] = sample
	w.head = (w.head + 1) % w.capacity
	if w.count < w.capacity {
		w.count++
	}
}

// Count returns the current number of samples stored in the window.
func (w *MetricWindow) Count() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.count
}

// GetSamples returns a copy of all current samples in chronological order.
func (w *MetricWindow) GetSamples() []MetricSample {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.count == 0 {
		return nil
	}

	result := make([]MetricSample, w.count)
	if w.count < w.capacity {
		copy(result, w.samples[:w.count])
		return result
	}

	// Ring buffer is full; read from oldest to newest
	idx := 0
	for i := w.head; i < w.capacity; i++ {
		result[idx] = w.samples[i]
		idx++
	}
	for i := 0; i < w.head; i++ {
		result[idx] = w.samples[i]
		idx++
	}

	return result
}

// LatestSample returns the most recently inserted sample.
func (w *MetricWindow) LatestSample() (MetricSample, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.count == 0 {
		return MetricSample{}, false
	}

	latestIdx := (w.head - 1 + w.capacity) % w.capacity
	return w.samples[latestIdx], true
}

// Stats computes statistical summaries (Average, Peak, EMA) across the current window.
type WindowStats struct {
	Count               int
	AverageCPU          float64
	PeakCPU             float64
	ExponentialAvgCPU   float64
	AverageMemory       int64
	PeakMemory          int64
	AverageNetworkBytes float64
	AverageQPS          float64
}

// ComputeStats calculates aggregated metrics across the window.
func (w *MetricWindow) ComputeStats(alpha float64) WindowStats {
	samples := w.GetSamples()
	if len(samples) == 0 {
		return WindowStats{}
	}

	var sumCPU float64
	var peakCPU float64
	var sumMem int64
	var peakMem int64
	var sumNet float64
	var sumQPS float64

	cpuValues := make([]float64, len(samples))
	for i, s := range samples {
		sumCPU += s.CPUMillicores
		if s.CPUMillicores > peakCPU {
			peakCPU = s.CPUMillicores
		}
		cpuValues[i] = s.CPUMillicores

		sumMem += s.MemoryWorkingSet
		if s.MemoryWorkingSet > peakMem {
			peakMem = s.MemoryWorkingSet
		}

		sumNet += s.NetworkBytesPerSec
		sumQPS += s.RequestQPS
	}

	n := float64(len(samples))
	avgCPU := sumCPU / n
	avgMem := int64(float64(sumMem) / n)
	avgNet := sumNet / n
	avgQPS := sumQPS / n

	// Calculate Exponential Moving Average (EMA)
	if alpha <= 0 || alpha > 1 {
		alpha = 0.3 // Default smoothing factor
	}
	emaCPU := samples[0].CPUMillicores
	for i := 1; i < len(samples); i++ {
		emaCPU = alpha*samples[i].CPUMillicores + (1-alpha)*emaCPU
	}

	// If at least 4 samples exist, compute P95 by sorting
	if len(cpuValues) >= 4 {
		sort.Float64s(cpuValues)
		p95Idx := int(float64(len(cpuValues)-1) * 0.95)
		peakCPU = cpuValues[p95Idx]
	}

	return WindowStats{
		Count:               len(samples),
		AverageCPU:          avgCPU,
		PeakCPU:             peakCPU,
		ExponentialAvgCPU:   emaCPU,
		AverageMemory:       avgMem,
		PeakMemory:          peakMem,
		AverageNetworkBytes: avgNet,
		AverageQPS:          avgQPS,
	}
}

// IsConsistentlyBelowThresholds checks whether all samples in the window satisfy idle conditions:
// CPU <= cpuLimit, Network <= netLimit, and QPS <= qpsLimit.
func (w *MetricWindow) IsConsistentlyBelowThresholds(cpuLimit, netLimit, qpsLimit float64) bool {
	samples := w.GetSamples()
	if len(samples) == 0 {
		return false
	}

	for _, s := range samples {
		if s.CPUMillicores > cpuLimit {
			return false
		}
		if s.NetworkBytesPerSec > netLimit {
			return false
		}
		if s.RequestQPS > qpsLimit {
			return false
		}
	}

	return true
}

// Duration returns the total time span covered by the samples in the window.
func (w *MetricWindow) Duration() time.Duration {
	samples := w.GetSamples()
	if len(samples) < 2 {
		return 0
	}
	return samples[len(samples)-1].Timestamp.Sub(samples[0].Timestamp)
}
