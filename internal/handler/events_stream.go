package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
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
//	GET /api/v1/events/stream/?ticket=<short-lived-ticket>
//
// EventSource cannot set custom headers, so browsers should first POST to
// /api/v1/streams/tickets/ and pass the one-use ticket in the stream URL.
//
// Frontend usage:
//
//	const es = new EventSource('/api/v1/events/stream/?ticket=' + ticket);
//	es.addEventListener('cluster.connected', e => { ... });
//
// The bus only emits resource-lifecycle events that mirror existing readable
// state, so per-event RBAC is not enforced here — same posture as the
// pre-existing implementation.
type EventStreamHandler struct {
	bus     *events.Bus
	jwt     *auth.JWTManager
	queries middleware.TokenUserQuerier
	tickets *auth.StreamTicketStore
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

// authenticateRequest validates the request via a one-use stream ticket or
// Authorization header. Returns true if either path succeeded, or if no JWT
// manager is wired (dev/test mode).
func (h *EventStreamHandler) authenticateRequest(r *http.Request) bool {
	_, ok := auth.AuthorizeStreamRequestWithTickets(r, h.queries, h.jwt, h.tickets, auth.StreamKindEvents, uuid.Nil)
	return ok
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
