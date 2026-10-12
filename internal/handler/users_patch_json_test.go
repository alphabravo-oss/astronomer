package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

type userJSONPatchStore struct {
	ResourceQuerier
	current sqlc.User
	params  sqlc.UpdateUserParams
	writes  int
}

func (q *userJSONPatchStore) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return q.current, nil
}

func (q *userJSONPatchStore) GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error) {
	return q.current, nil
}

func (q *userJSONPatchStore) UpdateUser(_ context.Context, params sqlc.UpdateUserParams) (sqlc.User, error) {
	q.params = params
	q.writes++
	return sqlc.User{ID: params.ID, Email: params.Email, Username: params.Username, FirstName: params.FirstName, LastName: params.LastName, IsActive: params.IsActive}, nil
}

func (q *userJSONPatchStore) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	return sqlc.AuditOutbox{ID: arg.ID}, nil
}

func TestUpdateUserJSONPatchPreservesV1OmittedNullAndEmpty(t *testing.T) {
	for _, body := range []string{`{}`, `{"email":null,"username":null,"first_name":null,"last_name":null,"is_active":null}`, `{"email":"","username":" ","first_name":"","last_name":""}`, `{"unknown":[],"future":{},"unrelated_zero":0}`} {
		q := &userJSONPatchStore{current: sqlc.User{ID: uuid.New(), Email: "old@example.com", Username: "old", FirstName: "First", LastName: "Last", IsActive: true}}
		h := NewResourceHandlerWithQueries(q, nil)
		h.SetUserRunTx(func(ctx context.Context, fn func(UserMutationTx) error) error { return fn(&userJSONPatchTx{store: q}) })
		r := withUserRouteID(httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body)), q.current.ID.String())
		w := httptest.NewRecorder()
		h.UpdateUser(w, r)
		if w.Code != http.StatusOK || q.writes != 1 || q.params.Email != q.current.Email || q.params.Username != q.current.Username || q.params.FirstName != q.current.FirstName || q.params.LastName != q.current.LastName || !q.params.IsActive {
			t.Fatalf("v1 preserve semantics changed: status=%d writes=%d", w.Code, q.writes)
		}
	}
}

func TestCreateUserValidateEmailUsesSafeFieldError(t *testing.T) {
	q := &createUserTrackingQuerier{}
	h := NewResourceHandlerWithQueries(q, nil)
	wireCreateUserTestTx(h, q)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"invalid","password":"sensitive-fixture"}`))
	h.CreateUser(w, r)
	if w.Code != http.StatusUnprocessableEntity || q.createN != 0 || !strings.Contains(w.Body.String(), `"field":"email"`) || strings.Contains(w.Body.String(), "sensitive-fixture") {
		t.Fatalf("validation failed to return safe typed field error: status=%d writes=%d", w.Code, q.createN)
	}
}

type userJSONPatchTx struct {
	UserMutationTx
	store *userJSONPatchStore
}

func (tx *userJSONPatchTx) GetUserByIDForUpdate(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	return tx.store.GetUserByIDForUpdate(ctx, id)
}
func (tx *userJSONPatchTx) UpdateUser(ctx context.Context, arg sqlc.UpdateUserParams) (sqlc.User, error) {
	return tx.store.UpdateUser(ctx, arg)
}
func (tx *userJSONPatchTx) UpsertAuditOutbox(ctx context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	return tx.store.UpsertAuditOutbox(ctx, arg)
}
