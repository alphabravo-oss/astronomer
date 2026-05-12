// Package tasks — telemetry sender.
//
// Migration 046 introduced the `telemetry.enabled` + `telemetry.endpoint`
// settings. When `enabled` is `true`, this task POSTs a small JSON
// payload to `endpoint` once per night. The payload is intentionally
// aggregate-only: no user PII, no cluster names, no resource bodies —
// just enough for us to track install count, version drift, and rough
// fleet size:
//
//	{
//	  "instance_id":    "<observability InstanceID>",
//	  "version":        "<pkg/version>",
//	  "cluster_count":  N,
//	  "user_count":     N,
//	  "project_count": N,
//	  "as_of":          RFC3339 UTC
//	}
//
// Failures never fail the worker pod — the task logs and returns nil
// so the daily schedule keeps moving. Retries are not useful: the
// telemetry signal we lose for one day is recoverable from the next
// successful POST.
package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// TelemetrySendType is the periodic task identifier.
const TelemetrySendType = "telemetry:send"

// telemetryQuerier is the slice of the runtime querier the telemetry
// task needs. Production wires *sqlc.Queries which satisfies all four;
// tests can hand a tiny fake.
type telemetryQuerier interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	CountClusters(ctx context.Context) (int64, error)
	CountUsers(ctx context.Context) (int64, error)
	CountProjects(ctx context.Context) (int64, error)
}

// TelemetryPayload is the wire shape POSTed to the configured endpoint.
// Exported so tests can decode + assert.
type TelemetryPayload struct {
	InstanceID    string    `json:"instance_id"`
	Version       string    `json:"version"`
	ClusterCount  int64     `json:"cluster_count"`
	UserCount     int64     `json:"user_count"`
	ProjectCount  int64     `json:"project_count"`
	AsOf          time.Time `json:"as_of"`
}

// NewTelemetrySendTask returns a task that POSTs aggregated counts to
// the configured telemetry endpoint when the operator has opted in.
func NewTelemetrySendTask() *asynq.Task {
	return asynq.NewTask(TelemetrySendType, nil, asynq.MaxRetry(0))
}

// HandleTelemetrySend is the asynq handler. It runs under the same
// leader-election dance as the other periodic tasks so multi-replica
// installs don't double-post.
func HandleTelemetrySend(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, TelemetrySendType, func() error {
		if runtimeDeps.Queries == nil {
			runtimeLogger().InfoContext(ctx, "telemetry: runtime not configured, skipping")
			return nil
		}
		q, ok := runtimeDeps.Queries.(telemetryQuerier)
		if !ok {
			runtimeLogger().WarnContext(ctx, "telemetry: runtime querier missing required methods, skipping")
			return nil
		}
		return sendTelemetry(ctx, q, runtimeDeps.HTTPClient, time.Now().UTC())
	})
}

// sendTelemetry is the pure implementation, called from the handler
// and the tests. now() is parameterised so the test can pin the
// timestamp.
func sendTelemetry(ctx context.Context, q telemetryQuerier, client *http.Client, now time.Time) error {
	enabled, _ := readTelemetryBool(ctx, q, "telemetry.enabled")
	if !enabled {
		runtimeLogger().DebugContext(ctx, "telemetry: opt-in disabled, skipping")
		return nil
	}
	endpoint, _ := readTelemetryString(ctx, q, "telemetry.endpoint")
	if endpoint == "" {
		runtimeLogger().WarnContext(ctx, "telemetry: endpoint not configured, skipping")
		return nil
	}

	clusters, err := q.CountClusters(ctx)
	if err != nil {
		return fmt.Errorf("count clusters: %w", err)
	}
	users, err := q.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	projects, err := q.CountProjects(ctx)
	if err != nil {
		return fmt.Errorf("count projects: %w", err)
	}

	payload := TelemetryPayload{
		InstanceID:   observability.InstanceID(),
		Version:      version.Version,
		ClusterCount: clusters,
		UserCount:    users,
		ProjectCount: projects,
		AsOf:         now,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal telemetry payload: %w", err)
	}

	if client == nil {
		client = http.DefaultClient
	}

	postCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telemetry request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "astronomer-go/"+version.Version)

	resp, err := client.Do(req)
	if err != nil {
		// Logged + swallowed. The telemetry endpoint being down today
		// doesn't justify a worker pod restart; the next day's run
		// will retry on the daily cadence.
		runtimeLogger().WarnContext(ctx, "telemetry: POST failed", "error", err, "endpoint", endpoint)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		runtimeLogger().WarnContext(ctx, "telemetry: non-2xx from endpoint", "status", resp.StatusCode, "endpoint", endpoint)
		return nil
	}
	runtimeLogger().InfoContext(ctx, "telemetry: posted", "endpoint", endpoint, "cluster_count", clusters, "user_count", users, "project_count", projects)
	return nil
}

// readTelemetryBool reads a JSONB boolean setting, falling back to
// `false` when the row is absent (telemetry is OPT-IN — absence ==
// "off") or the value isn't a boolean.
func readTelemetryBool(ctx context.Context, q telemetryQuerier, key string) (bool, bool) {
	row, err := q.GetPlatformSetting(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, true
		}
		return false, false
	}
	var v bool
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return false, false
	}
	return v, true
}

// readTelemetryString reads a JSONB string setting. Same fallback
// semantics as readTelemetryBool.
func readTelemetryString(ctx context.Context, q telemetryQuerier, key string) (string, bool) {
	row, err := q.GetPlatformSetting(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", true
		}
		return "", false
	}
	var v string
	if err := json.Unmarshal(row.Value, &v); err != nil {
		return "", false
	}
	return v, true
}
