package activator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/finalyearproject/adaptive-k8s-scheduler/api/v1alpha1"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/action"
	"github.com/finalyearproject/adaptive-k8s-scheduler/pkg/dependency"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// CheckpointRecordLister retrieves CheckpointRecords for restoration.
type CheckpointRecordLister interface {
	ListReady(ctx context.Context, namespace string) ([]*v1alpha1.CheckpointRecord, error)
	Get(ctx context.Context, namespace, name string) (*v1alpha1.CheckpointRecord, error)
}

// CooldownRecorder registers a warmup cooldown to prevent immediate re-checkpointing.
type CooldownRecorder interface {
	RecordCooldown(namespace, podOrServiceName string, duration time.Duration)
}

// ActivatorConfig holds configuration parameters for the Activator reverse proxy.
type ActivatorConfig struct {
	Port              int           `json:"port"`
	DefaultTimeout    time.Duration `json:"defaultTimeout"`
	DefaultNamespace  string        `json:"defaultNamespace"`
	MaxRequestBody    int64         `json:"maxRequestBody"`
	WarmupCooldown    time.Duration `json:"warmupCooldown"`
	ReadinessTimeout  time.Duration `json:"readinessTimeout"`
}

func DefaultConfig() ActivatorConfig {
	return ActivatorConfig{
		Port:             8083,
		DefaultTimeout:   30 * time.Second,
		DefaultNamespace: "ecommerce",
		MaxRequestBody:   10 * 1024 * 1024, // 10 MB
		WarmupCooldown:   10 * time.Minute,
		ReadinessTimeout: 45 * time.Second,
	}
}

// ActivatorServer coordinates scale-from-zero traffic buffering and dependency-aware auto-start.
type ActivatorServer struct {
	cfg          ActivatorConfig
	k8sClient    kubernetes.Interface
	readiness    ReadinessChecker
	restoreEng   *action.RestoreEngine
	records      CheckpointRecordLister
	cooldown     CooldownRecorder
	singleFlight *SingleFlightGroup
	logger       *zap.Logger
	httpClient   *http.Client
}

func NewActivatorServer(
	cfg ActivatorConfig,
	k8sClient kubernetes.Interface,
	readiness ReadinessChecker,
	restoreEng *action.RestoreEngine,
	records CheckpointRecordLister,
	cooldown CooldownRecorder,
	logger *zap.Logger,
) *ActivatorServer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &ActivatorServer{
		cfg:          cfg,
		k8sClient:    k8sClient,
		readiness:    readiness,
		restoreEng:   restoreEng,
		records:      records,
		cooldown:     cooldown,
		singleFlight: NewSingleFlightGroup(),
		logger:       logger,
		httpClient: &http.Client{
			Timeout: cfg.DefaultTimeout,
		},
	}
}

// TargetServiceDetails represents parsed destination service metadata.
type TargetServiceDetails struct {
	Namespace string
	Name      string
	Port      int
}

// ParseTargetService determines the destination service from HTTP request metadata.
func (s *ActivatorServer) ParseTargetService(r *http.Request) (*TargetServiceDetails, error) {
	// 1. Header "X-Target-Service": e.g. "ecommerce/backend-api:3000" or "backend-api:3000"
	targetHdr := r.Header.Get("X-Target-Service")
	if targetHdr == "" {
		targetHdr = r.URL.Query().Get("target_service")
	}

	// 2. If not in header/query, parse from Host header (e.g. "backend-api.ecommerce:3000" or "backend-api:3000")
	if targetHdr == "" && r.Host != "" && !strings.Contains(r.Host, "localhost") && !strings.Contains(r.Host, "127.0.0.1") {
		targetHdr = r.Host
	}

	if targetHdr == "" {
		return nil, fmt.Errorf("missing target service identification (provide X-Target-Service header, target_service query param, or Host header)")
	}

	ns := s.cfg.DefaultNamespace
	nameAndPort := targetHdr
	if strings.Contains(targetHdr, "/") {
		parts := strings.SplitN(targetHdr, "/", 2)
		ns = parts[0]
		nameAndPort = parts[1]
	}

	name := nameAndPort
	port := 80
	if strings.Contains(nameAndPort, ":") {
		parts := strings.SplitN(nameAndPort, ":", 2)
		name = parts[0]
		p, err := strconv.Atoi(parts[1])
		if err == nil && p > 0 {
			port = p
		}
	}
	if strings.Contains(name, ".") {
		// e.g. backend-api.ecommerce.svc.cluster.local
		parts := strings.Split(name, ".")
		name = parts[0]
		if len(parts) > 1 && parts[1] != "svc" && parts[1] != "cluster" {
			ns = parts[1]
		}
	}

	return &TargetServiceDetails{
		Namespace: ns,
		Name:      name,
		Port:      port,
	}, nil
}

