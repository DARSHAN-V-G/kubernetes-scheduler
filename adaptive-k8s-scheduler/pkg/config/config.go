package config

import (
	"flag"
	"os"
	"strconv"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/metrics"
)

// AppConfig encapsulates all operational configurations for the custom scheduler and collector.
type AppConfig struct {
	KubeConfig               string
	SchedulerName            string
	LeaderElect              bool
	LeaderElectResourceName  string
	LeaderElectNamespace     string
	Collector                *metrics.CollectorConfig
}

// LoadConfig parses CLI flags with environment variable fallbacks.
func LoadConfig() *AppConfig {
	cfg := &AppConfig{
		Collector: metrics.DefaultCollectorConfig(),
	}

	flag.StringVar(&cfg.KubeConfig, "kubeconfig", getEnv("KUBECONFIG", ""), "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&cfg.SchedulerName, "scheduler-name", getEnv("SCHEDULER_NAME", "adaptive-scheduler"), "Name of the scheduler used in pod.spec.schedulerName.")
	flag.BoolVar(&cfg.LeaderElect, "leader-elect", getEnvBool("LEADER_ELECT", true), "Enable leader election for HA.")
	flag.StringVar(&cfg.LeaderElectResourceName, "leader-elect-resource-name", getEnv("LEADER_ELECT_RESOURCE_NAME", "adaptive-scheduler"), "Name of the lease resource.")
	flag.StringVar(&cfg.LeaderElectNamespace, "leader-elect-resource-namespace", getEnv("LEADER_ELECT_NAMESPACE", "kube-system"), "Namespace for the leader election lease.")

	// Collector options
	flag.StringVar(&cfg.Collector.PrometheusURL, "prometheus-url", getEnv("PROMETHEUS_URL", "http://127.0.0.1:9090"), "URL of the co-located Prometheus server.")
	flag.DurationVar(&cfg.Collector.ScrapeInterval, "scrape-interval", getEnvDuration("SCRAPE_INTERVAL", 10*time.Second), "Telemetry collection interval.")
	flag.DurationVar(&cfg.Collector.HTTPTimeout, "http-timeout", getEnvDuration("HTTP_TIMEOUT", 5*time.Second), "Prometheus query HTTP timeout.")
	flag.IntVar(&cfg.Collector.WindowSize, "window-size", getEnvInt("WINDOW_SIZE", 5), "Sliding window sample count for metrics smoothing.")
	flag.Float64Var(&cfg.Collector.IdleCPUThreshold, "idle-cpu-threshold", getEnvFloat("IDLE_CPU_THRESHOLD", 20.0), "CPU threshold in millicores below which a pod is idle.")
	flag.Float64Var(&cfg.Collector.IdleNetThreshold, "idle-net-threshold", getEnvFloat("IDLE_NET_THRESHOLD", 10240.0), "Network bandwidth in bytes/sec below which a pod is idle.")
	flag.Float64Var(&cfg.Collector.IdleQPSThreshold, "idle-qps-threshold", getEnvFloat("IDLE_QPS_THRESHOLD", 0.1), "QPS or packet rate below which a pod is idle.")
	flag.DurationVar(&cfg.Collector.IdleMinDuration, "idle-min-duration", getEnvDuration("IDLE_MIN_DURATION", 60*time.Second), "Continuous inactivity duration to qualify as idle.")
	flag.IntVar(&cfg.Collector.HTTPPort, "http-port", getEnvInt("HTTP_PORT", 8081), "Port for health checks, telemetry, and snapshot API.")

	flag.Parse()

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		b, err := strconv.ParseBool(val)
		if err == nil {
			return b
		}
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		i, err := strconv.Atoi(val)
		if err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		f, err := strconv.ParseFloat(val, 64)
		if err == nil {
			return f
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		d, err := time.ParseDuration(val)
		if err == nil {
			return d
		}
	}
	return defaultVal
}
