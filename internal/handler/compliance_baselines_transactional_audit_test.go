package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func cloneBaselineSettings(source map[string]sqlc.PlatformSetting) map[string]sqlc.PlatformSetting {
	out := make(map[string]sqlc.PlatformSetting, len(source))
	for key, row := range source {
		row.Value = append([]byte(nil), row.Value...)
		out[key] = row
	}
	return out
}

func cloneBaselineQuotaPlans(source map[string]sqlc.QuotaPlan) map[string]sqlc.QuotaPlan {
	out := make(map[string]sqlc.QuotaPlan, len(source))
	for key, row := range source {
		out[key] = row
	}
	return out
}

func baselineRollbackRunTx(q *fakeBaselineDB) runTxFunc {
	return func(_ context.Context, fn func(ComplianceBaselineMutationTx) error) error {
		q.mu.Lock()
		settings := cloneBaselineSettings(q.settings)
		plans := cloneBaselineQuotaPlans(q.quotaPlans)
		applications := append([]sqlc.ComplianceBaselineApplication(nil), q.applications...)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.auditRows...)
		q.mu.Unlock()
		if err := fn(q); err != nil {
			q.mu.Lock()
			q.settings, q.quotaPlans, q.applications, q.auditRows = settings, plans, applications, audits
			q.mu.Unlock()
			return err
		}
		return nil
	}
}

func TestComplianceApplyAuditFailureRollsBackAllState(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	baselineID := q.seedBaseline("pci_dss_4_0", "PCI-DSS 4.0")
	q.auditErr = errors.New("audit-SENTINEL")
	h := NewComplianceBaselinesHandler(q, baselineRollbackRunTx(q), nil)
	r := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+baselineID.String()+"/apply/", callerID, []byte(`{"notes":"private change ticket"}`)),
		"id", baselineID.String(),
	)
	w := httptest.NewRecorder()
	h.Apply(w, r)

	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(q.settings) != 0 || len(q.quotaPlans) != 0 || len(q.applications) != 0 || len(q.auditRows) != 0 {
		t.Fatalf("rollback retained settings=%d plans=%d applications=%d audits=%d", len(q.settings), len(q.quotaPlans), len(q.applications), len(q.auditRows))
	}
	if q.activeLocks != 1 {
		t.Fatalf("active-row locks=%d, want 1", q.activeLocks)
	}
}

func TestComplianceApplyAuditOmitsOperatorNotes(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	baselineID := q.seedBaseline("soc2", "SOC 2")
	h := NewComplianceBaselinesHandler(q, baselineRollbackRunTx(q), nil)
	r := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+baselineID.String()+"/apply/", callerID, []byte(`{"notes":"secret-ticket-body"}`)),
		"id", baselineID.String(),
	)
	w := httptest.NewRecorder()
	h.Apply(w, r)

	if w.Code != http.StatusOK || len(q.auditRows) != 1 {
		t.Fatalf("status=%d audits=%d body=%s", w.Code, len(q.auditRows), w.Body.String())
	}
	detail := string(q.auditRows[0].Detail)
	if strings.Contains(detail, "secret-ticket-body") || !strings.Contains(detail, `"notes_present":true`) {
		t.Fatalf("audit detail=%s", detail)
	}
}

func TestComplianceRevertAuditFailureRollsBackRestoredState(t *testing.T) {
	callerID := uuid.New()
	q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
	baselineID := q.seedBaseline("soc2", "SOC 2")
	h := NewComplianceBaselinesHandler(q, baselineRollbackRunTx(q), nil)
	apply := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baselines/"+baselineID.String()+"/apply/", callerID, []byte(`{}`)),
		"id", baselineID.String(),
	)
	h.Apply(httptest.NewRecorder(), apply)
	if len(q.applications) != 1 || q.applications[0].Status != "applied" {
		t.Fatalf("apply fixture=%+v", q.applications)
	}
	applicationID := q.applications[0].ID
	q.auditRows = nil
	q.auditErr = errors.New("audit-SENTINEL")
	w := httptest.NewRecorder()
	revert := withURLParam(
		authedRequest(http.MethodPost, "/api/v1/admin/compliance-baseline-applications/"+applicationID.String()+"/revert/", callerID, nil),
		"id", applicationID.String(),
	)
	h.Revert(w, revert)

	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "audit_unavailable") || strings.Contains(w.Body.String(), "audit-SENTINEL") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if q.applications[0].Status != "applied" || len(q.auditRows) != 0 {
		t.Fatalf("rollback retained applications=%+v audits=%d", q.applications, len(q.auditRows))
	}
	if q.appLocks != 1 || q.activeLocks < 2 {
		t.Fatalf("application locks=%d active locks=%d", q.appLocks, q.activeLocks)
	}
}
