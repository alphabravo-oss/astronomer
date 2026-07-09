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
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rancher/remotedialer"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel/connectauth"
)

// A4 audit actions, shared verbatim with the hub connect path so both connect
// surfaces emit the same forensic action strings.
const (
	actionAgentConnected  = "agent.connected"
	actionAgentAuthFailed = "agent.auth_failed"
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
	// limiter is the SAME *tunnel.ConnectFailureLimiter shared with the hub
	// connect path, so an attacker hammering both /connect and the legacy
	// /ws/agent/tunnel route accumulates failures against one per-IP view.
	// Optional; nil-safe (no throttling when unset).
	limiter *tunnel.ConnectFailureLimiter
	// validator is retained so the authorize audit can type-assert it to an
	// audit.Querier for the fail-open connect/auth-failure records.
	validator tunnel.AgentTokenValidator
	// allowInsecureNilValidator (L18) gates the legacy "nil validator accepts any
	// connection unauthenticated" behavior. It defaults FALSE — a nil validator
	// now FAILS CLOSED (rejects) so a mis-wired production server can never accept
	// unauthenticated tunnels. Only test/demo code that deliberately runs without
	// a DB validator sets it true.
	allowInsecureNilValidator bool
}

// SetAllowInsecureNilValidator opts into accepting connections when no validator
// is wired (test/demo only). Production never calls this, so a nil validator
// fails closed. Set once at startup.
func (s *RemoteServer) SetAllowInsecureNilValidator(v bool) { s.allowInsecureNilValidator = v }

// SetConnectLimiter wires the shared A4 connect failure-limiter (set once at
// startup). Nil-safe.
func (s *RemoteServer) SetConnectLimiter(lim *tunnel.ConnectFailureLimiter) {
	s.limiter = lim
}

