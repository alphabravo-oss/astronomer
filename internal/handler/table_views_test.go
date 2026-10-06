package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

// tableViewStoreFake is an in-memory store that enforces the same ownership
// scoping and unique-name constraint as the SQL queries.
type tableViewStoreFake struct {
	rows   []sqlc.UserTableView
	audits []sqlc.UpsertAuditOutboxParams
}

func (f *tableViewStoreFake) ListUserTableViews(_ context.Context, arg sqlc.ListUserTableViewsParams) ([]sqlc.UserTableView, error) {
	var out []sqlc.UserTableView
	for _, row := range f.rows {
		if row.UserID == arg.UserID && row.TableKey == arg.TableKey {
			out = append(out, row)
		}
	}
	return out, nil
}

func (f *tableViewStoreFake) LockUserTableViewScope(context.Context, string) error { return nil }

func (f *tableViewStoreFake) CountUserTableViews(ctx context.Context, arg sqlc.CountUserTableViewsParams) (int64, error) {
	rows, _ := f.ListUserTableViews(ctx, sqlc.ListUserTableViewsParams(arg))
	return int64(len(rows)), nil
}

func (f *tableViewStoreFake) CreateUserTableView(_ context.Context, arg sqlc.CreateUserTableViewParams) (sqlc.UserTableView, error) {
	for _, row := range f.rows {
		if row.UserID == arg.UserID && row.TableKey == arg.TableKey && row.Name == arg.Name {
			return sqlc.UserTableView{}, &pgconn.PgError{Code: "23505"}
		}
	}
	row := sqlc.UserTableView{ID: uuid.New(), UserID: arg.UserID, TableKey: arg.TableKey, Name: arg.Name, State: arg.State, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.rows = append(f.rows, row)
	return row, nil
}

func (f *tableViewStoreFake) find(id, user uuid.UUID) int {
	for i, row := range f.rows {
		if row.ID == id && row.UserID == user {
			return i
		}
	}
	return -1
}

func (f *tableViewStoreFake) GetUserTableView(_ context.Context, arg sqlc.GetUserTableViewParams) (sqlc.UserTableView, error) {
	if i := f.find(arg.ID, arg.UserID); i >= 0 {
		return f.rows[i], nil
	}
	return sqlc.UserTableView{}, pgx.ErrNoRows
}

func (f *tableViewStoreFake) UpdateUserTableView(_ context.Context, arg sqlc.UpdateUserTableViewParams) (sqlc.UserTableView, error) {
	i := f.find(arg.ID, arg.UserID)
	if i < 0 {
		return sqlc.UserTableView{}, pgx.ErrNoRows
	}
	if arg.Name.Valid {
		f.rows[i].Name = arg.Name.String
	}
	if arg.State != nil {
		f.rows[i].State = arg.State
	}
	return f.rows[i], nil
}

func (f *tableViewStoreFake) ClearUserTableViewDefault(_ context.Context, arg sqlc.ClearUserTableViewDefaultParams) error {
	for i, row := range f.rows {
		if row.UserID == arg.UserID && row.TableKey == arg.TableKey && row.ID != arg.ID {
			f.rows[i].IsDefault = false
		}
	}
	return nil
}

func (f *tableViewStoreFake) SetUserTableViewDefault(_ context.Context, arg sqlc.SetUserTableViewDefaultParams) (sqlc.UserTableView, error) {
	i := f.find(arg.ID, arg.UserID)
	if i < 0 {
		return sqlc.UserTableView{}, pgx.ErrNoRows
	}
	f.rows[i].IsDefault = arg.IsDefault
	return f.rows[i], nil
}

func (f *tableViewStoreFake) DeleteUserTableView(_ context.Context, arg sqlc.DeleteUserTableViewParams) (sqlc.UserTableView, error) {
	i := f.find(arg.ID, arg.UserID)
	if i < 0 {
		return sqlc.UserTableView{}, pgx.ErrNoRows
	}
	row := f.rows[i]
	f.rows = append(f.rows[:i], f.rows[i+1:]...)
	return row, nil
}

func (f *tableViewStoreFake) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.audits = append(f.audits, arg)
	return sqlc.AuditOutbox{}, nil
}

