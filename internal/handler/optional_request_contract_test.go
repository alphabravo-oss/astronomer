package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

func TestComplianceApplyJSONChunkedInvalidBodyCannotWrite(t *testing.T) {
	for _, body := range []string{`{"notes":`, `{"notes":"first"}{"notes":"second"}`} {
		callerID := uuid.New()
		q := newFakeBaselineDB(sqlc.User{ID: callerID, IsSuperuser: true})
		id := q.seedBaseline("soc2", "SOC 2")
		h := NewComplianceBaselinesHandler(q, baselineRollbackRunTx(q), nil)
		r := withURLParam(authedRequest(http.MethodPost, "/", callerID, []byte(body)), "id", id.String())
		r.ContentLength = -1
		w := httptest.NewRecorder()
		h.Apply(w, r)
		if w.Code != http.StatusBadRequest || len(q.settings) != 0 || len(q.applications) != 0 || len(q.auditRows) != 0 {
			t.Fatalf("chunked invalid body reached mutation: status=%d settings=%d applications=%d audits=%d", w.Code, len(q.settings), len(q.applications), len(q.auditRows))
		}
	}
}

func TestShellElevationJSONMalformedBodyRetainsLeastPrivilege(t *testing.T) {
	for _, body := range []string{"", `{"elevate":true`, `{"elevate":true}{}`, `{"mode":"admin"} trailing`} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r.ContentLength = -1
		if parseShellElevation(r) {
			t.Fatal("invalid optional body requested shell elevation")
		}
	}
	if !parseShellElevation(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"elevate":true}`))) {
		t.Fatal("valid explicit elevation request was lost")
	}
}

func TestShellJSONMalformedElevationCreatesOnlyReadOnlySession(t *testing.T) {
	h, q, requester, bindings, userID, clusterID := newTestKubectlHandlerWithRBAC(t)
	bindings.list = []rbac.RoleBinding{{UserID: userID.String(), RoleRules: []rbac.Rule{
		{Resource: "pods", Verbs: []string{"read", "list", "watch", "create", "update", "delete"}},
		{Resource: "clusters", Verbs: []string{"read", "update", "delete"}},
	}}}
	r := authReq(http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/shell/sessions/", `{"elevate":true}{}`, userID, false)
	r.ContentLength = -1
	w := httptest.NewRecorder()
	newKubectlRouter(h).ServeHTTP(w, r)
	if w.Code != http.StatusCreated || len(q.sessions) != 1 {
		t.Fatalf("least-privilege compatibility changed: status=%d sessions=%d", w.Code, len(q.sessions))
	}
	verbs := clusterRoleVerbs(t, requester.bodyFor("/clusterroles"))
	if len(verbs) == 0 || containsAny(verbs, "create", "update", "patch", "delete", "*") != "" {
		t.Fatal("malformed elevation request granted write verbs")
	}
}

func TestDatasourceJSONDecoderErrorDoesNotExposeRequestFields(t *testing.T) {
	callerID := uuid.New()
	h := NewDashboardHandler(newFakeDashboardQuerier(sqlc.User{ID: callerID, IsSuperuser: true}))
	w := httptest.NewRecorder()
	r := authedRequest(http.MethodPost, "/", callerID, []byte(`{"name":123,"sensitive-fixture":{"credential":"not-for-errors"}}`))
	h.AdminCreateDatasource(w, r)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "sensitive-fixture") || strings.Contains(w.Body.String(), "not-for-errors") || strings.Contains(w.Body.String(), "DatasourceRequest") {
		t.Fatalf("request error must use a bounded category: status=%d body=%s", w.Code, w.Body.String())
	}
}
