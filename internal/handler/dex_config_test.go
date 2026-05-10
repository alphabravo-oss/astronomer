package handler

// Phase B4 — Dex shim tests.
//
// Three layers:
//   1. validateConnectorConfig — type-by-type required-fields contract
//      (microsoft, ldap.userSearch, saml, oidc, ...).
//   2. renderDexConfig — settings + connectors → Dex YAML, including
//      decryption of secret fields and forwarding of expiry/extra blocks.
//   3. End-to-end /apply against an httptest.Server that mocks the
//      Kubernetes API: confirms the handler PATCHes (or POSTs on 404) the
//      ConfigMap into the management cluster.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// fakeDexQuerier is an in-memory DexQuerier used by every test in this file.
type fakeDexQuerier struct {
	connectors map[uuid.UUID]sqlc.DexConnector
	settings   *sqlc.DexSetting
	platform   *sqlc.PlatformConfiguration
	ssoByProv  map[string]sqlc.SsoConfiguration
	createErr  error
	upsertErr  error
}

func newFakeDexQuerier() *fakeDexQuerier {
	return &fakeDexQuerier{
		connectors: make(map[uuid.UUID]sqlc.DexConnector),
		ssoByProv:  make(map[string]sqlc.SsoConfiguration),
	}
}

func (f *fakeDexQuerier) GetDexConnectorByID(_ context.Context, id uuid.UUID) (sqlc.DexConnector, error) {
	c, ok := f.connectors[id]
	if !ok {
		return sqlc.DexConnector{}, errors.New("not found")
	}
	return c, nil
}

func (f *fakeDexQuerier) GetDexConnectorByName(_ context.Context, name string) (sqlc.DexConnector, error) {
	for _, c := range f.connectors {
		if c.Name == name {
			return c, nil
		}
	}
	return sqlc.DexConnector{}, errors.New("not found")
}

