package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeSIEMQuerier is the in-memory SIEMQuerier the handler tests use.
type fakeSIEMQuerier struct {
	mu         sync.Mutex
	byID       map[uuid.UUID]sqlc.SiemForwarder
	byName     map[string]sqlc.SiemForwarder
	queue      []sqlc.SiemForwardQueue
	status     map[uuid.UUID]sqlc.SiemForwarderStatus
	users      map[uuid.UUID]sqlc.User
	operations map[uuid.UUID]sqlc.SIEMTestOperation
	enqueueCnt int
	auditCnt   int
}

func newFakeSIEMQuerier(users ...sqlc.User) *fakeSIEMQuerier {
	q := &fakeSIEMQuerier{
		byID:       map[uuid.UUID]sqlc.SiemForwarder{},
		byName:     map[string]sqlc.SiemForwarder{},
		status:     map[uuid.UUID]sqlc.SiemForwarderStatus{},
		users:      map[uuid.UUID]sqlc.User{},
		operations: map[uuid.UUID]sqlc.SIEMTestOperation{},
	}
	for _, u := range users {
		q.users[u.ID] = u
	}
	return q
}

func (f *fakeSIEMQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	u, ok := f.users[id]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return u, nil
}

func (f *fakeSIEMQuerier) ListSIEMForwarders(_ context.Context) ([]sqlc.SiemForwarder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.SiemForwarder, 0, len(f.byID))
	for _, v := range f.byID {
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeSIEMQuerier) GetSIEMForwarder(_ context.Context, id uuid.UUID) (sqlc.SiemForwarder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.byID[id]
	if !ok {
		return sqlc.SiemForwarder{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeSIEMQuerier) GetSIEMForwarderByName(_ context.Context, name string) (sqlc.SiemForwarder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.byName[name]
	if !ok {
		return sqlc.SiemForwarder{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeSIEMQuerier) CreateSIEMForwarder(_ context.Context, arg sqlc.CreateSIEMForwarderParams) (sqlc.SiemForwarder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.SiemForwarder{
		ID:              uuid.New(),
		Name:            arg.Name,
		Transport:       arg.Transport,
		Endpoint:        arg.Endpoint,
		AuthEncrypted:   arg.AuthEncrypted,
		EventFilters:    arg.EventFilters,
		Format:          arg.Format,
		TlsSkipVerify:   arg.TlsSkipVerify,
		CaCertPem:       arg.CaCertPem,
		BatchSize:       arg.BatchSize,
		FlushIntervalMs: arg.FlushIntervalMs,
		TimeoutSeconds:  arg.TimeoutSeconds,
		Enabled:         arg.Enabled,
		CreatedBy:       arg.CreatedBy,
	}
	f.byID[row.ID] = row
	f.byName[row.Name] = row
	return row, nil
}

func (f *fakeSIEMQuerier) UpdateSIEMForwarder(_ context.Context, arg sqlc.UpdateSIEMForwarderParams) (sqlc.SiemForwarder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.byID[arg.ID]
	if !ok {
		return sqlc.SiemForwarder{}, pgx.ErrNoRows
	}
	delete(f.byName, row.Name)
	row.Name = arg.Name
	row.Transport = arg.Transport
	row.Endpoint = arg.Endpoint
	row.AuthEncrypted = arg.AuthEncrypted
	row.EventFilters = arg.EventFilters
	row.Format = arg.Format
	row.TlsSkipVerify = arg.TlsSkipVerify
	row.CaCertPem = arg.CaCertPem
	row.BatchSize = arg.BatchSize
	row.FlushIntervalMs = arg.FlushIntervalMs
	row.TimeoutSeconds = arg.TimeoutSeconds
	row.Enabled = arg.Enabled
	f.byID[row.ID] = row
	f.byName[row.Name] = row
	return row, nil
}

func (f *fakeSIEMQuerier) DeleteSIEMForwarder(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.byID[id]
	if ok {
		delete(f.byName, row.Name)
	}
	delete(f.byID, id)
	delete(f.status, id)
	return nil
}

func (f *fakeSIEMQuerier) EnqueueSIEMEvent(_ context.Context, arg sqlc.EnqueueSIEMEventParams) (sqlc.SiemForwardQueue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueueCnt++
	row := sqlc.SiemForwardQueue{
		ID:          int64(f.enqueueCnt),
		ForwarderID: arg.ForwarderID,
		EventName:   arg.EventName,
		Payload:     arg.Payload,
		Severity:    arg.Severity,
	}
	f.queue = append(f.queue, row)
	return row, nil
}

func (f *fakeSIEMQuerier) GetSIEMForwarderStatus(_ context.Context, id uuid.UUID) (sqlc.SiemForwarderStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.status[id]
	if !ok {
		return sqlc.SiemForwarderStatus{}, pgx.ErrNoRows
	}
	return v, nil
}

func (f *fakeSIEMQuerier) CountSIEMQueueByForwarder(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, row := range f.queue {
		if row.ForwarderID == id {
			n++
		}
	}
	return n, nil
}

// CreateAuditLogV1 lets the fake double as an AuthAuditWriter so the
// handler's recordAudit calls don't crash.
func (f *fakeSIEMQuerier) CreateAuditLogV1(_ context.Context, _ sqlc.CreateAuditLogV1Params) error {
	return nil
}

func (f *fakeSIEMQuerier) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.mu.Lock()
	f.auditCnt++
	f.mu.Unlock()
	return sqlc.AuditOutbox{ID: arg.ID, DedupeKey: arg.DedupeKey}, nil
}

func (f *fakeSIEMQuerier) CreateSIEMTestOperationAndQueue(_ context.Context, arg sqlc.CreateSIEMTestOperationAndQueueParams) (sqlc.SIEMTestOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.operations[arg.ID]; ok {
		existing.Created = false
		return existing, nil
	}
	f.enqueueCnt++
	row := sqlc.SiemForwardQueue{ID: int64(f.enqueueCnt), ForwarderID: arg.ForwarderID, EventName: "siem.test_ping", Payload: arg.Payload, Severity: "info"}
	f.queue = append(f.queue, row)
	now := time.Now().UTC()
	operation := sqlc.SIEMTestOperation{ID: arg.ID, ForwarderID: arg.ForwarderID, QueueID: row.ID, Status: "pending", RequestedBy: arg.RequestedBy, CreatedAt: now, UpdatedAt: now, Created: true}
	f.operations[arg.ID] = operation
	return operation, nil
}

func (f *fakeSIEMQuerier) GetSIEMTestOperation(_ context.Context, arg sqlc.GetSIEMTestOperationParams) (sqlc.SIEMTestOperation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.operations[arg.ID]
	if !ok || row.ForwarderID != arg.ForwarderID {
		return sqlc.SIEMTestOperation{}, pgx.ErrNoRows
	}
	return row, nil
}

func authedSIEMRequest(method, target string, callerID uuid.UUID, body []byte) *http.Request {
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

func newSIEMTestHandler(t *testing.T, q *fakeSIEMQuerier) *SIEMHandler {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	h := NewSIEMHandler(q, enc, nil)
	h.SetAuditWriter(q)
	h.SetRunTx(func(_ context.Context, fn func(SIEMMutationTx) error) error { return fn(q) })
	return h
}

func TestSIEMHandler_CRUD(t *testing.T) {
	superID := uuid.New()
	q := newFakeSIEMQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newSIEMTestHandler(t, q)

	// Create
	createBody, _ := json.Marshal(map[string]any{
		"name":          "splunk-audit",
		"transport":     "splunk_hec",
		"endpoint":      "https://splunk.example.com:8088",
		"auth":          `{"token":"secret-token"}`,
		"event_filters": []string{"audit.*"},
		"batch_size":    50,
	})
	w := httptest.NewRecorder()
	h.Create(w, authedSIEMRequest(http.MethodPost, "/api/v1/admin/siem-forwarders/", superID, createBody))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data siemForwarderResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.Name != "splunk-audit" {
		t.Errorf("name mismatch: %q", created.Data.Name)
	}
	if created.Data.Auth != SIEMAuthSentinel {
		t.Errorf("auth leaked: %q", created.Data.Auth)
	}
	if !created.Data.AuthConfigured {
		t.Errorf("auth_configured should be true after create")
	}
	if created.Data.BatchSize != 50 {
		t.Errorf("batch_size mismatch: %d", created.Data.BatchSize)
	}
	fwdID, err := uuid.Parse(created.Data.ID)
	if err != nil {
		t.Fatalf("invalid forwarder id: %v", err)
	}

	// Get
	w = httptest.NewRecorder()
	getReq := withChiParam(
		authedSIEMRequest(http.MethodGet, "/api/v1/admin/siem-forwarders/"+fwdID.String()+"/", superID, nil),
		"id", fwdID.String(),
	)
	h.Get(w, getReq)
	if w.Code != http.StatusOK {
		t.Fatalf("get: status=%d body=%s", w.Code, w.Body.String())
	}

	// Update (toggle enabled, change filters, omit auth — handler must
	// preserve the existing ciphertext).
	updateBody, _ := json.Marshal(map[string]any{
		"enabled":       false,
		"event_filters": []string{"audit.*", "cluster.*"},
	})
	w = httptest.NewRecorder()
	updReq := withChiParam(
		authedSIEMRequest(http.MethodPut, "/api/v1/admin/siem-forwarders/"+fwdID.String()+"/", superID, updateBody),
		"id", fwdID.String(),
	)
	h.Update(w, updReq)
	if w.Code != http.StatusOK {
		t.Fatalf("update: status=%d body=%s", w.Code, w.Body.String())
	}
	q.mu.Lock()
	if q.byID[fwdID].AuthEncrypted == "" {
		t.Errorf("update with omitted auth should have preserved the ciphertext")
	}
	if q.byID[fwdID].Enabled {
		t.Errorf("enabled should be false after update")
	}
	q.mu.Unlock()

	// List
	w = httptest.NewRecorder()
	h.List(w, authedSIEMRequest(http.MethodGet, "/api/v1/admin/siem-forwarders/", superID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", w.Code, w.Body.String())
	}

	// Delete
	w = httptest.NewRecorder()
	delReq := withChiParam(
		authedSIEMRequest(http.MethodDelete, "/api/v1/admin/siem-forwarders/"+fwdID.String()+"/", superID, nil),
		"id", fwdID.String(),
	)
	h.Delete(w, delReq)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSIEMHandler_TestEndpoint(t *testing.T) {
	superID := uuid.New()
	q := newFakeSIEMQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	fwdID := uuid.New()
	q.byID[fwdID] = sqlc.SiemForwarder{
		ID:        fwdID,
		Name:      "splunk",
		Transport: "splunk_hec",
		Endpoint:  "https://splunk:8088",
		Enabled:   true,
	}
	q.byName["splunk"] = q.byID[fwdID]
	h := newSIEMTestHandler(t, q)

	w := httptest.NewRecorder()
	req := withChiParam(
		authedSIEMRequest(http.MethodPost, "/api/v1/admin/siem-forwarders/"+fwdID.String()+"/test/", superID, nil),
		"id", fwdID.String(),
	)
	req.Header.Set("Idempotency-Key", "siem-test-1")
	h.Test(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("test endpoint status=%d body=%s", w.Code, w.Body.String())
	}
	q.mu.Lock()
	if q.enqueueCnt != 1 {
		t.Errorf("expected 1 enqueue; got %d", q.enqueueCnt)
	}
	if len(q.queue) == 0 || q.queue[0].EventName != "siem.test_ping" {
		t.Errorf("queue payload not test ping: %+v", q.queue)
	}
	q.mu.Unlock()

	replay := httptest.NewRecorder()
	h.Test(replay, req.Clone(req.Context()))
	if replay.Code != http.StatusAccepted || replay.Header().Get("Location") != w.Header().Get("Location") {
		t.Fatalf("replay status=%d location=%q body=%s", replay.Code, replay.Header().Get("Location"), replay.Body.String())
	}
	q.mu.Lock()
	if q.enqueueCnt != 1 || q.auditCnt != 1 {
		t.Fatalf("replay duplicated queue/audit: %d/%d", q.enqueueCnt, q.auditCnt)
	}
	q.mu.Unlock()
}

func TestSIEMHandlerTestRequiresIdempotencyKey(t *testing.T) {
	superID, fwdID := uuid.New(), uuid.New()
	q := newFakeSIEMQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	q.byID[fwdID] = sqlc.SiemForwarder{ID: fwdID, Name: "sink", Transport: "syslog_udp", Enabled: true}
	h := newSIEMTestHandler(t, q)
	req := withChiParam(authedSIEMRequest(http.MethodPost, "/", superID, nil), "id", fwdID.String())
	recorder := httptest.NewRecorder()
	h.Test(recorder, req)
	if recorder.Code != http.StatusBadRequest || q.enqueueCnt != 0 {
		t.Fatalf("missing key status=%d enqueues=%d body=%s", recorder.Code, q.enqueueCnt, recorder.Body.String())
	}
}

func TestSIEMHandler_RequiresSuperuser(t *testing.T) {
	regularID := uuid.New()
	q := newFakeSIEMQuerier(sqlc.User{ID: regularID, IsSuperuser: false})
	h := newSIEMTestHandler(t, q)

	// List should be 403.
	w := httptest.NewRecorder()
	h.List(w, authedSIEMRequest(http.MethodGet, "/api/v1/admin/siem-forwarders/", regularID, nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-superuser List: status=%d (want 403)", w.Code)
	}
	// Create should be 403.
	body, _ := json.Marshal(map[string]any{"name": "x", "transport": "syslog_udp", "endpoint": "1.2.3.4:514"})
	w = httptest.NewRecorder()
	h.Create(w, authedSIEMRequest(http.MethodPost, "/api/v1/admin/siem-forwarders/", regularID, body))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-superuser Create: status=%d (want 403)", w.Code)
	}
	// Test should be 403.
	w = httptest.NewRecorder()
	req := withChiParam(
		authedSIEMRequest(http.MethodPost, "/api/v1/admin/siem-forwarders/abc/test/", regularID, nil),
		"id", uuid.New().String(),
	)
	h.Test(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("non-superuser Test: status=%d (want 403)", w.Code)
	}
}

func TestSIEMHandler_ValidationRejectsBadTransport(t *testing.T) {
	superID := uuid.New()
	q := newFakeSIEMQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newSIEMTestHandler(t, q)

	body, _ := json.Marshal(map[string]any{
		"name":      "x",
		"transport": "nonsense",
		"endpoint":  "1.2.3.4:514",
	})
	w := httptest.NewRecorder()
	h.Create(w, authedSIEMRequest(http.MethodPost, "/api/v1/admin/siem-forwarders/", superID, body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid transport should 400; got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSIEMHandler_StatusReadsLiveQueueDepth(t *testing.T) {
	superID := uuid.New()
	q := newFakeSIEMQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	fwdID := uuid.New()
	q.byID[fwdID] = sqlc.SiemForwarder{ID: fwdID, Name: "a", Transport: "syslog_udp", Endpoint: "x", Enabled: true}
	q.queue = []sqlc.SiemForwardQueue{
		{ID: 1, ForwarderID: fwdID}, {ID: 2, ForwarderID: fwdID}, {ID: 3, ForwarderID: fwdID},
	}
	q.status[fwdID] = sqlc.SiemForwarderStatus{
		ForwarderID:     fwdID,
		LastSentAt:      pgtype.Timestamptz{},
		LastError:       "boom",
		DroppedTotal:    7,
		DispatchedTotal: 99,
	}
	h := newSIEMTestHandler(t, q)
	w := httptest.NewRecorder()
	req := withChiParam(
		authedSIEMRequest(http.MethodGet, "/api/v1/admin/siem-forwarders/"+fwdID.String()+"/status/", superID, nil),
		"id", fwdID.String(),
	)
	h.Status(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: code=%d body=%s", w.Code, w.Body.String())
	}
	var resp siemStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if resp.QueueDepth != 3 {
		t.Errorf("queue_depth mismatch: %d", resp.QueueDepth)
	}
	if resp.LastError != "boom" {
		t.Errorf("last_error mismatch: %q", resp.LastError)
	}
	if resp.DroppedTotal != 7 {
		t.Errorf("dropped_total mismatch: %d", resp.DroppedTotal)
	}
	if resp.DispatchedTotal != 99 {
		t.Errorf("dispatched_total mismatch: %d", resp.DispatchedTotal)
	}
}
