package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type transactionalExtensionQ struct {
	*fakeExtensionQuerier
	audits   []sqlc.UpsertAuditOutboxParams
	seen     []sqlc.UpsertAuditOutboxParams
	auditErr error
	locks    int
}

func (q *transactionalExtensionQ) GetUIExtensionByNameForUpdate(_ context.Context, name string) (sqlc.UIExtension, error) {
	q.locks++
	row, ok := q.rows[name]
	if !ok {
		return sqlc.UIExtension{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *transactionalExtensionQ) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	q.seen = append(q.seen, arg)
	if q.auditErr != nil {
		return sqlc.AuditOutbox{}, q.auditErr
	}
	q.audits = append(q.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, Detail: arg.Detail}, nil
}

func cloneExtensionRows(source map[string]sqlc.UIExtension) map[string]sqlc.UIExtension {
	out := make(map[string]sqlc.UIExtension, len(source))
	for key, row := range source {
		row.Manifest = append(json.RawMessage(nil), row.Manifest...)
		out[key] = row
	}
	return out
}

func fakeExtensionRunTx(q *transactionalExtensionQ) extensionRunTxFunc {
	return func(_ context.Context, fn func(ExtensionMutationTx) error) error {
		rows := cloneExtensionRows(q.rows)
		audits := append([]sqlc.UpsertAuditOutboxParams(nil), q.audits...)
		if err := fn(q); err != nil {
			q.rows, q.audits = rows, audits
			return err
		}
		return nil
	}
}

func transactionalExtensionHandler(q *transactionalExtensionQ) *ExtensionHandler {
	h := NewExtensionHandler(q)
	h.SetCurrentVersion("0.9.1")
	h.SetRunTx(fakeExtensionRunTx(q))
	return h
}

// TestEveryExtensionMutationEntryPointReachesTransaction prevents a new
// handler implementation from quietly bypassing the production transaction.
// Behavioral tests below prove rollback; this structural guard keeps the full
// public entry-point inventory attached to those transaction-owning helpers.
func TestEveryExtensionMutationEntryPointReachesTransaction(t *testing.T) {
	calls := parsedMethodCalls(t, "ExtensionHandler")
	for method, requiredCall := range map[string]string{
		"Install":            "runTx",
		"setEnabled":         "runTx",
		"markBundleVerified": "runTx",
		"Enable":             "setEnabled",
		"Disable":            "setEnabled",
		"VerifyBundle":       "markBundleVerified",
	} {
		if !calls[method][requiredCall] {
			t.Errorf("ExtensionHandler.%s does not reach transactional path via h.%s", method, requiredCall)
		}
	}
}

func TestExtensionInstallCommitsStateAndRedactedAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name      string
		auditErr  error
		wantCode  int
		wantRows  int
		wantAudit int
	}{
		{name: "commit", wantCode: http.StatusOK, wantRows: 1, wantAudit: 1},
		{name: "audit rollback", auditErr: errors.New("audit-SENTINEL"), wantCode: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &transactionalExtensionQ{fakeExtensionQuerier: newFakeExtensionQuerier(), auditErr: tc.auditErr}
			h := transactionalExtensionHandler(q)
			manifest := sampleExtensionManifest()
			manifest.DisplayName = "MANIFEST-SECRET-MARKER"
			raw, _ := json.Marshal(InstallExtensionRequest{Manifest: manifest, Source: "marketplace", Enable: true})
			w := httptest.NewRecorder()
			h.Install(w, extensionReq(t, http.MethodPost, "/api/v1/extensions/", string(raw)))

			if w.Code != tc.wantCode || len(q.rows) != tc.wantRows || len(q.audits) != tc.wantAudit {
				t.Fatalf("status=%d rows=%d audits=%d body=%s", w.Code, len(q.rows), len(q.audits), w.Body.String())
			}
			if len(q.seen) != 1 || strings.Contains(string(q.seen[0].Detail), "MANIFEST-SECRET-MARKER") || strings.Contains(string(q.seen[0].Detail), "extensionPoints") {
				t.Fatalf("audit contains manifest payload: %+v", q.seen)
			}
			if tc.auditErr != nil && (strings.Contains(w.Body.String(), "SENTINEL") || !strings.Contains(w.Body.String(), "audit_unavailable")) {
				t.Fatalf("unsafe/untruthful error body=%s", w.Body.String())
			}
		})
	}
}

func TestExtensionEnableDisableLockAndRollbackWithAudit(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		q := &transactionalExtensionQ{fakeExtensionQuerier: newFakeExtensionQuerier(), auditErr: errors.New("audit unavailable")}
		q.rows["cost-insights"] = sqlc.UIExtension{ID: uuid.New(), Name: "cost-insights", Version: "1.0.0", CompatibilityStatus: "compatible", Enabled: !enabled}
		h := transactionalExtensionHandler(q)
		w := httptest.NewRecorder()
		r := extensionReq(t, http.MethodPost, "/", "")
		if enabled {
			h.Enable(w, r)
		} else {
			h.Disable(w, r)
		}
		if w.Code != http.StatusServiceUnavailable || q.locks != 1 || q.rows["cost-insights"].Enabled != !enabled || len(q.audits) != 0 {
			t.Fatalf("enabled=%v status=%d locks=%d row=%+v audits=%d", enabled, w.Code, q.locks, q.rows["cost-insights"], len(q.audits))
		}
	}
}

func TestExtensionBundleGateLocksAndRollsBackWhenAuditFails(t *testing.T) {
	manifest := tier2Manifest()
	manifestBytes, _ := json.Marshal(manifest)
	checksum := manifest.ExtensionPoints.ClusterTabs[0].Render.Bundle.SHA256
	q := &transactionalExtensionQ{fakeExtensionQuerier: newFakeExtensionQuerier(), auditErr: errors.New("audit unavailable")}
	q.rows[manifest.Name] = sqlc.UIExtension{ID: uuid.New(), Name: manifest.Name, Version: manifest.Version, Manifest: manifestBytes}
	h := transactionalExtensionHandler(q)
	ok, err := h.markBundleVerified(httptest.NewRequest(http.MethodPost, "/", nil), manifest.Name, checksum)
	if err == nil || ok || q.locks != 1 || q.rows[manifest.Name].BundleVerified || len(q.audits) != 0 {
		t.Fatalf("ok=%v err=%v locks=%d row=%+v audits=%d", ok, err, q.locks, q.rows[manifest.Name], len(q.audits))
	}
}

func TestExtensionInstallFailsClosedWithoutTransactionRunner(t *testing.T) {
	h := NewExtensionHandler(newFakeExtensionQuerier())
	h.SetCurrentVersion("0.9.1")
	raw, _ := json.Marshal(InstallExtensionRequest{Manifest: sampleExtensionManifest(), Source: "marketplace", Enable: true})
	w := httptest.NewRecorder()
	h.Install(w, extensionReq(t, http.MethodPost, "/api/v1/extensions/", string(raw)))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"code":"runner_unwired"`) {
		t.Fatalf("status=%d body=%s, want 503 runner_unwired", w.Code, w.Body.String())
	}
}
