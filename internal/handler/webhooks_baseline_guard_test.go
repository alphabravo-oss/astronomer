package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/compliance"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// activeBaselineFakeQuerier embeds fakeWebhookQuerier and overrides the
// two compliance-guard methods so the active baseline is PCI-DSS 4.0 —
// which lists "audit_log_sink" in its required_webhooks set.
type activeBaselineFakeQuerier struct {
	*fakeWebhookQuerier
}

func (f *activeBaselineFakeQuerier) GetActiveComplianceBaselineApplication(_ context.Context) (sqlc.ComplianceBaselineApplication, error) {
	return sqlc.ComplianceBaselineApplication{
		ID:         uuid.New(),
		BaselineID: uuid.New(),
		Status:     "applied",
	}, nil
}

func (f *activeBaselineFakeQuerier) GetComplianceBaseline(_ context.Context, _ uuid.UUID) (sqlc.ComplianceBaseline, error) {
	b, _ := compliance.BySlug("pci_dss_4_0")
	return sqlc.ComplianceBaseline{Slug: b.Slug, Name: b.Name, Enabled: true}, nil
}

// TestWebhooksHandler_BaselineDeletionGuard_Override verifies the
// compliance deletion guard: a webhook the active baseline marks
// required cannot be deleted (409) unless the caller holds the RBAC
// override permission.
func TestWebhooksHandler_BaselineDeletionGuard_Override(t *testing.T) {
	superID := uuid.New()
	base := newFakeWebhookQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	q := &activeBaselineFakeQuerier{fakeWebhookQuerier: base}
	h := newWebhookTestHandler(t, q)

	// Seed a subscription named "audit_log_sink" — the name PCI-DSS 4.0
	// lists as required.
	subID := uuid.New()
	base.subsByID[subID] = sqlc.WebhookSubscription{ID: subID, Name: "audit_log_sink"}
	base.subsByName["audit_log_sink"] = base.subsByID[subID]

	// 1) No override → guard blocks with 409.
	w := httptest.NewRecorder()
	h.Delete(w, withChiParam(
		authedWebhookRequest(http.MethodDelete, "/api/v1/admin/webhooks/"+subID.String()+"/", superID, nil),
		"id", subID.String(),
	))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 without override, got %d body=%s", w.Code, w.Body.String())
	}
	if _, ok := base.subsByID[subID]; !ok {
		t.Fatalf("subscription must NOT be deleted when guard blocks")
	}

	// 2) Override present → delete succeeds with 204.
	h.SetBaselineOverrideChecker(func(_ *http.Request) bool { return true })
	w = httptest.NewRecorder()
	h.Delete(w, withChiParam(
		authedWebhookRequest(http.MethodDelete, "/api/v1/admin/webhooks/"+subID.String()+"/", superID, nil),
		"id", subID.String(),
	))
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204 with override, got %d body=%s", w.Code, w.Body.String())
	}
	if _, ok := base.subsByID[subID]; ok {
		t.Fatalf("subscription should be deleted once override bypasses the guard")
	}
}
