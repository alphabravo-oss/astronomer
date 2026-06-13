package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeSettingsQuerier is the in-memory PlatformSettingsQuerier used by
// the handler tests. The map is keyed by setting key; values are raw
// JSONB. The audit method (CreateAuditLogV1) is implemented so that
// recordAudit doesn't no-op silently.
type fakeSettingsQuerier struct {
	mu       sync.Mutex
	user     sqlc.User
	userErr  error
	rows     map[string]sqlc.PlatformSetting
	auditOps []string
}

func newFakeSettingsQuerier(user sqlc.User) *fakeSettingsQuerier {
	return &fakeSettingsQuerier{
		user: user,
		rows: map[string]sqlc.PlatformSetting{},
	}
}

func (f *fakeSettingsQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, f.userErr
}

func (f *fakeSettingsQuerier) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[key]
	if !ok {
		return sqlc.PlatformSetting{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeSettingsQuerier) ListPlatformSettings(_ context.Context) ([]sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.PlatformSetting, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeSettingsQuerier) ListPlatformSettingsByPrefix(_ context.Context, prefix string) ([]sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.PlatformSetting{}
	for k, r := range f.rows {
		if strings.HasPrefix(k, prefix) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSettingsQuerier) UpsertPlatformSetting(_ context.Context, arg sqlc.UpsertPlatformSettingParams) (sqlc.PlatformSetting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row := sqlc.PlatformSetting{
		Key:         arg.Key,
		Value:       arg.Value,
		Description: arg.Description,
		UpdatedBy:   arg.UpdatedBy,
	}
	f.rows[arg.Key] = row
	return row, nil
}

func (f *fakeSettingsQuerier) DeletePlatformSetting(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, key)
	return nil
}

// CreateAuditLogV1 satisfies auditWriterV1 so recordAudit() inside the
// PUT / DELETE handlers writes through. We only need to count calls.
func (f *fakeSettingsQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditOps = append(f.auditOps, arg.Action)
	return nil
}

// authedRequest builds an httptest request with an injected authenticated user.
func authedRequest(method, target string, callerID uuid.UUID, body []byte) *http.Request {
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

// withURLParam attaches a chi URLParam to the request (chi normally
// does this during routing; tests need to do it manually).
func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestSettings_GetSetDeleteCycle(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSettingsQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewPlatformSettingsHandler(q)

	// 1) Initial GET → should return the registry default.
	w := httptest.NewRecorder()
	req := withURLParam(
		authedRequest(http.MethodGet, "/api/v1/admin/settings/branding.product_name/", callerID, nil),
		"key", "branding.product_name",
	)
	h.Get(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("initial GET status = %d, body=%s", w.Code, w.Body.String())
	}
	var initial map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode initial: %v", err)
	}
	var initialPayload settingResponse
	if err := json.Unmarshal(initial["data"], &initialPayload); err != nil {
		t.Fatalf("decode initial data: %v", err)
	}
	if !initialPayload.IsDefault {
		t.Fatalf("initial is_default = false, want true")
	}
	if string(initialPayload.Value) != `"Astronomer"` {
		t.Fatalf("initial value = %s, want \"Astronomer\"", initialPayload.Value)
	}

	// 2) PUT a new value.
	put := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/admin/settings/branding.product_name/", callerID, []byte(`{"value":"Megacorp"}`)),
		"key", "branding.product_name",
	)
	pw := httptest.NewRecorder()
	h.Update(pw, put)
	if pw.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", pw.Code, pw.Body.String())
	}

	// 3) GET → should return Megacorp.
	w2 := httptest.NewRecorder()
	g2 := withURLParam(
		authedRequest(http.MethodGet, "/api/v1/admin/settings/branding.product_name/", callerID, nil),
		"key", "branding.product_name",
	)
	h.Get(w2, g2)
	var second map[string]json.RawMessage
	_ = json.Unmarshal(w2.Body.Bytes(), &second)
	var secondPayload settingResponse
	_ = json.Unmarshal(second["data"], &secondPayload)
	if secondPayload.IsDefault {
		t.Fatalf("after PUT is_default = true, want false")
	}
	if string(secondPayload.Value) != `"Megacorp"` {
		t.Fatalf("after PUT value = %s, want \"Megacorp\"", secondPayload.Value)
	}

	// 4) DELETE → resets.
	dw := httptest.NewRecorder()
	d := withURLParam(
		authedRequest(http.MethodDelete, "/api/v1/admin/settings/branding.product_name/", callerID, nil),
		"key", "branding.product_name",
	)
	h.Delete(dw, d)
	if dw.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d", dw.Code)
	}

	// 5) GET → back to default.
	w3 := httptest.NewRecorder()
	g3 := withURLParam(
		authedRequest(http.MethodGet, "/api/v1/admin/settings/branding.product_name/", callerID, nil),
		"key", "branding.product_name",
	)
	h.Get(w3, g3)
	var third map[string]json.RawMessage
	_ = json.Unmarshal(w3.Body.Bytes(), &third)
	var thirdPayload settingResponse
	_ = json.Unmarshal(third["data"], &thirdPayload)
	if !thirdPayload.IsDefault {
		t.Fatalf("after DELETE is_default = false, want true")
	}

	// Audit: one update + one reset.
	wantActions := []string{"admin.platform_settings.updated", "admin.platform_settings.reset"}
	if len(q.auditOps) != len(wantActions) {
		t.Fatalf("audit ops = %v, want %v", q.auditOps, wantActions)
	}
	for i, a := range wantActions {
		if q.auditOps[i] != a {
			t.Fatalf("audit ops[%d] = %q, want %q", i, q.auditOps[i], a)
		}
	}
}