// ServeHTTP handles scale-to-zero interception, buffering, and auto-start.
func (s *ActivatorServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Health check endpoint for activator itself
	if r.URL.Path == "/healthz" || r.URL.Path == "/activator/health" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	target, err := s.ParseTargetService(r)
	if err != nil {
		s.logger.Warn("Failed to parse target service", zap.Error(err), zap.String("url", r.URL.String()))
		http.Error(w, "Bad Request: "+err.Error(), http.StatusBadRequest)
		return
	}

	targetBaseURL := s.readiness.GetServiceTargetURL(target.Namespace, target.Name, target.Port)

	// Check if the service already has ready endpoints in Kubernetes
	isReady, err := s.readiness.IsServiceReady(r.Context(), target.Namespace, target.Name)
	if err == nil && isReady {
		// Service is already live: fast path direct reverse proxy
		s.proxyDirect(w, r, targetBaseURL)
		return
	}

	// Service is scaled to zero / hibernated: buffer request and trigger DAG restore
	s.logger.Info("Target service is dormant (0 ready endpoints); buffering request and initiating auto-start",
		zap.String("namespace", target.Namespace),
		zap.String("service", target.Name),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
	)

	bufferedReq, err := BufferHTTPRequest(r, s.cfg.MaxRequestBody)
	if err != nil {
		s.logger.Error("Failed to buffer incoming request", zap.Error(err))
		http.Error(w, "Failed buffering request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	flightKey := fmt.Sprintf("%s/%s", target.Namespace, target.Name)
	restoreErr := s.singleFlight.DoWithTimeout(r.Context(), flightKey, s.cfg.ReadinessTimeout, func() error {
		return s.restoreWorkloadWithDependencies(r.Context(), target.Namespace, target.Name)
	})

	if restoreErr != nil {
		s.logger.Error("Failed auto-starting dormant workload and dependencies",
			zap.String("service", flightKey),
			zap.Error(restoreErr),
		)
		http.Error(w, fmt.Sprintf("504 Gateway Timeout: auto-start failed for %s: %v", flightKey, restoreErr), http.StatusGatewayTimeout)
		return
	}

	// Workload and dependencies are now Ready on the Kubernetes Service EndpointSlice!
	var resp *http.Response
	var reqErr error
	for attempt := 0; attempt < 5; attempt++ {
		targetReq, err := bufferedReq.ToHTTPRequest(r.Context(), targetBaseURL)
		if err != nil {
			s.logger.Error("Failed constructing target request from buffer", zap.Error(err))
			http.Error(w, "Internal Proxy Error", http.StatusInternalServerError)
			return
		}
		resp, reqErr = s.httpClient.Do(targetReq)
		if reqErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if reqErr != nil {
		s.logger.Error("Failed forwarding buffered request to newly restored service",
			zap.String("targetURL", targetBaseURL),
			zap.Error(reqErr),
		)
		http.Error(w, "502 Bad Gateway: restored service unreachable: "+reqErr.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers and body to client
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *ActivatorServer) proxyDirect(w http.ResponseWriter, r *http.Request, targetBaseURL string) {
	destURL, err := url.Parse(targetBaseURL + r.URL.RequestURI())
	if err != nil {
		http.Error(w, "Invalid target URL", http.StatusInternalServerError)
		return
	}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, destURL.String(), r.Body)
	if err != nil {
		http.Error(w, "Failed creating proxy request", http.StatusInternalServerError)
		return
	}
	outReq.Header = r.Header.Clone()
	outReq.Host = r.Host

	resp, err := s.httpClient.Do(outReq)
	if err != nil {
		http.Error(w, "Bad Gateway: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// restoreWorkloadWithDependencies resolves the multi-pod DAG and restores all prerequisites in topological order.
func (s *ActivatorServer) restoreWorkloadWithDependencies(ctx context.Context, namespace, targetService string) error {
	s.logger.Info("Resolving dependency DAG for auto-start",
		zap.String("namespace", namespace),
		zap.String("target", targetService),
	)

	// 1. Build workload list from cluster state and CheckpointRecords
	workloads := s.discoverWorkloadGraph(ctx, namespace)
	targetKey := dependency.WorkloadKey{Namespace: namespace, Name: targetService}

	found := false
	for _, w := range workloads {
		if w.Key == targetKey || w.ServiceName == targetService {
			found = true
			break
		}
	}
	if !found {
		workloads = append(workloads, dependency.WorkloadInfo{
			Key:         targetKey,
			ServiceName: targetService,
			IsReady:     false,
			IsHibernate: true,
		})
	}

	g := dependency.BuildGraph(workloads)
	plan, err := g.ResolveRestorationStages(targetKey)
	if err != nil {
		return fmt.Errorf("resolve restoration stages for %s: %w", targetKey, err)
	}

	if len(plan.Stages) == 0 {
		s.logger.Info("No unready stages needed restoration", zap.String("target", targetService))
		return nil
	}

	s.logger.Info("Restoration plan generated",
		zap.String("target", targetService),
		zap.Int("totalStages", len(plan.Stages)),
	)

	// 2. Execute each stage sequentially; workloads inside a stage are restored concurrently
	for stageIdx, stage := range plan.Stages {
		s.logger.Info("Executing restoration stage",
			zap.Int("stage", stageIdx),
			zap.Int("workloadCount", len(stage)),
		)

		var wg sync.WaitGroup
		errChan := make(chan error, len(stage))

		for _, itemKey := range stage {
			wg.Add(1)
			go func(key dependency.WorkloadKey) {
				defer wg.Done()
				if err := s.restoreSingleWorkload(ctx, key.Namespace, key.Name); err != nil {
					errChan <- fmt.Errorf("restore %s: %w", key, err)
					return
				}
				// Wait for this service's EndpointSlice to report Ready
				if waitErr := s.readiness.WaitUntilServiceReady(ctx, key.Namespace, key.Name, s.cfg.ReadinessTimeout); waitErr != nil {
					s.logger.Warn("Readiness check timed out or failed for stage workload (continuing)",
						zap.String("workload", key.String()),
						zap.Error(waitErr),
					)
				}
			}(itemKey)
		}

		wg.Wait()
		close(errChan)

		if len(errChan) > 0 {
			var errMsgs []string
			for e := range errChan {
				errMsgs = append(errMsgs, e.Error())
			}
			return fmt.Errorf("stage %d restoration failed: %s", stageIdx, strings.Join(errMsgs, "; "))
		}
	}

	// 3. Register warmup cooldown to prevent immediate re-checkpointing
	if s.cooldown != nil {
		s.cooldown.RecordCooldown(namespace, targetService, s.cfg.WarmupCooldown)
	}

	return nil
}

// restoreSingleWorkload reconstitutes one workload from its CheckpointRecord.
func (s *ActivatorServer) restoreSingleWorkload(ctx context.Context, namespace, workloadName string) error {
	if s.records == nil || s.restoreEng == nil {
		s.logger.Info("Restore engine or records lister uninitialized; skipping direct pod creation (test mode)",
			zap.String("workload", workloadName),
		)
		return nil
	}

	readyRecords, err := s.records.ListReady(ctx, namespace)
	if err != nil {
		return fmt.Errorf("list ready checkpoint records in %s: %w", namespace, err)
	}

	var match *v1alpha1.CheckpointRecord
	for _, rec := range readyRecords {
		if rec.Spec.SourcePodName == workloadName || rec.Name == workloadName || strings.HasPrefix(rec.Spec.SourcePodName, workloadName) {
			match = rec
			break
		}
	}

	if match == nil {
		// CheckpointRecord might be named after pod, attempt direct get
		rec, getErr := s.records.Get(ctx, namespace, workloadName)
		if getErr == nil && rec != nil {
			match = rec
		}
	}

	if match == nil {
		return fmt.Errorf("no Ready CheckpointRecord found for workload %s/%s", namespace, workloadName)
	}

	s.logger.Info("Reconstituting pod from CheckpointRecord",
		zap.String("record", match.Name),
		zap.String("sourcePod", match.Spec.SourcePodName),
		zap.String("archive", match.Spec.CheckpointPath),
	)

	_, err = s.restoreEng.RestorePod(ctx, match)
	if err != nil {
		return fmt.Errorf("RestorePod failed: %w", err)
	}

	return nil
}

// discoverWorkloadGraph builds WorkloadInfo items from cluster Pods and CheckpointRecords.
func (s *ActivatorServer) discoverWorkloadGraph(ctx context.Context, namespace string) []dependency.WorkloadInfo {
	var results []dependency.WorkloadInfo

	// 1. Inspect live pods in cluster
	if s.k8sClient != nil {
		pods, err := s.k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, pod := range pods.Items {
				w := dependency.WorkloadInfoFromPod(&pod, false)
				results = append(results, w)
			}
		}
	}

	// 2. Inspect CheckpointRecords for hibernated workloads
	if s.records != nil {
		records, err := s.records.ListReady(ctx, namespace)
		if err == nil {
			for _, rec := range records {
				// Reconstruct dependencies from podSpecSnapshot if available
				var annotations map[string]string
				var labels map[string]string

				if rec.Spec.PodSpecSnapshot != "" {
					var snapshot corev1.Pod
					if err := json.Unmarshal([]byte(rec.Spec.PodSpecSnapshot), &snapshot); err == nil {
						annotations = snapshot.Annotations
						labels = snapshot.Labels
					}
				}

				serviceName := rec.Spec.SourcePodName
				// Trim replica hashes if deployment pod
				parts := strings.Split(serviceName, "-")
				if len(parts) > 2 {
					serviceName = strings.Join(parts[:len(parts)-2], "-")
				}

				results = append(results, dependency.WorkloadInfo{
					Key:          dependency.WorkloadKey{Namespace: rec.Namespace, Name: rec.Spec.SourcePodName},
					ServiceName:  serviceName,
					Dependencies: dependency.ParseDependencies(rec.Namespace, annotations),
					IsReady:      false,
					IsHibernate:  true,
					Labels:       labels,
					Annotations:  annotations,
				})
			}
		}
	}

	return results
}
