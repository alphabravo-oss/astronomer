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

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// MaxServiceProxyResponseSize caps proxied response bodies (10 MiB) to protect
// agent memory. Mirrors the Python agent's behaviour.
const MaxServiceProxyResponseSize = 10 * 1024 * 1024

// defaultServiceProxyTimeout is used when the request payload doesn't supply
// one. Matches the Python implementation.
const defaultServiceProxyTimeout = 30 * time.Second

// ServiceProxy forwards HTTP requests received over the tunnel to in-cluster
// ClusterIP Services using their internal DNS name.
type ServiceProxy struct {
	client *http.Client
	log    *slog.Logger
}

// NewServiceProxy constructs a ServiceProxy. The HTTP client used for outbound
// calls reuses connections across requests for efficiency.
func NewServiceProxy(log *slog.Logger) *ServiceProxy {
	if log == nil {
		log = slog.Default()
	}
	return &ServiceProxy{
		client: &http.Client{Timeout: defaultServiceProxyTimeout},
		log:    log,
	}
}

// HandleRequest decodes a SERVICE_PROXY_REQUEST and returns a
// SERVICE_PROXY_RESPONSE. The response body is always base64-encoded, matching
// the Go protocol convention (K8sResponsePayload, etc.).
func (sp *ServiceProxy) HandleRequest(ctx context.Context, msg *protocol.Message) (*protocol.Message, error) {
	var req protocol.ServiceProxyRequestPayload
	if err := json.Unmarshal(msg.Payload, &req); err != nil {
		return sp.errorResponse(msg, http.StatusBadRequest, fmt.Errorf("decode service proxy request: %w", err)), nil
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	port := req.Port
	if port == 0 {
		port = 80
	}
	path := req.Path
	if path == "" {
		path = "/"
	}

	url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s",
		req.ServiceName, req.Namespace, port, path)

	sp.log.Info("service proxy request",
		"service", req.ServiceName,
		"namespace", req.Namespace,
		"port", port,
		"method", method,
		"path", path,
	)

	var bodyReader io.Reader
	if req.Body != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.Body)
		if err != nil {
			return sp.errorResponse(msg, http.StatusBadRequest, fmt.Errorf("decode body: %w", err)), nil
		}
		bodyReader = bytes.NewReader(decoded)
	}

	timeout := defaultServiceProxyTimeout
	if req.TimeoutSecs > 0 {
		timeout = time.Duration(req.TimeoutSecs) * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, method, url, bodyReader)
	if err != nil {
		return sp.errorResponse(msg, http.StatusInternalServerError, err), nil
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := sp.client.Do(httpReq)
	if err != nil {
		// Distinguish timeout vs general failure for status code parity with Python.
		if reqCtx.Err() == context.DeadlineExceeded {
			return sp.errorResponse(msg, http.StatusGatewayTimeout, fmt.Errorf("service did not respond within %s", timeout)), nil
		}
		return sp.errorResponse(msg, http.StatusBadGateway, err), nil
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, MaxServiceProxyResponseSize+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return sp.errorResponse(msg, http.StatusBadGateway, fmt.Errorf("read response body: %w", err)), nil
	}
	if int64(len(body)) > MaxServiceProxyResponseSize {
		return sp.errorResponse(msg, http.StatusRequestEntityTooLarge,
			fmt.Errorf("response too large (>%d bytes)", MaxServiceProxyResponseSize)), nil
	}

	respHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		respHeaders[k] = resp.Header.Get(k)
	}

	out := protocol.ServiceProxyResponsePayload{
		StatusCode: resp.StatusCode,
		Headers:    respHeaders,
		Body:       base64.StdEncoding.EncodeToString(body),
	}
	return sp.wrapResponse(msg, &out), nil
}

func (sp *ServiceProxy) errorResponse(msg *protocol.Message, status int, err error) *protocol.Message {
	body := []byte(err.Error())
	return sp.wrapResponse(msg, &protocol.ServiceProxyResponsePayload{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Body:       base64.StdEncoding.EncodeToString(body),
		Error:      err.Error(),
	})
}

func (sp *ServiceProxy) wrapResponse(msg *protocol.Message, out *protocol.ServiceProxyResponsePayload) *protocol.Message {
	payload, _ := json.Marshal(out)
	return &protocol.Message{
		Type:      protocol.MsgServiceProxyResponse,
		StreamID:  msg.StreamID,
		RequestID: msg.RequestID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}
