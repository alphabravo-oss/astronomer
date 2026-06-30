package server

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeManifestRenderer struct {
	manifest string
	err      error
	calls    int
}

func (f *fakeManifestRenderer) RenderAgentManifestForCluster(_ context.Context, _ uuid.UUID) (string, error) {
	f.calls++
	return f.manifest, f.err
}

type fakeDecommReader struct {
	row sqlc.ClusterDecommission
	err error
}

func (f *fakeDecommReader) GetLatestClusterDecommissionByCluster(_ context.Context, _ uuid.UUID) (sqlc.ClusterDecommission, error) {
	return f.row, f.err
}

// TestDesiredStateTombstone_WithheldWhileDecommissioning asserts M14: a
// pending/running decommission row makes the provider return an ERROR and
// render NOTHING — the agent's applyResponse logs-and-skips on msg.Error so a
// restarted pull agent can't re-apply mid-teardown.
func TestDesiredStateTombstone_WithheldWhileDecommissioning(t *testing.T) {
	id := uuid.New()
	for _, status := range []string{"pending", "running"} {
		renderer := &fakeManifestRenderer{manifest: fakeAgentManifest}
		a := &DesiredStateAdapter{
			renderer: renderer,
			decomm:   &fakeDecommReader{row: sqlc.ClusterDecommission{Status: status}},
		}
		resp, err := a.DesiredState(context.Background(), id.String(), "")
		if err == nil {
			t.Fatalf("status=%s: expected desired state withheld error, got nil", status)
		}
		if len(resp.Manifests) != 0 {
			t.Errorf("status=%s: expected no manifests, got %d", status, len(resp.Manifests))
		}
		if renderer.calls != 0 {
			t.Errorf("status=%s: renderer must not be called when withholding", status)
		}
	}
}

// TestDesiredStateTombstone_RendersWhenHealthy asserts the gate fires ONLY for
// an in-flight decommission: a succeeded row, or no row at all (pgx.ErrNoRows),
// renders normally.
func TestDesiredStateTombstone_RendersWhenHealthy(t *testing.T) {
	id := uuid.New()

	cases := []struct {
		name   string
		reader *fakeDecommReader
	}{
		{"no_row", &fakeDecommReader{err: errors.New("no rows in result set")}},
		{"succeeded", &fakeDecommReader{row: sqlc.ClusterDecommission{Status: "succeeded"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			renderer := &fakeManifestRenderer{manifest: fakeAgentManifest}
			a := &DesiredStateAdapter{renderer: renderer, decomm: tc.reader}
			resp, err := a.DesiredState(context.Background(), id.String(), "")
			if err != nil {
				t.Fatalf("expected normal render, got error: %v", err)
			}
			if len(resp.Manifests) == 0 {
				t.Errorf("expected manifests to render for a healthy cluster")
			}
			if renderer.calls != 1 {
				t.Errorf("renderer should be called once, got %d", renderer.calls)
			}
		})
	}
}

// TestDesiredStateTombstone_NilReaderRendersNormally ensures back-compat when
// no decommission reader is wired (defensive nil-guard).
func TestDesiredStateTombstone_NilReaderRendersNormally(t *testing.T) {
	renderer := &fakeManifestRenderer{manifest: fakeAgentManifest}
	a := &DesiredStateAdapter{renderer: renderer, decomm: nil}
	if _, err := a.DesiredState(context.Background(), uuid.New().String(), ""); err != nil {
		t.Fatalf("nil decomm reader should render normally, got %v", err)
	}
}
