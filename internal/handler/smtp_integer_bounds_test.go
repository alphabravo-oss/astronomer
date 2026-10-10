package handler

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type smtpIntegerEffectQueries struct {
	*smtpFakeQuerier
	baselineCalls int
	auditCalls    int
}

func (q *smtpIntegerEffectQueries) GetActiveComplianceBaselineApplication(ctx context.Context) (sqlc.ComplianceBaselineApplication, error) {
	q.baselineCalls++
	return q.smtpFakeQuerier.GetActiveComplianceBaselineApplication(ctx)
}

func (q *smtpIntegerEffectQueries) UpsertAuditOutbox(ctx context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	q.auditCalls++
	return q.smtpFakeQuerier.UpsertAuditOutbox(ctx, arg)
}

func smtpIntegerUpdateFixture(t *testing.T) (*SMTPHandler, *smtpIntegerEffectQueries, context.Context, *int) {
	t.Helper()
	base := &smtpFakeQuerier{row: &sqlc.SmtpSettings{Enabled: true, Host: "smtp.example.test", Port: 587, PasswordEncrypted: "retained-ciphertext", FromAddress: "sender@example.test", AuthMechanism: "plain", Encryption: "starttls", RequireTls: true, TimeoutSeconds: 30}}
	ctx, _ := buildSuperuserCtx(t, base)
	queries := &smtpIntegerEffectQueries{smtpFakeQuerier: base}
	// An overflow request with a fresh password must be rejected before the
	// nil encryptor's503 response, baseline reads, transaction or audit effects.
	h := NewSMTPHandler(queries, nil, nil)
	txCalls := new(int)
	h.SetRunTx(func(_ context.Context, fn func(SMTPMutationTx) error) error {
		(*txCalls)++
		return fn(queries)
	})
	return h, queries, ctx, txCalls
}

func TestSMTPUpdateRejectsIntegerOverflowBeforeEncryptionAndMutation(t *testing.T) {
	cases := []struct {
		name, field, value string
	}{
		{"port-positive-wrap", "port", "4294967883"},
		{"port-negative-wrap", "port", "-4294966709"},
		{"port-upper-edge", "port", "2147483648"},
		{"port-lower-edge", "port", "-2147483649"},
		{"timeout-positive-wrap", "timeout_seconds", "4294967326"},
		{"timeout-negative-wrap", "timeout_seconds", "-4294967266"},
		{"timeout-upper-edge", "timeout_seconds", "2147483648"},
		{"timeout-lower-edge", "timeout_seconds", "-2147483649"},
	}
	for _, enabled := range []bool{true, false} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/enabled-%t", tc.name, enabled), func(t *testing.T) {
				h, q, ctx, txCalls := smtpIntegerUpdateFixture(t)
				original := q.row
				body := fmt.Sprintf(`{"enabled":%t,%q:%s,"password":"fresh-secret"}`, enabled, tc.field, tc.value)
				rec := httptest.NewRecorder()
				h.Update(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/smtp/", strings.NewReader(body)).WithContext(ctx))
				if rec.Code != http.StatusBadRequest {
					t.Fatalf("overflow status=%d body=%s", rec.Code, rec.Body.String())
				}
				if *txCalls != 0 || q.upserts != 0 || q.auditCalls != 0 || q.baselineCalls != 0 || q.row != original || q.row.PasswordEncrypted != "retained-ciphertext" || q.row.Port != 587 || q.row.TimeoutSeconds != 30 || strings.Contains(rec.Body.String(), "fresh-secret") {
					t.Fatal("rejected overflow reached effects or changed retained settings")
				}
			})
		}
	}
}

func TestSMTPUpdatePreservesRepresentableStagingAndEnabledLimits(t *testing.T) {
	cases := []struct {
		name          string
		enabled       bool
		port, timeout int32
	}{
		{"enabled-minimum", true, 1, 1},
		{"enabled-maximum", true, 65535, 600},
		{"disabled-minimum", false, math.MinInt32, math.MinInt32},
		{"disabled-maximum", false, math.MaxInt32, math.MaxInt32},
		{"disabled-zero", false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, q, ctx, txCalls := smtpIntegerUpdateFixture(t)
			body := fmt.Sprintf(`{"enabled":%t,"port":%d,"timeout_seconds":%d,"password":%q}`, tc.enabled, tc.port, tc.timeout, PasswordSentinelEncrypted)
			rec := httptest.NewRecorder()
			h.Update(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/smtp/", strings.NewReader(body)).WithContext(ctx))
			if rec.Code != http.StatusOK || *txCalls != 1 || q.upserts != 1 || q.auditCalls != 1 || q.row.Enabled != tc.enabled || q.row.Port != tc.port || q.row.TimeoutSeconds != tc.timeout || q.row.PasswordEncrypted != "retained-ciphertext" {
				t.Fatalf("representable settings changed: status=%d row=%+v tx=%d audit=%d", rec.Code, q.row, *txCalls, q.auditCalls)
			}
		})
	}
	t.Run("omitted-integers", func(t *testing.T) {
		h, q, ctx, txCalls := smtpIntegerUpdateFixture(t)
		rec := httptest.NewRecorder()
		h.Update(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/smtp/", strings.NewReader(`{"from_name":"changed"}`)).WithContext(ctx))
		if rec.Code != http.StatusOK || *txCalls != 1 || q.row.Port != 587 || q.row.TimeoutSeconds != 30 || q.row.FromName != "changed" || q.row.PasswordEncrypted != "retained-ciphertext" {
			t.Fatal("omitted numeric fields changed retained settings", rec.Code)
		}
	})
}

func TestSMTPUpdateRetainsEnabledSemanticValidationAndAuthorization(t *testing.T) {
	for _, body := range []string{`{"port":0}`, `{"port":65536}`, `{"timeout_seconds":0}`, `{"timeout_seconds":601}`} {
		t.Run(body, func(t *testing.T) {
			h, q, ctx, txCalls := smtpIntegerUpdateFixture(t)
			rec := httptest.NewRecorder()
			h.Update(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/smtp/", strings.NewReader(body)).WithContext(ctx))
			if rec.Code != http.StatusBadRequest || *txCalls != 0 || q.upserts != 0 || q.auditCalls != 0 {
				t.Fatal("representable invalid enabled configuration reached mutation", rec.Code)
			}
		})
	}
	t.Run("unauthenticated-overflow", func(t *testing.T) {
		h, q, _, txCalls := smtpIntegerUpdateFixture(t)
		rec := httptest.NewRecorder()
		h.Update(rec, httptest.NewRequest(http.MethodPut, "/api/v1/admin/smtp/", strings.NewReader(`{"port":4294967883}`)))
		if rec.Code != http.StatusForbidden || *txCalls != 0 || q.upserts != 0 || q.auditCalls != 0 || q.baselineCalls != 0 {
			t.Fatal("integer validation replaced original authorization boundary", rec.Code)
		}
	})
}

// Assert the effect observer retains the real transaction interface instead
// of bypassing the audit outbox behavior.
var _ SMTPMutationTx = (*smtpIntegerEffectQueries)(nil)