func TestSettings_RejectsUnknownKey(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSettingsQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewPlatformSettingsHandler(q)

	// PUT to an unknown key → 404 unknown_key.
	put := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/admin/settings/not.real/", callerID, []byte(`{"value":"x"}`)),
		"key", "not.real",
	)
	w := httptest.NewRecorder()
	h.Update(w, put)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unknown_key") {
		t.Fatalf("body missing unknown_key: %s", w.Body.String())
	}
}

func TestSettings_RejectsBadType(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSettingsQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	h := NewPlatformSettingsHandler(q)

	// feature.catalog is a bool — pushing "yes" should be rejected.
	put := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/admin/settings/feature.catalog/", callerID, []byte(`{"value":"yes"}`)),
		"key", "feature.catalog",
	)
	w := httptest.NewRecorder()
	h.Update(w, put)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "validation_error") {
		t.Fatalf("body missing validation_error: %s", w.Body.String())
	}

	// And the enum reject.
	bp := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/admin/settings/banner.global_color/", callerID, []byte(`{"value":"pink"}`)),
		"key", "banner.global_color",
	)
	bw := httptest.NewRecorder()
	h.Update(bw, bp)
	if bw.Code != http.StatusBadRequest {
		t.Fatalf("enum status = %d, want 400", bw.Code)
	}

	// Int out of range.
	ttl := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/admin/settings/token.default_ttl_min/", callerID, []byte(`{"value":-1}`)),
		"key", "token.default_ttl_min",
	)
	tw := httptest.NewRecorder()
	h.Update(tw, ttl)
	if tw.Code != http.StatusBadRequest {
		t.Fatalf("int-range status = %d, want 400", tw.Code)
	}
}

