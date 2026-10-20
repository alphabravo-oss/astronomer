package delivery

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestPendingSystemReleaseIsNewestDraftAboveCurrent(t *testing.T) {
	t.Parallel()
	row := func(seq int64, state string) sqlc.ListDeliverySystemReleasesRow {
		return sqlc.ListDeliverySystemReleasesRow{ID: uuid.New(), ReleaseSequence: seq, State: state}
	}
	newest, stale := row(5, "draft"), row(2, "draft")
	releases := []sqlc.ListDeliverySystemReleasesRow{newest, row(4, "draft"), row(3, "released"), stale}
	if got := pendingSystemRelease(releases); got == nil || got.ID != newest.ID {
		t.Fatalf("pending = %+v, want sequence 5", got)
	}
	if got := currentSystemRelease(releases); got == nil || got.ReleaseSequence != 3 {
		t.Fatalf("current = %+v", got)
	}
	if got := pendingSystemRelease([]sqlc.ListDeliverySystemReleasesRow{row(3, "released"), stale}); got != nil {
		t.Fatalf("draft older than the current release reported pending: %+v", got)
	}
	if got := pendingSystemRelease([]sqlc.ListDeliverySystemReleasesRow{stale}); got == nil || got.ID != stale.ID {
		t.Fatal("first draft on a fresh install not reported pending")
	}
}