// RateLimitMiddleware returns a pre-upgrade gate that rejects (HTTP 429) a
// source IP already over the shared failure threshold BEFORE remotedialer
// hijacks the connection. Per-IP only; nil-safe (pass-through when no limiter
// is wired). Mirrors the hub path's 429 body so both connect surfaces behave
// identically.
func (s *RemoteServer) RateLimitMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.limiter != nil {
				ipKey, _ := tunnel.ConnectClientIP(r)
				if blocked, retryAfter := s.limiter.Blocked(ipKey); blocked {
					secs := int(math.Ceil(retryAfter.Seconds()))
					if secs < 1 {
						secs = 1
					}
					w.Header().Set("Retry-After", strconv.Itoa(secs))
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"code":"rate_limited"}`))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NewRemoteServer constructs the tunnel server. The validator is the same
// AgentTokenValidator the legacy hub uses — we reuse it so a single registration
// token is honoured by either tunnel implementation during migration.
func NewRemoteServer(logger *slog.Logger, validator tunnel.AgentTokenValidator) *RemoteServer {
	if logger == nil {
		logger = slog.Default()
	}
	rs := &RemoteServer{log: logger, validator: validator}
	rs.server = remotedialer.New(rs.authorize(validator), rs.errorWriter)
	return rs
}

// authorize builds the remotedialer.Authorizer closure. The signature is:
//
//	func(req *http.Request) (clientKey string, authed bool, err error)
//
// We extract the cluster_id from (in priority order) the URL chi router param,
// the ?cluster_id= query, or the X-Cluster-ID header. We extract the bearer
// token from the Authorization header. Credential checks share hub A3 logic
// via connectauth.Validate: post-adoption registration tokens are rejected,
// durable agent tokens (hashed) are accepted, and the token's cluster_id MUST
// match the requested cluster_id. The returned clientKey is the cluster UUID
// string — that is what callers pass to DialerFor / HasSession.
func (s *RemoteServer) authorize(validator tunnel.AgentTokenValidator) remotedialer.Authorizer {
	log := s.log
	return func(req *http.Request) (string, bool, error) {
		ipKey, ipAddr := tunnel.ConnectClientIP(req)
		clusterID := extractClusterID(req)
		if clusterID == "" {
			// Pre-DB, cheap — not a credential probe; do not count it.
			return "", false, fmt.Errorf("cluster_id missing from request")
		}

		token := extractBearerToken(req)
		if token == "" {
			return "", false, fmt.Errorf("authorization bearer token missing")
		}

		// L18: a nil validator FAILS CLOSED by default — a mis-wired production
		// server (no DB validator) must never accept unauthenticated tunnels.
		// Only test/demo code that opts in via SetAllowInsecureNilValidator gets
		// the legacy accept-any behavior.
		if validator == nil {
			if !s.allowInsecureNilValidator {
				log.Error("tunnel2: validator is nil and insecure nil-validator is NOT enabled — refusing connection (fail closed)",
					slog.String("cluster_id", clusterID))
				return "", false, fmt.Errorf("server misconfigured: no agent-token validator")
			}
			log.Warn("tunnel2: validator is nil, accepting connection unauthenticated (insecure test/demo mode)",
				slog.String("cluster_id", clusterID),
			)
			return clusterID, true, nil
		}

		clusterUUID, err := uuid.Parse(clusterID)
		if err != nil {
			// Malformed cluster UUID is a cheap PRE-DB rejection, not a
			// credential probe — do not count it against the limiter.
			return "", false, fmt.Errorf("invalid cluster_id")
		}

		// SEC-R01: same A3 validation as the hub (adoption gate + durable hash).
		res, err := connectauth.Validate(req.Context(), validator, clusterUUID, token)
		if err != nil {
			// A4 / M5+M6: a failed DB-backed token lookup is the probe surface —
			// count it (shared per-IP view) and audit it (fail-open).
			if s.limiter != nil {
				s.limiter.Fail(ipKey)
			}
			s.recordAuthFailed(req.Context(), clusterID, ipAddr, req, err.Error())
			log.Warn("tunnel2: agent authentication failed",
				slog.String("cluster_id", clusterID),
				slog.String("error", err.Error()),
			)
			return "", false, err
		}

		// Best-effort adoption stamp when a durable authenticates (mirrors hub).
		if res.Kind == connectauth.KindAgent && res.AgentToken.ID != uuid.Nil {
			if err := validator.MarkClusterAgentTokenAdopted(req.Context(), res.AgentToken.ID); err != nil {
				log.Warn("tunnel2: failed to mark cluster agent token adopted",
					slog.String("cluster_id", clusterID),
					slog.String("error", err.Error()),
				)
			}
		}

		// Success: clear this IP's failure history and audit the connect.
		if s.limiter != nil {
			s.limiter.Reset(ipKey)
		}
		s.recordConnected(req.Context(), clusterID, ipAddr, req, res.Kind)
		log.Info("tunnel2: agent authorized",
			slog.String("cluster_id", clusterID),
			slog.String("token_kind", connectauth.TokenKindLabel(res.Kind)),
		)
		return clusterID, true, nil
	}
}

// recordConnected / recordAuthFailed mirror the hub's fail-open connect audit
// (A4 / M6) on the remotedialer path. NOTE: the remotedialer authorize layer
// only sees the HTTP upgrade request, which carries no agent_version (that
// arrives in post-upgrade frames remotedialer abstracts away), so the version
// is omitted here — the hub path records it for the deployed agent.
func (s *RemoteServer) recordConnected(ctx context.Context, clusterID string, ipAddr *netip.Addr, r *http.Request, tokenKind string) {
	q, ok := s.validator.(audit.Querier)
	if !ok {
		return
	}
	audit.Record(ctx, q, audit.Event{
		Source:          "tunnel",
		ActorAuthMethod: "agent_token",
		Action:          actionAgentConnected,
		ResourceType:    "cluster",
		ResourceID:      clusterID,
		ResourceName:    clusterID,
		StatusCode:      200,
		IPAddress:       ipAddr,
		UserAgent:       r.UserAgent(),
		Detail: map[string]any{
			"cluster_id": clusterID,
			"source_ip":  ipString(ipAddr),
			"token_kind": connectauth.TokenKindLabel(tokenKind),
			"transport":  "remotedialer",
		},
	})
}

func (s *RemoteServer) recordAuthFailed(ctx context.Context, clusterID string, ipAddr *netip.Addr, r *http.Request, reason string) {
	q, ok := s.validator.(audit.Querier)
	if !ok {
		return
	}
	audit.Record(ctx, q, audit.Event{
		Source:          "tunnel",
		ActorAuthMethod: "agent_token",
		Action:          actionAgentAuthFailed,
		ResourceType:    "cluster",
		ResourceID:      clusterID,
		StatusCode:      403,
		IPAddress:       ipAddr,
		UserAgent:       r.UserAgent(),
		Detail: map[string]any{
			"cluster_id": clusterID,
			"source_ip":  ipString(ipAddr),
			"token_kind": "invalid",
			"reason":     reason,
			"transport":  "remotedialer",
		},
	})
}

func ipString(addr *netip.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.String()
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