func TestSettings_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSettingsQuerier(sqlc.User{ID: callerID, IsSuperuser: false})
	h := NewPlatformSettingsHandler(q)

	get := authedRequest(http.MethodGet, "/api/v1/admin/settings/", callerID, nil)
	w := httptest.NewRecorder()
	h.List(w, get)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestSettings_PublicBrandingNoSecrets(t *testing.T) {
	q := newFakeSettingsQuerier(sqlc.User{IsSuperuser: true})
	// Set a telemetry endpoint AND a branding name. The public branding
	// reader must NOT include the telemetry row.
	q.rows["telemetry.endpoint"] = sqlc.PlatformSetting{Key: "telemetry.endpoint", Value: []byte(`"https://evil.example/leak"`)}
	q.rows["branding.product_name"] = sqlc.PlatformSetting{Key: "branding.product_name", Value: []byte(`"Megacorp"`)}
	h := NewPlatformSettingsHandler(q)

	// PRE-AUTH — no user in context.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/branding/", nil)
	w := httptest.NewRecorder()
	h.PublicBranding(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "telemetry") {
		t.Fatalf("public branding leaked telemetry: %s", body)
	}
	if strings.Contains(body, "evil.example") {
		t.Fatalf("public branding leaked telemetry endpoint URL: %s", body)
	}
	if !strings.Contains(body, "Megacorp") {
		t.Fatalf("public branding missing the configured value: %s", body)
	}
	// And the feature flags must not appear pre-auth.
	if strings.Contains(body, "feature.catalog") {
		t.Fatalf("public branding leaked feature flags: %s", body)
	}
}

func TestSettings_PublicBannerStripsTelemetry(t *testing.T) {
	q := newFakeSettingsQuerier(sqlc.User{})
	// Populate every namespace; the public banner reader must only
	// return banner.* keys.
	q.rows["banner.login_text"] = sqlc.PlatformSetting{Key: "banner.login_text", Value: []byte(`"For authorized use only."`)}
	q.rows["telemetry.endpoint"] = sqlc.PlatformSetting{Key: "telemetry.endpoint", Value: []byte(`"https://evil/"`)}
	q.rows["feature.catalog"] = sqlc.PlatformSetting{Key: "feature.catalog", Value: []byte(`false`)}
	h := NewPlatformSettingsHandler(q)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/banner/", nil)
	w := httptest.NewRecorder()
	h.PublicBanner(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "telemetry") {
		t.Fatalf("public banner leaked telemetry: %s", body)
	}
	if strings.Contains(body, "feature.catalog") {
		t.Fatalf("public banner leaked feature flags: %s", body)
	}
	if !strings.Contains(body, "For authorized use only.") {
		t.Fatalf("public banner missing banner.login_text: %s", body)
	}
}

func TestSettings_FeaturesReturnsOnlyFeatureBooleans(t *testing.T) {
	callerID := uuid.New()
	q := newFakeSettingsQuerier(sqlc.User{ID: callerID, IsSuperuser: false})
	q.rows["feature.catalog"] = sqlc.PlatformSetting{Key: "feature.catalog", Value: []byte(`false`)}
	q.rows["feature.argocd"] = sqlc.PlatformSetting{Key: "feature.argocd", Value: []byte(`true`)}
	q.rows["telemetry.endpoint"] = sqlc.PlatformSetting{Key: "telemetry.endpoint", Value: []byte(`"https://telemetry.example"`)}
	q.rows["branding.product_name"] = sqlc.PlatformSetting{Key: "branding.product_name", Value: []byte(`"Megacorp"`)}
	h := NewPlatformSettingsHandler(q)

	req := authedRequest(http.MethodGet, "/api/v1/settings/features/", callerID, nil)
	w := httptest.NewRecorder()
	h.Features(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	var flags map[string]bool
	if err := json.Unmarshal(envelope["data"], &flags); err != nil {
		t.Fatalf("decode feature flags: %v", err)
	}
	if flags["feature.catalog"] {
		t.Fatalf("feature.catalog = true, want false")
	}
	if !flags["feature.argocd"] {
		t.Fatalf("feature.argocd = false, want true")
	}
	if !flags["feature.projects"] {
		t.Fatalf("feature.projects default = false, want true")
	}
	if _, ok := flags["telemetry.endpoint"]; ok {
		t.Fatalf("features leaked telemetry row: %+v", flags)
	}
	if _, ok := flags["branding.product_name"]; ok {
		t.Fatalf("features leaked branding row: %+v", flags)
	}
}

// --- FeatureGate tests ---

func TestFeatureGate_404sWhenDisabled(t *testing.T) {
	q := newFakeSettingsQuerier(sqlc.User{IsSuperuser: true})
	q.rows["feature.catalog"] = sqlc.PlatformSetting{Key: "feature.catalog", Value: []byte(`false`)}
	cache := NewSettingsCache(q, 30*1000_000_000) // 30s
	mw := featureGateForTest("feature.catalog", cache)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/charts/", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if called {
		t.Fatalf("next was called; want skipped")
	}
}

