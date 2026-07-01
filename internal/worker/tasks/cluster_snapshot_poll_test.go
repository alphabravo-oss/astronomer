// Worker tests for the cluster snapshot lifecycle tasks (migration 052).
//
// These tests stand the workers up against an in-memory fake querier +
// driver so the poll / dispatch / cleanup paths can be exercised
// without a Postgres or a real Velero install.

package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakePollQuerier is the in-memory implementation of
// ClusterSnapshotPollQuerier used by the worker tests.
type fakePollQuerier struct {
	mu        sync.Mutex
	snapshots map[uuid.UUID]sqlc.ClusterSnapshot
	restores  map[uuid.UUID]sqlc.ClusterRestore
	schedules map[uuid.UUID]sqlc.ClusterSnapshotSchedule
}

func newFakePollQuerier() *fakePollQuerier {
	return &fakePollQuerier{
		snapshots: map[uuid.UUID]sqlc.ClusterSnapshot{},
		restores:  map[uuid.UUID]sqlc.ClusterRestore{},
		schedules: map[uuid.UUID]sqlc.ClusterSnapshotSchedule{},
	}
}

func (f *fakePollQuerier) ListPendingClusterSnapshots(_ context.Context, lim int32) ([]sqlc.ClusterSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.ClusterSnapshot{}
	for _, r := range f.snapshots {
		if r.Phase == "New" || r.Phase == "InProgress" {
			out = append(out, r)
			if int32(len(out)) >= lim {
				break
			}
		}
	}
	return out, nil
}

func (f *fakePollQuerier) MarkSnapshotPhase(_ context.Context, arg sqlc.MarkSnapshotPhaseParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.snapshots[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	row.Phase = arg.Phase
	row.StartTime = arg.StartTime
	row.CompletionTime = arg.CompletionTime
	row.WarningsCount = arg.WarningsCount
	row.ErrorsCount = arg.ErrorsCount
	row.LastPollError = arg.LastPollError
	row.LastPollAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	f.snapshots[arg.ID] = row
	return nil
}

func (f *fakePollQuerier) ListExpiredTerminalSnapshots(_ context.Context, lim int32) ([]sqlc.ClusterSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.ClusterSnapshot{}
	now := time.Now()
	for _, r := range f.snapshots {
		if !r.ExpiresAt.Valid || r.ExpiresAt.Time.After(now) {
			continue
		}
		if _, term := terminalSnapshotPhases[r.Phase]; !term {
			continue
		}
		out = append(out, r)
		if int32(len(out)) >= lim {
			break
		}
	}
	return out, nil
}

func (f *fakePollQuerier) DeleteClusterSnapshot(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.snapshots, id)
	return nil
}