func (f *fakeDexQuerier) ListDexConnectors(_ context.Context) ([]sqlc.DexConnector, error) {
	out := make([]sqlc.DexConnector, 0, len(f.connectors))
	for _, c := range f.connectors {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeDexQuerier) ListEnabledDexConnectors(_ context.Context) ([]sqlc.DexConnector, error) {
	out := make([]sqlc.DexConnector, 0, len(f.connectors))
	for _, c := range f.connectors {
		if c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeDexQuerier) CreateDexConnector(_ context.Context, arg sqlc.CreateDexConnectorParams) (sqlc.DexConnector, error) {
	if f.createErr != nil {
		return sqlc.DexConnector{}, f.createErr
	}
	for _, existing := range f.connectors {
		if existing.Name == arg.Name {
			return sqlc.DexConnector{}, errors.New("duplicate key value violates unique constraint")
		}
	}
	row := sqlc.DexConnector{
		ID:          uuid.New(),
		Name:        arg.Name,
		Type:        arg.Type,
		DisplayName: arg.DisplayName,
		Config:      arg.Config,
		Enabled:     arg.Enabled,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	f.connectors[row.ID] = row
	return row, nil
}

func (f *fakeDexQuerier) UpdateDexConnector(_ context.Context, arg sqlc.UpdateDexConnectorParams) (sqlc.DexConnector, error) {
	row, ok := f.connectors[arg.ID]
	if !ok {
		return sqlc.DexConnector{}, errors.New("not found")
	}
	row.Type = arg.Type
	row.DisplayName = arg.DisplayName
	row.Config = arg.Config
	row.Enabled = arg.Enabled
	row.UpdatedAt = time.Now().UTC()
	f.connectors[arg.ID] = row
	return row, nil
}

func (f *fakeDexQuerier) DeleteDexConnector(_ context.Context, id uuid.UUID) error {
	delete(f.connectors, id)
	return nil
}

func (f *fakeDexQuerier) GetDexSettings(_ context.Context, id uuid.UUID) (sqlc.DexSetting, error) {
	if f.settings == nil || f.settings.ID != id {
		return sqlc.DexSetting{}, errors.New("not found")
	}
	return *f.settings, nil
}

func (f *fakeDexQuerier) UpsertDexSettings(_ context.Context, arg sqlc.UpsertDexSettingsParams) (sqlc.DexSetting, error) {
	if f.upsertErr != nil {
		return sqlc.DexSetting{}, f.upsertErr
	}
	row := sqlc.DexSetting{
		ID:            arg.ID,
		IssuerUrl:     arg.IssuerUrl,
		ClusterID:     arg.ClusterID,
		Namespace:     arg.Namespace,
		ReleaseName:   arg.ReleaseName,
		ConfigmapName: arg.ConfigmapName,
		PublicClients: arg.PublicClients,
		Expiry:        arg.Expiry,
		Extra:         arg.Extra,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	f.settings = &row
	return row, nil
}

func (f *fakeDexQuerier) GetPlatformConfig(_ context.Context) (sqlc.PlatformConfiguration, error) {
	if f.platform == nil {
		return sqlc.PlatformConfiguration{}, errors.New("not found")
	}
	return *f.platform, nil
}

func (f *fakeDexQuerier) GetSSOConfigurationByProvider(_ context.Context, provider string) (sqlc.SsoConfiguration, error) {
	c, ok := f.ssoByProv[provider]
	if !ok {
		return sqlc.SsoConfiguration{}, errors.New("not found")
	}
	return c, nil
}

func (f *fakeDexQuerier) CreateSSOConfiguration(_ context.Context, arg sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error) {
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
		DefaultGlobalRoleID:   arg.DefaultGlobalRoleID,
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	f.ssoByProv[arg.Provider] = row
	return row, nil
}

func (f *fakeDexQuerier) UpdateSSOConfiguration(_ context.Context, arg sqlc.UpdateSSOConfigurationParams) (sqlc.SsoConfiguration, error) {
	row, ok := f.ssoByProv[""]
	_ = ok
	for prov, r := range f.ssoByProv {
		if r.ID == arg.ID {
			row = r
			row.IsEnabled = arg.IsEnabled
			row.DisplayName = arg.DisplayName
			row.Config = arg.Config
			row.ClientID = arg.ClientID
			row.ClientSecretEncrypted = arg.ClientSecretEncrypted
			row.AllowedOrganizations = arg.AllowedOrganizations
			row.AllowedDomains = arg.AllowedDomains
			row.AutoCreateUsers = arg.AutoCreateUsers
			row.DefaultGlobalRoleID = arg.DefaultGlobalRoleID
			row.UpdatedAt = time.Now().UTC()
			f.ssoByProv[prov] = row
			return row, nil
		}
	}
	return sqlc.SsoConfiguration{}, errors.New("not found")
}

// ----- 1. validateConnectorConfig --------------------------------------

func TestValidateConnectorConfig_RequiredFieldsByType(t *testing.T) {
	tests := []struct {
		name     string
		typ      string
		config   map[string]any
		wantOK   bool
		wantMiss []string
	}{
		{
			name:   "microsoft happy path",
			typ:    "microsoft",
			config: map[string]any{"tenant": "abc", "clientID": "id", "clientSecret": "s"},
			wantOK: true,
		},
		{
			name:     "microsoft missing tenant",
			typ:      "microsoft",
			config:   map[string]any{"clientID": "id", "clientSecret": "s"},
			wantOK:   false,
			wantMiss: []string{"tenant"},
		},
		{
			name:     "microsoft empty clientSecret",
			typ:      "microsoft",
			config:   map[string]any{"tenant": "abc", "clientID": "id", "clientSecret": ""},
			wantOK:   false,
			wantMiss: []string{"clientSecret"},
		},
		{
			name: "ldap happy path",
			typ:  "ldap",
			config: map[string]any{
				"host":   "ldap.example.com",
				"bindDN": "cn=admin,dc=example,dc=com",
				"bindPW": "secret",
				"userSearch": map[string]any{
					"baseDN":    "ou=People,dc=example,dc=com",
					"username":  "uid",
					"idAttr":    "uid",
					"emailAttr": "mail",
				},
			},
			wantOK: true,
		},
		{
			name: "ldap missing nested field",
			typ:  "ldap",
			config: map[string]any{
				"host":   "ldap.example.com",
				"bindDN": "cn=admin,dc=example,dc=com",
				"bindPW": "secret",
				"userSearch": map[string]any{
					"baseDN":   "ou=People,dc=example,dc=com",
					"username": "uid",
				},
			},
			wantOK:   false,
			wantMiss: []string{"userSearch.idAttr", "userSearch.emailAttr"},
		},
		{
			name:     "ldap missing parent userSearch",
			typ:      "ldap",
			config:   map[string]any{"host": "h", "bindDN": "d", "bindPW": "p"},
			wantOK:   false,
			wantMiss: []string{"userSearch"},
		},
		{
			name:   "saml happy path",
			typ:    "saml",
			config: map[string]any{"ssoURL": "https://sso", "entityIssuer": "ent"},
			wantOK: true,
		},
		{
			name:     "saml missing fields",
			typ:      "saml",
			config:   map[string]any{},
			wantOK:   false,
			wantMiss: []string{"ssoURL", "entityIssuer"},
		},
		{
			name:   "oidc happy path",
			typ:    "oidc",
			config: map[string]any{"issuer": "https://i", "clientID": "id", "clientSecret": "s"},
			wantOK: true,
		},
		{
			name:     "github missing client",
			typ:      "github",
			config:   map[string]any{"clientID": "id"},
			wantOK:   false,
			wantMiss: []string{"clientSecret"},
		},
		{
			name:   "okta happy path",
			typ:    "okta",
			config: map[string]any{"issuer": "https://o", "clientID": "id", "clientSecret": "s"},
			wantOK: true,
		},
		{
			name:   "gitlab happy path",
			typ:    "gitlab",
			config: map[string]any{"baseURL": "https://gitlab.example.com", "clientID": "id", "clientSecret": "s"},
			wantOK: true,
		},
		{
			name:   "google happy path",
			typ:    "google",
			config: map[string]any{"clientID": "id", "clientSecret": "s"},
			wantOK: true,
		},
		{
			name:   "bitbucket happy path",
			typ:    "bitbucket",
			config: map[string]any{"clientID": "id", "clientSecret": "s"},
			wantOK: true,
		},
		{
			name:   "oauth happy path",
			typ:    "oauth",
			config: map[string]any{"clientID": "id", "clientSecret": "s", "tokenURL": "tu", "authorizationURL": "au", "userInfoURL": "ui"},
			wantOK: true,
		},
		{
			name:     "unknown type",
			typ:      "magic",
			config:   map[string]any{},
			wantOK:   false,
			wantMiss: []string{"unknown connector type"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConnectorConfig(tc.typ, tc.config)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			for _, m := range tc.wantMiss {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("expected error to mention %q, got %q", m, err.Error())
				}
			}
		})
	}
}

// ----- 2. renderDexConfig ----------------------------------------------

func TestRenderDexConfig_DecryptsSecretsAndForwardsExpiry(t *testing.T) {
	keyStr, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	enc, err := auth.NewEncryptor(keyStr)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	h := NewDexHandler(newFakeDexQuerier())
	h.SetEncryptor(enc)

	encryptedSecret, err := enc.Encrypt("super-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	connectorCfg, _ := json.Marshal(map[string]any{
		"tenant":       "tenant-id",
		"clientID":     "client-id",
		"clientSecret": encryptedSecret,
	})
	connectors := []sqlc.DexConnector{
		{
			ID:          uuid.New(),
			Name:        "azure-prod",
			Type:        "microsoft",
			DisplayName: "Azure AD (prod)",
			Config:      connectorCfg,
			Enabled:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}
	settings := sqlc.DexSetting{
		ID:            dexSettingsSingletonID,
		IssuerUrl:     "https://dex.example.com",
		Namespace:     "dex",
		ReleaseName:   "dex",
		ConfigmapName: "astronomer-dex-config",
		PublicClients: json.RawMessage(`[{"id":"astronomer","name":"Astronomer","redirectURIs":["https://astro.example.com/api/v1/auth/callback/dex/"],"secret":"client-secret"}]`),
		Expiry:        json.RawMessage(`{"idTokens":"1h","refreshTokens":{"reuseInterval":"3s","validIfNotUsedFor":"168h"}}`),
		Extra:         json.RawMessage(`{"logger":{"level":"info"}}`),
	}

	out, err := h.renderDexConfig(settings, connectors)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := doc["issuer"], "https://dex.example.com"; got != want {
		t.Errorf("issuer = %v, want %v", got, want)
	}
	storage, _ := doc["storage"].(map[string]any)
	if storage["type"] != "kubernetes" {
		t.Errorf("storage.type = %v, want kubernetes", storage["type"])
	}
	connsRaw, ok := doc["connectors"].([]any)
	if !ok || len(connsRaw) != 1 {
		t.Fatalf("connectors malformed: %#v", doc["connectors"])
	}
	c := connsRaw[0].(map[string]any)
	if c["type"] != "microsoft" {
		t.Errorf("connector type = %v", c["type"])
	}
	if c["id"] != "azure-prod" {
		t.Errorf("connector id = %v", c["id"])
	}
	cfg := c["config"].(map[string]any)
	if cfg["clientSecret"] != "super-secret" {
		t.Errorf("clientSecret should be decrypted plaintext, got %v", cfg["clientSecret"])
	}
	// Expiry forwarded verbatim.
	exp, _ := doc["expiry"].(map[string]any)
	if exp["idTokens"] != "1h" {
		t.Errorf("expiry not forwarded, got %#v", exp)
	}
	// Extra blocks merged at top level.
	if _, ok := doc["logger"]; !ok {
		t.Errorf("extra not merged into top-level: %#v", doc)
	}
	// staticClients carried from settings.
	clients, _ := doc["staticClients"].([]any)
	if len(clients) != 1 {
		t.Errorf("staticClients length = %d, want 1", len(clients))
	}
}

// ----- 3. End-to-end /apply against an httptest mock K8s server ---------

// proxyToHTTPTest builds a respFn for the existing test-helper stubK8sRequester
// that forwards each k8s call to the supplied httptest.Server. We reuse the
// shared stub (defined in backups_velero_test.go) so the handler package only
// needs one stub implementation across all tests.
func proxyToHTTPTest(srv *httptest.Server) func(req stubReq) (*protocol.K8sResponsePayload, error) {
	return func(req stubReq) (*protocol.K8sResponsePayload, error) {
		url := srv.URL + req.Path
		httpReq, err := http.NewRequest(req.Method, url, bytes.NewReader(req.Body))
		if err != nil {
			return nil, err
		}
		for k, v := range req.Headers {
			httpReq.Header.Set(k, v)
		}
		resp, err := srv.Client().Do(httpReq)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return &protocol.K8sResponsePayload{
			StatusCode: resp.StatusCode,
			Body:       base64StdEncode(respBody),
		}, nil
	}
}

func TestApply_PatchesConfigMapOnLiveCluster(t *testing.T) {
	// Mock K8s API: write the ConfigMap, then restart the Dex deployment.
	var seenMethod, seenPath string
	var seenBody []byte
	var configMapBody []byte
	var sawRestart bool
	mockK8s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenBody, _ = io.ReadAll(r.Body)
		if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/dex/configmaps/astronomer-dex-config" {
			configMapBody = append([]byte(nil), seenBody...)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"ConfigMap","metadata":{"name":"astronomer-dex-config"}}`))
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/apis/apps/v1/namespaces/dex/deployments/dex" {
			sawRestart = true
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"Deployment","metadata":{"name":"dex"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
	}))
	defer mockK8s.Close()

	q := newFakeDexQuerier()
	clusterID := uuid.New()
	q.settings = &sqlc.DexSetting{
		ID:            dexSettingsSingletonID,
		IssuerUrl:     "https://dex.example.com",
		Namespace:     "dex",
		ReleaseName:   "dex",
		ConfigmapName: "astronomer-dex-config",
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		PublicClients: json.RawMessage(`[]`),
		Expiry:        json.RawMessage(`{}`),
		Extra:         json.RawMessage(`{}`),
	}
	connectorCfg, _ := json.Marshal(map[string]any{
		"tenant":       "abc",
		"clientID":     "id",
		"clientSecret": "plaintext",
	})
	cid := uuid.New()
	q.connectors[cid] = sqlc.DexConnector{
		ID:        cid,
		Name:      "azure",
		Type:      "microsoft",
		Config:    connectorCfg,
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stub := &stubK8sRequester{respFn: proxyToHTTPTest(mockK8s)}
	h := NewDexHandler(q)
	h.SetK8sRequester(stub)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/apply/", nil)
	w := httptest.NewRecorder()
	h.Apply(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if seenMethod != http.MethodPatch {
		t.Errorf("expected final request to be PATCH, got %s", seenMethod)
	}
	if seenPath != "/apis/apps/v1/namespaces/dex/deployments/dex" {
		t.Errorf("unexpected final path: %s", seenPath)
	}
	if !bytes.Contains(configMapBody, []byte("config.yaml")) {
		t.Errorf("configmap body missing config.yaml key: %s", configMapBody)
	}
	if !bytes.Contains(configMapBody, []byte("microsoft")) {
		t.Errorf("configmap body missing connector type: %s", configMapBody)
	}
	if !sawRestart {
		t.Fatalf("expected deployment restart patch")
	}
	if !bytes.Contains(seenBody, []byte("kubectl.kubernetes.io/restartedAt")) {
		t.Errorf("restart patch missing restartedAt annotation: %s", seenBody)
	}
}

func TestApply_FallsBackToPostOnNotFound(t *testing.T) {
	var requests []string
	var configMapBody []byte
	var restartBody []byte
	mockK8s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		// First (PATCH) returns 404 to simulate "configmap doesn't exist yet".
		// Second (POST) creates it. Third PATCH restarts the deployment.
		if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/dex/configmaps/astronomer-dex-config" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/dex/configmaps" {
			configMapBody = append([]byte(nil), body...)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"kind":"ConfigMap"}`))
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/apis/apps/v1/namespaces/dex/deployments/dex" {
			restartBody = append([]byte(nil), body...)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"Deployment"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
	}))
	defer mockK8s.Close()

	q := newFakeDexQuerier()
	clusterID := uuid.New()
	q.settings = &sqlc.DexSetting{
		ID:            dexSettingsSingletonID,
		IssuerUrl:     "https://dex.example.com",
		Namespace:     "dex",
		ReleaseName:   "dex",
		ConfigmapName: "astronomer-dex-config",
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		PublicClients: json.RawMessage(`[]`),
		Expiry:        json.RawMessage(`{}`),
		Extra:         json.RawMessage(`{}`),
	}
	stub := &stubK8sRequester{respFn: proxyToHTTPTest(mockK8s)}
	h := NewDexHandler(q)
	h.SetK8sRequester(stub)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/apply/", nil)
	w := httptest.NewRecorder()
	h.Apply(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(requests) != 3 {
		t.Fatalf("expected config PATCH, config POST, deployment PATCH; got %#v", requests)
	}
	if requests[0] != "PATCH /api/v1/namespaces/dex/configmaps/astronomer-dex-config" ||
		requests[1] != "POST /api/v1/namespaces/dex/configmaps" ||
		requests[2] != "PATCH /apis/apps/v1/namespaces/dex/deployments/dex" {
		t.Errorf("unexpected request sequence: %#v", requests)
	}
	if !bytes.Contains(configMapBody, []byte("config.yaml")) {
		t.Errorf("configmap body missing config.yaml key: %s", configMapBody)
	}
	if !bytes.Contains(restartBody, []byte("kubectl.kubernetes.io/restartedAt")) {
		t.Errorf("restart patch missing restartedAt annotation: %s", restartBody)
	}
}

