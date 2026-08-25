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

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func loggingSavedSearchTestRequest(method, target string, body any, userID uuid.UUID, params map[string]string) *http.Request {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	ctx := middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID.String()})
	routeContext := chi.NewRouteContext()
	for key, value := range params {
		routeContext.URLParams.Add(key, value)
	}
	return req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, routeContext))
}

func newSavedSearchHandler(t *testing.T, outputType string) (*LoggingHandler, *loggingFakeQuerier, sqlc.LoggingOutput, uuid.UUID) {
	t.Helper()
	clusterID := uuid.New()
	userID := uuid.New()
	queries := newLoggingFakeQuerier()
	output, err := queries.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name: "logs", OutputType: outputType, Configuration: json.RawMessage(`{}`),
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewLoggingHandler(queries)
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbRead)}}},
	}}})
	return h, queries, output, userID
}

func TestLoggingSavedSearchLifecycleIsOwnerAndOutputScoped(t *testing.T) {
	h, queries, output, ownerID := newSavedSearchHandler(t, "loki")
	create := loggingSavedSearchTestRequest(http.MethodPost, "/api/v1/logging/saved-searches/", map[string]any{
		"output_id": output.ID.String(), "name": "Errors", "query": `{app="api"} |= "error"`,
		"namespaces": []string{"platform", "payments", "platform"}, "limit": 250, "live_tail": true,
	}, ownerID, nil)
	createRec := httptest.NewRecorder()
	h.CreateSavedSearch(createRec, create)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Data loggingSavedSearchResponse `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.OutputID != output.ID || created.Data.Limit != 250 || !created.Data.LiveTail {
		t.Fatalf("created saved search = %+v", created.Data)
	}
	if got := strings.Join(created.Data.Namespaces, ","); got != "payments,platform" {
		t.Fatalf("normalized namespaces = %q", got)
	}

	listReq := loggingSavedSearchTestRequest(http.MethodGet, "/api/v1/logging/saved-searches/?output_id="+output.ID.String(), nil, ownerID, nil)
	listRec := httptest.NewRecorder()
	h.ListSavedSearches(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), created.Data.ID.String()) {
		t.Fatalf("owner list status/body = %d %s", listRec.Code, listRec.Body.String())
	}

	otherUser := uuid.New()
	otherList := loggingSavedSearchTestRequest(http.MethodGet, "/api/v1/logging/saved-searches/?output_id="+output.ID.String(), nil, otherUser, nil)
	otherRec := httptest.NewRecorder()
	h.ListSavedSearches(otherRec, otherList)
	if otherRec.Code != http.StatusOK || strings.Contains(otherRec.Body.String(), created.Data.ID.String()) {
		t.Fatalf("other-user list status/body = %d %s", otherRec.Code, otherRec.Body.String())
	}

	otherDelete := loggingSavedSearchTestRequest(http.MethodDelete, "/api/v1/logging/saved-searches/"+created.Data.ID.String()+"/", nil, otherUser, map[string]string{"id": created.Data.ID.String()})
	otherDeleteRec := httptest.NewRecorder()
	h.DeleteSavedSearch(otherDeleteRec, otherDelete)
	if otherDeleteRec.Code != http.StatusNotFound {
		t.Fatalf("cross-owner delete status = %d; body=%s", otherDeleteRec.Code, otherDeleteRec.Body.String())
	}
	if _, ok := queries.saved[created.Data.ID]; !ok {
		t.Fatal("cross-owner delete removed the saved search")
	}

	update := loggingSavedSearchTestRequest(http.MethodPut, "/api/v1/logging/saved-searches/"+created.Data.ID.String()+"/", map[string]any{
		"name": "Warnings", "query": `{app="api"} |= "warn"`, "limit": 50, "direction": "forward",
	}, ownerID, map[string]string{"id": created.Data.ID.String()})
	updateRec := httptest.NewRecorder()
	h.UpdateSavedSearch(updateRec, update)
	if updateRec.Code != http.StatusOK || !strings.Contains(updateRec.Body.String(), "Warnings") {
		t.Fatalf("update status/body = %d %s", updateRec.Code, updateRec.Body.String())
	}

	deleteReq := loggingSavedSearchTestRequest(http.MethodDelete, "/api/v1/logging/saved-searches/"+created.Data.ID.String()+"/", nil, ownerID, map[string]string{"id": created.Data.ID.String()})
	deleteRec := httptest.NewRecorder()
	h.DeleteSavedSearch(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; body=%s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestLoggingSavedSearchRejectsUnsupportedTailAndInvalidBounds(t *testing.T) {
	h, _, output, ownerID := newSavedSearchHandler(t, "elasticsearch")
	for name, body := range map[string]map[string]any{
		"unsupported_tail": {"output_id": output.ID.String(), "name": "Tail", "live_tail": true},
		"invalid_limit":    {"output_id": output.ID.String(), "name": "Huge", "limit": 1001},
		"invalid_namespace": {"output_id": output.ID.String(), "name": "Bad namespace",
			"namespaces": []string{"../kube-system"}},
		"unknown_field": {"output_id": output.ID.String(), "name": "Unknown", "phantom": true},
	} {
		t.Run(name, func(t *testing.T) {
			req := loggingSavedSearchTestRequest(http.MethodPost, "/api/v1/logging/saved-searches/", body, ownerID, nil)
			rec := httptest.NewRecorder()
			h.CreateSavedSearch(rec, req)
			if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLoggingSavedSearchAuditFailureFailsClosed(t *testing.T) {
	h, queries, output, ownerID := newSavedSearchHandler(t, "loki")
	h.SetRunTx(func(_ context.Context, fn func(LoggingMutationTx) error) error {
		tx := newStagedLoggingMutationTx()
		tx.auditErr = errors.New("audit-SENTINEL")
		return fn(tx)
	})
	req := loggingSavedSearchTestRequest(http.MethodPost, "/api/v1/logging/saved-searches/", map[string]any{
		"output_id": output.ID.String(), "name": "Errors", "query": `{app="api"}`,
	}, ownerID, nil)
	rec := httptest.NewRecorder()

	h.CreateSavedSearch(rec, req)

	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "audit_unavailable") || strings.Contains(rec.Body.String(), "audit-SENTINEL") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(queries.saved) != 0 {
		t.Fatalf("audit failure retained saved searches: %+v", queries.saved)
	}
}