func (f *fakePollQuerier) ListPendingClusterRestores(_ context.Context, lim int32) ([]sqlc.ClusterRestore, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.ClusterRestore{}
	for _, r := range f.restores {
		if r.Phase == "New" || r.Phase == "InProgress" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakePollQuerier) MarkRestorePhase(_ context.Context, arg sqlc.MarkRestorePhaseParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.restores[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	row.Phase = arg.Phase
	row.StartTime = arg.StartTime
	row.CompletionTime = arg.CompletionTime
	row.WarningsCount = arg.WarningsCount
	row.ErrorsCount = arg.ErrorsCount
	row.LastPollError = arg.LastPollError
	f.restores[arg.ID] = row
	return nil
}

func (f *fakePollQuerier) GetClusterSnapshotByID(_ context.Context, id uuid.UUID) (sqlc.ClusterSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.snapshots[id]
	if !ok {
		return sqlc.ClusterSnapshot{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakePollQuerier) ListEnabledSnapshotSchedules(_ context.Context) ([]sqlc.ClusterSnapshotSchedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.ClusterSnapshotSchedule{}
	for _, r := range f.schedules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakePollQuerier) MarkSnapshotScheduleRan(_ context.Context, arg sqlc.MarkSnapshotScheduleRanParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.schedules[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	row.LastRunAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	row.LastRunStatus = arg.LastRunStatus
	f.schedules[arg.ID] = row
	return nil
}

func (f *fakePollQuerier) CreateClusterSnapshot(_ context.Context, arg sqlc.CreateClusterSnapshotParams) (sqlc.ClusterSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.ClusterSnapshot{
		ID:              uuid.New(),
		ClusterID:       arg.ClusterID,
		VeleroName:      arg.VeleroName,
		VeleroNamespace: arg.VeleroNamespace,
		Source:          arg.Source,
		Spec:            arg.Spec,
		Phase:           arg.Phase,
		ExpiresAt:       arg.ExpiresAt,
		CreatedBy:       arg.CreatedBy,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	f.snapshots[row.ID] = row
	return row, nil
}

// fakeDriver records every Get / Post call and returns canned status.
type fakeDriver struct {
	mu sync.Mutex

	// Status to return for any GetBackup call (keyed by snapshot name).
	backupStatus map[string]VeleroBackupStatusSnapshot
	// Status to return for GetRestore calls.
	restoreStatus map[string]VeleroRestoreStatusSnapshot
	// Posts collected for assertion.
	posted []map[string]any
	// Optional injected error for posts.
	postErr error
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{
		backupStatus:  map[string]VeleroBackupStatusSnapshot{},
		restoreStatus: map[string]VeleroRestoreStatusSnapshot{},
	}
}

func (d *fakeDriver) GetBackup(_ context.Context, _, _, name string) (VeleroBackupStatusSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.backupStatus[name]; ok {
		return s, nil
	}
	return VeleroBackupStatusSnapshot{NotFound: true}, nil
}

func (d *fakeDriver) GetRestore(_ context.Context, _, _, name string) (VeleroRestoreStatusSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s, ok := d.restoreStatus[name]; ok {
		return s, nil
	}
	return VeleroRestoreStatusSnapshot{NotFound: true}, nil
}

func (d *fakeDriver) PostBackup(_ context.Context, _ string, body map[string]any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.posted = append(d.posted, body)
	return d.postErr
}

// ----------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------

func TestPoller_UpdatesPhase(t *testing.T) {
	q := newFakePollQuerier()
	d := newFakeDriver()
	clusterID := uuid.New()

	row, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
		ClusterID:       clusterID,
		VeleroName:      "snap-1",
		VeleroNamespace: "velero",
		Phase:           "InProgress",
	})

	d.backupStatus["snap-1"] = VeleroBackupStatusSnapshot{
		Phase:          "Completed",
		Warnings:       1,
		Errors:         0,
		StartTime:      time.Now().Add(-time.Hour),
		CompletionTime: time.Now(),
	}

	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{Queries: q, Driver: d})
	defer ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})

	var outcomes []string
	SetSnapshotOutcomeRecorder(func(cid, oc string) { outcomes = append(outcomes, cid+":"+oc) })

	if err := HandleClusterSnapshotPoll(context.Background(), nil); err != nil {
		t.Fatalf("poll: %v", err)
	}

	updated, _ := q.GetClusterSnapshotByID(context.Background(), row.ID)
	if updated.Phase != "Completed" {
		t.Fatalf("expected phase=Completed, got %q", updated.Phase)
	}
	if updated.WarningsCount != 1 {
		t.Fatalf("warnings_count not mirrored: %d", updated.WarningsCount)
	}
	if len(outcomes) != 1 || outcomes[0] != clusterID.String()+":completed" {
		t.Fatalf("expected one outcome recorded; got %+v", outcomes)
	}
}

func TestPoller_MissingCRDMarksDeleted(t *testing.T) {
	q := newFakePollQuerier()
	d := newFakeDriver()
	row, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
		ClusterID:  uuid.New(),
		VeleroName: "missing-snap",
		Phase:      "InProgress",
	})

	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{Queries: q, Driver: d})
	defer ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})
	SetSnapshotOutcomeRecorder(func(_, _ string) {})

	if err := HandleClusterSnapshotPoll(context.Background(), nil); err != nil {
		t.Fatalf("poll: %v", err)
	}
	updated, _ := q.GetClusterSnapshotByID(context.Background(), row.ID)
	if updated.Phase != "Deleted" {
		t.Fatalf("expected phase=Deleted when CRD missing, got %q", updated.Phase)
	}
}

func TestScheduledDispatcher_FiresOnCron(t *testing.T) {
	q := newFakePollQuerier()
	d := newFakeDriver()

	// Schedule with cron "* * * * *" (every minute) and last_run_at = 2m
	// ago. The next-run should fall ≤ now, so the dispatcher fires.
	now := time.Now().UTC()
	last := now.Add(-2 * time.Minute)
	sched := sqlc.ClusterSnapshotSchedule{
		ID:           uuid.New(),
		ClusterID:    uuid.New(),
		Name:         "minutely",
		CronSchedule: "* * * * *",
		Enabled:      true,
		LastRunAt:    pgtype.Timestamptz{Time: last, Valid: true},
		Spec:         json.RawMessage(`{"includedNamespaces":["argocd"],"ttl":"24h"}`),
		CreatedAt:    last,
	}
	q.schedules[sched.ID] = sched

	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{Queries: q, Driver: d})
	defer ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})

	if err := HandleClusterSnapshotDispatchScheduled(context.Background(), nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(d.posted) != 1 {
		t.Fatalf("expected 1 Velero POST after cron fires, got %d", len(d.posted))
	}
	if len(q.snapshots) != 1 {
		t.Fatalf("expected 1 new snapshot row inserted, got %d", len(q.snapshots))
	}

	// Mark-ran callback should have stamped the schedule.
	updated, _ := q.schedules[sched.ID], true
	if updated.LastRunStatus != "fired" {
		t.Fatalf("expected last_run_status=fired, got %q", updated.LastRunStatus)
	}
}

