package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type securityTxHarness struct {
	auditErr       error
	committed      *sqlc.ClusterSecurityPolicy
	committedAudit []sqlc.AuditOutbox
}

func (h *securityTxHarness) runTx(ctx context.Context, fn func(SecurityMutationTx) error) error {
	tx := &fakeSecurityMutationTx{auditErr: h.auditErr}
	if err := fn(tx); err != nil {
		return err
	}
	h.committed = tx.policy
	h.committedAudit = append([]sqlc.AuditOutbox(nil), tx.audits...)
	return nil
}

type fakeSecurityMutationTx struct {
	auditErr error
	policy   *sqlc.ClusterSecurityPolicy
	audits   []sqlc.AuditOutbox
}

func (f *fakeSecurityMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if f.auditErr != nil {
		return sqlc.AuditOutbox{}, f.auditErr
	}
	row := sqlc.AuditOutbox{
		ID: arg.ID, DedupeKey: arg.DedupeKey, Action: arg.Action,
		ResourceType: arg.ResourceType, ResourceID: arg.ResourceID,
		Detail: arg.Detail, Status: "pending",
	}
	f.audits = append(f.audits, row)
	return row, nil
}

func (f *fakeSecurityMutationTx) CreateClusterSecurityPolicy(_ context.Context, arg sqlc.CreateClusterSecurityPolicyParams) (sqlc.ClusterSecurityPolicy, error) {
	row := sqlc.ClusterSecurityPolicy{ID: uuid.New(), ClusterID: arg.ClusterID, TemplateID: arg.TemplateID, SyncStatus: arg.SyncStatus}
	f.policy = &row
	return row, nil
}

func (f *fakeSecurityMutationTx) CreatePodSecurityTemplate(context.Context, sqlc.CreatePodSecurityTemplateParams) (sqlc.PodSecurityTemplate, error) {
	return sqlc.PodSecurityTemplate{}, errors.New("unexpected CreatePodSecurityTemplate")
}
func (f *fakeSecurityMutationTx) UpdatePodSecurityTemplate(context.Context, sqlc.UpdatePodSecurityTemplateParams) (sqlc.PodSecurityTemplate, error) {
	return sqlc.PodSecurityTemplate{}, errors.New("unexpected UpdatePodSecurityTemplate")
}
func (f *fakeSecurityMutationTx) DeletePodSecurityTemplate(context.Context, uuid.UUID) error {
	return errors.New("unexpected DeletePodSecurityTemplate")
}
func (f *fakeSecurityMutationTx) UpdateClusterSecurityPolicyApplied(context.Context, uuid.UUID) error {
	return errors.New("unexpected UpdateClusterSecurityPolicyApplied")
}
func (f *fakeSecurityMutationTx) DeleteClusterSecurityPolicy(context.Context, uuid.UUID) error {
	return errors.New("unexpected DeleteClusterSecurityPolicy")
}
func (f *fakeSecurityMutationTx) CancelSecurityScan(context.Context, sqlc.CancelSecurityScanParams) (sqlc.SecurityScanResult, error) {
	return sqlc.SecurityScanResult{}, errors.New("unexpected CancelSecurityScan")
}

func securityPolicyCreateRequest(t *testing.T) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{"cluster_id": uuid.New(), "template_id": uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewRequest(http.MethodPost, "/api/v1/security/policies/", bytes.NewReader(body))
}

func TestSecurityPolicyCreateCommitsDomainAndAuditIntentTogether(t *testing.T) {
	harness := &securityTxHarness{}
	h := NewSecurityHandler(sqlc.New(nil))
	h.SetRunTx(harness.runTx)
	rec := httptest.NewRecorder()
	h.CreatePolicy(rec, securityPolicyCreateRequest(t))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if harness.committed == nil || len(harness.committedAudit) != 1 {
		t.Fatalf("committed policy=%#v audit=%#v", harness.committed, harness.committedAudit)
	}
	if got := harness.committedAudit[0]; got.Action != "security.policy.create" || got.ResourceID != harness.committed.ID.String() {
		t.Fatalf("audit row = %#v", got)
	}
}

func TestSecurityPolicyCreateRollsBackWhenAuditIntentFails(t *testing.T) {
	harness := &securityTxHarness{auditErr: errors.New("audit outbox unavailable")}
	h := NewSecurityHandler(sqlc.New(nil))
	h.SetRunTx(harness.runTx)
	rec := httptest.NewRecorder()
	h.CreatePolicy(rec, securityPolicyCreateRequest(t))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("status=%d body=%s, want 503 audit_unavailable", rec.Code, rec.Body.String())
	}
	if harness.committed != nil || len(harness.committedAudit) != 0 {
		t.Fatalf("rollback failed: policy=%#v audit=%#v", harness.committed, harness.committedAudit)
	}
}
