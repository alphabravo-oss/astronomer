package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/notify"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeNotifyQuerier satisfies NotificationTemplateQuerier for tests.
type fakeNotifyQuerier struct {
	rows  map[string]sqlc.NotificationTemplate
	users map[uuid.UUID]sqlc.User
}

func (f *fakeNotifyQuerier) GetNotificationTemplate(_ context.Context, key string) (sqlc.NotificationTemplate, error) {
	row, ok := f.rows[key]
	if !ok {
		return sqlc.NotificationTemplate{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeNotifyQuerier) ListNotificationTemplates(_ context.Context) ([]sqlc.NotificationTemplate, error) {
	out := make([]sqlc.NotificationTemplate, 0, len(f.rows))
	for _, v := range f.rows {
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeNotifyQuerier) UpsertNotificationTemplate(_ context.Context, arg sqlc.UpsertNotificationTemplateParams) (sqlc.NotificationTemplate, error) {
	if f.rows == nil {
		f.rows = map[string]sqlc.NotificationTemplate{}
	}
	row := sqlc.NotificationTemplate{
		ID:          uuid.New(),
		TemplateKey: arg.TemplateKey,
		Channel:     arg.Channel,
		SubjectTpl:  arg.SubjectTpl,
		BodyTpl:     arg.BodyTpl,
		BodyFormat:  arg.BodyFormat,
		Enabled:     arg.Enabled,
		UpdatedBy:   arg.UpdatedBy,
	}
	f.rows[arg.TemplateKey] = row
	return row, nil
}

func (f *fakeNotifyQuerier) DeleteNotificationTemplate(_ context.Context, key string) error {
	delete(f.rows, key)
	return nil
}

func (f *fakeNotifyQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return sqlc.User{}, pgx.ErrNoRows
}

func newNotifyHandler(t *testing.T) (*NotificationTemplateHandler, *fakeNotifyQuerier, context.Context) {
	t.Helper()
	q := &fakeNotifyQuerier{rows: map[string]sqlc.NotificationTemplate{}, users: map[uuid.UUID]sqlc.User{}}
	id := uuid.New()
	q.users[id] = sqlc.User{ID: id, IsSuperuser: true, IsActive: true}
	ctx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{ID: id.String()})
	return NewNotificationTemplateHandler(q, nil), q, ctx
}

// withNotifyURLParam wraps a request so chi.URLParam(r, "key")
// returns the supplied value. Avoids spinning up the full router for
// unit tests.
func withNotifyURLParam(req *http.Request, name, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(name, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestHandler_RequiresSuperuser(t *testing.T) {
	q := &fakeNotifyQuerier{rows: map[string]sqlc.NotificationTemplate{}, users: map[uuid.UUID]sqlc.User{}}
	id := uuid.New()
	q.users[id] = sqlc.User{ID: id, IsSuperuser: false, IsActive: true}
	ctx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{ID: id.String()})
	h := NewNotificationTemplateHandler(q, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/notification-templates/", nil).WithContext(ctx)
	h.List(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandler_List(t *testing.T) {
	h, _, ctx := newNotifyHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/notification-templates/", nil).WithContext(ctx)
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(wrap.Data.Items) != len(notify.Registry()) {
		t.Errorf("got %d items, want %d", len(wrap.Data.Items), len(notify.Registry()))
	}
}

func TestHandler_GetReturnsDefaults(t *testing.T) {
	h, _, ctx := newNotifyHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/notification-templates/"+notify.KeyEmailPasswordReset+"/", nil).WithContext(ctx)
	req = withNotifyURLParam(req, "key", notify.KeyEmailPasswordReset)
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data templateDetail `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("json: %v", err)
	}
	resp := wrap.Data
	if resp.HasOverride {
		t.Errorf("expected has_override=false")
	}
	def, _ := notify.Lookup(notify.KeyEmailPasswordReset)
	if resp.Body != def.Body {
		t.Errorf("body drift from registry default")
	}
}

func TestHandler_UpdateRoundTrip(t *testing.T) {
	h, q, ctx := newNotifyHandler(t)
	body, _ := json.Marshal(templateUpsert{
		Subject: strPtr("CUSTOM SUBJECT {{.Branding.ProductName}}"),
		Body:    strPtr("hello {{.Data.Username}}"),
		Enabled: boolPtr(true),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body)).WithContext(ctx)
	req = withNotifyURLParam(req, "key", notify.KeyEmailAccountLocked)
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if row, ok := q.rows[notify.KeyEmailAccountLocked]; !ok {
		t.Fatal("row was not persisted")
	} else if row.BodyTpl != "hello {{.Data.Username}}" {
		t.Errorf("body not stored: %q", row.BodyTpl)
	}
}

func TestHandler_ResetRemovesOverride(t *testing.T) {
	h, q, ctx := newNotifyHandler(t)
	q.rows[notify.KeyEmailAccountLocked] = sqlc.NotificationTemplate{
		TemplateKey: notify.KeyEmailAccountLocked,
		Channel:     notify.ChannelEmail,
		BodyTpl:     "x",
		Enabled:     true,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(ctx)
	req = withNotifyURLParam(req, "key", notify.KeyEmailAccountLocked)
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := q.rows[notify.KeyEmailAccountLocked]; ok {
		t.Error("row not removed")
	}
}

func TestHandler_PreviewRendersAgainstSample(t *testing.T) {
	h, _, ctx := newNotifyHandler(t)
	body, _ := json.Marshal(previewRequest{
		Subject: "Hello {{.Branding.ProductName}}",
		Body:    "User {{.Data.Username}}",
		Variables: map[string]any{
			"Branding": map[string]any{"ProductName": "Astronomer", "SupportURL": "x", "LoginURL": "y"},
			"Data":     map[string]any{"Username": "alice", "UnlockAt": "soon"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).WithContext(ctx)
	req = withNotifyURLParam(req, "key", notify.KeyEmailAccountLocked)
	h.Preview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data previewResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("json: %v", err)
	}
	resp := wrap.Data
	if resp.Subject != "Hello Astronomer" {
		t.Errorf("subject: %q", resp.Subject)
	}
	if resp.Body != "User alice" {
		t.Errorf("body: %q", resp.Body)
	}
}

func TestHandler_PreviewRejectsMissingRequiredVariable(t *testing.T) {
	h, _, ctx := newNotifyHandler(t)
	// password_reset requires Data.ResetURL + Branding.ProductName etc.
	body, _ := json.Marshal(previewRequest{
		Variables: map[string]any{
			// Missing Data.ResetURL.
			"Branding": map[string]any{"ProductName": "P", "SupportURL": "S", "LoginURL": "L"},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)).WithContext(ctx)
	req = withNotifyURLParam(req, "key", notify.KeyEmailPasswordReset)
	h.Preview(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preview missing-required: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data struct {
			Missing []string `json:"missing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(wrap.Data.Missing) == 0 {
		t.Errorf("expected missing[] in response, got body=%s", rec.Body.String())
	}
}

func TestHandler_VariablesEndpoint(t *testing.T) {
	h, _, ctx := newNotifyHandler(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req = withNotifyURLParam(req, "key", notify.KeyEmailPasswordReset)
	h.Variables(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("variables: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
