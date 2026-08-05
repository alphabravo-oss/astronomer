package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type charlieOperationFake struct{ receipt sqlc.CharlieActionReceipt }

func (f charlieOperationFake) Get(context.Context, uuid.UUID, string) (sqlc.CharlieActionReceipt, error) {
	return f.receipt, nil
}

func TestCharlieOperationStatusIsBoundedAndBrowserOnly(t *testing.T) {
	now := time.Now().UTC()
	handler := NewCharlieOperationHandler(charlieOperationFake{receipt: sqlc.CharlieActionReceipt{
		CharlieActionID: "operation-a", Capability: "astronomer.queue.retry_task", Effect: "write", State: "succeeded",
		ResultStatus: "completed", ArgumentDigest: "secret-digest", AuthorizationHash: "secret-auth",
		LeaseOwner: "private-replica", CreatedAt: now, UpdatedAt: now,
		DispatchedAt: pgtype.Timestamptz{Time: now, Valid: true}, VerifiedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}})

	request := authenticatedCharlieRequest(http.MethodGet, "/", "", uuid.New(), "jwt")
	route := chi.NewRouteContext()
	route.URLParams.Add("operation_id", "operation-a")
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, route))
	recorder := httptest.NewRecorder()
	handler.Get(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "operation-a") {
		t.Fatalf("operation status = %d %s", recorder.Code, recorder.Body.String())
	}
	for _, prohibited := range []string{"argument_digest", "authorization_hash", "lease_owner", "secret-digest", "secret-auth", "private-replica"} {
		if strings.Contains(recorder.Body.String(), prohibited) {
			t.Fatalf("operation status leaked %q: %s", prohibited, recorder.Body.String())
		}
	}

	apiRequest := authenticatedCharlieRequest(http.MethodGet, "/", "", uuid.New(), "api_token")
	apiRequest = apiRequest.WithContext(context.WithValue(apiRequest.Context(), chi.RouteCtxKey, route))
	apiRecorder := httptest.NewRecorder()
	handler.Get(apiRecorder, apiRequest)
	if apiRecorder.Code != http.StatusUnauthorized {
		t.Fatalf("API token operation status = %d", apiRecorder.Code)
	}
}