func TestFeatureGate_AllowsWhenEnabled(t *testing.T) {
	q := newFakeSettingsQuerier(sqlc.User{IsSuperuser: true})
	q.rows["feature.catalog"] = sqlc.PlatformSetting{Key: "feature.catalog", Value: []byte(`true`)}
	cache := NewSettingsCache(q, 30*1000_000_000)
	mw := featureGateForTest("feature.catalog", cache)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/charts/", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !called {
		t.Fatalf("next was not called")
	}
}

// Belt-and-suspenders: missing row → treat as enabled (the operator
// hasn't set a value, so the default `true` from the registry applies).
func TestFeatureGate_DefaultsToEnabledOnNoRow(t *testing.T) {
	q := newFakeSettingsQuerier(sqlc.User{IsSuperuser: true})
	// No rows at all.
	cache := NewSettingsCache(q, 30*1000_000_000)
	mw := featureGateForTest("feature.catalog", cache)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/charts/", nil)
	w := httptest.NewRecorder()
	mw(next).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !called {
		t.Fatalf("next was not called")
	}
}

func TestFeatureGate_CacheHit(t *testing.T) {
	// Verify: PUT through the handler invalidates the cache so the
	// next gate read sees the new value WITHOUT waiting for the 30s
	// TTL to expire.
	callerID := uuid.New()
	q := newFakeSettingsQuerier(sqlc.User{ID: callerID, IsSuperuser: true})
	q.rows["feature.catalog"] = sqlc.PlatformSetting{Key: "feature.catalog", Value: []byte(`true`)}
	cache := NewSettingsCache(q, 30*1000_000_000)
	h := NewPlatformSettingsHandler(q)
	h.SetCache(cache)
	mw := featureGateForTest("feature.catalog", cache)

	allowed := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Prime the cache: gate allows.
	r1 := httptest.NewRequest(http.MethodGet, "/", nil)
	w1 := httptest.NewRecorder()
	mw(allowed).ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("prime status = %d, want 200", w1.Code)
	}

	// Now mutate via the handler.
	put := withURLParam(
		authedRequest(http.MethodPut, "/api/v1/admin/settings/feature.catalog/", callerID, []byte(`{"value":false}`)),
		"key", "feature.catalog",
	)
	pw := httptest.NewRecorder()
	h.Update(pw, put)
	if pw.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", pw.Code, pw.Body.String())
	}

	// Immediate next request must see the new value (cache invalidated).
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	w2 := httptest.NewRecorder()
	mw(allowed).ServeHTTP(w2, r2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("post-PUT status = %d, want 404 (cache invalidate failed?)", w2.Code)
	}
}

// featureGateForTest builds the middleware.FeatureGate against the
// shared SettingsCache. Kept inside this package so the test imports
// stay consistent with the runtime wiring.
func featureGateForTest(key string, cache *SettingsCache) func(http.Handler) http.Handler {
	return middleware.FeatureGate(key, cache)
}

// Compile-time check: PlatformSettingsQuerier is satisfied.
var _ PlatformSettingsQuerier = (*fakeSettingsQuerier)(nil)

// Compile-time check: the fake also satisfies SettingsReader so it
// can drive the cache directly.
var _ SettingsReader = (*fakeSettingsQuerier)(nil)

// Compile-time guard: pgtype is reachable so tests can ignore the
// unused-import warning when assertion helpers don't need it.
var _ = pgtype.UUID{}
