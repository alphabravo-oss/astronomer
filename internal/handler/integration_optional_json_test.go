package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

func TestCatalogOptionalBodyRejectsBeforeEffects(t *testing.T) {
	for _, body := range []string{`null`, `{`, `{} {}`, `{"typo":true}`, `{"revision":"bad"}`, `{"values_override":42}`} {
		for _, rollback := range []bool{false, true} {
			q, clusterID, _, _ := newInstalledCatalogAuditQuerier()
			id := uuid.New()
			q.installations[id] = sqlc.InstalledChart{ID: id, ClusterID: clusterID, Status: "deployed", Revision: 2}
			h := NewCatalogHandler(q)
			h.SetRunTx(func(_ context.Context, fn func(CatalogMutationTx) error) error { return fn(q) })
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.Header.Set("Idempotency-Key", uuid.NewString())
			r = withChiParams(r, map[string]string{"id": id.String()})
			w := httptest.NewRecorder()
			if rollback {
				h.RollbackInstalledChart(w, r)
			} else {
				h.UpgradeInstalledChart(w, r)
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("rollback=%v body=%q status=%d: %s", rollback, body, w.Code, w.Body.String())
			}
			if len(q.audits) != 0 || len(q.operations) != 0 || q.installations[id].Status != "deployed" {
				t.Fatal("malformed body changed the installation or queued an operation")
			}
		}
	}
}

type forbiddenOptionalProbe struct{ fakeVaultProbe }

func (forbiddenOptionalProbe) Test(context.Context, sqlc.VaultConnection, string, string) (TestResult, error) {
	panic("malformed input reached Vault")
}

func TestVaultOptionalBodyRejectsBeforeEffects(t *testing.T) {
	for _, body := range []string{`null`, `{`, `{} {}`, `{"typo":true}`, `{"probe_path":42}`} {
		q := newFakeVaultQuerier()
		caller, id := uuid.New(), uuid.New()
		q.users[caller] = sqlc.User{ID: caller, IsSuperuser: true}
		q.conns[id] = sqlc.VaultConnection{ID: id, Name: "test", Enabled: true}
		h := wireVaultMutationFixture(NewVaultHandler(q), q)
		h.SetProbe(forbiddenOptionalProbe{})
		r := makeVaultRequest(t, http.MethodPost, "/", caller, nil)
		r.Body = io.NopCloser(strings.NewReader(body))
		r = withChiParams(r, map[string]string{"id": id.String()})
		w := httptest.NewRecorder()
		h.Test(w, r)
		if w.Code != http.StatusBadRequest || len(q.audits) != 0 {
			t.Fatalf("body=%q status=%d audits=%d: %s", body, w.Code, len(q.audits), w.Body.String())
		}
	}
}