func newTableViewsTestHandler(store *tableViewStoreFake) *TableViewsHandler {
	return NewTableViewsHandler(store, func(_ context.Context, fn func(TableViewsMutationTx) error) error {
		// Work on a copy so a failed closure rolls back like a real transaction.
		snapshot := append([]sqlc.UserTableView(nil), store.rows...)
		audits := len(store.audits)
		if err := fn(store); err != nil {
			store.rows = snapshot
			store.audits = store.audits[:audits]
			return err
		}
		return nil
	})
}

func tableViewRequest(method, target string, user uuid.UUID, id string, body any) *http.Request {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, target, bytes.NewReader(encoded))
	if user != uuid.Nil {
		r = r.WithContext(reqctx.WithUser(r.Context(), &reqctx.User{ID: user.String(), AuthMethod: "jwt"}))
	}
	if id != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return r
}

func createView(t *testing.T, h *TableViewsHandler, user uuid.UUID, key, name string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.Create(w, tableViewRequest(http.MethodPost, "/api/v1/auth/me/table-views/", user, "", map[string]any{
		"table_key": key, "name": name, "state": map[string]any{"v": 1, "search": "crash"},
	}))
	return w
}

func TestTableViewsRequireAuthentication(t *testing.T) {
	h := newTableViewsTestHandler(&tableViewStoreFake{})
	w := httptest.NewRecorder()
	h.List(w, tableViewRequest(http.MethodGet, "/x?table_key=pods", uuid.Nil, "", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestTableViewsCreateListAndAudit(t *testing.T) {
	store := &tableViewStoreFake{}
	h := newTableViewsTestHandler(store)
	user := uuid.New()
	if w := createView(t, h, user, "pods", "Crashing"); w.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", w.Code, w.Body.String())
	}
	if len(store.audits) != 1 || store.audits[0].Action != "user.table_view.created" {
		t.Fatalf("audits = %#v", store.audits)
	}
	w := httptest.NewRecorder()
	h.List(w, tableViewRequest(http.MethodGet, "/x?table_key=pods", user, "", nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"name":"Crashing"`)) {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	// Another user sees nothing.
	w = httptest.NewRecorder()
	h.List(w, tableViewRequest(http.MethodGet, "/x?table_key=pods", uuid.New(), "", nil))
	if bytes.Contains(w.Body.Bytes(), []byte("Crashing")) {
		t.Fatalf("view leaked across users: %s", w.Body.String())
	}
}

func TestTableViewsRejectDuplicateNameAndCap(t *testing.T) {
	store := &tableViewStoreFake{}
	h := newTableViewsTestHandler(store)
	user := uuid.New()
	createView(t, h, user, "pods", "A")
	if w := createView(t, h, user, "pods", "A"); w.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d", w.Code)
	}
	for i := 1; i < 20; i++ {
		if w := createView(t, h, user, "pods", "view-"+string(rune('a'+i))); w.Code != http.StatusCreated {
			t.Fatalf("view %d status = %d %s", i, w.Code, w.Body.String())
		}
	}
	if w := createView(t, h, user, "pods", "one-too-many"); w.Code != http.StatusConflict {
		t.Fatalf("cap status = %d", w.Code)
	}
	// The cap is per table.
	if w := createView(t, h, user, "events", "A"); w.Code != http.StatusCreated {
		t.Fatalf("other table status = %d", w.Code)
	}
}

func TestTableViewsValidation(t *testing.T) {
	h := newTableViewsTestHandler(&tableViewStoreFake{})
	user := uuid.New()
	for name, body := range map[string]map[string]any{
		"bad key":     {"table_key": "Pods!", "name": "x", "state": map[string]any{}},
		"empty name":  {"table_key": "pods", "name": " ", "state": map[string]any{}},
		"bad state":   {"table_key": "pods", "name": "x", "state": map[string]any{"evil": 1}},
		"array state": {"table_key": "pods", "name": "x", "state": []string{}},
		"extra field": {"table_key": "pods", "name": "x", "state": map[string]any{}, "user_id": uuid.NewString()},
	} {
		w := httptest.NewRecorder()
		h.Create(w, tableViewRequest(http.MethodPost, "/x", user, "", body))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", name, w.Code)
		}
	}
}

func TestTableViewsUpdateDefaultAndOwnership(t *testing.T) {
	store := &tableViewStoreFake{}
	h := newTableViewsTestHandler(store)
	owner, other := uuid.New(), uuid.New()
	createView(t, h, owner, "pods", "A")
	createView(t, h, owner, "pods", "B")
	a, b := store.rows[0].ID, store.rows[1].ID

	set := func(user uuid.UUID, id uuid.UUID, body any) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.Update(w, tableViewRequest(http.MethodPatch, "/x", user, id.String(), body))
		return w
	}
	if w := set(owner, a, map[string]any{"is_default": true}); w.Code != http.StatusOK {
		t.Fatalf("default a = %d %s", w.Code, w.Body.String())
	}
	if w := set(owner, b, map[string]any{"is_default": true}); w.Code != http.StatusOK {
		t.Fatalf("default b = %d", w.Code)
	}
	if store.rows[0].IsDefault || !store.rows[1].IsDefault {
		t.Fatalf("default flags = %v %v", store.rows[0].IsDefault, store.rows[1].IsDefault)
	}
	if w := set(owner, a, map[string]any{"name": "Renamed"}); w.Code != http.StatusOK || store.rows[0].Name != "Renamed" {
		t.Fatalf("rename = %d %q", w.Code, store.rows[0].Name)
	}
	if w := set(other, a, map[string]any{"name": "Hijack"}); w.Code != http.StatusNotFound {
		t.Fatalf("cross-user update = %d", w.Code)
	}
	if w := set(owner, a, map[string]any{}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty update = %d", w.Code)
	}
}

func TestTableViewsDelete(t *testing.T) {
	store := &tableViewStoreFake{}
	h := newTableViewsTestHandler(store)
	owner := uuid.New()
	createView(t, h, owner, "pods", "A")
	id := store.rows[0].ID

	w := httptest.NewRecorder()
	h.Delete(w, tableViewRequest(http.MethodDelete, "/x", uuid.New(), id.String(), nil))
	if w.Code != http.StatusNotFound || len(store.rows) != 1 {
		t.Fatalf("cross-user delete = %d rows=%d", w.Code, len(store.rows))
	}
	w = httptest.NewRecorder()
	h.Delete(w, tableViewRequest(http.MethodDelete, "/x", owner, id.String(), nil))
	if w.Code != http.StatusNoContent || len(store.rows) != 0 {
		t.Fatalf("delete = %d rows=%d", w.Code, len(store.rows))
	}
	if last := store.audits[len(store.audits)-1]; last.Action != "user.table_view.deleted" {
		t.Fatalf("last audit = %s", last.Action)
	}
	w = httptest.NewRecorder()
	h.Delete(w, tableViewRequest(http.MethodDelete, "/x", owner, "not-a-uuid", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", w.Code)
	}
}

func TestTableViewsMandatoryAuditFailureRollsBack(t *testing.T) {
	store := &tableViewStoreFake{}
	h := NewTableViewsHandler(store, func(_ context.Context, _ func(TableViewsMutationTx) error) error {
		return errors.New("tx unavailable")
	})
	w := createView(t, h, uuid.New(), "pods", "A")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", w.Code)
	}
}
