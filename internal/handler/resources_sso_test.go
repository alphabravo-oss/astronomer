package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type fakeSSOSettingsQuerier struct {
	listEnabled []sqlc.SsoConfiguration
	byProvider  map[string]sqlc.SsoConfiguration
	byID        map[uuid.UUID]sqlc.SsoConfiguration
	created     *sqlc.CreateSSOConfigurationParams
	deletedID   uuid.UUID
}

func (f *fakeSSOSettingsQuerier) ListSSOConfigurations(context.Context, sqlc.ListSSOConfigurationsParams) ([]sqlc.SsoConfiguration, error) {
	return f.listEnabled, nil
}

func (f *fakeSSOSettingsQuerier) GetEnabledSSOProviders(context.Context) ([]sqlc.SsoConfiguration, error) {
	return f.listEnabled, nil
}

func (f *fakeSSOSettingsQuerier) GetSSOConfigurationByProvider(_ context.Context, provider string) (sqlc.SsoConfiguration, error) {
	if row, ok := f.byProvider[provider]; ok {
		return row, nil
	}
	return sqlc.SsoConfiguration{}, pgx.ErrNoRows
}

func (f *fakeSSOSettingsQuerier) GetSSOConfigurationByID(_ context.Context, id uuid.UUID) (sqlc.SsoConfiguration, error) {
	if row, ok := f.byID[id]; ok {
		return row, nil
	}
	return sqlc.SsoConfiguration{}, pgx.ErrNoRows
}

func (f *fakeSSOSettingsQuerier) CreateSSOConfiguration(_ context.Context, arg sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error) {
	f.created = &arg
	row := sqlc.SsoConfiguration{
		ID:                    uuid.New(),
		Provider:              arg.Provider,
		IsEnabled:             arg.IsEnabled,
		DisplayName:           arg.DisplayName,
		Config:                arg.Config,
		ClientID:              arg.ClientID,
		ClientSecretEncrypted: arg.ClientSecretEncrypted,
		AllowedOrganizations:  arg.AllowedOrganizations,
		AllowedDomains:        arg.AllowedDomains,
		AutoCreateUsers:       arg.AutoCreateUsers,
	}
	if f.byProvider == nil {
		f.byProvider = map[string]sqlc.SsoConfiguration{}
	}
	if f.byID == nil {
		f.byID = map[uuid.UUID]sqlc.SsoConfiguration{}
	}
	f.byProvider[row.Provider] = row
	f.byID[row.ID] = row
	return row, nil
}

func (f *fakeSSOSettingsQuerier) DeleteSSOConfiguration(_ context.Context, id uuid.UUID) error {
	f.deletedID = id
	delete(f.byID, id)
	return nil
}

func TestCreateSSOProviderRegistersGenericOIDCProvider(t *testing.T) {
	issuer := newOIDCTestIssuer(t)
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	jwtManager := auth.NewJWTManager("test-secret", 60)
	ssoMgr := auth.NewSSOManager(enc, jwtManager, "https://astro.example.com/api/v1")
	ssoMgr.SetDiscoveryClient(auth.NewOIDCDiscoveryClient(issuer.Client()))

	q := &fakeSSOSettingsQuerier{
		byProvider: map[string]sqlc.SsoConfiguration{},
		byID:       map[uuid.UUID]sqlc.SsoConfiguration{},
	}
	audit := &resourceAuditQuerier{}
	h := &ResourceHandler{queries: audit, sso: q, encryptor: enc, ssoMgr: ssoMgr}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/sso/", stringsReader(`{
		"type":"oidc",
		"name":"Corporate SSO",
		"enabled":true,
		"config":{
			"client_id":"astronomer-ui",
			"client_secret":"super-secret",
			"metadata_url":"`+issuer.URL+`/.well-known/openid-configuration",
			"auto_create_users":true
		}
	}`))
	rr := httptest.NewRecorder()

	h.CreateSSOProvider(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if q.created == nil {
		t.Fatal("expected CreateSSOConfiguration to be called")
	}
	if q.created.Provider != "corporate-sso" {
		t.Fatalf("provider key=%q want corporate-sso", q.created.Provider)
	}
	var cfg auth.SSOProviderConfig
	if err := json.Unmarshal(q.created.Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if cfg.IssuerURL != issuer.URL {
		t.Fatalf("issuer_url=%q want %q", cfg.IssuerURL, issuer.URL)
	}
	if !ssoMgr.HasProvider("corporate-sso") {
		t.Fatal("expected provider to be registered in SSO manager")
	}
	if len(audit.rows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(audit.rows))
	}
	row := audit.rows[0]
	if row.Action != "sso.provider.create" || row.ResourceType != "sso_provider" {
		t.Fatalf("audit row=%+v, want sso.provider.create on sso_provider", row)
	}
	if row.ResourceName != "Corporate SSO" {
		t.Fatalf("audit resource_name=%q want Corporate SSO", row.ResourceName)
	}
	assertAuditDetail(t, row.Detail, "provider", "corporate-sso")
	assertAuditDetail(t, row.Detail, "type", "oidc")
}

func TestListSSOProvidersReturnsEnabledRows(t *testing.T) {
	row := sqlc.SsoConfiguration{
		ID:          uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Provider:    "dex",
		IsEnabled:   true,
		DisplayName: "Dex",
		Config:      json.RawMessage(`{"issuer_url":"https://dex.example.com"}`),
	}
	h := &ResourceHandler{
		sso: &fakeSSOSettingsQuerier{
			listEnabled: []sqlc.SsoConfiguration{row},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/sso/", nil)
	rr := httptest.NewRecorder()

	h.ListSSOProviders(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data []SSOProviderResponse `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("providers=%d want 1", len(resp.Data))
	}
	if resp.Data[0].Provider != "dex" || resp.Data[0].Type != "oidc" {
		t.Fatalf("unexpected provider response: %+v", resp.Data[0])
	}
}

