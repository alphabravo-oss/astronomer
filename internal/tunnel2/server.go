// Package tunnel2 wraps github.com/rancher/remotedialer to provide a
// reverse-WebSocket tunnel between the management server and remote cluster
// agents. It is the migration target replacing the bespoke JSON tunnel in
// internal/tunnel; both packages run side-by-side until traffic is cut over.
//
// The remotedialer model is dramatically simpler than the per-feature
// JSON-message protocol: the server gets a `remotedialer.Dialer` per connected
// agent, and any code that wants to talk to an in-cluster service just plumbs
// that dialer into an http.Transport. There is no per-feature originator on
// the server and no per-message handler on the agent.
package tunnel2

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rancher/remotedialer"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

// HeaderClusterID is the canonical header the agent sets on the WS upgrade
// request. The server prefers this over the ?cluster_id= query parameter
// because it is harder to forge through an HTTP proxy that strips queries.
const HeaderClusterID = "X-Cluster-ID"

// RemoteServer is a thin wrapper over *remotedialer.Server that owns the
// authorization closure. It exposes only the methods the rest of the codebase
// needs so handler code does not depend on the remotedialer package directly.
type RemoteServer struct {
	server *remotedialer.Server
	log    *slog.Logger
}

// NewRemoteServer constructs the tunnel server. The validator is the same
// AgentTokenValidator the legacy hub uses — we reuse it so a single registration
// token is honoured by either tunnel implementation during migration.
func NewRemoteServer(logger *slog.Logger, validator tunnel.AgentTokenValidator) *RemoteServer {
	if logger == nil {
		logger = slog.Default()
	}
	rs := &RemoteServer{log: logger}
	rs.server = remotedialer.New(rs.authorize(validator), rs.errorWriter)
	return rs
}

// authorize builds the remotedialer.Authorizer closure. The signature is:
//
//	func(req *http.Request) (clientKey string, authed bool, err error)
//
// We extract the cluster_id from (in priority order) the URL chi router param,
// the ?cluster_id= query, or the X-Cluster-ID header. We extract the bearer
// token from the Authorization header. The token is validated against the
// cluster_registration_tokens table and the token row's cluster_id MUST match
// the requested cluster_id. The returned clientKey is the cluster UUID string
// — that is what callers pass to DialerFor / HasSession.
func (s *RemoteServer) authorize(validator tunnel.AgentTokenValidator) remotedialer.Authorizer {
	log := s.log
	return func(req *http.Request) (string, bool, error) {
		clusterID := extractClusterID(req)
		if clusterID == "" {
			return "", false, fmt.Errorf("cluster_id missing from request")
		}

		token := extractBearerToken(req)
		if token == "" {
			return "", false, fmt.Errorf("authorization bearer token missing")
		}

		// validator may be nil in test mode (NewApp without DB). When nil the
		// tunnel accepts any cluster_id presented with any non-empty token.
		// Production wiring always passes a real validator.
		if validator == nil {
			log.Warn("tunnel2: validator is nil, accepting connection unauthenticated",
				slog.String("cluster_id", clusterID),
			)
			return clusterID, true, nil
		}

		tokenRecord, err := validator.GetRegistrationTokenByToken(req.Context(), token)
		if err != nil {
			log.Warn("tunnel2: invalid registration token",
				slog.String("cluster_id", clusterID),
				slog.String("error", err.Error()),
			)
			return "", false, fmt.Errorf("invalid registration token")
		}
		if tokenRecord.ClusterID.String() != clusterID {
			log.Warn("tunnel2: registration token cluster mismatch",
				slog.String("expected_cluster_id", tokenRecord.ClusterID.String()),
				slog.String("provided_cluster_id", clusterID),
			)
			return "", false, fmt.Errorf("registration token does not match cluster")
		}

		log.Info("tunnel2: agent authorized",
			slog.String("cluster_id", clusterID),
		)
		return clusterID, true, nil
	}
}

// errorWriter logs and forwards remotedialer error responses.
func (s *RemoteServer) errorWriter(rw http.ResponseWriter, req *http.Request, code int, err error) {
	s.log.Warn("tunnel2: rejecting request",
		slog.String("remote", req.RemoteAddr),
		slog.Int("code", code),
		slog.String("error", err.Error()),
	)
	remotedialer.DefaultErrorWriter(rw, req, code, err)
}

// ServeHTTP exposes the underlying remotedialer.Server handler. Mount on the
// chi router; remotedialer hijacks the connection for WS upgrade.
func (s *RemoteServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.server.ServeHTTP(w, r)
}

// DialerFor returns a remotedialer.Dialer that opens connections through the
// agent identified by clusterID. The returned dialer has signature:
//
//	func(ctx context.Context, network, address string) (net.Conn, error)
//
// which matches http.Transport.DialContext exactly.
func (s *RemoteServer) DialerFor(clusterID string) remotedialer.Dialer {
	return s.server.Dialer(clusterID)
}

// HasSession reports whether the named cluster currently has a live tunnel.
func (s *RemoteServer) HasSession(clusterID string) bool {
	return s.server.HasSession(clusterID)
}

// ListClients returns the cluster IDs of all connected agents.
func (s *RemoteServer) ListClients() []string {
	return s.server.ListClients()
}

// extractClusterID pulls the cluster_id out of (in order): chi URL param,
// ?cluster_id= query parameter, X-Cluster-ID header.
func extractClusterID(req *http.Request) string {
	if id := chi.URLParam(req, "cluster_id"); id != "" {
		return id
	}
	if id := req.URL.Query().Get("cluster_id"); id != "" {
		return id
	}
	return req.Header.Get(HeaderClusterID)
}

// extractBearerToken returns the token portion of an Authorization: Bearer
// header. Returns the empty string if no bearer token is present.
func extractBearerToken(req *http.Request) string {
	h := req.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
