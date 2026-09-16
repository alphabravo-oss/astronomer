package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/platformsettings"
)

type inactiveUserRetentionFake struct {
	RuntimeQuerier
	calls      int
	arg        sqlc.DeactivateInactiveUsersParams
	rows       []sqlc.DeactivateInactiveUsersRow
	err        error
	setting    sqlc.PlatformSetting
	settingErr error
}

func (f *inactiveUserRetentionFake) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	if f.settingErr != nil {
		return sqlc.PlatformSetting{}, f.settingErr
	}
	if f.setting.Value == nil {
		return sqlc.PlatformSetting{}, pgx.ErrNoRows
	}
	return f.setting, nil
}

func (f *inactiveUserRetentionFake) DeactivateInactiveUsers(_ context.Context, arg sqlc.DeactivateInactiveUsersParams) ([]sqlc.DeactivateInactiveUsersRow, error) {
	f.calls++
	f.arg = arg
	return f.rows, f.err
}

func TestInactiveUserRetentionUsesConfiguredWindow(t *testing.T) {
	fake := &inactiveUserRetentionFake{setting: sqlc.PlatformSetting{Value: json.RawMessage("120")}, rows: []sqlc.DeactivateInactiveUsersRow{{
		ID: uuid.New(), Username: "inactive-user", Email: "inactive@example.test",
	}}}
	ctx := testRuntimeContext(RuntimeDependencies{
		Queries: fake,
	})

	before := time.Now().UTC()
	if err := HandleInactiveUserRetention(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	after := time.Now().UTC()
	if fake.calls != 1 {
		t.Fatalf("deactivate calls = %d, want 1", fake.calls)
	}
	if fake.arg.RetentionDays != 120 {
		t.Fatalf("retention days = %d, want 120", fake.arg.RetentionDays)
	}
	if fake.arg.InvalidatedAt.Before(before) || fake.arg.InvalidatedAt.After(after) {
		t.Fatalf("invalidated_at %s outside [%s, %s]", fake.arg.InvalidatedAt, before, after)
	}
	wantCutoff := fake.arg.InvalidatedAt.Add(-120 * 24 * time.Hour)
	if !fake.arg.Cutoff.Equal(wantCutoff) {
		t.Fatalf("cutoff = %s, want %s", fake.arg.Cutoff, wantCutoff)
	}
}

func TestInactiveUserRetentionDefaultsSecurely(t *testing.T) {
	fake := &inactiveUserRetentionFake{}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: fake})
	if err := HandleInactiveUserRetention(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if fake.arg.RetentionDays != platformsettings.DefaultInactiveUserRetentionDays {
		t.Fatalf("retention days = %d, want default %d", fake.arg.RetentionDays, platformsettings.DefaultInactiveUserRetentionDays)
	}
}

func TestInactiveUserRetentionSkipsNonLeader(t *testing.T) {
	fake := &inactiveUserRetentionFake{}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: fake, Leader: &fakeLeader{held: false}})
	if err := HandleInactiveUserRetention(ctx, &asynq.Task{}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if fake.calls != 0 {
		t.Fatalf("non-leader deactivate calls = %d, want 0", fake.calls)
	}
}

func TestInactiveUserRetentionPropagatesAtomicQueryFailure(t *testing.T) {
	want := errors.New("audit partition unavailable")
	fake := &inactiveUserRetentionFake{err: want}
	err := HandleInactiveUserRetention(testRuntimeContext(RuntimeDependencies{Queries: fake}), &asynq.Task{})
	if !errors.Is(err, want) {
		t.Fatalf("handle error = %v, want wrapped %v", err, want)
	}
}

func TestInactiveUserRetentionOperatorSetting(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"30", true}, {"0", false}, {"3651", false}, {"1.5", false}, {`"30"`, false}, {"null", false}} {
		t.Run(tc.value, func(t *testing.T) {
			fake := &inactiveUserRetentionFake{setting: sqlc.PlatformSetting{Value: json.RawMessage(tc.value)}}
			err := HandleInactiveUserRetention(testRuntimeContext(RuntimeDependencies{Queries: fake}), &asynq.Task{})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid && fake.calls != 0 {
				t.Fatal("invalid setting deactivated users")
			}
			if tc.valid && fake.arg.RetentionDays != 30 {
				t.Fatalf("days=%d", fake.arg.RetentionDays)
			}
		})
	}
}

func TestInactiveUserRetentionSettingsFailureDoesNotDeactivate(t *testing.T) {
	fake := &inactiveUserRetentionFake{settingErr: errors.New("db unavailable")}
	if err := HandleInactiveUserRetention(testRuntimeContext(RuntimeDependencies{Queries: fake}), &asynq.Task{}); err == nil || fake.calls != 0 {
		t.Fatalf("error=%v calls=%d", err, fake.calls)
	}
}
