package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeWebhookQuerier is a hand-rolled minimal implementation of
// WebhookQuerier for the handler tests.
type fakeWebhookQuerier struct {
	mu          sync.Mutex
	subsByID    map[uuid.UUID]sqlc.WebhookSubscription
	subsByName  map[string]sqlc.WebhookSubscription
	deliveries  map[uuid.UUID]sqlc.WebhookDelivery
	users       map[uuid.UUID]sqlc.User
	createCount int
	retryCount  int
}

func newFakeWebhookQuerier(users ...sqlc.User) *fakeWebhookQuerier {
	q := &fakeWebhookQuerier{
		subsByID:   map[uuid.UUID]sqlc.WebhookSubscription{},
		subsByName: map[string]sqlc.WebhookSubscription{},
		deliveries: map[uuid.UUID]sqlc.WebhookDelivery{},
		users:      map[uuid.UUID]sqlc.User{},
	}
	for _, u := range users {
		q.users[u.ID] = u
	}
	return q
}

func (f *fakeWebhookQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return u, nil
}

// T6.064 — baseline-guard surface stubs. Default behaviour returns
// ErrNoRows so tests run without setting up an active baseline;
// guard-specific tests can override the methods via embedding.
func (f *fakeWebhookQuerier) GetActiveComplianceBaselineApplication(_ context.Context) (sqlc.ComplianceBaselineApplication, error) {
	return sqlc.ComplianceBaselineApplication{}, pgx.ErrNoRows
}

func (f *fakeWebhookQuerier) GetComplianceBaseline(_ context.Context, _ uuid.UUID) (sqlc.ComplianceBaseline, error) {
	return sqlc.ComplianceBaseline{}, pgx.ErrNoRows
}

