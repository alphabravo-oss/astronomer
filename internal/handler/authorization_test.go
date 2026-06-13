package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func TestAuthorizationSupportMissingWiringFailsClosedForAuthenticatedRequests(t *testing.T) {
	var support authorizationSupport

	unauthenticatedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	unauthenticatedRec := httptest.NewRecorder()
	if !support.authorizeGlobalAction(unauthenticatedRec, unauthenticatedReq, rbac.ResourceClusters, rbac.VerbRead) {
		t.Fatalf("unauthenticated direct test request should retain test-mode passthrough")
	}

	ctx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{
		ID:         "11111111-1111-1111-1111-111111111111",
		AuthMethod: "jwt",
	})
	authenticatedReq := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	authenticatedRec := httptest.NewRecorder()
	if support.authorizeGlobalAction(authenticatedRec, authenticatedReq, rbac.ResourceClusters, rbac.VerbRead) {
		t.Fatalf("authenticated request should fail closed when authorization is not configured")
	}
	if authenticatedRec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", authenticatedRec.Code, http.StatusInternalServerError, authenticatedRec.Body.String())
	}
}

func TestAuthorizationSupportErrorIncludesRequestID(t *testing.T) {
	var support authorizationSupport
	ctx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{
		ID:         "11111111-1111-1111-1111-111111111111",
		AuthMethod: "jwt",
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req.Header.Set("X-Request-ID", "req-authz-1")
	rec := httptest.NewRecorder()

	handler := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if support.authorizeGlobalAction(w, r, rbac.ResourceClusters, rbac.VerbRead) {
			t.Fatalf("authenticated request should fail closed when authorization is not configured")
		}
	}))
	handler.ServeHTTP(rec, req)

	var body map[string]map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"]["request_id"] != "req-authz-1" {
		t.Fatalf("request_id = %q, want req-authz-1; body=%s", body["error"]["request_id"], rec.Body.String())
	}
}

type superuserGateTestQuerier struct {
	user sqlc.User
	err  error
}

func (q superuserGateTestQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	if q.err != nil {
		return sqlc.User{}, q.err
	}
	return q.user, nil
}

func TestRequireSuperuser(t *testing.T) {
	userID := uuid.New()
	tests := []struct {
		name       string
		authUserID string
		querier    userByIDQuerier
		wantStatus int
		wantOK     bool
	}{
		{
			name:       "missing authenticated user",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid authenticated user id",
			authUserID: "not-a-uuid",
			querier:    superuserGateTestQuerier{user: sqlc.User{IsSuperuser: true}},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "store unavailable",
			authUserID: userID.String(),
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "caller not found",
			authUserID: userID.String(),
			querier:    superuserGateTestQuerier{err: errors.New("not found")},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "non superuser",
			authUserID: userID.String(),
			querier:    superuserGateTestQuerier{user: sqlc.User{ID: userID, IsSuperuser: false}},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "superuser",
			authUserID: userID.String(),
			querier:    superuserGateTestQuerier{user: sqlc.User{ID: userID, IsSuperuser: true}},
			wantStatus: http.StatusOK,
			wantOK:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authUserID != "" {
				req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: tt.authUserID}))
			}
			rec := httptest.NewRecorder()
			_, ok := requireSuperuser(rec, req, tt.querier, superuserGateConfig{})
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if ok {
				return
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}
