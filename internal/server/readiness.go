package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type dbHealthChecker interface {
	Health(ctx context.Context) error
}

// dbPoolSaturationReporter is an optional add-on to dbHealthChecker so the
// readiness probe can flip the pod out of Service rotation when the pgxpool
// is fully empty AND callers are queueing. Implemented by *db.DB.
type dbPoolSaturationReporter interface {
	// PoolWaitingForConn returns true when the pool has been at zero
	// available connections AND queueing acquires for a sustained interval.
	// Implementations decide what "sustained" means.
	PoolWaitingForConn() bool
}

type queuePinger interface {
	Ping() error
}

type hubStatusProvider interface {
	ConnectedClusters() []string
}

type readinessCheck struct {
	OK                bool   `json:"ok"`
	Error             string `json:"error,omitempty"`
	ConnectedClusters int    `json:"connected_clusters,omitempty"`
}

type readinessHandler struct {
	db      dbHealthChecker
	queue   queuePinger
	hub     hubStatusProvider
	timeout time.Duration
	now     func() time.Time
}

func newReadinessHandler(db dbHealthChecker, queue queuePinger, hub hubStatusProvider) *readinessHandler {
	return &readinessHandler{
		db:      db,
		queue:   queue,
		hub:     hub,
		timeout: 2 * time.Second,
		now:     time.Now,
	}
}

func (h *readinessHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		http.NotFound(w, r)
		return
	}

	statusCode := http.StatusOK
	checks := map[string]readinessCheck{}

	if h.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
		err := h.db.Health(ctx)
		cancel()
		checks["database"] = readinessCheck{OK: err == nil, Error: errString(err)}
		if err != nil {
			statusCode = http.StatusServiceUnavailable
		}
		// Pool saturation gate. When the pool is wedged at empty
		// with waiters, returning 503 deregisters this pod from Service
		// endpoints so traffic moves to a sibling with headroom. The
		// optional interface keeps tests + non-pgx implementations clean.
		if sat, ok := h.db.(dbPoolSaturationReporter); ok && sat.PoolWaitingForConn() {
			checks["database"] = readinessCheck{
				OK:    false,
				Error: "pgx pool exhausted: callers queueing for connections",
			}
			statusCode = http.StatusServiceUnavailable
		}
	}

	if h.queue != nil {
		err := h.queue.Ping()
		checks["redis"] = readinessCheck{OK: err == nil, Error: errString(err)}
		if err != nil {
			statusCode = http.StatusServiceUnavailable
		}
	}

	// Fail closed if the hub is missing entirely.
	// A nil hub means readiness was wired without the tunnel hub — the
	// pod should not be in Service rotation in that state. Previously
	// this case was silently skipped, so a misconfigured pod looked
	// healthy.
	if h.hub == nil {
		checks["tunnel_hub"] = readinessCheck{
			OK:    false,
			Error: "tunnel hub not initialized",
		}
		statusCode = http.StatusServiceUnavailable
	} else {
		checks["tunnel_hub"] = readinessCheck{
			OK:                true,
			ConnectedClusters: len(h.hub.ConnectedClusters()),
		}
	}

	body := map[string]any{
		"status": map[bool]string{
			true:  "ok",
			false: "not_ready",
		}[statusCode == http.StatusOK],
		"time":   h.now().UTC().Format(time.RFC3339),
		"checks": checks,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(body)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