func TestScheduledDispatcher_SkipsWhenNotDue(t *testing.T) {
	q := newFakePollQuerier()
	d := newFakeDriver()

	// Cron "0 0 * * *" = once a day at midnight; last_run_at = 1m ago so
	// next run is 23h+ away.
	now := time.Now().UTC()
	sched := sqlc.ClusterSnapshotSchedule{
		ID:           uuid.New(),
		ClusterID:    uuid.New(),
		Name:         "daily-mn",
		CronSchedule: "0 0 * * *",
		Enabled:      true,
		LastRunAt:    pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true},
		CreatedAt:    now.Add(-time.Hour),
	}
	q.schedules[sched.ID] = sched

	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{Queries: q, Driver: d})
	defer ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})

	if err := HandleClusterSnapshotDispatchScheduled(context.Background(), nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(d.posted) != 0 {
		t.Fatalf("expected no POSTs when schedule isn't due, got %d", len(d.posted))
	}
}

func TestCleanupExpired_DropsTerminalRows(t *testing.T) {
	q := newFakePollQuerier()

	// One terminal+expired row → drop. One terminal+future → keep.
	// One in-progress+expired → keep (poller still owns it).
	expired := sqlc.ClusterSnapshot{
		ID:        uuid.New(),
		Phase:     "Completed",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}
	future := sqlc.ClusterSnapshot{
		ID:        uuid.New(),
		Phase:     "Completed",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}
	inflight := sqlc.ClusterSnapshot{
		ID:        uuid.New(),
		Phase:     "InProgress",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	}
	q.snapshots[expired.ID] = expired
	q.snapshots[future.ID] = future
	q.snapshots[inflight.ID] = inflight

	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{Queries: q})
	defer ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})

	if err := HandleClusterSnapshotCleanupExpired(context.Background(), nil); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, ok := q.snapshots[expired.ID]; ok {
		t.Fatalf("expected expired+terminal row to be deleted")
	}
	if _, ok := q.snapshots[future.ID]; !ok {
		t.Fatalf("expected future-expiry row to be kept")
	}
	if _, ok := q.snapshots[inflight.ID]; !ok {
		t.Fatalf("expected in-progress row to be kept")
	}
}

func TestScheduleIsDue_FirstRunUsesCreatedAt(t *testing.T) {
	// Never-run schedule: parser is fed CreatedAt as the floor, so a
	// 1m-ago createdAt with "* * * * *" should be due.
	now := time.Now().UTC()
	sched := sqlc.ClusterSnapshotSchedule{
		CronSchedule: "* * * * *",
		CreatedAt:    now.Add(-2 * time.Minute),
	}
	due, err := scheduleIsDue(sched, now)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !due {
		t.Fatalf("expected first-time schedule to be due")
	}
}

func TestScheduleIsDue_RejectsMalformedExpr(t *testing.T) {
	sched := sqlc.ClusterSnapshotSchedule{
		CronSchedule: "not-a-cron",
		CreatedAt:    time.Now(),
	}
	_, err := scheduleIsDue(sched, time.Now())
	if err == nil {
		t.Fatalf("expected error for malformed cron")
	}
}

func TestPoller_NilDepsIsNoOp(t *testing.T) {
	// Reset deps so we can verify the no-op path. Save+restore so other
	// tests aren't affected.
	prev := getClusterSnapshotDeps()
	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})
	defer ConfigureClusterSnapshotTasks(prev)

	if err := HandleClusterSnapshotPoll(context.Background(), nil); err != nil {
		t.Fatalf("poll unwired path errored: %v", err)
	}
	if err := HandleClusterSnapshotDispatchScheduled(context.Background(), nil); err != nil {
		t.Fatalf("dispatch unwired path errored: %v", err)
	}
	if err := HandleClusterSnapshotCleanupExpired(context.Background(), nil); err != nil {
		t.Fatalf("cleanup unwired path errored: %v", err)
	}
}

