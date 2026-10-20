package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

type checkedPageCase struct {
	name, query, errorCode string
	limit, offset          int32
}

func checkedPageCases(defaultLimit int32) []checkedPageCase {
	return []checkedPageCase{
		{"default", "", "", defaultLimit, 0},
		{"normal", "limit=25&offset=42", "", 25, 42},
		{"zero", "limit=0&offset=0", "", defaultLimit, 0},
		{"negative", "limit=-1&offset=-1", "", defaultLimit, 0},
		{"negative-overflow", "limit=-99999999999999999999999&offset=-99999999999999999999999", "", defaultLimit, 0},
		{"malformed", "limit=invalid&offset=invalid", "", defaultLimit, 0},
		{"limit-overflow", "limit=2147483648", "", 200, 0},
		{"upper-window", "limit=200&offset=10000", "", 200, 10000},
		{"offset-rejected", "offset=10001", "offset_not_supported", 0, 0},
		{"offset-repeated", "offset=1&offset=2", "offset_not_supported", 0, 0},
		{"offset-overflow", "offset=18446744073709551616", "offset_not_supported", 0, 0},
		{"cursor-rejected", "cursor=", "cursor_not_supported", 0, 0},
	}
}

func assertCheckedConsumerPage(t *testing.T, response *httptest.ResponseRecorder, tc checkedPageCase, calls int, limit, offset int32) {
	t.Helper()
	if tc.errorCode != "" {
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusBadRequest || calls != 0 || body.Error.Code != tc.errorCode {
			t.Fatalf("rejected window: status=%d calls=%d code=%s", response.Code, calls, body.Error.Code)
		}
		return
	}
	var body struct {
		Pagination paging.Metadata `json:"pagination"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || calls != 1 || limit != tc.limit || offset != tc.offset || body.Pagination.Limit != int(limit) || body.Pagination.Offset != int(offset) || body.Pagination.Total == nil || *body.Pagination.Total != 0 {
		t.Fatalf("SQL/metadata window: status=%d calls=%d SQL=%d/%d metadata=%+v", response.Code, calls, limit, offset, body.Pagination)
	}
}

type checkedOutboxPageQuerier struct {
	*fakeAdminTaskOutboxQuerier
	arg   sqlc.ListTaskOutboxParams
	calls int
}

func (q *checkedOutboxPageQuerier) ListTaskOutbox(_ context.Context, arg sqlc.ListTaskOutboxParams) ([]sqlc.TaskOutbox, error) {
	q.arg, q.calls = arg, q.calls+1
	return []sqlc.TaskOutbox{}, nil
}

func TestCheckedOutboxSQLWindowMatchesLegacyMetadata(t *testing.T) {
	for _, route := range []string{"list", "dead"} {
		t.Run(route, func(t *testing.T) {
			for _, tc := range checkedPageCases(50) {
				t.Run(tc.name, func(t *testing.T) {
					actor := uuid.New()
					q := &checkedOutboxPageQuerier{fakeAdminTaskOutboxQuerier: &fakeAdminTaskOutboxQuerier{users: map[uuid.UUID]sqlc.User{actor: {ID: actor, IsSuperuser: true}}}}
					h := NewAdminTaskOutboxHandler(q)
					response := httptest.NewRecorder()
					request := makeAdminTaskOutboxRequest(http.MethodGet, "/api/v1/admin/task-outbox/?"+tc.query, actor)
					if route == "dead" {
						h.ListDead(response, request)
					} else {
						h.List(response, request)
					}
					assertCheckedConsumerPage(t, response, tc, q.calls, q.arg.Limit, q.arg.Offset)
					if tc.errorCode == "" && route == "dead" && q.arg.Status != "dead" {
						t.Fatal("dead-page status filter changed")
					}
				})
			}
		})
	}
}

type checkedInstalledPageQuerier struct {
	*idorCatalogQuerier
	limit, offset int32
	calls         int
	scopes        []uuid.UUID
}

func (q *checkedInstalledPageQuerier) ListInstalledCharts(_ context.Context, arg sqlc.ListInstalledChartsParams) ([]sqlc.InstalledChart, error) {
	q.limit, q.offset, q.calls = arg.Limit, arg.Offset, q.calls+1
	return []sqlc.InstalledChart{}, nil
}

func (q *checkedInstalledPageQuerier) ListInstalledChartsForScopes(_ context.Context, arg sqlc.ListInstalledChartsForScopesParams) ([]sqlc.InstalledChart, error) {
	q.limit, q.offset, q.calls, q.scopes = arg.QueryLimit, arg.QueryOffset, q.calls+1, arg.ClusterIds
	return []sqlc.InstalledChart{}, nil
}

func TestCheckedInstalledSQLWindowMatchesLegacyMetadataAndScopes(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		name := "all"
		if scoped {
			name = "scoped"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range checkedPageCases(20) {
				t.Run(tc.name, func(t *testing.T) {
					cluster := uuid.New()
					q := &checkedInstalledPageQuerier{idorCatalogQuerier: newIDORCatalogQuerier()}
					h := NewCatalogHandler(q)
					if scoped {
						h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: catalogReadBindings(cluster)})
					} else {
						h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: "catalog", Verbs: []string{"read"}}}}}})
					}
					response := httptest.NewRecorder()
					h.ListInstalledCharts(response, authedCatalogReq(http.MethodGet, "/api/v1/catalog/installed/?"+tc.query, nil))
					assertCheckedConsumerPage(t, response, tc, q.calls, q.limit, q.offset)
					if tc.errorCode == "" && scoped && (len(q.scopes) != 1 || q.scopes[0] != cluster) {
						t.Fatal("SQL page lost the current authorized cluster scope")
					}
				})
			}
		})
	}
}
