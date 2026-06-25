package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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

func (f *fakeExtensionQuerier) SetUIExtensionBundleVerified(_ context.Context, arg sqlc.SetUIExtensionBundleVerifiedParams) (sqlc.UIExtension, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.rows[arg.Name]
	if !ok {
		return sqlc.UIExtension{}, pgx.ErrNoRows
	}
	row.BundleVerified = arg.BundleVerified
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

func TestExtensionHandler_VerifyBundleSignatureAndChecksum(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	bundle := []byte("console.log('hello from extension');")
	sig := ed25519.Sign(priv, bundle)

	h := NewExtensionHandler(newFakeExtensionQuerier())
	if err := h.SetTrustedBundleKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("set key: %v", err)
	}

	reqBody := func(b, s []byte, checksum string) string {
		raw, _ := json.Marshal(VerifyBundleRequest{
			Bundle:    base64.StdEncoding.EncodeToString(b),
			Signature: base64.StdEncoding.EncodeToString(s),
			Checksum:  checksum,
		})
		return string(raw)
	}

	// Happy path: correct signature + matching checksum verifies.
	rr := httptest.NewRecorder()
	h.VerifyBundle(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/verify-bundle/", reqBody(bundle, sig, bundleChecksum(bundle))))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeExtensionResp[VerifyBundleResponse](t, rr)
	if !resp.Verified {
		t.Fatalf("verified=false, want true")
	}

	// Tampered bundle: signature no longer valid -> 400.
	tampered := append([]byte{}, bundle...)
	tampered[0] ^= 0xFF
	rr = httptest.NewRecorder()
	h.VerifyBundle(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/verify-bundle/", reqBody(tampered, sig, "")))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("tampered status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}

	// No trusted key configured: fails closed with 503.
	hNoKey := NewExtensionHandler(newFakeExtensionQuerier())
	rr = httptest.NewRecorder()
	hNoKey.VerifyBundle(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/verify-bundle/", reqBody(bundle, sig, "")))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-key status=%d body=%s, want 503", rr.Code, rr.Body.String())
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

func hasError(findings []ExtensionValidationFinding, substr string) bool {
	for _, f := range findings {
		if strings.Contains(f.Message, substr) || strings.Contains(f.Field, substr) {
			return true
		}
	}
	return false
}

// tier1Manifest returns a valid Tier-1 declarative widget manifest.
func tier1Manifest() ExtensionManifest {
	m := sampleExtensionManifest()
	m.ExtensionPoints.Widgets = []ExtensionWidgetPoint{{
		ID:    "cost-summary",
		Title: "Cost summary",
		DataSources: []DataSourceRef{{
			ID:     "podCost",
			Proxy:  "astronomer-api",
			Method: "GET",
			Path:   "/api/v1/clusters/{clusterId}/monitoring/cost",
			Query:  map[string]string{"window": "30d"},
			RBAC:   RBACRequirement{Resource: "monitoring", Verb: "read", Scope: "cluster"},
			Shape:  "list",
			Fields: []string{"namespace", "usd"},
		}},
		Render: &ExtensionRender{Declarative: &DeclarativeWidget{
			Kind:       "table",
			DataSource: "podCost",
			Fields: []FieldBinding{
				{Path: "namespace", Label: "Namespace", Format: "text"},
				{Path: "usd", Label: "Cost (USD)", Format: "currency"},
			},
		}},
	}}
	return m
}

func TestValidateExtensionManifest_Tier1Valid(t *testing.T) {
	resp := validateExtensionManifest(tier1Manifest(), "0.9.1")
	if !resp.Valid {
		t.Fatalf("expected valid Tier-1 manifest, errors=%+v", resp.Errors)
	}
}

func TestValidateExtensionManifest_RBACCeiling(t *testing.T) {
	m := tier1Manifest()
	// dataSource needs monitoring:read but only clusters:read declared.
	m.Permissions = []string{"clusters:read"}
	resp := validateExtensionManifest(m, "0.9.1")
	if resp.Valid {
		t.Fatalf("expected invalid: rbac not in permissions[]")
	}
	if !hasError(resp.Errors, "not declared in permissions[]") {
		t.Fatalf("missing ceiling error, got %+v", resp.Errors)
	}
}

func TestValidateExtensionManifest_WildcardAndBadEnums(t *testing.T) {
	m := tier1Manifest()
	ds := &m.ExtensionPoints.Widgets[0].DataSources[0]
	ds.RBAC.Verb = "*"
	ds.Proxy = "evil"
	ds.Method = "PUT"
	ds.Shape = "blob"
	ds.Fields = []string{"*"}
	ds.Path = "/etc/../passwd"
	resp := validateExtensionManifest(m, "0.9.1")
	if resp.Valid {
		t.Fatalf("expected invalid manifest")
	}
	for _, want := range []string{"rbac.verb", "proxy", "method", "shape", "fields", "path"} {
		if !hasError(resp.Errors, want) {
			t.Fatalf("missing %q error, got %+v", want, resp.Errors)
		}
	}
}

func TestValidateExtensionManifest_BadDataSourceRefAndFormat(t *testing.T) {
	m := tier1Manifest()
	m.ExtensionPoints.Widgets[0].Render.Declarative.DataSource = "nope"
	m.ExtensionPoints.Widgets[0].Render.Declarative.Fields[0].Format = "html"
	resp := validateExtensionManifest(m, "0.9.1")
	if resp.Valid {
		t.Fatalf("expected invalid manifest")
	}
	if !hasError(resp.Errors, "dataSource") || !hasError(resp.Errors, "format") {
		t.Fatalf("missing dataSource/format error, got %+v", resp.Errors)
	}
}

func TestValidateExtensionManifest_TierExclusivity(t *testing.T) {
	m := tier1Manifest()
	m.ExtensionPoints.Widgets[0].Render.Bundle = &BundleDescriptor{}
	resp := validateExtensionManifest(m, "0.9.1")
	if !hasError(resp.Errors, "either declarative or bundle, not both") {
		t.Fatalf("missing exclusivity error, got %+v", resp.Errors)
	}
}

// tier2Manifest returns a valid Tier-2 bundle cluster tab manifest.
func tier2Manifest() ExtensionManifest {
	m := sampleExtensionManifest()
	m.ExtensionPoints.ClusterTabs = []ExtensionClusterTab{{
		Label:     "Cost",
		Component: "ClusterCostTab",
		Render: &ExtensionRender{Bundle: &BundleDescriptor{
			URL:           "https://cdn.vendor.example/cost/bundle.js",
			SHA256:        "sha256:" + strings.Repeat("a", 64),
			Integrity:     "sha384-abc",
			Signature:     base64.StdEncoding.EncodeToString([]byte("signature-bytes-here")),
			Entry:         "index.js",
			SandboxOrigin: "https://ext-cost.sandbox.astronomer.local",
			Component:     "ClusterCostTab",
			CSP: ExtensionCSP{
				ScriptSrc:  []string{"'self'"},
				ConnectSrc: []string{"'self'"},
				FrameSrc:   []string{"'none'"},
				ImageSrc:   []string{"'self'", "data:"},
			},
			DataSources: []DataSourceRef{{
				ID:     "podCost",
				Proxy:  "astronomer-api",
				Method: "GET",
				Path:   "/api/v1/clusters/{clusterId}/monitoring/cost",
				RBAC:   RBACRequirement{Resource: "monitoring", Verb: "read", Scope: "cluster"},
				Shape:  "list",
			}},
		}},
	}}
	return m
}

func TestValidateExtensionManifest_Tier2Valid(t *testing.T) {
	resp := validateExtensionManifest(tier2Manifest(), "0.9.1")
	if !resp.Valid {
		t.Fatalf("expected valid Tier-2 manifest, errors=%+v", resp.Errors)
	}
}

func TestValidateExtensionManifest_Tier2BadBundle(t *testing.T) {
	m := tier2Manifest()
	b := m.ExtensionPoints.ClusterTabs[0].Render.Bundle
	b.SHA256 = "sha256:nothex"
	b.Integrity = "md5-xyz"
	b.URL = "http://insecure.example/x.js"
	b.SandboxOrigin = "ftp://nope"
	b.CSP.FrameSrc = nil
	b.CSP.ConnectSrc = []string{"*"}
	resp := validateExtensionManifest(m, "0.9.1")
	if resp.Valid {
		t.Fatalf("expected invalid Tier-2 bundle")
	}
	for _, want := range []string{"sha256", "integrity", "url", "sandboxOrigin", "frameSrc", "connectSrc"} {
		if !hasError(resp.Errors, want) {
			t.Fatalf("missing %q error, got %+v", want, resp.Errors)
		}
	}
}

func TestExtensionHandler_BundleGatedWithoutTrustedKey(t *testing.T) {
	q := newFakeExtensionQuerier()
	h := NewExtensionHandler(q) // no trusted key
	h.SetCurrentVersion("0.9.1")
	raw, _ := json.Marshal(InstallExtensionRequest{Manifest: tier2Manifest(), Source: "unit-test", Enable: true})
	rr := httptest.NewRecorder()
	h.Install(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/", string(raw)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeExtensionResp[ExtensionRecordResponse](t, rr)
	if resp.Enabled {
		t.Fatalf("Tier-2 bundle must not be enabled without a trusted key")
	}
}

// seedExtension stores a row directly so /mounts/ gates can be exercised
// without driving the full install pipeline.
func (f *fakeExtensionQuerier) seedExtension(t *testing.T, m ExtensionManifest, enabled bool, status string, bundleVerified bool) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[m.Name] = sqlc.UIExtension{
		ID:                  uuid.New(),
		Name:                m.Name,
		DisplayName:         extensionDisplayName(m),
		Version:             m.Version,
		Enabled:             enabled,
		CompatibilityStatus: status,
		Manifest:            raw,
		BundleVerified:      bundleVerified,
		InstalledAt:         time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
}

func mountsResponse(t *testing.T, h *ExtensionHandler) ExtensionMountsResponse {
	t.Helper()
	rr := httptest.NewRecorder()
	h.Mounts(rr, httptest.NewRequest(http.MethodGet, "/api/v1/extensions/mounts/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("mounts status=%d body=%s", rr.Code, rr.Body.String())
	}
	return decodeExtensionResp[ExtensionMountsResponse](t, rr)
}

func TestExtensionHandler_MountsTier1Projection(t *testing.T) {
	q := newFakeExtensionQuerier()
	q.seedExtension(t, tier1Manifest(), true, "compatible", false)
	h := NewExtensionHandler(q)

	resp := mountsResponse(t, h)
	if len(resp.DashboardWidgets) != 1 {
		t.Fatalf("want 1 dashboard widget, got %d (%+v)", len(resp.DashboardWidgets), resp)
	}
	w := resp.DashboardWidgets[0]
	if w.Tier != 1 {
		t.Fatalf("want tier 1, got %d", w.Tier)
	}
	if w.Point != "dashboardWidget" || w.PointID != "cost-summary" {
		t.Fatalf("unexpected point projection: %+v", w)
	}
	if w.Render == nil || w.Render.Declarative == nil {
		t.Fatalf("declarative render missing: %+v", w.Render)
	}
	if len(w.DataSources) != 1 || w.DataSources[0].ID != "podCost" || w.DataSources[0].Shape != "list" {
		t.Fatalf("dataSource projection wrong: %+v", w.DataSources)
	}
	// Legacy (no-render) sidebar + clusterTab from the sample manifest must be
	// skipped — they mount nothing.
	if len(resp.Sidebar) != 0 || len(resp.ClusterTabs) != 0 {
		t.Fatalf("legacy no-render points must not mount: sidebar=%d clusterTabs=%d", len(resp.Sidebar), len(resp.ClusterTabs))
	}
}

func TestExtensionHandler_MountsGates(t *testing.T) {
	disabled := tier1Manifest()
	disabled.Name = "disabled-ext"
	incompatible := tier1Manifest()
	incompatible.Name = "incompatible-ext"

	q := newFakeExtensionQuerier()
	q.seedExtension(t, disabled, false, "compatible", false)      // disabled -> skipped
	q.seedExtension(t, incompatible, true, "incompatible", false) // incompatible -> skipped
	h := NewExtensionHandler(q)

	resp := mountsResponse(t, h)
	if len(resp.DashboardWidgets) != 0 {
		t.Fatalf("disabled/incompatible extensions must not mount, got %+v", resp.DashboardWidgets)
	}
}

func TestExtensionHandler_MountsTier2RequiresVerifiedBundle(t *testing.T) {
	q := newFakeExtensionQuerier()
	// Enabled + compatible but bundle_verified=false -> the Tier-2 tab is gated.
	q.seedExtension(t, tier2Manifest(), true, "compatible", false)
	h := NewExtensionHandler(q)

	if got := mountsResponse(t, h); len(got.ClusterTabs) != 0 {
		t.Fatalf("unverified Tier-2 bundle must not mount, got %+v", got.ClusterTabs)
	}

	// Flip bundle_verified -> the tab mounts, tier 2, paths stripped.
	q.seedExtension(t, tier2Manifest(), true, "compatible", true)
	resp := mountsResponse(t, h)
	if len(resp.ClusterTabs) != 1 {
		t.Fatalf("want 1 verified cluster tab, got %d", len(resp.ClusterTabs))
	}
	tab := resp.ClusterTabs[0]
	if tab.Tier != 2 || tab.Render == nil || tab.Render.Bundle == nil {
		t.Fatalf("expected verified Tier-2 bundle render: %+v", tab)
	}
	// dataSource ids surface, but the upstream descriptor paths must NOT leak.
	if len(tab.DataSources) != 1 || tab.DataSources[0].ID != "podCost" {
		t.Fatalf("bundle dataSource ids not projected: %+v", tab.DataSources)
	}
	if len(tab.Render.Bundle.DataSources) != 0 {
		t.Fatalf("bundle descriptor dataSources (with upstream paths) must not be exposed: %+v", tab.Render.Bundle.DataSources)
	}
	if tab.Render.Bundle.SandboxOrigin == "" || tab.Render.Bundle.SHA256 == "" {
		t.Fatalf("bundle render must keep loader-facing fields: %+v", tab.Render.Bundle)
	}
}