func TestApply_503WhenNoK8sRequester(t *testing.T) {
	q := newFakeDexQuerier()
	q.settings = &sqlc.DexSetting{
		ID:            dexSettingsSingletonID,
		IssuerUrl:     "https://dex.example.com",
		ClusterID:     pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Namespace:     "dex",
		ReleaseName:   "dex",
		ConfigmapName: "astronomer-dex-config",
		PublicClients: json.RawMessage(`[]`),
		Expiry:        json.RawMessage(`{}`),
		Extra:         json.RawMessage(`{}`),
	}
	h := NewDexHandler(q)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/apply/", nil)
	w := httptest.NewRecorder()
	h.Apply(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// ----- 4. CRUD round-trip via httptest+chi -----------------------------

func TestCreateAndGetConnector_RedactsSecret(t *testing.T) {
	q := newFakeDexQuerier()
	keyStr, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(keyStr)
	h := NewDexHandler(q)
	h.SetEncryptor(enc)

	body := map[string]any{
		"type":         "microsoft",
		"name":         "azure-prod",
		"display_name": "Azure AD",
		"config":       map[string]any{"tenant": "t", "clientID": "id", "clientSecret": "supersecret"},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/connectors/", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.CreateConnector(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	if len(q.connectors) != 1 {
		t.Fatalf("expected 1 connector, got %d", len(q.connectors))
	}
	var stored sqlc.DexConnector
	for _, c := range q.connectors {
		stored = c
	}
	cfg := decodeJSONMap(stored.Config)
	storedSecret, _ := cfg["clientSecret"].(string)
	if storedSecret == "supersecret" {
		t.Errorf("clientSecret should be encrypted at rest, got plaintext")
	}
	if storedSecret == "" {
		t.Errorf("clientSecret missing")
	}
	if pt, err := enc.Decrypt(storedSecret); err != nil || pt != "supersecret" {
		t.Errorf("encrypted secret didn't round-trip: pt=%q err=%v", pt, err)
	}

	// GET by ID — secret should be redacted.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/dex/connectors/"+stored.ID.String()+"/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", stored.ID.String())
	getReq = getReq.WithContext(context.WithValue(getReq.Context(), chi.RouteCtxKey, rctx))
	gw := httptest.NewRecorder()
	h.GetConnector(gw, getReq)
	if gw.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", gw.Code, gw.Body.String())
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(gw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	gotCfg := resp.Data["config"].(map[string]any)
	if gotCfg["clientSecret"] != "" {
		t.Errorf("clientSecret should be redacted in API response, got %v", gotCfg["clientSecret"])
	}
	if gotCfg["__clientSecret_set"] != true {
		t.Errorf("expected __clientSecret_set marker, got %v", gotCfg["__clientSecret_set"])
	}
}

func TestCreateConnector_400OnMissingRequired(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	body := map[string]any{
		"type": "microsoft",
		"name": "azure",
		"config": map[string]any{
			"clientID":     "id",
			"clientSecret": "s",
		},
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/connectors/", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.CreateConnector(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "tenant") {
		t.Errorf("expected error to mention 'tenant', got %s", w.Body.String())
	}
}

func TestRegisterAsSSO_CreatesProviderRow(t *testing.T) {
	q := newFakeDexQuerier()
	keyStr, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(keyStr)
	q.platform = &sqlc.PlatformConfiguration{
		ID:           1,
		ServerUrl:    "http://astronomer.example.com",
		PlatformName: "Astronomer",
	}
	q.settings = &sqlc.DexSetting{
		ID:            dexSettingsSingletonID,
		IssuerUrl:     "https://dex.example.com",
		Namespace:     "dex",
		ReleaseName:   "dex",
		ConfigmapName: "astronomer-dex-config",
		ClusterID:     pgtype.UUID{Bytes: uuid.New(), Valid: true},
		PublicClients: json.RawMessage(`[]`),
		Expiry:        json.RawMessage(`{}`),
		Extra:         json.RawMessage(`{}`),
	}
	h := NewDexHandler(q)
	h.SetEncryptor(enc)

	body := map[string]any{
		"client_id":     "astronomer",
		"client_secret": "shared-secret",
		"display_name":  "Sign in with Dex",
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/register-as-sso/", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	h.RegisterAsSSO(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	row, ok := q.ssoByProv["dex"]
	if !ok {
		t.Fatalf("expected dex SSO row to be created")
	}
	if row.ClientID != "astronomer" {
		t.Errorf("client_id = %s", row.ClientID)
	}
	if row.ClientSecretEncrypted == "" {
		t.Errorf("expected encrypted secret")
	}
	if pt, err := enc.Decrypt(row.ClientSecretEncrypted); err != nil || pt != "shared-secret" {
		t.Errorf("secret didn't round-trip: pt=%q err=%v", pt, err)
	}
	// Config JSON contains issuer_url.
	var cfg map[string]any
	_ = json.Unmarshal(row.Config, &cfg)
	if cfg["issuer_url"] != "https://dex.example.com" {
		t.Errorf("issuer_url not stored in sso config: %v", cfg)
	}
	var clients []map[string]any
	if err := json.Unmarshal(q.settings.PublicClients, &clients); err != nil {
		t.Fatalf("unmarshal public_clients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("public clients len=%d want 1", len(clients))
	}
	gotURIs := mergeStringList(clients[0]["redirectURIs"], nil)
	if !containsString(gotURIs, "http://astronomer.example.com/api/v1/auth/callback/dex") {
		t.Fatalf("missing normalized callback URI: %v", gotURIs)
	}
	if !containsString(gotURIs, "http://astronomer.example.com/api/v1/auth/callback/dex/") {
		t.Fatalf("missing slash callback URI: %v", gotURIs)
	}
	if clients[0]["secret"] != "shared-secret" {
		t.Fatalf("public client secret not synchronized")
	}
}

func TestRenderDexConfig_DefaultsRedirectURI(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	settings := sqlc.DexSetting{
		ID:            dexSettingsSingletonID,
		IssuerUrl:     "https://dex.example.com/dex",
		Namespace:     "dex",
		ReleaseName:   "dex",
		ConfigmapName: "astronomer-dex-config",
		ClusterID:     pgtype.UUID{Bytes: uuid.New(), Valid: true},
		PublicClients: json.RawMessage(`[]`),
		Expiry:        json.RawMessage(`{}`),
		Extra:         json.RawMessage(`{}`),
	}
	connectors := []sqlc.DexConnector{
		{
			ID:          uuid.New(),
			Name:        "oidc-live",
			Type:        "oidc",
			DisplayName: "OIDC Live",
			Config:      json.RawMessage(`{"issuer":"https://issuer.example.com","clientID":"id","clientSecret":"secret"}`),
			Enabled:     true,
		},
	}
	rendered, err := h.renderDexConfig(settings, connectors)
	if err != nil {
		t.Fatalf("renderDexConfig error: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(rendered, &doc); err != nil {
		t.Fatalf("unmarshal yaml: %v", err)
	}
	connList, _ := doc["connectors"].([]any)
	if len(connList) != 1 {
		t.Fatalf("connectors len=%d want 1", len(connList))
	}
	conn, _ := connList[0].(map[string]any)
	cfg, _ := conn["config"].(map[string]any)
	if got := cfg["redirectURI"]; got != "https://dex.example.com/dex/callback" {
		t.Fatalf("redirectURI=%v", got)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestUpdateSettings_RejectsMissingIssuer(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	body, _ := json.Marshal(map[string]any{"namespace": "dex"})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/dex/settings/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.UpdateSettings(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "issuer_url") {
		t.Errorf("expected error to mention issuer_url, got %s", w.Body.String())
	}
}

// base64StdEncode mirrors what the agent does so the handler's
// ensureSuccess / decodeResponseBody helpers see the right shape.
func base64StdEncode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
