package registration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// casFakeQuerier extends the in-memory fakeQuerier with the optional
// compare-and-swap phase writer. beforeCAS, if set, runs once immediately
// before the swap is evaluated — letting a test interleave a concurrent
// transition to reproduce the lost-update race.
type casFakeQuerier struct {
	*fakeQuerier
	beforeCAS func()
}

func (f *casFakeQuerier) UpdateClusterRegistrationPhaseCAS(_ context.Context, id uuid.UUID, expectedPhase, nextPhase string, startedAt, completedAt pgtype.Timestamptz) (sqlc.UpdateClusterRegistrationPhaseRow, error) {
	if f.beforeCAS != nil {
		hook := f.beforeCAS
		f.beforeCAS = nil
		hook() // acquires f.mu itself; must run before we lock below
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.clusters[id]
	if !ok {
		return sqlc.UpdateClusterRegistrationPhaseRow{}, pgx.ErrNoRows
	}
	if r.RegistrationPhase != expectedPhase {
		// CAS miss: the row moved off the phase the caller read.
		return sqlc.UpdateClusterRegistrationPhaseRow{}, pgx.ErrNoRows
	}
	r.RegistrationPhase = nextPhase
	if !r.RegistrationStartedAt.Valid && startedAt.Valid {
		r.RegistrationStartedAt = startedAt
	}
	r.RegistrationCompletedAt = completedAt
	return sqlc.UpdateClusterRegistrationPhaseRow{
		ID:                      r.ID,
		RegistrationPhase:       r.RegistrationPhase,
		RegistrationStartedAt:   r.RegistrationStartedAt,
		RegistrationCompletedAt: r.RegistrationCompletedAt,
		InstallBaseline:         r.InstallBaseline,
	}, nil
}

// TestAdvance_CASRejectsLostUpdate reproduces the phase-machine lost-update
// race: an operator Cancel (provisioning→failed) races the apply worker's
// success event (provisioning→ready). Both callers read phase='provisioning';
// the apply worker commits first. With the compare-and-swap write, the Cancel
// caller's conditional UPDATE matches zero rows and is rejected as an illegal
// transition instead of clobbering 'ready' back to 'failed' and firing
// terminal side-effects for the wrong outcome.
func TestAdvance_CASRejectsLostUpdate(t *testing.T) {
	ctx := context.Background()
	base := newFakeQuerier()
	q := &casFakeQuerier{fakeQuerier: base}
	id := uuid.New()
	yes := true
	base.seed(id, PhaseProvisioning, &yes)
	svc := New(q, nil)

	// The apply worker (racer B) commits provisioning→ready in the window
	// between the Cancel handler's read and its CAS write.
	q.beforeCAS = func() {
		base.mu.Lock()
		base.clusters[id].RegistrationPhase = string(PhaseReady)
		base.mu.Unlock()
	}

	_, err := svc.Advance(ctx, id, EventCancel, WithError("operator cancelled"))
	if err == nil {
		t.Fatalf("expected the losing Cancel to be rejected, got nil error")
	}
	if !isIllegalTransition(err) {
		t.Fatalf("expected ErrIllegalTransition on CAS miss, got %v", err)
	}

	rec, _ := q.GetClusterRegistrationRecord(ctx, id)
	if rec.RegistrationPhase != string(PhaseReady) {
		t.Fatalf("phase = %q, want %q (the apply-success winner must not be clobbered to failed)",
			rec.RegistrationPhase, PhaseReady)
	}
	// The losing Cancel must not have written its side-effect 'cancelled' step.
	base.mu.Lock()
	defer base.mu.Unlock()
	for _, s := range base.steps {
		if s.StepName == "cancelled" {
			t.Fatalf("a 'cancelled' step was written for the losing Cancel; side-effects fired for the wrong outcome")
		}
	}
}

// TestAdvance_CASAllowsLegitTransition confirms the CAS path is not
// over-eager: a normal, uncontended transition still commits and returns the
// updated record.
func TestAdvance_CASAllowsLegitTransition(t *testing.T) {
	ctx := context.Background()
	base := newFakeQuerier()
	q := &casFakeQuerier{fakeQuerier: base}
	id := uuid.New()
	yes := true
	base.seed(id, PhaseProvisioning, &yes)
	svc := New(q, nil)

	rec, err := svc.Advance(ctx, id, EventTemplateApplied)
	if err != nil {
		t.Fatalf("legit provisioning→ready transition failed: %v", err)
	}
	if rec.RegistrationPhase != string(PhaseReady) {
		t.Fatalf("phase = %q, want %q", rec.RegistrationPhase, PhaseReady)
	}
}

func isIllegalTransition(err error) bool {
	return errors.Is(err, ErrIllegalTransition)
}
