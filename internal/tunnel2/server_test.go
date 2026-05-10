package tunnel2_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/rancher/remotedialer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/remoteproxy"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel2"
)

// stubValidator is a minimum-viable AgentTokenValidator for tests. It accepts
// exactly one (clusterID, token) pair.
type stubValidator struct {
	clusterID uuid.UUID
	token     string
}

func (s *stubValidator) GetRegistrationTokenByToken(_ context.Context, token string) (sqlc.ClusterRegistrationToken, error) {
	if token != s.token {
		return sqlc.ClusterRegistrationToken{}, errNotFound{}
	}
	return sqlc.ClusterRegistrationToken{ID: uuid.New(), ClusterID: s.clusterID, Token: token}, nil
}

func (s *stubValidator) MarkRegistrationTokenUsed(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (s *stubValidator) GetClusterAgentTokenByClusterID(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterAgentToken, error) {
	return sqlc.ClusterAgentToken{}, errNotFound{}
}

func (s *stubValidator) GetClusterAgentTokenByToken(_ context.Context, token string) (sqlc.ClusterAgentToken, error) {
	return sqlc.ClusterAgentToken{}, errNotFound{}
}

func (s *stubValidator) UpsertClusterAgentToken(_ context.Context, arg sqlc.UpsertClusterAgentTokenParams) (sqlc.ClusterAgentToken, error) {
	return sqlc.ClusterAgentToken{
		ID:        uuid.New(),
		ClusterID: arg.ClusterID,
		Token:     arg.Token,
	}, nil
}

func (s *stubValidator) TouchClusterAgentToken(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (s *stubValidator) UpdateClusterHeartbeat(_ context.Context, _ sqlc.UpdateClusterHeartbeatParams) error {
	return nil
}

func (s *stubValidator) UpsertClusterHealthStatus(_ context.Context, _ sqlc.UpsertClusterHealthStatusParams) (sqlc.ClusterHealthStatus, error) {
	return sqlc.ClusterHealthStatus{}, nil
}

func (s *stubValidator) CreateAgentConnection(_ context.Context, arg sqlc.CreateAgentConnectionParams) (sqlc.AgentConnection, error) {
	return sqlc.AgentConnection{
		ID:        uuid.New(),
		ClusterID: arg.ClusterID,
		AgentID:   arg.AgentID,
		SessionID: arg.SessionID,
		Status:    arg.Status,
	}, nil
}

func (s *stubValidator) DisconnectActiveConnectionsByCluster(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (s *stubValidator) UpdateAgentConnectionStatus(_ context.Context, _ sqlc.UpdateAgentConnectionStatusParams) error {
	return nil
}

func (s *stubValidator) UpdateAgentConnectionPing(_ context.Context, _ uuid.UUID) error {
	return nil
}

type errNotFound struct{}

func (errNotFound) Error() string { return "token not found" }

// TestEndToEnd_RemotedialerListPods wires the new tunnel server, connects an
// in-process agent that runs remotedialer.ConnectToProxy, then uses
// remoteproxy.K8sClient to list pods through the tunnel against a fake API
// server stood up on the agent side.
//
// This is the load-bearing migration proof: it exercises the full path
// server -> remotedialer -> agent -> agent's local network -> "API server".
func TestEndToEnd_RemotedialerListPods(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	clusterID := uuid.New()
	token := "test-token-xyz"
	validator := &stubValidator{clusterID: clusterID, token: token}

	// 1. Stand up the management server with the new tunnel.
	rs := tunnel2.NewRemoteServer(logger, validator)

	// chi-style URL with {cluster_id}; for the test we just route /connect/...
	mux := http.NewServeMux()
	mux.Handle("/api/v1/connect/", http.StripPrefix("/api/v1/connect/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Parse the path segment as the cluster_id and stash it as a query
		// param so the authorize() fallback picks it up. (chi.URLParam is
		// unavailable when we use net/http directly.)
		seg := strings.TrimSuffix(r.URL.Path, "/")
		q := r.URL.Query()
		q.Set("cluster_id", seg)
		r.URL.RawQuery = q.Encode()
		rs.ServeHTTP(w, r)
	})))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 2. Stand up a fake API server that the agent will dial *locally*.
	apiSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"kind":       "PodList",
			"apiVersion": "v1",
			"metadata":   map[string]any{},
			"items": []map[string]any{{
				"metadata": map[string]any{"name": "tunneled-pod", "namespace": "default"},
				"status":   map[string]any{"phase": "Running"},
			}},
		})
	}))
	defer apiSrv.Close()

	// 3. Run the agent in-process. We bypass agent2.ConnectAndServe because
	//    that loops forever — instead we call remotedialer.ConnectToProxy
	//    once with a custom local dialer that redirects every dial to apiSrv.
	apiTarget := strings.TrimPrefix(apiSrv.URL, "https://")

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1) + "/api/v1/connect/" + clusterID.String() + "/"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("X-Cluster-ID", clusterID.String())

	allow := func(_, _ string) bool { return true }

	// localDialer redirects every "kubernetes.default.svc:443" dial to apiSrv.
	localDialer := func(ctx context.Context, network, _ string) (net.Conn, error) { //nolint:revive
		var d net.Dialer
		return d.DialContext(ctx, network, apiTarget)
	}

	agentCtx, agentCancel := context.WithCancel(context.Background())
	defer agentCancel()
	agentDone := make(chan struct{})
	go func() {
		defer close(agentDone)
		_ = remotedialer.ConnectToProxyWithDialer(agentCtx, wsURL, headers, allow, defaultWS(), localDialer, nil)
	}()

	// 4. Wait for the session to register on the server side.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rs.HasSession(clusterID.String()) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !rs.HasSession(clusterID.String()) {
		t.Fatalf("session never came online")
	}

	// 5. Now talk to the cluster as if we were any handler.
	client, err := remoteproxy.K8sClient(rs, clusterID.String())
	if err != nil {
		t.Fatalf("K8sClient: %v", err)
	}
	pods, err := client.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list pods: %v", err)
	}
	if len(pods.Items) != 1 || pods.Items[0].Name != "tunneled-pod" {
		t.Fatalf("unexpected pod list: %+v", pods.Items)
	}

	agentCancel()
	select {
	case <-agentDone:
	case <-time.After(2 * time.Second):
		t.Logf("agent did not exit promptly")
	}
}

func defaultWS() *websocket.Dialer {
	return &websocket.Dialer{HandshakeTimeout: 5 * time.Second}
}

// keep gofmt quiet on the websocket import (used through defaultWS).
var _ = (*websocket.Dialer)(nil)
