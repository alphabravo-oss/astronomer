package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestClusterAgentUpgradeRejectsMalformedOptionalBodyBeforeEffects(t *testing.T) {
	for _, body := range []string{`null`, `{"target_version":`, `{"target_version":42}`, `{} {}`, `{"typo":true}`} {
		for _, mutate := range []bool{false, true} {
			q := &fakeClusterAgentQuerier{}
			h := NewClusterAgentHandler(q)
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.Header.Set("Idempotency-Key", uuid.NewString())
			ctx := chi.NewRouteContext()
			ctx.URLParams.Add("cluster_id", uuid.NewString())
			r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
			w := httptest.NewRecorder()
			if mutate {
				h.Upgrade(w, r)
			} else {
				h.UpgradePlan(w, r)
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("mutate=%v body=%q status=%d: %s", mutate, body, w.Code, w.Body.String())
			}
			if len(q.created) != 0 || len(q.audits) != 0 {
				t.Fatal("malformed body caused effects")
			}
		}
	}
}
