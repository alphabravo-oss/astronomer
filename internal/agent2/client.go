// Package agent2 is the cluster-side client for the remotedialer-based tunnel.
// It is the agent half of the migration from internal/tunnel + internal/agent.
//
// Conceptually all the agent does now is:
//
//	1. Open a long-lived WS connection to the management server.
//	2. Tell remotedialer "any inbound dial request is OK" (or filter by host).
//	3. remotedialer takes care of everything else — when the server-side code
//	   calls dialer.DialContext, this end opens a normal net.Dial to the local
//	   address and ferries bytes through the WS multiplex.
//
// There is no per-feature handler registration any more. K8s API calls
// initiated by the server arrive as dial requests to kubernetes.default.svc:443
// and a stock client-go transport on the server side speaks straight to the
// in-cluster API server.
package agent2

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rancher/remotedialer"
)

// defaultBackoff is the base reconnect delay; remotedialer.ClientConnect adds
// its own 5-second pause on error, so we keep our outer loop simple and just
// honour ctx.Done.
const defaultBackoff = 5 * time.Second

// ConnectAndServe runs the agent until ctx is cancelled. On every disconnect
// it sleeps `defaultBackoff` and reconnects. The serverURL should be the HTTPS
// origin of the management server, e.g. "https://astronomer.example.com" — we
// rewrite the scheme to ws/wss internally.
//
// The clusterID is sent as both a path segment (so a chi router can pick it up
// with {cluster_id}) AND the X-Cluster-ID header (belt-and-braces — survives
// proxies that strip query params or rewrite paths).
//
// The token is the registration token from cluster_registration_tokens.
func ConnectAndServe(ctx context.Context, logger *slog.Logger, serverURL, clusterID, token string) error {
	if logger == nil {
		logger = slog.Default()
	}
	if serverURL == "" {
		return errors.New("agent2: server URL is required")
	}
	if clusterID == "" {
		return errors.New("agent2: cluster_id is required")
	}
	if token == "" {
		return errors.New("agent2: agent token is required")
	}

	wsURL, err := buildWSURL(serverURL, clusterID)
	if err != nil {
		return fmt.Errorf("agent2: build ws url: %w", err)
	}

	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	headers.Set("X-Cluster-ID", clusterID)

	// Allow dialing anything: the agent's RBAC + network policy is what limits
	// the blast radius. The server side only invokes dials we put in code.
	allow := func(proto, address string) bool { return true }

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		logger.Info("agent2: connecting to proxy",
			slog.String("url", wsURL),
			slog.String("cluster_id", clusterID),
		)

		err := remotedialer.ConnectToProxy(ctx, wsURL, headers, allow, dialer(), nil)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn("agent2: tunnel disconnected", slog.String("error", err.Error()))
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		logger.Info("agent2: reconnecting", slog.Duration("after", defaultBackoff))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(defaultBackoff):
		}
	}
}

// dialer returns the websocket dialer used for the upgrade handshake. We bump
// the read buffer up — the in-cluster API server can return very large list
// responses (e.g. all pods in a busy cluster) and a small read buffer turns
// those into many tiny frames.
func dialer() *websocket.Dialer {
	return &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   64 * 1024,
		WriteBufferSize:  64 * 1024,
	}
}

// buildWSURL converts an http(s):// or ws(s):// origin into the full ws(s)://
// path the server mounts the remotedialer handler on.
func buildWSURL(serverURL, clusterID string) (string, error) {
	base := strings.TrimRight(serverURL, "/")
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	case strings.HasPrefix(base, "ws://"), strings.HasPrefix(base, "wss://"):
		// already ws-scheme
	default:
		return "", fmt.Errorf("unsupported scheme in server url: %q", serverURL)
	}
	return base + "/api/v1/connect/" + clusterID + "/", nil
}
