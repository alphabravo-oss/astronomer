package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// K8sProxy forwards Kubernetes API requests from the tunnel to the local cluster.
type K8sProxy struct {
	client     *kubernetes.Clientset
	restConfig *rest.Config
	httpClient *http.Client
	log        *slog.Logger
}

// NewK8sProxy creates a K8sProxy using the in-cluster configuration.
func NewK8sProxy(log *slog.Logger) (*K8sProxy, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("get in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}

	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	return &K8sProxy{
		client:     clientset,
		restConfig: cfg,
		httpClient: &http.Client{Transport: transport},
		log:        log,
	}, nil
}

// NewK8sProxyWithConfig creates a K8sProxy with an explicit rest.Config.
// Useful for testing or running outside the cluster.
func NewK8sProxyWithConfig(cfg *rest.Config, log *slog.Logger) (*K8sProxy, error) {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes clientset: %w", err)
	}

	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	return &K8sProxy{
		client:     clientset,
		restConfig: cfg,
		httpClient: &http.Client{Transport: transport},
		log:        log,
	}, nil
}

// HandleRequest processes a K8S_REQUEST message and returns a K8S_RESPONSE.
func (p *K8sProxy) HandleRequest(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	var req protocol.K8sRequestPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return nil, fmt.Errorf("unmarshal k8s request: %w", err)
	}

	p.log.Info("proxying k8s request", "method", req.Method, "path", req.Path)

	// Build the target URL using the API server host from the rest config.
	targetURL := p.restConfig.Host + req.Path

	// Decode body if present.
	var bodyReader io.Reader
	if req.Body != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return nil, fmt.Errorf("decode request body: %w", err)
		}
		bodyReader = bytes.NewReader(decoded)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, targetURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute k8s request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read k8s response body: %w", err)
	}

	respHeaders := make(map[string]string)
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	responsePayload := protocol.K8sResponsePayload{
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       base64.StdEncoding.EncodeToString(respBody),
	}

	payloadBytes, err := json.Marshal(responsePayload)
	if err != nil {
		return nil, fmt.Errorf("marshal k8s response: %w", err)
	}

	return &protocol.Message{
		Type:      protocol.MsgK8sResponse,
		StreamID:  msg.StreamID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}, nil
}
