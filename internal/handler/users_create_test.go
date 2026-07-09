package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// createUserTrackingQuerier embeds the full ResourceQuerier stub and records CreateUser.
type createUserTrackingQuerier struct {
	resourceAuditQuerier
	userID     uuid.UUID
	lastCreate *sqlc.CreateUserParams
	createN    int
	resetN     int
}

func (q *createUserTrackingQuerier) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	q.createN++
	cp := arg
	q.lastCreate = &cp
	return sqlc.User{ID: uuid.New(), Email: arg.Email, Username: arg.Username, IsActive: true}, nil
}

func (q *createUserTrackingQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	return sqlc.User{ID: id, Email: "u@example.com", Username: "u", IsActive: true}, nil
}

func (q *createUserTrackingQuerier) UpdateUserPassword(_ context.Context, _ sqlc.UpdateUserPasswordParams) error {
	q.resetN++
	return nil
}

func withUserRouteID(r *http.Request, id string) *http.Request {
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
}

// TEST-01: CreateUser shipped handler rejects weak passwords and never calls store.
func TestCreateUser_HTTPRejectsWeakPassword(t *testing.T) {
	q := &createUserTrackingQuerier{}
	h := NewResourceHandlerWithQueries(q, nil)
	body := `{"email":"a@example.com","username":"alice","password":"short"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.CreateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if q.createN != 0 {
		t.Fatal("CreateUser store must not be called for weak passwords")
	}
}

// TEST-01: strong password reaches the store and is hashed.
func TestCreateUser_HTTPAcceptsStrongPassword(t *testing.T) {
	q := &createUserTrackingQuerier{}
	h := NewResourceHandlerWithQueries(q, nil)
	body := `{"email":"a@example.com","username":"alice","password":"ValidPassw0rd"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.CreateUser(rec, req)
	if rec.Code >= 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.createN != 1 {
		t.Fatalf("CreateUser store calls=%d want 1", q.createN)
	}
	if q.lastCreate == nil || q.lastCreate.Password == "ValidPassw0rd" || q.lastCreate.Password == "" {
		t.Fatal("password must be stored hashed, not plaintext")
	}
}

// TEST-01: ResetUserPassword with weak supplied password is rejected before update.
func TestResetUserPassword_HTTPRejectsWeak(t *testing.T) {
	q := &createUserTrackingQuerier{userID: uuid.New()}
	h := NewResourceHandlerWithQueries(q, nil)
	id := q.userID
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+id.String()+"/reset-password/",
		bytes.NewBufferString(`{"password":"short"}`))
	req.ContentLength = int64(len(`{"password":"short"}`))
	req = withUserRouteID(req, id.String())
	rec := httptest.NewRecorder()
	h.ResetUserPassword(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if q.resetN != 0 {
		t.Fatal("UpdateUserPassword must not run for weak password")
	}
}
