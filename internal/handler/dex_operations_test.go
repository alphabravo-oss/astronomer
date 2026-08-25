package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type dexOperationTxFake struct {
	DexMutationTx
	row        sqlc.DexOperation
	queueCount int
	auditCount int
}

func (tx *dexOperationTxFake) ReserveDexOperation(_ context.Context, arg sqlc.ReserveDexOperationParams) (sqlc.ReserveDexOperationRow, error) {
	if tx.row.ID != uuid.Nil {
		if tx.row.ID != arg.ID || tx.row.RequestDigest != arg.RequestDigest || tx.row.Action != arg.Action {
			return sqlc.ReserveDexOperationRow{}, pgx.ErrNoRows
		}
		return reserveDexOperationRow(tx.row, false), nil
	}
	now := time.Now().UTC()
	tx.row = sqlc.DexOperation{ID: arg.ID, Action: arg.Action, TargetID: arg.TargetID,
		IdempotencyScope: arg.IdempotencyScope, IdempotencyKey: arg.IdempotencyKey,
		RequestDigest: arg.RequestDigest, PayloadEncrypted: arg.PayloadEncrypted,
		Status: "pending", Phase: "queued", CreatedBy: arg.CreatedBy, CreatedAt: now, UpdatedAt: now}
	return reserveDexOperationRow(tx.row, true), nil
}

func (tx *dexOperationTxFake) QueueDexOperation(_ context.Context, arg sqlc.QueueDexOperationParams) (sqlc.QueueDexOperationRow, error) {
	tx.queueCount++
	tx.row.RuntimeGeneration = arg.RuntimeGeneration
	return queueDexOperationRow(tx.row), nil
}

func reserveDexOperationRow(row sqlc.DexOperation, created bool) sqlc.ReserveDexOperationRow {
	return sqlc.ReserveDexOperationRow{
		ID: row.ID, Action: row.Action, TargetID: row.TargetID, RuntimeGeneration: row.RuntimeGeneration,
		IdempotencyScope: row.IdempotencyScope, IdempotencyKey: row.IdempotencyKey,
		RequestDigest: row.RequestDigest, PayloadEncrypted: row.PayloadEncrypted,
		Status: row.Status, Phase: row.Phase, AttemptCount: row.AttemptCount, LockedUntil: row.LockedUntil,
		ErrorCode: row.ErrorCode, CreatedBy: row.CreatedBy, CompletedAt: row.CompletedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Created: created,
	}
}

func queueDexOperationRow(row sqlc.DexOperation) sqlc.QueueDexOperationRow {
	return sqlc.QueueDexOperationRow(row)
}

func (tx *dexOperationTxFake) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	tx.auditCount++
	return sqlc.AuditOutbox{ID: arg.ID}, nil
}

func dexOperationRequest(actor uuid.UUID, key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/apply/", nil)
	req.Header.Set("Idempotency-Key", key)
	return req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: actor.String(), AuthMethod: "jwt"}))
}

func TestDexApplyQueuesDurablyWithoutKubernetesAndReplaysExactlyOnce(t *testing.T) {
	q := newFakeDexQuerier()
	q.settings = &sqlc.DexSetting{ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example.com",
		ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Namespace: "dex", ReleaseName: "dex",
		RuntimeSecretName: "runtime", DeploymentName: "dex", ServiceName: "dex", RuntimeGeneration: 4,
		PublicClients: []byte("[]"), Expiry: []byte("{}"), Extra: []byte("{}")}
	h := NewDexHandler(q)
	tx := &dexOperationTxFake{}
	h.SetRunTx(func(_ context.Context, fn func(DexMutationTx) error) error { return fn(tx) })
	actor := uuid.New()
	for i := 0; i < 2; i++ {
		recorder := httptest.NewRecorder()
		h.Apply(recorder, dexOperationRequest(actor, "dex-apply-1"))
		if recorder.Code != http.StatusAccepted || recorder.Header().Get("Retry-After") != "2" || recorder.Header().Get("Location") == "" {
			t.Fatalf("attempt %d status=%d headers=%v body=%s", i, recorder.Code, recorder.Header(), recorder.Body.String())
		}
	}
	if tx.queueCount != 1 || tx.auditCount != 1 {
		t.Fatalf("queue/audit=%d/%d, want exactly once", tx.queueCount, tx.auditCount)
	}
	q.settings.RuntimeGeneration++
	recorder := httptest.NewRecorder()
	h.Apply(recorder, dexOperationRequest(actor, "dex-apply-1"))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("changed input status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
