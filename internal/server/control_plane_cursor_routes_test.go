package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type mutableCursorRouteRBAC struct{ bindings []rbac.RoleBinding }

func (q *mutableCursorRouteRBAC) GetUserBindings(context.Context, string) ([]rbac.RoleBinding, error) {
	return q.bindings, nil
}

type controlPlaneCursorRouteQuerier struct {
	*sqlc.Queries
	firstReads, afterReads int
}

func (q *controlPlaneCursorRouteQuerier) ListControlPlaneCursorAlertsAllFirst(context.Context, int32) ([]sqlc.ControlPlaneAlert, error) {
	q.firstReads++
	rows := make([]sqlc.ControlPlaneAlert, 3)
	for i := range rows {
		rows[i] = sqlc.ControlPlaneAlert{ID: uuid.New(), FiredAt: time.Now().Add(-time.Duration(i) * time.Second)}
	}
	return rows, nil
}
func (q *controlPlaneCursorRouteQuerier) ListControlPlaneSilences(context.Context, sqlc.ListControlPlaneSilencesParams) ([]sqlc.ControlPlaneSilence, error) {
	q.firstReads++
	rows := make([]sqlc.ControlPlaneSilence, 3)
	for i := range rows {
		rows[i] = sqlc.ControlPlaneSilence{ID: uuid.New(), CreatedAt: time.Now().Add(-time.Duration(i) * time.Second)}
	}
	return rows, nil
}
func (q *controlPlaneCursorRouteQuerier) ListControlPlaneCursorAlertsAllAfter(context.Context, sqlc.ListControlPlaneCursorAlertsAllAfterParams) ([]sqlc.ControlPlaneAlert, error) {
	q.afterReads++
	return nil, nil
}
func (q *controlPlaneCursorRouteQuerier) ListControlPlaneSilencesAfter(context.Context, sqlc.ListControlPlaneSilencesAfterParams) ([]sqlc.ControlPlaneSilence, error) {
	q.afterReads++
	return nil, nil
}
func TestControlPlaneCursorProductionRoutesReauthorizeContinuation(t *testing.T) {
	for _, collection := range []string{"alerts", "silences"} {
		t.Run(collection, func(t *testing.T) {
			q := &controlPlaneCursorRouteQuerier{}
			h := handler.NewControlPlaneHandler(q, nil)
			codec, err := paging.NewCursorCodec("fixture-cursor-authority")
			if err != nil {
				t.Fatal(err)
			}
			h.SetCursorCodec(codec)
			auth := &mutableCursorRouteRBAC{bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceAlerts), Verbs: []string{string(rbac.VerbRead)}}}}}}
			jwtMgr := iauth.MustNewJWTManager("control-plane-cursor-route-secret", 60)
			router := NewRouter(&config.Config{}, RouterDependencies{CoreAuth: CoreAuthDependencies{JWT: jwtMgr, RBACEngine: rbac.NewEngine(), RBACQueries: auth}, ClusterResources: ClusterResourceDependencies{ControlPlane: h}})
			token, err := jwtMgr.GenerateAccessToken(uuid.New())
			if err != nil {
				t.Fatal(err)
			}
			request := func(query string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/controllers/"+collection+"/?"+query, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			first := request("limit=2&cursor=")
			if first.Code != http.StatusOK {
				t.Fatalf("actual registered route rejected first page: %d %s", first.Code, first.Body.String())
			}
			var page paging.Response[json.RawMessage]
			if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if page.Pagination.NextCursor == nil || !page.Pagination.HasMore {
				t.Fatal("first page lacks actual sentinel token")
			}
			auth.bindings = nil
			next := request("limit=2&cursor=" + url.QueryEscape(*page.Pagination.NextCursor))
			if next.Code != http.StatusForbidden || q.firstReads != 1 || q.afterReads != 0 {
				t.Fatalf("revoked route status%d first%d after%d", next.Code, q.firstReads, q.afterReads)
			}
		})
	}
}
