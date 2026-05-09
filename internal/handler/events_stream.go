package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// EventStreamHandler serves Server-Sent Events from the in-memory bus.
//
// Auth contract:
//
//	GET /api/v1/events/stream/?token=<jwt-or-api-token>
//
// EventSource cannot set custom headers, so the JWT is accepted via the
// `?token=` query parameter ONLY on this endpoint. The token is validated
// via the same JWTManager used by the rest of the API; api_token-style
// (astro_*) tokens are also accepted via SHA-256 hash lookup. No other
// route accepts query-string auth — the loosened contract is scoped to
// the one handler that needs it.
//
// Frontend usage:
//
//	const es = new EventSource('/api/v1/events/stream/?token=' + jwt);
//	es.addEventListener('cluster.connected', e => { ... });
//
// The bus only emits resource-lifecycle events that mirror existing readable
// state, so per-event RBAC is not enforced here — same posture as the
// pre-existing implementation.
type EventStreamHandler struct {
	bus     *events.Bus
	jwt     *auth.JWTManager
	queries middleware.TokenUserQuerier
}

// NewEventStreamHandler wraps a bus.
func NewEventStreamHandler(bus *events.Bus) *EventStreamHandler {
	return &EventStreamHandler{bus: bus}
}

// SetAuth wires the JWT manager + token querier so the handler can validate
// the `?token=` query parameter (EventSource cannot set Authorization).
// Both arguments are optional; when nil the handler accepts unauthenticated
// connections (used by tests / dev runs without auth wired).
func (h *EventStreamHandler) SetAuth(jwt *auth.JWTManager, queries middleware.TokenUserQuerier) {
	if h == nil {
		return
	}
	h.jwt = jwt
	h.queries = queries
}

// authenticateRequest validates the request via Authorization header (preferred)
// or `?token=` query parameter (EventSource fallback). Returns true if either
// path succeeded, or if no JWT manager is wired (dev/test mode).
func (h *EventStreamHandler) authenticateRequest(r *http.Request) bool {
	if h.jwt == nil {
		return true
	}
	token := bearerFromHeader(r.Header.Get("Authorization"))
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return false
	}
	if strings.HasPrefix(token, "astro_") {
		if h.queries == nil {
			// No DB -> can't verify api_token; reject.
			return false
		}
		hash := sha256.Sum256([]byte(token))
		hashStr := hex.EncodeToString(hash[:])
		apiToken, err := h.queries.GetTokenByHash(r.Context(), hashStr)
		if err != nil {
			return false
		}
		if apiToken.ExpiresAt.Valid && apiToken.ExpiresAt.Time.Before(time.Now()) {
			return false
		}
		dbUser, err := h.queries.GetUserByID(r.Context(), apiToken.UserID)
		if err != nil || !dbUser.IsActive {
			return false
		}
		return true
	}
	claims, err := h.jwt.ValidateToken(token)
	if err != nil {
		return false
	}
	if claims.UserID == uuid.Nil {
		return false
	}
	return true
}

func bearerFromHeader(h string) string {
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}

// Stream is the GET handler for SSE.
func (h *EventStreamHandler) Stream(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.bus == nil {
		http.Error(w, "event stream not available", http.StatusServiceUnavailable)
		return
	}
	if !h.authenticateRequest(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Initial comment to flush headers and prove the stream is alive.
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// Keepalive comment every 25s. EventSource auto-reconnects on close, but
	// some intermediate proxies idle-close silent connections — sending a
	// comment frame keeps the path warm without producing a frontend event.
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	ch := h.bus.Subscribe(r.Context())
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			// SSE frame: id + event + data + blank line.
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, payload)
			flusher.Flush()
		}
	}
}
