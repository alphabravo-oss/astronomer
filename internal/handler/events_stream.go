package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// EventStreamHandler serves Server-Sent Events from the in-memory bus
// (optionally Redis-backed for multi-replica fan-out).
//
// Auth contract:
//
//	GET /api/v1/events/stream/?ticket=<short-lived-ticket>
//
// EventSource cannot set custom headers, so browsers should first POST to
// /api/v1/streams/tickets/ and pass the one-use ticket in the stream URL.
//
// SEC-R07: when authorization is wired, per-event delivery is filtered by
// clusters:read (or list) on the event's cluster_id. Events without a
// cluster_id pass through only for unrestricted (superuser) principals;
// restricted users drop unscoped events.
type EventStreamHandler struct {
	bus     *events.Bus
	jwt     *auth.JWTManager
	queries middleware.TokenUserQuerier
	tickets *auth.StreamTicketStore
	authz   authorizationSupport
	// keepaliveInterval overrides the 25s heartbeat cadence (tests only).
	keepaliveInterval time.Duration
	// bindingRefreshInterval overrides the 5-minute RBAC binding
	// re-snapshot cadence (tests only).
	bindingRefreshInterval time.Duration
}

// NewEventStreamHandler wraps a bus.
func NewEventStreamHandler(bus *events.Bus) *EventStreamHandler {
	return &EventStreamHandler{bus: bus}
}

// SetAuth wires the JWT manager + token querier for legacy query/header
// stream auth. Both arguments are optional; when nil the handler accepts
// unauthenticated connections (used by tests / dev runs without auth wired).
func (h *EventStreamHandler) SetAuth(jwt *auth.JWTManager, queries middleware.TokenUserQuerier) {
	if h == nil {
		return
	}
	h.jwt = jwt
	h.queries = queries
}

func (h *EventStreamHandler) SetStreamTickets(tickets *auth.StreamTicketStore) {
	if h == nil {
		return
	}
	h.tickets = tickets
}

// SetAuthorization enables per-event cluster RBAC filtering (SEC-R07).
func (h *EventStreamHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if h == nil {
		return
	}
	h.authz.engine = engine
	h.authz.querier = querier
}

// authenticateRequest validates the request via a one-use stream ticket or
// Authorization header. Returns the user ID (may be Nil in dev mode) and ok.
func (h *EventStreamHandler) authenticateRequest(r *http.Request) (uuid.UUID, bool) {
	return auth.AuthorizeStreamRequestWithTickets(r, h.queries, h.jwt, h.tickets, auth.StreamKindEvents, uuid.Nil)
}

// Stream is the GET handler for SSE.
func (h *EventStreamHandler) Stream(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.bus == nil {
		http.Error(w, "event stream not available", http.StatusServiceUnavailable)
		return
	}
	userID, ok := h.authenticateRequest(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Load bindings for the stream (SEC-R07), re-snapshotted every 5
	// minutes below (T5/D10) so mid-stream revocations stop leaking events
	// after at most one interval instead of the entire stream lifetime.
	bindings, restricted := h.snapshotStreamBindings(r.Context(), userID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Initial comment to flush headers and prove the stream is alive.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	// Heartbeat every 25s: a real data frame the client can observe (its
	// watchdog resets on any frame), unlike an SSE comment which never
	// reaches onmessage. Also keeps idle-closing proxies warm.
	interval := h.keepaliveInterval
	if interval <= 0 {
		interval = 25 * time.Second
	}
	keepalive := time.NewTicker(interval)
	defer keepalive.Stop()

	// T5 (D10): re-snapshot the caller's RBAC bindings every 5 minutes so a
	// revocation mid-stream takes effect within one interval. On a refresh
	// error the previous snapshot stays in force until the next tick.
	refreshInterval := h.bindingRefreshInterval
	if refreshInterval <= 0 {
		refreshInterval = 5 * time.Minute
	}
	bindingRefresh := time.NewTicker(refreshInterval)
	defer bindingRefresh.Stop()

	ch := h.bus.Subscribe(r.Context())
	for {
		select {
		case <-r.Context().Done():
			return
		case <-bindingRefresh.C:
			if b, r2 := h.snapshotStreamBindings(r.Context(), userID); r2 || !restricted {
				bindings, restricted = b, r2
			}
		case <-keepalive.C:
			ping, err := json.Marshal(struct {
				Type string    `json:"type"`
				Time time.Time `json:"time"`
			}{Type: "sys.ping", Time: time.Now().UTC()})
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", ping); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// sys.* frames are unscoped control-plane signals (no cluster_id)
			// and are exempt from the SEC-R07 drop — without this, restricted
			// users' client watchdogs would fire spuriously.
			if restricted && !isSysEvent(ev.Type) && !eventAllowedForUser(h.authz, bindings, ev) {
				continue
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			// Default-message SSE frame: id + data + blank line (no event:
			// line — the JSON envelope carries `type` and the client
			// dispatches on it in onmessage).
			if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.ID, payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// snapshotStreamBindings loads the caller's RBAC bindings for SEC-R07
// per-event filtering. Returns restricted=false for superusers/unrestricted
// principals (authz not wired, dev connections) or when the binding load
// fails — callers on the refresh path must then keep the previous snapshot
// rather than fail open.
func (h *EventStreamHandler) snapshotStreamBindings(ctx context.Context, userID uuid.UUID) ([]rbac.RoleBinding, bool) {
	if h.authz.engine == nil || h.authz.querier == nil || userID == uuid.Nil {
		return nil, false
	}
	b, err := h.authz.querier.GetUserBindings(ctx, userID.String())
	if err != nil {
		return nil, false
	}
	return b, true
}

// isSysEvent reports whether the event type is a sys.* control frame,
// which carries no cluster_id and bypasses per-event cluster RBAC.
func isSysEvent(t events.Type) bool {
	return strings.HasPrefix(string(t), "sys.")
}

// eventAllowedForUser implements SEC-R07 per-event cluster RBAC.
// Events without a parseable cluster_id are dropped for restricted users
// (fail closed — no fleet-wide leak of unscoped lifecycle noise).
func eventAllowedForUser(a authorizationSupport, bindings []rbac.RoleBinding, ev events.Event) bool {
	if a.engine == nil {
		return true
	}
	clusterID, ok := clusterIDFromEventData(ev.Data)
	if !ok {
		return false
	}
	return a.allowsCluster(bindings, clusterID, rbac.ResourceClusters, rbac.VerbRead) ||
		a.allowsCluster(bindings, clusterID, rbac.ResourceClusters, rbac.VerbList)
}

// clusterIDFromEventData extracts cluster_id from common event payload shapes.
func clusterIDFromEventData(data any) (uuid.UUID, bool) {
	switch v := data.(type) {
	case map[string]any:
		return parseClusterIDField(v["cluster_id"])
	case json.RawMessage:
		var m map[string]any
		if err := json.Unmarshal(v, &m); err != nil {
			return uuid.Nil, false
		}
		return parseClusterIDField(m["cluster_id"])
	default:
		// Best-effort re-marshal for struct payloads.
		raw, err := json.Marshal(data)
		if err != nil {
			return uuid.Nil, false
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return uuid.Nil, false
		}
		return parseClusterIDField(m["cluster_id"])
	}
}

func parseClusterIDField(v any) (uuid.UUID, bool) {
	switch t := v.(type) {
	case string:
		id, err := uuid.Parse(t)
		return id, err == nil
	case uuid.UUID:
		return t, t != uuid.Nil
	default:
		return uuid.Nil, false
	}
}