func TestDeleteSSOProviderRemovesProviderFromManager(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	jwtManager := auth.NewJWTManager("test-secret", 60)
	ssoMgr := auth.NewSSOManager(enc, jwtManager, "https://astro.example.com/api/v1")
	secret, err := enc.Encrypt("secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := ssoMgr.RegisterProvider("github", "client-id", secret, "", nil); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	row := sqlc.SsoConfiguration{
		ID:          uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Provider:    "github",
		IsEnabled:   true,
		DisplayName: "GitHub",
	}
	q := &fakeSSOSettingsQuerier{
		byID: map[uuid.UUID]sqlc.SsoConfiguration{row.ID: row},
	}
	audit := &resourceAuditQuerier{}
	h := &ResourceHandler{queries: audit, sso: q, ssoMgr: ssoMgr}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/sso/"+row.ID.String()+"/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", row.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rr := httptest.NewRecorder()

	h.DeleteSSOProvider(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if q.deletedID != row.ID {
		t.Fatalf("deleted id=%s want %s", q.deletedID, row.ID)
	}
	if ssoMgr.HasProvider("github") {
		t.Fatal("expected provider to be removed from SSO manager")
	}
	if len(audit.rows) != 1 {
		t.Fatalf("audit rows=%d want 1", len(audit.rows))
	}
	auditRow := audit.rows[0]
	if auditRow.Action != "sso.provider.delete" || auditRow.ResourceType != "sso_provider" {
		t.Fatalf("audit row=%+v, want sso.provider.delete on sso_provider", auditRow)
	}
	if auditRow.ResourceID != row.ID.String() || auditRow.ResourceName != "GitHub" {
		t.Fatalf("audit target=(%q,%q), want (%q,GitHub)", auditRow.ResourceID, auditRow.ResourceName, row.ID.String())
	}
	assertAuditDetail(t, auditRow.Detail, "provider", "github")
}

func stringsReader(s string) *strings.Reader {
	return strings.NewReader(s)
}

func assertAuditDetail(t *testing.T, raw json.RawMessage, key, want string) {
	t.Helper()
	var detail map[string]any
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("decode audit detail %s: %v", raw, err)
	}
	got, ok := detail[key].(string)
	if !ok {
		t.Fatalf("audit detail[%q]=%#v, want %q; detail=%v", key, detail[key], want, detail)
	}
	if got != want {
		t.Fatalf("audit detail[%q]=%q, want %q", key, got, want)
	}
}

func newOIDCTestIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var issuerURL string
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 issuerURL,
			"authorization_endpoint": issuerURL + "/authorize",
			"token_endpoint":         issuerURL + "/token",
			"userinfo_endpoint":      issuerURL + "/userinfo",
			"jwks_uri":               issuerURL + "/jwks",
		})
	})
	srv := httptest.NewServer(mux)
	issuerURL = srv.URL
	t.Cleanup(srv.Close)
	return srv
}
