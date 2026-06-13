package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeExtensionQuerier struct {
	mu    sync.Mutex
	rows  map[string]sqlc.UIExtension
	audit int
}

func newFakeExtensionQuerier() *fakeExtensionQuerier {
	return &fakeExtensionQuerier{rows: map[string]sqlc.UIExtension{}}
}

func (f *fakeExtensionQuerier) ListUIExtensions(context.Context) ([]sqlc.UIExtension, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.UIExtension, 0, len(f.rows))
	for _, row := range f.rows {
		out = append(out, row)
	}
	return out, nil
}

func (f *fakeExtensionQuerier) UpsertUIExtension(_ context.Context, arg sqlc.UpsertUIExtensionParams) (sqlc.UIExtension, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.UIExtension{
		ID:                  uuid.New(),
		Name:                arg.Name,
		DisplayName:         arg.DisplayName,
		Version:             arg.Version,
		Source:              arg.Source,
		Checksum:            arg.Checksum,
		Enabled:             arg.Enabled,
		CompatibilityStatus: arg.CompatibilityStatus,
		Manifest:            arg.Manifest,
		InstalledBy:         arg.InstalledBy,
		InstalledAt:         time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	if existing, ok := f.rows[arg.Name]; ok {
		row.ID = existing.ID
		row.InstalledAt = existing.InstalledAt
	}
	f.rows[arg.Name] = row
	return row, nil
}

func (f *fakeExtensionQuerier) SetUIExtensionEnabled(_ context.Context, arg sqlc.SetUIExtensionEnabledParams) (sqlc.UIExtension, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[arg.Name]
	if !ok {
		return sqlc.UIExtension{}, pgx.ErrNoRows
	}
	row.Enabled = arg.Enabled
	row.UpdatedAt = time.Now().UTC()
	f.rows[arg.Name] = row
	return row, nil
}

func (f *fakeExtensionQuerier) CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error {
	f.audit++
	return nil
}

func extensionReq(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "cost-insights")
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func decodeExtensionResp[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var wrapped struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode body: %v body=%s", err, rr.Body.String())
	}
	return wrapped.Data
}

func TestExtensionHandler_ValidateRejectsUnsafeManifest(t *testing.T) {
	h := NewExtensionHandler(newFakeExtensionQuerier())
	h.SetCurrentVersion("0.9.1")
	body := `{
		"apiVersion":"extensions.astronomer.io/v1alpha1",
		"name":"bad-extension",
		"version":"0.1.0",
		"compatibleAstronomer":">=0.9.0 <1.0.0",
		"entry":"../index.js",
		"permissions":["clusters"],
		"csp":{"scriptSrc":["'unsafe-eval'"]},
		"extensionPoints":{"sidebar":[{"label":"Bad","path":"/dashboard/extensions/bad-extension"}]}
	}`
	rr := httptest.NewRecorder()
	h.Validate(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/validate/", body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeExtensionResp[ExtensionValidationResponse](t, rr)
	if resp.Valid {
		t.Fatalf("valid=true, want false")
	}
	if len(resp.Errors) < 3 {
		t.Fatalf("expected entry, permission, and csp errors, got %+v", resp.Errors)
	}
}

func TestExtensionHandler_InstallPersistsCompatibleManifest(t *testing.T) {
	q := newFakeExtensionQuerier()
	h := NewExtensionHandler(q)
	h.SetCurrentVersion("0.9.1")
	manifest := sampleExtensionManifest()
	raw, _ := json.Marshal(InstallExtensionRequest{Manifest: manifest, Source: "unit-test", Enable: true})
	rr := httptest.NewRecorder()
	h.Install(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/", string(raw)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeExtensionResp[ExtensionRecordResponse](t, rr)
	if !resp.Enabled {
		t.Fatalf("enabled=false, want true")
	}
	if resp.CompatibilityStatus != "compatible" {
		t.Fatalf("compatibility=%q", resp.CompatibilityStatus)
	}
	if q.audit != 1 {
		t.Fatalf("audit rows=%d, want 1", q.audit)
	}
}

func TestExtensionHandler_EnableBlocksIncompatibleExtension(t *testing.T) {
	q := newFakeExtensionQuerier()
	manifest := sampleExtensionManifest()
	manifest.CompatibleAstronomer = ">=2.0.0"
	raw, _ := json.Marshal(manifest)
	q.rows["cost-insights"] = sqlc.UIExtension{
		ID:                  uuid.New(),
		Name:                "cost-insights",
		DisplayName:         "Cost Insights",
		Version:             "0.1.0",
		Source:              "unit-test",
		CompatibilityStatus: "incompatible",
		Manifest:            raw,
		InstalledAt:         time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	h := NewExtensionHandler(q)
	h.SetCurrentVersion("0.9.1")
	rr := httptest.NewRecorder()
	h.Enable(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/cost-insights/enable/", ""))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
