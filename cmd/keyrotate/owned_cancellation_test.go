package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// The PID comes only from this exclusive DB, application name, and fixture's
// exact advisory wait. Never cancel an arbitrary pooled or shared backend.
func blockedRotationPID(t *testing.T, ctx context.Context, db *sql.DB) int {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT a.pid FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.datname=current_database() AND a.application_name='plan032-w16-rotation' AND a.pid<>pg_backend_pid() AND a.state='active' AND l.locktype='advisory' AND NOT l.granted AND l.objid=$1 AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database())`, rotationFixtureLock)
	if err != nil {
		t.Fatal("read exclusively owned blocked rotation backend")
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) != nil {
			t.Fatal("read blocked backend identity")
		}
		ids = append(ids, id)
	}
	if rows.Err() != nil || len(ids) != 1 {
		t.Fatal("exactly one exclusively owned blocked rotation backend required")
	}
	return ids[0]
}

func rotationBackendActive(t *testing.T, ctx context.Context, db *sql.DB, pid int) bool {
	t.Helper()
	var active bool
	// A returned pooled connection can later execute the observer itself or a
	// different query. Observe this exact UPDATE, excluding the observer PID.
	statement := casUpdateSQL(target{"smtp_settings", "id", "password_encrypted"})
	if db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND pid<>pg_backend_pid() AND datname=current_database() AND application_name='plan032-w16-rotation' AND state='active' AND query=$2)`, pid, statement).Scan(&active) != nil {
		t.Fatal("read owned backend statement lifecycle")
	}
	return active
}

func awaitRotationBackendCompletion(t *testing.T, ctx context.Context, db *sql.DB, pid int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for rotationBackendActive(t, ctx, db, pid) {
		select {
		case <-deadline.C:
			t.Fatal("owned server statement did not finish within fixture bound")
		case <-ctx.Done():
			t.Fatal("owned lifecycle fixture expired")
		case <-tick.C:
		}
	}
}

func characterizeRotationCancellation(t *testing.T, ctx context.Context, db *sql.DB, serverCancel bool) {
	t.Helper()
	keys := syntheticRotationKeys(t)
	ids, _ := seedRotationRows(t, ctx, db, keys)
	unlock := pauseRotationRow(t, ctx, db, ids[1])
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan stats, 1)
	go func() { result <- rotateFixture(run, db, keys, false) }()
	waitRotationBlocked(t, ctx, db)
	pid := blockedRotationPID(t, ctx, db)
	if !keys.primary.IsPrimaryCiphertext(fixtureCiphertext(t, ctx, db, ids[0])) {
		t.Fatal("first acknowledged CAS did not commit before interruption")
	}
	if serverCancel {
		var cancelled bool
		if db.QueryRowContext(ctx, `SELECT pg_cancel_backend($1)`, pid).Scan(&cancelled) != nil || !cancelled {
			t.Fatal("cancel exclusively owned blocked statement")
		}
	} else {
		cancel()
	}
	s := rotationResult(t, result)
	// A cancelled context can fail both the blocked UPDATE and the subsequent
	// page SELECT. The contract counts failed operations, not cancellations.
	if s.scanned != 2 || s.rewrote != 1 || s.failed == 0 {
		t.Fatalf("interrupted progress=%+v", s)
	}
	stillActive := rotationBackendActive(t, ctx, db, pid)
	if serverCancel {
		// Server cancellation must finish the statement before opening its gate.
		awaitRotationBackendCompletion(t, ctx, db, pid)
	}
	unlock()
	awaitRotationBackendCompletion(t, ctx, db, pid)
	// Client cancellation can leave an autocommit outcome unknown. Derive the
	// exact resume expectation only after observing server completion and the
	// persisted envelope, rather than accepting either progress count blindly.
	persisted := fixtureCiphertext(t, ctx, db, ids[1])
	current := keys.primary.IsPrimaryCiphertext(persisted)
	value, err := keys.mixed.Decrypt(persisted)
	if err != nil || value != "synthetic-row-b" {
		t.Fatal("interrupted statement corrupted persisted credential")
	}
	if !current && !strings.HasPrefix(persisted, "astronomer:v1:fernet:old:") {
		t.Fatal("interrupted statement produced unsupported envelope")
	}
	if serverCancel && current {
		t.Fatal("server-cancelled UPDATE committed despite completed abort")
	}
	wantRewrite, wantSkip := 1, 1
	if current {
		wantRewrite, wantSkip = 0, 2
	}
	if s := rotateFixture(ctx, db, keys, false); s.failed != 0 || s.rewrote != wantRewrite || s.skipped != wantSkip || s.scanned != 2 {
		t.Fatalf("resume disagrees with measured persisted state: %+v", s)
	}
	for _, id := range ids {
		ct := fixtureCiphertext(t, ctx, db, id)
		if !keys.primary.IsPrimaryCiphertext(ct) {
			t.Fatal("resumed row lacks current envelope")
		}
		if _, err := keys.primary.Decrypt(ct); err != nil {
			t.Fatal("resumed current row cannot decrypt")
		}
		if _, err := keys.old.Decrypt(ct); err == nil {
			t.Fatal("old-only key unexpectedly reads resumed row")
		}
	}
	t.Logf("server_cancel=%t backend_pid=%d active_after_client_result=%t committed_after_server_completion=%t expected_resume_rewrites=%d expected_resume_skips=%d", serverCancel, pid, stillActive, current, wantRewrite, wantSkip)
	// Release the gate function and rows before the next independently seeded
	// scenario. Registered bounded cleanups remain harmless and idempotent.
	if _, err := db.ExecContext(ctx, `DROP TRIGGER plan032_rotation_pause ON smtp_settings; DROP FUNCTION plan032_rotation_pause()`); err != nil {
		t.Fatal("remove completed owned cancellation gate")
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM smtp_settings WHERE id IN ($1,$2)`, ids[0], ids[1]); err != nil {
		t.Fatal("remove completed owned cancellation scenario")
	}
}
