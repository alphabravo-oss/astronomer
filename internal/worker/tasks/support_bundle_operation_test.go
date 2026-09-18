package tasks

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type supportBundleGeneratorFunc func(context.Context, io.Writer) error

func (fn supportBundleGeneratorFunc) Generate(ctx context.Context, output io.Writer) error {
	return fn(ctx, output)
}

type supportBundleTaskStore struct {
	row       sqlc.SupportBundleOperation
	succeeded *sqlc.MarkSupportBundleOperationSucceededParams
	retrying  string
	failed    string
	recovered time.Time
}

func (s *supportBundleTaskStore) RecoverSupportBundleOperationOutbox(_ context.Context, staleBefore time.Time) (int64, error) {
	s.recovered = staleBefore
	return 1, nil
}

func (s *supportBundleTaskStore) ClaimSupportBundleOperation(context.Context, sqlc.ClaimSupportBundleOperationParams) (sqlc.SupportBundleOperation, error) {
	if s.row.Status == "succeeded" {
		return sqlc.SupportBundleOperation{}, pgx.ErrNoRows
	}
	s.row.Status = "running"
	s.row.AttemptCount++
	return s.row, nil
}

func (s *supportBundleTaskStore) GetSupportBundleOperation(context.Context, uuid.UUID) (sqlc.GetSupportBundleOperationRow, error) {
	return sqlc.GetSupportBundleOperationRow{ID: s.row.ID, Status: s.row.Status, ExpiresAt: s.row.ExpiresAt}, nil
}

func (s *supportBundleTaskStore) MarkSupportBundleOperationSucceeded(_ context.Context, arg sqlc.MarkSupportBundleOperationSucceededParams) (sqlc.SupportBundleOperation, error) {
	s.succeeded = &arg
	s.row.Status = "succeeded"
	return s.row, nil
}

func (s *supportBundleTaskStore) MarkSupportBundleOperationRetrying(_ context.Context, arg sqlc.MarkSupportBundleOperationRetryingParams) (sqlc.SupportBundleOperation, error) {
	s.retrying = arg.ErrorCode
	s.row.Status = "retrying"
	return s.row, nil
}

func (s *supportBundleTaskStore) MarkSupportBundleOperationFailed(_ context.Context, arg sqlc.MarkSupportBundleOperationFailedParams) (sqlc.SupportBundleOperation, error) {
	s.failed = arg.ErrorCode
	s.row.Status = "failed"
	return s.row, nil
}

func TestSupportBundleRuntimePersistsBoundedArtifactAndIsIdempotent(t *testing.T) {
	id := uuid.New()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	store := &supportBundleTaskStore{row: sqlc.SupportBundleOperation{ID: id, Status: "pending", ExpiresAt: now.Add(time.Hour)}}
	calls := 0
	runtime := SupportBundleRuntime{
		Queries: store,
		Generator: supportBundleGeneratorFunc(func(_ context.Context, output io.Writer) error {
			calls++
			_, err := output.Write([]byte("zip-content"))
			return err
		}),
		Now: func() time.Time { return now },
	}
	if err := runtime.generate(context.Background(), id); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if calls != 1 || store.succeeded == nil {
		t.Fatalf("calls=%d succeeded=%v", calls, store.succeeded != nil)
	}
	wantDigest := sha256.Sum256([]byte("zip-content"))
	if got := store.succeeded.ArtifactSha256.String; got != fmt.Sprintf("%x", wantDigest[:]) {
		t.Fatalf("sha256=%q", got)
	}
	if string(store.succeeded.Artifact) != "zip-content" {
		t.Fatalf("artifact=%q", store.succeeded.Artifact)
	}
	if err := runtime.generate(context.Background(), id); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if calls != 1 {
		t.Fatalf("idempotent replay generated artifact again: calls=%d", calls)
	}
}

func TestSupportBundleRuntimeMarksDeadlineForRetry(t *testing.T) {
	id := uuid.New()
	store := &supportBundleTaskStore{row: sqlc.SupportBundleOperation{ID: id, Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}}
	runtime := SupportBundleRuntime{
		Queries: store,
		Generator: supportBundleGeneratorFunc(func(ctx context.Context, _ io.Writer) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		GenerationTimeout: 20 * time.Millisecond,
	}
	err := runtime.generate(context.Background(), id)
	if err == nil || store.retrying != "generation_timeout" {
		t.Fatalf("err=%v retrying=%q", err, store.retrying)
	}
}

func TestBoundedSupportBundleArtifactRejectsOverflow(t *testing.T) {
	buffer := &boundedBuffer{limit: 4}
	n, err := buffer.Write([]byte("abcdef"))
	if n != 4 || !errors.Is(err, errSupportBundleArtifactTooLarge) || buffer.String() != "abcd" {
		t.Fatalf("write=(%d,%v), artifact=%q", n, err, buffer.String())
	}
}

func TestNewSupportBundleOperationTaskRejectsNilID(t *testing.T) {
	if _, err := NewSupportBundleOperationTask(uuid.Nil); err == nil {
		t.Fatal("expected nil operation ID to fail")
	}
}

func TestSupportBundleRecoveryRequeuesStaleDeliveredIntent(t *testing.T) {
	now := time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC)
	store := &supportBundleTaskStore{}
	runtime := SupportBundleRuntime{
		Queries: store,
		Generator: supportBundleGeneratorFunc(func(context.Context, io.Writer) error {
			return nil
		}),
		Now: func() time.Time { return now },
	}
	if err := runtime.HandleRecovery(context.Background(), NewSupportBundleRecoveryTask()); err != nil {
		t.Fatalf("HandleRecovery: %v", err)
	}
	if want := now.Add(-2 * time.Minute); !store.recovered.Equal(want) {
		t.Fatalf("stale_before=%s want=%s", store.recovered, want)
	}
}
