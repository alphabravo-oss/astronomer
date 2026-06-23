package tasks

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeDriftHelm returns a canned status (and/or error) for every release.
type fakeDriftHelm struct {
	status *protocol.HelmResultPayload
	err    error
}

func (f *fakeDriftHelm) Status(_ context.Context, _, _, _ string) (*protocol.HelmResultPayload, error) {
	return f.status, f.err
}

// fakeDriftQuerier records the drift marks the sweep writes.
type fakeDriftQuerier struct {
	charts []sqlc.InstalledChart
	marks  map[uuid.UUID]sqlc.MarkInstalledChartDriftParams
}

func (f *fakeDriftQuerier) ListInstalledChartsForDriftSweep(_ context.Context, _ int32) ([]sqlc.InstalledChart, error) {
	return f.charts, nil
}

func (f *fakeDriftQuerier) MarkInstalledChartDrift(_ context.Context, arg sqlc.MarkInstalledChartDriftParams) error {
	if f.marks == nil {
		f.marks = map[uuid.UUID]sqlc.MarkInstalledChartDriftParams{}
	}
	f.marks[arg.ID] = arg
	return nil
}

func TestRunToolDriftSweep(t *testing.T) {
	id := uuid.New()
	chart := sqlc.InstalledChart{ID: id, ReleaseName: "rel", Namespace: "ns", Revision: 3}

	cases := []struct {
		name        string
		helm        HelmStatusProber
		wantDrift   bool
		wantDetailN bool // detail must be non-empty when drift
		wantNoWrite bool // transient probe failure must skip the write entirely
	}{
		{
			name:      "converged",
			helm:      &fakeDriftHelm{status: &protocol.HelmResultPayload{Status: "deployed", Revision: 3}},
			wantDrift: false,
		},
		{
			name:        "revision ahead",
			helm:        &fakeDriftHelm{status: &protocol.HelmResultPayload{Status: "deployed", Revision: 5}},
			wantDrift:   true,
			wantDetailN: true,
		},
		{
			name:        "status not deployed",
			helm:        &fakeDriftHelm{status: &protocol.HelmResultPayload{Status: "failed", Revision: 3}},
			wantDrift:   true,
			wantDetailN: true,
		},
		{
			name:        "release missing",
			helm:        &fakeDriftHelm{err: errors.New("Error: release: not found")},
			wantDrift:   true,
			wantDetailN: true,
		},
		{
			// Transient probe failure must NOT overwrite the prior drift
			// state: the sweep skips the write so a genuine drift signal
			// isn't erased on a one-off blip.
			name:        "transient probe error",
			helm:        &fakeDriftHelm{err: errors.New("cluster agent not connected")},
			wantNoWrite: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeDriftQuerier{charts: []sqlc.InstalledChart{chart}}
			if err := runToolDriftSweep(context.Background(), ToolDriftSweepDeps{Queries: q, Helm: tc.helm}); err != nil {
				t.Fatalf("runToolDriftSweep: %v", err)
			}
			mark, ok := q.marks[id]
			if tc.wantNoWrite {
				if ok {
					t.Fatalf("expected MarkInstalledChartDrift NOT to be called on transient probe failure, got %+v", mark)
				}
				return
			}
			if !ok {
				t.Fatal("expected MarkInstalledChartDrift to be called")
			}
			if mark.DriftDetected != tc.wantDrift {
				t.Fatalf("drift detected = %v, want %v (detail=%q)", mark.DriftDetected, tc.wantDrift, mark.DriftDetail)
			}
			if tc.wantDetailN && mark.DriftDetail == "" {
				t.Fatal("expected non-empty drift detail when drift detected")
			}
			if !tc.wantDrift && mark.DriftDetail != "" {
				t.Fatalf("expected empty drift detail when no drift, got %q", mark.DriftDetail)
			}
		})
	}
}
