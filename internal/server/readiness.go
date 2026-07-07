package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db"
)

type dbHealthChecker interface {
	Health(ctx context.Context) error
}

// schemaVersionChecker is an optional add-on to dbHealthChecker: when the db
// dependency implements it, /readyz fails until the applied schema reaches the
// binary's embedded ExpectedSchemaVersion (C-03). *db.DB implements it; the test
// fakes don't, so the gate only runs against the real database.
type schemaVersionChecker interface {
	// SchemaVersion returns the applied schema version and the latest row's
	// golang-migrate dirty flag. dirty=true means the newest migration is in
	// progress or crashed mid-DDL, so the gate must treat it as not-ready.
	SchemaVersion(ctx context.Context) (int64, bool, error)
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
	// locatorError, when non-empty, fails readiness with a tunnel_locator check.
	// Set once at startup for the L19 HA self-check (replicas>1 + RedisURL set but
	// ASTRONOMER_POD_IP missing → cross-pod proxy silently off → 503s). Static, so
	// no lock needed.
	locatorError string
	// expectedSchemaVersion is the migration floor /readyz requires (C-03).
	// 0 disables the check.
	expectedSchemaVersion int64
}

func newReadinessHandler(dbChecker dbHealthChecker, queue queuePinger, hub hubStatusProvider) *readinessHandler {
	return &readinessHandler{
		db:                    dbChecker,
		queue:                 queue,
		hub:                   hub,
		timeout:               2 * time.Second,
		now:                   time.Now,
		expectedSchemaVersion: db.ExpectedSchemaVersion,
	}
}

// withLocatorError sets the L19 HA self-check error (empty = healthy) and returns
// the handler for fluent wiring. nil-safe.
func (h *readinessHandler) withLocatorError(msg string) *readinessHandler {
	if h != nil {
		h.locatorError = msg
	}
	return h
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
		// Schema-version floor (C-03). During an upgrade, migrations run
		// post-upgrade, so a new pod can pass every other check while still
		// pointed at the OLD schema. Hold the pod out of Service rotation until
		// schema_migrations.version >= the version this binary was built against.
		if sv, ok := h.db.(schemaVersionChecker); ok && h.expectedSchemaVersion > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
			got, dirty, err := sv.SchemaVersion(ctx)
			cancel()
			switch {
			case err != nil:
				checks["schema_version"] = readinessCheck{OK: false, Error: err.Error()}
				statusCode = http.StatusServiceUnavailable
			case dirty:
				// golang-migrate marks the row dirty BEFORE running the
				// migration and clears it only on success, so dirty=true means
				// the latest migration is mid-flight or crashed mid-DDL. Hold
				// the pod out of rotation rather than serve a half-applied schema.
				checks["schema_version"] = readinessCheck{
					OK:    false,
					Error: "schema migration in progress or failed (dirty)",
				}
				statusCode = http.StatusServiceUnavailable
			case got < h.expectedSchemaVersion:
				checks["schema_version"] = readinessCheck{
					OK: false,
					Error: fmt.Sprintf("applied schema version %d < required %d (migrations not yet applied)",
						got, h.expectedSchemaVersion),
				}
				statusCode = http.StatusServiceUnavailable
			default:
				checks["schema_version"] = readinessCheck{OK: true}
			}
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

	// L19: a misconfigured HA deployment (cross-pod locator off) is reported as a
	// hard readiness failure so the rollout stalls loudly instead of silently
	// 503-ing cross-pod requests.
	if h.locatorError != "" {
		checks["tunnel_locator"] = readinessCheck{OK: false, Error: h.locatorError}
		statusCode = http.StatusServiceUnavailable
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