func (f *fakeWebhookQuerier) ListWebhookSubscriptions(_ context.Context) ([]sqlc.WebhookSubscription, error) {
	out := make([]sqlc.WebhookSubscription, 0, len(f.subsByID))
	for _, v := range f.subsByID {
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeWebhookQuerier) GetWebhookSubscription(_ context.Context, id uuid.UUID) (sqlc.WebhookSubscription, error) {
	v, ok := f.subsByID[id]
	if !ok {
		return sqlc.WebhookSubscription{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeWebhookQuerier) GetWebhookSubscriptionByName(_ context.Context, name string) (sqlc.WebhookSubscription, error) {
	v, ok := f.subsByName[name]
	if !ok {
		return sqlc.WebhookSubscription{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeWebhookQuerier) CreateWebhookSubscription(_ context.Context, arg sqlc.CreateWebhookSubscriptionParams) (sqlc.WebhookSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.subsByName[arg.Name]; dup {
		return sqlc.WebhookSubscription{}, errors.New("duplicate name")
	}
	row := sqlc.WebhookSubscription{
		ID:              uuid.New(),
		Name:            arg.Name,
		Url:             arg.Url,
		SecretEncrypted: arg.SecretEncrypted,
		EventFilters:    arg.EventFilters,
		PayloadTemplate: arg.PayloadTemplate,
		ExtraHeaders:    arg.ExtraHeaders,
		Enabled:         arg.Enabled,
		MaxRetries:      arg.MaxRetries,
		TimeoutSeconds:  arg.TimeoutSeconds,
		CreatedBy:       arg.CreatedBy,
	}
	f.subsByID[row.ID] = row
	f.subsByName[row.Name] = row
	f.createCount++
	return row, nil
}

func (f *fakeWebhookQuerier) UpdateWebhookSubscription(_ context.Context, arg sqlc.UpdateWebhookSubscriptionParams) (sqlc.WebhookSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.subsByID[arg.ID]
	if !ok {
		return sqlc.WebhookSubscription{}, pgx.ErrNoRows
	}
	delete(f.subsByName, row.Name)
	row.Name = arg.Name
	row.Url = arg.Url
	row.SecretEncrypted = arg.SecretEncrypted
	row.EventFilters = arg.EventFilters
	row.PayloadTemplate = arg.PayloadTemplate
	row.ExtraHeaders = arg.ExtraHeaders
	row.Enabled = arg.Enabled
	row.MaxRetries = arg.MaxRetries
	row.TimeoutSeconds = arg.TimeoutSeconds
	f.subsByID[row.ID] = row
	f.subsByName[row.Name] = row
	return row, nil
}

func (f *fakeWebhookQuerier) DeleteWebhookSubscription(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.subsByID[id]
	if ok {
		delete(f.subsByID, id)
		delete(f.subsByName, row.Name)
	}
	return nil
}

func (f *fakeWebhookQuerier) GetWebhookDelivery(_ context.Context, id uuid.UUID) (sqlc.WebhookDelivery, error) {
	v, ok := f.deliveries[id]
	if !ok {
		return sqlc.WebhookDelivery{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeWebhookQuerier) ListWebhookDeliveriesBySubscription(_ context.Context, arg sqlc.ListWebhookDeliveriesBySubscriptionParams) ([]sqlc.WebhookDelivery, error) {
	out := []sqlc.WebhookDelivery{}
	for _, d := range f.deliveries {
		if d.SubscriptionID == arg.SubscriptionID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeWebhookQuerier) CountWebhookDeliveriesBySubscription(_ context.Context, id uuid.UUID) (int64, error) {
	n := int64(0)
	for _, d := range f.deliveries {
		if d.SubscriptionID == id {
			n++
		}
	}
	return n, nil
}

func (f *fakeWebhookQuerier) InsertWebhookDelivery(_ context.Context, arg sqlc.InsertWebhookDeliveryParams) (sqlc.WebhookDelivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.WebhookDelivery{
		ID:             uuid.New(),
		SubscriptionID: arg.SubscriptionID,
		EventName:      arg.EventName,
		EventID:        arg.EventID,
		Payload:        arg.Payload,
		PayloadSize:    arg.PayloadSize,
		Status:         arg.Status,
		NextAttemptAt:  arg.NextAttemptAt,
	}
	f.deliveries[row.ID] = row
	return row, nil
}

func (f *fakeWebhookQuerier) RetryWebhookDelivery(_ context.Context, arg sqlc.RetryWebhookDeliveryParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retryCount++
	if row, ok := f.deliveries[arg.ID]; ok {
		row.Status = "queued"
		row.NextAttemptAt = arg.NextAttemptAt
		f.deliveries[arg.ID] = row
	}
	return nil
}

// authedWebhookRequest builds a request authenticated as the given user.
func authedWebhookRequest(method, target string, callerID uuid.UUID, body []byte) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	}
	ctx := middleware.SetAuthenticatedUserForTest(r.Context(), &middleware.AuthenticatedUser{
		ID:         callerID.String(),
		AuthMethod: "jwt",
	})
	return r.WithContext(ctx)
}

func withChiParam(r *http.Request, k, v string) *http.Request {
	// Reuse an existing chi.RouteContext on the request if present so
	// callers can stack multiple params without each call wiping the
	// previous one.
	rctx, ok := r.Context().Value(chi.RouteCtxKey).(*chi.Context)
	if !ok || rctx == nil {
		rctx = chi.NewRouteContext()
	}
	rctx.URLParams.Add(k, v)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func newWebhookTestHandler(t *testing.T, q WebhookQuerier) *WebhookHandler {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	return NewWebhookHandler(q, enc, nil)
}

func TestWebhooksHandler_CRUD(t *testing.T) {
	superID := uuid.New()
	q := newFakeWebhookQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newWebhookTestHandler(t, q)

	// 1) Create.
	createBody, _ := json.Marshal(map[string]any{
		"name":           "slack-audit",
		"url":            "https://hooks.slack.com/services/T/B/X",
		"secret":         "swordfish",
		"event_filters":  []string{"audit.*"},
		"max_retries":    3,
		"timeout_seconds": 15,
	})
	w := httptest.NewRecorder()
	h.Create(w, authedWebhookRequest(http.MethodPost, "/api/v1/admin/webhooks/", superID, createBody))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data subscriptionResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.Name != "slack-audit" {
		t.Errorf("name mismatch: %q", created.Data.Name)
	}
	if created.Data.Secret != SecretSentinel {
		t.Errorf("secret leaked: %q", created.Data.Secret)
	}
	if !created.Data.SecretConfigured {
		t.Errorf("secret_configured should be true after create")
	}
	subID, err := uuid.Parse(created.Data.ID)
	if err != nil {
		t.Fatalf("invalid subscription id: %v", err)
	}

	// 2) Get.
	w = httptest.NewRecorder()
	getReq := withChiParam(
		authedWebhookRequest(http.MethodGet, "/api/v1/admin/webhooks/"+subID.String()+"/", superID, nil),
		"id", subID.String(),
	)
	h.Get(w, getReq)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", w.Code, w.Body.String())
	}

	// 3) Update (toggle enabled, change filters, omit secret).
	updateBody, _ := json.Marshal(map[string]any{
		"enabled":       false,
		"event_filters": []string{"audit.*", "cluster.*"},
	})
	w = httptest.NewRecorder()
	updReq := withChiParam(
		authedWebhookRequest(http.MethodPut, "/api/v1/admin/webhooks/"+subID.String()+"/", superID, updateBody),
		"id", subID.String(),
	)
	h.Update(w, updReq)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", w.Code, w.Body.String())
	}
	var updated struct {
		Data subscriptionResponse `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Data.Enabled {
		t.Errorf("expected enabled=false after update")
	}
	if len(updated.Data.EventFilters) != 2 {
		t.Errorf("expected 2 filters after update, got %d", len(updated.Data.EventFilters))
	}

	// 4) List.
	w = httptest.NewRecorder()
	h.List(w, authedWebhookRequest(http.MethodGet, "/api/v1/admin/webhooks/", superID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", w.Code, w.Body.String())
	}

	// 5) Delete.
	w = httptest.NewRecorder()
	delReq := withChiParam(
		authedWebhookRequest(http.MethodDelete, "/api/v1/admin/webhooks/"+subID.String()+"/", superID, nil),
		"id", subID.String(),
	)
	h.Delete(w, delReq)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", w.Code, w.Body.String())
	}
	if _, ok := q.subsByID[subID]; ok {
		t.Errorf("delete did not remove row from store")
	}
}

func TestWebhooksHandler_TestEndpoint(t *testing.T) {
	superID := uuid.New()
	q := newFakeWebhookQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newWebhookTestHandler(t, q)

	// Seed a subscription so we have a target.
	subID := uuid.New()
	q.subsByID[subID] = sqlc.WebhookSubscription{
		ID:           subID,
		Name:         "test-target",
		Url:          "https://x.invalid",
		EventFilters: json.RawMessage(`["*"]`),
		ExtraHeaders: json.RawMessage(`{}`),
		Enabled:      true,
	}
	q.subsByName["test-target"] = q.subsByID[subID]

	w := httptest.NewRecorder()
	req := withChiParam(
		authedWebhookRequest(http.MethodPost, "/api/v1/admin/webhooks/"+subID.String()+"/test/", superID, []byte("{}")),
		"id", subID.String(),
	)
	h.Test(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("test endpoint: status=%d body=%s", w.Code, w.Body.String())
	}
	// Ensure a delivery row was queued — this is the
	// "synthetic event flows through the same pipeline" contract.
	if got := len(q.deliveries); got != 1 {
		t.Errorf("expected 1 queued delivery, got %d", got)
	}
	for _, d := range q.deliveries {
		if d.EventName != "webhook.test_ping" {
			t.Errorf("event_name = %q, want webhook.test_ping", d.EventName)
		}
		if d.Status != "queued" {
			t.Errorf("status = %q, want queued", d.Status)
		}
	}
}

func TestWebhooksHandler_RequiresSuperuser(t *testing.T) {
	regularID := uuid.New()
	q := newFakeWebhookQuerier(sqlc.User{ID: regularID, IsSuperuser: false})
	h := newWebhookTestHandler(t, q)

	cases := []struct {
		name    string
		method  string
		fn      func(http.ResponseWriter, *http.Request)
		body    []byte
		params  map[string]string
	}{
		{
			name:   "list",
			method: http.MethodGet,
			fn:     h.List,
		},
		{
			name:   "create",
			method: http.MethodPost,
			fn:     h.Create,
			body:   []byte(`{"name":"x","url":"https://x","secret":"s"}`),
		},
		{
			name:   "get",
			method: http.MethodGet,
			fn:     h.Get,
			params: map[string]string{"id": uuid.New().String()},
		},
		{
			name:   "update",
			method: http.MethodPut,
			fn:     h.Update,
			body:   []byte(`{}`),
			params: map[string]string{"id": uuid.New().String()},
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			fn:     h.Delete,
			params: map[string]string{"id": uuid.New().String()},
		},
		{
			name:   "test",
			method: http.MethodPost,
			fn:     h.Test,
			body:   []byte(`{}`),
			params: map[string]string{"id": uuid.New().String()},
		},
		{
			name:   "deliveries",
			method: http.MethodGet,
			fn:     h.Deliveries,
			params: map[string]string{"id": uuid.New().String()},
		},
		{
			name:   "retry_delivery",
			method: http.MethodPost,
			fn:     h.RetryDelivery,
			params: map[string]string{"id": uuid.New().String(), "delivery_id": uuid.New().String()},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := authedWebhookRequest(tc.method, "/api/v1/admin/webhooks/", regularID, tc.body)
			for k, v := range tc.params {
				req = withChiParam(req, k, v)
			}
			tc.fn(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("expected 403 for non-superuser, got %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWebhooksHandler_RejectsUnsafeTemplate(t *testing.T) {
	superID := uuid.New()
	q := newFakeWebhookQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newWebhookTestHandler(t, q)

	// {{ call .Exec ... }} would be a foot-gun on a richer data bag —
	// the validator rejects it at create time.
	body, _ := json.Marshal(map[string]any{
		"name":             "evil",
		"url":              "https://x.invalid",
		"secret":           "s",
		"event_filters":    []string{"*"},
		"payload_template": `{{ .Exec "/bin/sh" }}`,
	})
	w := httptest.NewRecorder()
	h.Create(w, authedWebhookRequest(http.MethodPost, "/api/v1/admin/webhooks/", superID, body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsafe template, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestWebhooksHandler_DuplicateName_409(t *testing.T) {
	superID := uuid.New()
	q := newFakeWebhookQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	// Seed an existing subscription
	q.subsByName["taken"] = sqlc.WebhookSubscription{ID: uuid.New(), Name: "taken"}

	h := newWebhookTestHandler(t, q)
	body, _ := json.Marshal(map[string]any{
		"name":          "taken",
		"url":           "https://x.invalid",
		"secret":        "s",
		"event_filters": []string{"*"},
	})
	w := httptest.NewRecorder()
	h.Create(w, authedWebhookRequest(http.MethodPost, "/api/v1/admin/webhooks/", superID, body))
	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 on duplicate name, got %d", w.Code)
	}
}

func TestWebhooksHandler_RetryEndpoint(t *testing.T) {
	superID := uuid.New()
	q := newFakeWebhookQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newWebhookTestHandler(t, q)

	subID := uuid.New()
	delID := uuid.New()
	q.subsByID[subID] = sqlc.WebhookSubscription{ID: subID, Name: "x"}
	q.deliveries[delID] = sqlc.WebhookDelivery{ID: delID, SubscriptionID: subID, Status: "dropped"}

	w := httptest.NewRecorder()
	req := withChiParam(
		withChiParam(
			authedWebhookRequest(http.MethodPost, "/api/v1/admin/webhooks/"+subID.String()+"/deliveries/"+delID.String()+"/retry/", superID, nil),
			"id", subID.String(),
		),
		"delivery_id", delID.String(),
	)
	h.RetryDelivery(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("retry: status=%d body=%s", w.Code, w.Body.String())
	}
	if q.retryCount != 1 {
		t.Errorf("retry count = %d, want 1", q.retryCount)
	}
	if q.deliveries[delID].Status != "queued" {
		t.Errorf("delivery status after retry = %q, want queued", q.deliveries[delID].Status)
	}
}

func TestWebhooksHandler_RetryEndpoint_CrossSubscription_404(t *testing.T) {
	superID := uuid.New()
	q := newFakeWebhookQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newWebhookTestHandler(t, q)

	subA := uuid.New()
	subB := uuid.New()
	delID := uuid.New()
	q.subsByID[subA] = sqlc.WebhookSubscription{ID: subA}
	q.subsByID[subB] = sqlc.WebhookSubscription{ID: subB}
	// Delivery belongs to subA but the path references subB.
	q.deliveries[delID] = sqlc.WebhookDelivery{ID: delID, SubscriptionID: subA}

	w := httptest.NewRecorder()
	req := withChiParam(
		withChiParam(
			authedWebhookRequest(http.MethodPost, "/", superID, nil),
			"id", subB.String(),
		),
		"delivery_id", delID.String(),
	)
	h.RetryDelivery(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 on cross-subscription retry, got %d", w.Code)
	}
}
