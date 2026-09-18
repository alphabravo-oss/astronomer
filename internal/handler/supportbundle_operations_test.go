package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type supportBundleOperationFake struct {
	mu       sync.Mutex
	id       uuid.UUID
	request  sqlc.CreateSupportBundleOperationParams
	created  bool
	outboxes []sqlc.UpsertAuditOutboxParams
	status   sqlc.GetSupportBundleOperationRow
	artifact sqlc.GetSupportBundleArtifactRow
}

func (f *supportBundleOperationFake) CreateSupportBundleOperation(_ context.Context, arg sqlc.CreateSupportBundleOperationParams) (sqlc.CreateSupportBundleOperationRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.created && (f.request.IdempotencyScope != arg.IdempotencyScope || f.request.IdempotencyKey != arg.IdempotencyKey || f.request.RequestDigest != arg.RequestDigest) {
		return sqlc.CreateSupportBundleOperationRow{}, pgx.ErrNoRows
	}
	created := !f.created
	if created {
		f.created = true
		f.request = arg
		f.status = sqlc.GetSupportBundleOperationRow{ID: f.id, RequestedBy: arg.RequestedBy, Status: "pending", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	}
	return sqlc.CreateSupportBundleOperationRow{
		ID: f.id, RequestedBy: arg.RequestedBy, Status: f.status.Status,
		ExpiresAt: f.status.ExpiresAt, CreatedAt: f.status.CreatedAt,
		UpdatedAt: f.status.UpdatedAt, Created: created,
	}, nil
}

func (f *supportBundleOperationFake) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outboxes = append(f.outboxes, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func (f *supportBundleOperationFake) GetSupportBundleOperation(context.Context, uuid.UUID) (sqlc.GetSupportBundleOperationRow, error) {
	if f.status.ID == uuid.Nil {
		return sqlc.GetSupportBundleOperationRow{}, pgx.ErrNoRows
	}
	return f.status, nil
}

func (f *supportBundleOperationFake) GetSupportBundleArtifact(context.Context, uuid.UUID) (sqlc.GetSupportBundleArtifactRow, error) {
	if f.artifact.ID == uuid.Nil {
		return sqlc.GetSupportBundleArtifactRow{}, pgx.ErrNoRows
	}
	return f.artifact, nil
}

func TestSupportBundleCreateIsIdempotentAndAuditedOnce(t *testing.T) {
	userID, operationID := uuid.New(), uuid.New()
	queries := &fakeSupportBundleQuerier{user: sqlc.User{ID: userID, IsActive: true, IsSuperuser: true}}
	operations := &supportBundleOperationFake{id: operationID}
	h := NewSupportBundleHandler(queries, operations, nil, "")
	h.SetRunTx(func(ctx context.Context, fn func(SupportBundleMutationTx) error) error { return fn(operations) })

	for i := 0; i < 2; i++ {
		req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/support-bundles/", nil), userID)
		req.Header.Set("Idempotency-Key", "diagnostics-incident-42")
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d status=%d body=%s", i+1, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != supportBundleStatusURL(operationID) {
			t.Fatalf("request %d location=%q", i+1, got)
		}
	}
	if len(operations.outboxes) != 1 || operations.outboxes[0].Action != "admin.support_bundle.generation_accepted" {
		t.Fatalf("audit intents=%#v", operations.outboxes)
	}
}

func TestSupportBundleDownloadReadsCompletedArtifactAndAudits(t *testing.T) {
	userID, operationID := uuid.New(), uuid.New()
	queries := &fakeSupportBundleQuerier{user: sqlc.User{ID: userID, IsActive: true, IsSuperuser: true}}
	operations := &supportBundleOperationFake{artifact: sqlc.GetSupportBundleArtifactRow{
		ID: operationID, Filename: "bundle.zip", ArtifactContentType: "application/zip",
		Artifact: []byte("zip"), ArtifactSha256: pgtype.Text{String: "abc", Valid: true},
		ArtifactSize: 3, ExpiresAt: time.Now().Add(time.Hour),
	}}
	h := NewSupportBundleHandler(queries, operations, nil, "")
	req := withAuth(httptest.NewRequest(http.MethodGet, supportBundleStatusURL(operationID)+"download/", nil), userID)
	req = withURLParam(req, "id", operationID.String())
	rec := httptest.NewRecorder()
	h.Download(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "zip" {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
	if len(queries.audits) != 1 || queries.audits[0].Action != "admin.support_bundle.downloaded" {
		t.Fatalf("download audits=%#v", queries.audits)
	}
}

func TestSupportBundleDownloadRejectsPendingOperation(t *testing.T) {
	userID := uuid.New()
	queries := &fakeSupportBundleQuerier{user: sqlc.User{ID: userID, IsActive: true, IsSuperuser: true}}
	operations := &supportBundleOperationFake{}
	h := NewSupportBundleHandler(queries, operations, nil, "")
	req := withURLParam(withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/support-bundles/x/download/", nil), userID), "id", uuid.NewString())
	rec := httptest.NewRecorder()
	h.Download(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
