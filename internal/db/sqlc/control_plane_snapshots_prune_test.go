package sqlc

import (
	"strings"
	"testing"
)

// TestPruneControlPlaneSnapshotsKeepSetScopedToTerminal pins the retention
// keep-set to terminal (succeeded/failed) rows only. There is no live-Postgres
// unit harness in this repo, so this test locks the generated statement so the
// bug cannot silently regress.
//
// Bug: the keep-set subquery ("newest $2 rows") originally selected ANY status,
// so an in-flight (pending/running) row — always the newest, since the sweep
// creates a row then immediately prunes — consumed a retention slot and pushed
// the oldest terminal snapshot out of the keep-set. With retention=7 and one
// running row plus 7 succeeded rows, only 6 terminal snapshots survived. Scoping
// the keep-set to terminal rows means in-flight rows never occupy a slot.
func TestPruneControlPlaneSnapshotsKeepSetScopedToTerminal(t *testing.T) {
	sql := pruneControlPlaneSnapshots

	// The keep-set subquery must restrict to terminal rows so in-flight
	// (pending/running) snapshots never consume a retention slot.
	sub := sql[strings.Index(sql, "NOT IN"):]
	if !strings.Contains(sub, "s.status IN ('succeeded', 'failed')") {
		t.Fatalf("PruneControlPlaneSnapshots keep-set subquery must be scoped to terminal rows via s.status IN ('succeeded', 'failed'):\n%s", sql)
	}

	// The status filter must appear alongside the ORDER BY / LIMIT that define
	// the "newest $2" window — i.e. inside the subquery, before LIMIT $2.
	orderIdx := strings.Index(sub, "ORDER BY s.created_at DESC")
	statusIdx := strings.Index(sub, "s.status IN ('succeeded', 'failed')")
	if orderIdx < 0 || statusIdx < 0 || statusIdx > orderIdx {
		t.Fatalf("terminal-status filter must be applied before ORDER BY/LIMIT inside the keep-set subquery:\n%s", sql)
	}

	// Sanity: the outer delete still only ever touches terminal rows.
	if !strings.Contains(sql, "c.status IN ('succeeded', 'failed')") {
		t.Fatalf("PruneControlPlaneSnapshots outer delete must remain scoped to terminal rows:\n%s", sql)
	}
}
