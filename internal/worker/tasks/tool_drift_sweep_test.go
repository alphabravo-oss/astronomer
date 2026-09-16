package tasks

import (
	"context"
	"errors"
	"strings"
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
	charts      []sqlc.InstalledChart
	marks       map[uuid.UUID]sqlc.MarkInstalledChartDriftParams
	releases    map[uuid.UUID]sqlc.ReleaseInstalledChartDriftClaimParams
	claim       sqlc.ClaimInstalledChartsForDriftSweepParams
	markRows    int64
	releaseRows int64
	markErr     error
	releaseErr  error
}

func (f *fakeDriftQuerier) ClaimInstalledChartsForDriftSweep(_ context.Context, arg sqlc.ClaimInstalledChartsForDriftSweepParams) ([]sqlc.InstalledChart, error) {
	f.claim = arg
	return f.charts, nil
}

func (f *fakeDriftQuerier) ReleaseInstalledChartDriftClaim(_ context.Context, arg sqlc.ReleaseInstalledChartDriftClaimParams) (int64, error) {
	if f.releases == nil {
		f.releases = map[uuid.UUID]sqlc.ReleaseInstalledChartDriftClaimParams{}
	}
	f.releases[arg.ID] = arg
	rows := f.releaseRows
	if rows == 0 && f.releaseErr == nil {
		rows = 1
	}
	return rows, f.releaseErr
}

func (f *fakeDriftQuerier) MarkInstalledChartDrift(_ context.Context, arg sqlc.MarkInstalledChartDriftParams) (int64, error) {
	if f.marks == nil {
		f.marks = map[uuid.UUID]sqlc.MarkInstalledChartDriftParams{}
	}
	f.marks[arg.ID] = arg
	rows := f.markRows
	if rows == 0 && f.markErr == nil {
		rows = 1
	}
	return rows, f.markErr
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
				if _, released := q.releases[id]; !released {
					t.Fatal("expected transient failure to release the durable claim")
				}
				if !q.releases[id].ClaimToken.Valid || q.releases[id].ClaimToken != q.claim.ClaimToken {
					t.Fatal("expected release to be fenced by the claim token")
				}
				return
			}
			if q.claim.QueryLimit != toolDriftSweepBatch || !q.claim.LockedUntil.Valid || !q.claim.ClaimToken.Valid {
				t.Fatalf("claim = %+v, want bounded durable lease", q.claim)
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
			if mark.ClaimToken != q.claim.ClaimToken {
				t.Fatal("expected completion to be fenced by the claim token")
			}
		})
	}
}

func TestRunToolDriftSweepReturnsPersistenceFailure(t *testing.T) {
	id := uuid.New()
	q := &fakeDriftQuerier{
		charts:  []sqlc.InstalledChart{{ID: id, ReleaseName: "rel", Namespace: "ns"}},
		markErr: errors.New("database unavailable"),
	}
	err := runToolDriftSweep(context.Background(), ToolDriftSweepDeps{
		Queries: q,
		Helm:    &fakeDriftHelm{status: &protocol.HelmResultPayload{Status: "deployed"}},
	})
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("runToolDriftSweep error = %v, want persistence failure", err)
	}
}
