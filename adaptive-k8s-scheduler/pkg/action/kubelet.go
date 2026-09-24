package action

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubeletClient defines the interface for issuing checkpoint commands.
type KubeletClient interface {
	Checkpoint(ctx context.Context, nodeName, namespace, pod, container string) (*CheckpointResponse, error)
}

// HTTPKubeletClient communicates with Kubelet using Kubernetes RESTClient proxy or direct HTTPS.
type HTTPKubeletClient struct {
	client     kubernetes.Interface
	restClient rest.Interface
	httpClient *http.Client
	bearerToken string
	useProxy   bool
	logger     *zap.Logger
}

// NewHTTPKubeletClient initializes a Kubelet checkpoint client.
// If restConfig is provided and useProxy=true, it routes requests through the API server node proxy.
func NewHTTPKubeletClient(client kubernetes.Interface, restConfig *rest.Config, useProxy bool, logger *zap.Logger) (*HTTPKubeletClient, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	var restCli rest.Interface
	var token string
	if restConfig != nil {
		token = restConfig.BearerToken
		rc, err := rest.RESTClientFor(restConfig)
		if err == nil {
			restCli = rc
		}
	}

	// Fallback to service account token file if running in-cluster
	if token == "" {
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token"); err == nil {
			token = string(data)
		}
	}

	// Fallback direct HTTP client with relaxed TLS for local node-certs
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient := &http.Client{
		Transport: tr,
		Timeout:   60 * time.Second,
	}

	return &HTTPKubeletClient{
		client:      client,
		restClient:  restCli,
		httpClient:  httpClient,
		bearerToken: token,
		useProxy:    useProxy,
		logger:      logger,
	}, nil
}

// Checkpoint issues a POST request to trigger CRIU container checkpointing.
func (k *HTTPKubeletClient) Checkpoint(ctx context.Context, nodeName, namespace, pod, container string) (*CheckpointResponse, error) {
	if nodeName == "" || namespace == "" || pod == "" || container == "" {
		return nil, fmt.Errorf("nodeName, namespace, pod, and container must all be specified")
	}

	k.logger.Info("Triggering container checkpoint via Kubelet API",
		zap.String("node", nodeName),
		zap.String("pod", fmt.Sprintf("%s/%s", namespace, pod)),
		zap.String("container", container),
	)

	// Mode 1: API Server Node Proxy (preferred in cluster)
	if k.useProxy && k.client != nil {
		proxyAbsPath := fmt.Sprintf("/api/v1/nodes/%s/proxy/checkpoint/%s/%s/%s", nodeName, namespace, pod, container)
		req := k.client.CoreV1().RESTClient().Post().AbsPath(proxyAbsPath)

		raw, err := req.DoRaw(ctx)
		if err != nil {
			return nil, fmt.Errorf("apiserver node proxy checkpoint failed for %s/%s: %w", namespace, pod, err)
		}

		var resp CheckpointResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			// Some Kubelet versions return plaintext path or json object
			return &CheckpointResponse{Items: []string{string(raw)}}, nil
		}
		return &resp, nil
	}

	// Mode 2: Direct node call (https://<nodeName>:10250/checkpoint/...)
	directURL := fmt.Sprintf("https://%s:10250/checkpoint/%s/%s/%s", nodeName, namespace, pod, container)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, directURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct direct checkpoint request: %w", err)
	}

	if k.bearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+k.bearerToken)
	}

	httpResp, err := k.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("direct kubelet request to %s failed: %w", directURL, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kubelet returned status %d for checkpoint request", httpResp.StatusCode)
	}

	var resp CheckpointResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed decoding checkpoint response: %w", err)
	}

	return &resp, nil
}
