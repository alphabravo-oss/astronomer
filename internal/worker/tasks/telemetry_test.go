package tasks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeTelemetryQuerier covers the four methods telemetryQuerier needs.
type fakeTelemetryQuerier struct {
	settings map[string][]byte
	clusters int64
	users    int64
	projects int64
}

func (f *fakeTelemetryQuerier) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	v, ok := f.settings[key]
	if !ok {
		return sqlc.PlatformSetting{}, pgx.ErrNoRows
	}
	return sqlc.PlatformSetting{Key: key, Value: v}, nil
}
func (f *fakeTelemetryQuerier) CountClusters(_ context.Context) (int64, error) {
	return f.clusters, nil
}
func (f *fakeTelemetryQuerier) CountUsers(_ context.Context) (int64, error) { return f.users, nil }
func (f *fakeTelemetryQuerier) CountProjects(_ context.Context) (int64, error) {
	return f.projects, nil
}

func TestTelemetry_SkipsWhenDisabled(t *testing.T) {
	// telemetry.enabled is missing → treat as opt-out (false).
	posts := int32(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&posts, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	q := &fakeTelemetryQuerier{
		settings: map[string][]byte{
			"telemetry.endpoint": []byte(`"` + server.URL + `"`),
		},
		clusters: 1, users: 1, projects: 1,
	}
	if err := sendTelemetry(context.Background(), q, server.Client(), time.Now()); err != nil {
		t.Fatalf("sendTelemetry returned error: %v", err)
	}
	if atomic.LoadInt32(&posts) != 0 {
		t.Fatalf("posts = %d, want 0 (opt-in is off)", posts)
	}

	// Explicit false too.
	q.settings["telemetry.enabled"] = []byte(`false`)
	if err := sendTelemetry(context.Background(), q, server.Client(), time.Now()); err != nil {
		t.Fatalf("sendTelemetry returned error: %v", err)
	}
	if atomic.LoadInt32(&posts) != 0 {
		t.Fatalf("posts = %d after explicit false, want 0", posts)
	}
}

func TestTelemetry_PostsAggregatedCounts(t *testing.T) {
	var received TelemetryPayload
	var posts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&posts, 1)
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &received)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	q := &fakeTelemetryQuerier{
		settings: map[string][]byte{
			"telemetry.enabled":  []byte(`true`),
			"telemetry.endpoint": []byte(`"` + server.URL + `"`),
		},
		clusters: 7, users: 12, projects: 3,
	}
	now := time.Date(2026, 5, 12, 2, 30, 0, 0, time.UTC)
	if err := sendTelemetry(context.Background(), q, server.Client(), now); err != nil {
		t.Fatalf("sendTelemetry returned error: %v", err)
	}
	if atomic.LoadInt32(&posts) != 1 {
		t.Fatalf("posts = %d, want 1", posts)
	}
	if received.ClusterCount != 7 || received.UserCount != 12 || received.ProjectCount != 3 {
		t.Fatalf("counts = (%d, %d, %d), want (7, 12, 3)", received.ClusterCount, received.UserCount, received.ProjectCount)
	}
	if !received.AsOf.Equal(now) {
		t.Fatalf("as_of = %v, want %v", received.AsOf, now)
	}
}

// When the endpoint is unreachable the task swallows the error and
// returns nil — opt-in telemetry must NEVER fail the worker pod.
func TestTelemetry_EndpointFailureSwallowed(t *testing.T) {
	q := &fakeTelemetryQuerier{
		settings: map[string][]byte{
			"telemetry.enabled":  []byte(`true`),
			"telemetry.endpoint": []byte(`"http://127.0.0.1:1/unreachable"`),
		},
	}
	if err := sendTelemetry(context.Background(), q, &http.Client{Timeout: 250 * time.Millisecond}, time.Now()); err != nil {
		t.Fatalf("sendTelemetry returned error %v, want nil (swallowed)", err)
	}
}