// TestClusterSnapshotHandlers_GatedByLeader is the F04 regression: all three
// periodic handlers must run their bodies through runPeriodicTaskWithLeader so
// that on an N-replica deploy only the leader fires (otherwise N replicas each
// POST a duplicate Velero backup and race on last_run_at). We install a
// non-leader elector and assert none of the three handlers touch the DB/driver.
// If the leader wrapper is removed from any handler, the corresponding
// assertion fails.
func TestClusterSnapshotHandlers_GatedByLeader(t *testing.T) {
	defer resetRuntime()
	ConfigureRuntime(RuntimeDependencies{Leader: &fakeLeader{held: false}})

	q := newFakePollQuerier()
	d := newFakeDriver()

	// Pending snapshot whose Velero CR reports Completed: if poll runs, the
	// row flips to Completed.
	snap, _ := q.CreateClusterSnapshot(context.Background(), sqlc.CreateClusterSnapshotParams{
		ClusterID:  uuid.New(),
		VeleroName: "gated-snap",
		Phase:      "InProgress",
	})
	d.backupStatus["gated-snap"] = VeleroBackupStatusSnapshot{Phase: "Completed"}

	// Due schedule: if dispatch runs, it POSTs a backup.
	now := time.Now().UTC()
	sched := sqlc.ClusterSnapshotSchedule{
		ID:           uuid.New(),
		ClusterID:    uuid.New(),
		Name:         "gated-sched",
		CronSchedule: "* * * * *",
		Enabled:      true,
		LastRunAt:    pgtype.Timestamptz{Time: now.Add(-2 * time.Minute), Valid: true},
		CreatedAt:    now.Add(-2 * time.Minute),
	}
	q.schedules[sched.ID] = sched

	// Expired terminal snapshot: if cleanup runs, it is deleted.
	expired := sqlc.ClusterSnapshot{
		ID:        uuid.New(),
		Phase:     "Completed",
		ExpiresAt: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true},
	}
	q.snapshots[expired.ID] = expired

	ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{Queries: q, Driver: d})
	defer ConfigureClusterSnapshotTasks(ClusterSnapshotDeps{})
	SetSnapshotOutcomeRecorder(func(_, _ string) {})

	if err := HandleClusterSnapshotPoll(context.Background(), nil); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if err := HandleClusterSnapshotDispatchScheduled(context.Background(), nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := HandleClusterSnapshotCleanupExpired(context.Background(), nil); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if got, _ := q.GetClusterSnapshotByID(context.Background(), snap.ID); got.Phase != "InProgress" {
		t.Fatalf("poll ran on non-leader replica: phase=%q, want unchanged InProgress", got.Phase)
	}
	if len(d.posted) != 0 {
		t.Fatalf("dispatch ran on non-leader replica: %d Velero POSTs, want 0", len(d.posted))
	}
	if _, ok := q.snapshots[expired.ID]; !ok {
		t.Fatalf("cleanup ran on non-leader replica: expired row was deleted")
	}
}

func TestScheduleSnapshotName_Sanitized(t *testing.T) {
	if got := scheduleSnapshotName("Daily Argo", "20260512t010203"); got != "daily-argo-20260512t010203" {
		t.Errorf("unexpected name: %q", got)
	}
	// Crafted to force the trimming branch — name > 253 chars.
	long := ""
	for i := 0; i < 30; i++ {
		long += "abcdefghij"
	}
	got := scheduleSnapshotName(long, "stamp")
	if len(got) > 253 {
		t.Fatalf("name not truncated to 253: %d chars", len(got))
	}
}

func TestParseScheduleLabelSelector(t *testing.T) {
	got := parseScheduleLabelSelector(" tier = prod ,env=west, , badtoken , also=")
	if len(got) != 2 || got["tier"] != "prod" || got["env"] != "west" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestOutcomeForPhase(t *testing.T) {
	cases := map[string]string{
		"Completed":        "completed",
		"PartiallyFailed":  "partial",
		"Failed":           "failed",
		"FailedValidation": "failed",
		"Deleted":          "failed",
		"InProgress":       "",
	}
	for in, want := range cases {
		if got := outcomeForPhase(in); got != want {
			t.Errorf("outcomeForPhase(%q)=%q want %q", in, got, want)
		}
	}
}

// fmt is imported elsewhere; this file uses it implicitly via uuid.New +
// string formatting, but the linter may flag an unused import if we
// add it directly. Reference once to keep the import edges clean.
var _ = fmt.Sprintf
