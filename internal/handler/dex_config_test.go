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
	"fmt"
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
	auditRows  []sqlc.CreateAuditLogV1Params
}

func newFakeDexQuerier() *fakeDexQuerier {
	return &fakeDexQuerier{
		connectors: make(map[uuid.UUID]sqlc.DexConnector),
		ssoByProv:  make(map[string]sqlc.SsoConfiguration),
	}
}

func configureVerifiedDexRuntime(t *testing.T, q *fakeDexQuerier, h *DexHandler) {
	configureDexRuntime(t, q, h, true)
}

func configureDexRuntime(t *testing.T, q *fakeDexQuerier, h *DexHandler, deploymentReady bool) {
	t.Helper()
	if q.settings == nil {
		t.Fatal("settings must be configured first")
	}
	q.settings.Namespace = cmpOrTest(q.settings.Namespace, "dex")
	q.settings.ReleaseName = cmpOrTest(q.settings.ReleaseName, "dex")
	q.settings.DeploymentName = cmpOrTest(q.settings.DeploymentName, q.settings.ReleaseName)
	q.settings.ServiceName = cmpOrTest(q.settings.ServiceName, q.settings.ReleaseName)
	q.settings.RuntimeSecretName = cmpOrTest(q.settings.RuntimeSecretName, q.settings.ConfigmapName, "astronomer-dex-runtime")
	if q.settings.RuntimeGeneration == 0 {
		q.settings.RuntimeGeneration = 1
	}
	if !q.settings.ClusterID.Valid {
		q.settings.ClusterID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
	}
	samlConfig, _ := json.Marshal(map[string]any{"ssoURL": "https://idp.example.com/saml", "entityIssuer": "https://idp.example.com/metadata"})
	hasVerified := false
	for _, connector := range q.connectors {
		if connector.Name == "verified-saml" {
			hasVerified = true
		}
	}
	if !hasVerified {
		id := uuid.New()
		q.connectors[id] = sqlc.DexConnector{ID: id, Name: "verified-saml", Type: "saml", DisplayName: "Verified SAML", Config: samlConfig, Enabled: true}
	}

	secret := newDexRuntimeSecret(q.settings.Namespace, q.settings.RuntimeSecretName, nil)
	secret.Metadata.ResourceVersion = "1"
	deploymentRV := ""
	deploymentGeneration := ""
	h.SetK8sRequester(&stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		switch {
		case req.Method == http.MethodGet && strings.Contains(req.Path, "/secrets/"):
			raw, _ := json.Marshal(secret)
			return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode(raw)}, nil
		case req.Method == http.MethodPatch && strings.Contains(req.Path, "/secrets/"):
			var patch struct {
				Metadata struct {
					Labels      map[string]string `json:"labels"`
					Annotations map[string]string `json:"annotations"`
				} `json:"metadata"`
				Type string            `json:"type"`
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal(req.Body, &patch); err != nil {
				t.Fatal(err)
			}
			secret.Metadata.ResourceVersion = "2"
			secret.Metadata.Labels = patch.Metadata.Labels
			secret.Metadata.Annotations = patch.Metadata.Annotations
			secret.Type = patch.Type
			secret.Data = patch.Data
			raw, _ := json.Marshal(secret)
			return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode(raw)}, nil
		case req.Method == http.MethodPatch && strings.Contains(req.Path, "/deployments/"):
			var patch map[string]any
			_ = json.Unmarshal(req.Body, &patch)
			spec, _ := patch["spec"].(map[string]any)
			template, _ := spec["template"].(map[string]any)
			metadata, _ := template["metadata"].(map[string]any)
			annotations, _ := metadata["annotations"].(map[string]any)
			deploymentRV, _ = annotations["astronomer.io/dex-runtime-resource-version"].(string)
			deploymentGeneration, _ = annotations[dexRuntimeGenerationAnnotation].(string)
			return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode([]byte(`{}`))}, nil
		case req.Method == http.MethodGet && strings.Contains(req.Path, "/deployments/"):
			readyReplicas := 0
			conditions := []any{map[string]any{"type": "Available", "status": "False"}}
			if deploymentReady {
				readyReplicas = 1
				conditions = []any{map[string]any{"type": "Available", "status": "True"}}
			}
			deployment := map[string]any{
				"metadata": map[string]any{"generation": 3},
				"spec": map[string]any{"replicas": 1, "template": map[string]any{
					"metadata": map[string]any{"annotations": map[string]any{"astronomer.io/dex-runtime-resource-version": deploymentRV, dexRuntimeGenerationAnnotation: deploymentGeneration}},
					"spec":     map[string]any{"volumes": []any{map[string]any{"name": "config", "secret": map[string]any{"secretName": q.settings.RuntimeSecretName}}}},
				}},
				"status": map[string]any{"observedGeneration": 3, "updatedReplicas": 1, "readyReplicas": readyReplicas, "availableReplicas": readyReplicas, "unavailableReplicas": 1 - readyReplicas, "conditions": conditions},
			}
			raw, _ := json.Marshal(deployment)
			return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode(raw)}, nil
		case req.Method == http.MethodGet && strings.Contains(req.Path, "/services/"):
			return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode([]byte("ok"))}, nil
		default:
			t.Fatalf("unexpected Kubernetes request %s %s", req.Method, req.Path)
			return nil, nil
		}
	}})
}

func cmpOrTest(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
	if f.settings.RuntimeGeneration == 0 {
		f.settings.RuntimeGeneration = 1
	}
	f.settings.DeploymentName = cmpOrTest(f.settings.DeploymentName, f.settings.ReleaseName)
	f.settings.ServiceName = cmpOrTest(f.settings.ServiceName, f.settings.ReleaseName)
	return *f.settings, nil
}

func (f *fakeDexQuerier) UpsertDexSettings(_ context.Context, arg sqlc.UpsertDexSettingsParams) (sqlc.DexSetting, error) {
	if f.upsertErr != nil {
		return sqlc.DexSetting{}, f.upsertErr
	}
	generation := int64(1)
	if f.settings != nil {
		generation = f.settings.RuntimeGeneration + 1
	}
	row := sqlc.DexSetting{
		ID:                     arg.ID,
		IssuerUrl:              arg.IssuerUrl,
		ClusterID:              arg.ClusterID,
		Namespace:              arg.Namespace,
		ReleaseName:            arg.ReleaseName,
		ConfigmapName:          arg.ConfigmapName,
		RuntimeSecretName:      arg.RuntimeSecretName,
		PublicClients:          arg.PublicClients,
		PublicClientsEncrypted: arg.PublicClientsEncrypted,
		Expiry:                 arg.Expiry,
		Extra:                  arg.Extra,
		ChartReleaseName:       arg.ChartReleaseName,
		DeploymentName:         arg.DeploymentName,
		ServiceName:            arg.ServiceName,
		RuntimeGeneration:      generation,
		CreatedAt:              time.Now().UTC(),
		UpdatedAt:              time.Now().UTC(),
	}
	if f.settings != nil && f.settings.PublicClientsCutoverAt.Valid {
		row.PublicClients = json.RawMessage(`[]`)
		row.PublicClientsCutoverAt = f.settings.PublicClientsCutoverAt
	}
	f.settings = &row
	return row, nil
}

func (f *fakeDexQuerier) MarkDexRuntimeApplied(_ context.Context, arg sqlc.MarkDexRuntimeAppliedParams) (sqlc.DexSetting, error) {
	if f.settings == nil || f.settings.ID != arg.ID || f.settings.RuntimeGeneration != arg.RuntimeGeneration {
		return sqlc.DexSetting{}, errors.New("stale generation")
	}
	f.settings.RuntimeAppliedGeneration = arg.RuntimeGeneration
	return *f.settings, nil
}

func (f *fakeDexQuerier) BackfillDexPublicClientsEnvelope(_ context.Context, arg sqlc.BackfillDexPublicClientsEnvelopeParams) (sqlc.DexSetting, error) {
	if f.settings == nil || f.settings.ID != arg.ID || f.settings.PublicClientsEncrypted != "" ||
		!bytes.Equal(f.settings.PublicClients, arg.LegacyPublicClients) {
		return sqlc.DexSetting{}, errors.New("legacy migration conflict")
	}
	f.settings.PublicClientsEncrypted = arg.PublicClientsEncrypted
	f.settings.UpdatedAt = time.Now().UTC()
	return *f.settings, nil
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

func (f *fakeDexQuerier) EnableDexSSOForGeneration(_ context.Context, arg sqlc.EnableDexSSOForGenerationParams) (sqlc.SsoConfiguration, error) {
	if f.settings == nil || f.settings.RuntimeGeneration != arg.RuntimeGeneration || f.settings.RuntimeAppliedGeneration != arg.RuntimeGeneration {
		return sqlc.SsoConfiguration{}, errors.New("stale generation")
	}
	row, exists := f.ssoByProv["dex"]
	if !exists {
		row = sqlc.SsoConfiguration{ID: uuid.New(), Provider: "dex", AllowedOrganizations: json.RawMessage(`[]`), AllowedDomains: json.RawMessage(`[]`), AutoCreateUsers: true, CreatedAt: time.Now().UTC()}
	}
	row.IsEnabled, row.DisplayName, row.Config, row.ClientID, row.ClientSecretEncrypted = true, arg.DisplayName, arg.Config, arg.ClientID, arg.ClientSecretEncrypted
	row.UpdatedAt = time.Now().UTC()
	f.ssoByProv["dex"] = row
	return row, nil
}

func (f *fakeDexQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.auditRows = append(f.auditRows, arg)
	return nil
}

func (f *fakeDexQuerier) auditRowAt(t *testing.T, idx int) sqlc.CreateAuditLogV1Params {
	t.Helper()
	if len(f.auditRows) <= idx {
		t.Fatalf("audit rows=%d, want index %d", len(f.auditRows), idx)
	}
	return f.auditRows[idx]
}

func assertDexAudit(t *testing.T, row sqlc.CreateAuditLogV1Params, action, resourceType string) {
	t.Helper()
	if row.Action != action {
		t.Fatalf("audit action=%q want %q; row=%+v", row.Action, action, row)
	}
	if row.ResourceType != resourceType {
		t.Fatalf("audit resource_type=%q want %q; row=%+v", row.ResourceType, resourceType, row)
	}
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
				"host":   "ldap.example.com:636",
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
				"host":   "ldap.example.com:636",
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
			config:   map[string]any{"host": "h:636", "bindDN": "d", "bindPW": "p"},
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
			name:     "oidc rejects non-string secret",
			typ:      "oidc",
			config:   map[string]any{"issuer": "https://i", "clientID": "id", "clientSecret": map[string]any{"password": "synthetic-canary"}},
			wantOK:   false,
			wantMiss: []string{"clientSecret must be a string"},
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
			config: map[string]any{"clientID": "id", "clientSecret": "s", "tokenURL": "https://idp.example/token", "authorizationURL": "https://idp.example/authorize", "userInfoURL": "https://idp.example/userinfo"},
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

	publicClients := []map[string]any{{
		"id": "astronomer", "name": "Astronomer",
		"redirectURIs": []any{"https://astro.example.com/api/v1/auth/callback/dex/"},
		"secret":       "client-secret",
	}}
	out, err := h.renderDexConfig(settings, publicClients, connectors)
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
		defer func() {
			_ = resp.Body.Close()
		}()
		respBody, _ := io.ReadAll(resp.Body)
		return &protocol.K8sResponsePayload{
			StatusCode: resp.StatusCode,
			Body:       base64StdEncode(respBody),
		}, nil
	}
}

func TestApply_PatchesMetadataOnlyRuntimeSecretAndRollsByResourceVersion(t *testing.T) {
	var requests []string
	var secretBody, restartBody []byte
	metadataOnlySecret := newDexRuntimeSecret("dex", "astronomer-dex-runtime", nil)
	metadataOnlySecret.Data = nil
	metadataOnlySecret.Metadata.ResourceVersion = "18"
	currentSecret := metadataOnlySecret
	mockK8s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/dex/secrets/astronomer-dex-runtime" {
			current, _ := json.Marshal(currentSecret)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(current)
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/dex/secrets/astronomer-dex-runtime" {
			secretBody = append([]byte(nil), body...)
			_ = json.Unmarshal(body, &currentSecret)
			currentSecret.Metadata.ResourceVersion = "19"
			updated, _ := json.Marshal(currentSecret)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(updated)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/dex/deployments/dex" {
			deployment := map[string]any{"metadata": map[string]any{"generation": 2}, "spec": map[string]any{"replicas": 1, "template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"astronomer.io/dex-runtime-resource-version": "19", dexRuntimeGenerationAnnotation: "1"}}, "spec": map[string]any{"volumes": []any{map[string]any{"name": "config", "secret": map[string]any{"secretName": "astronomer-dex-runtime"}}}}}}, "status": map[string]any{"observedGeneration": 2, "updatedReplicas": 1, "readyReplicas": 1, "availableReplicas": 1, "unavailableReplicas": 0, "conditions": []any{map[string]any{"type": "Available", "status": "True"}}}}
			raw, _ := json.Marshal(deployment)
			_, _ = w.Write(raw)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/dex/services/http:dex:5556/proxy/healthz" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/apis/apps/v1/namespaces/dex/deployments/dex" {
			restartBody = append([]byte(nil), body...)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"kind":"Deployment"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockK8s.Close()

	q := newFakeDexQuerier()
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	ciphertext, _ := enc.Encrypt("hostile-'\"-secret")
	clusterID := uuid.New()
	q.settings = &sqlc.DexSetting{
		ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example.com", Namespace: "dex",
		ReleaseName: "dex", RuntimeSecretName: "astronomer-dex-runtime",
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, PublicClients: json.RawMessage(`[]`),
		Expiry: json.RawMessage(`{}`), Extra: json.RawMessage(`{}`),
	}
	connectorCfg, _ := json.Marshal(map[string]any{
		"tenant": "abc", "clientID": "id", "clientSecret": ciphertext,
	})
	cid := uuid.New()
	q.connectors[cid] = sqlc.DexConnector{
		ID: cid, Name: "azure", Type: "microsoft", Config: connectorCfg, Enabled: true,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	h := NewDexHandler(q)
	h.SetEncryptor(enc)
	h.SetK8sRequester(&stubK8sRequester{respFn: proxyToHTTPTest(mockK8s)})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/apply/", nil)
	w := httptest.NewRecorder()
	h.Apply(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	wantRequests := []string{
		"GET /api/v1/namespaces/dex/secrets/astronomer-dex-runtime",
		"PATCH /api/v1/namespaces/dex/secrets/astronomer-dex-runtime",
		"GET /api/v1/namespaces/dex/secrets/astronomer-dex-runtime",
		"PATCH /apis/apps/v1/namespaces/dex/deployments/dex",
		"GET /apis/apps/v1/namespaces/dex/deployments/dex",
		"GET /api/v1/namespaces/dex/services/http:dex:5556/proxy/healthz",
	}
	if strings.Join(requests, "|") != strings.Join(wantRequests, "|") {
		t.Fatalf("requests=%v want=%v", requests, wantRequests)
	}
	var secret dexRuntimeSecret
	if err := json.Unmarshal(secretBody, &secret); err != nil {
		t.Fatal(err)
	}
	rendered, err := base64.StdEncoding.DecodeString(secret.Data["config.yaml"])
	if err != nil || !bytes.Contains(rendered, []byte("hostile-'\"-secret")) {
		t.Fatalf("runtime Secret did not preserve hostile credential: err=%v config=%q", err, rendered)
	}
	metadataOnly := append([]byte(nil), secretBody...)
	var generic map[string]any
	_ = json.Unmarshal(metadataOnly, &generic)
	delete(generic, "data")
	metadataOnly, _ = json.Marshal(generic)
	if bytes.Contains(metadataOnly, []byte("hostile")) || bytes.Contains(metadataOnly, []byte("sha256")) {
		t.Fatalf("Secret metadata leaked content-derived material: %s", metadataOnly)
	}
	if !bytes.Contains(restartBody, []byte(`"astronomer.io/dex-runtime-resource-version":"19"`)) {
		t.Fatalf("restart patch did not use Secret resourceVersion: %s", restartBody)
	}
	auditRow := q.auditRowAt(t, 0)
	assertDexAudit(t, auditRow, "dex.config.apply", "dex_settings")
	assertAuditDetail(t, auditRow.Detail, "cluster_id", clusterID.String())
	assertAuditDetail(t, auditRow.Detail, "namespace", "dex")
}

func TestApply_FixedPointDoesNotMutateOrRollout(t *testing.T) {
	var requests []string
	q := newFakeDexQuerier()
	clusterID := uuid.New()
	q.settings = &sqlc.DexSetting{
		ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example.com", Namespace: "dex",
		ReleaseName: "dex", RuntimeSecretName: "astronomer-dex-runtime",
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true}, PublicClients: json.RawMessage(`[]`),
		Expiry: json.RawMessage(`{}`), Extra: json.RawMessage(`{}`),
	}
	h := NewDexHandler(q)
	rendered, err := h.renderDexConfig(*q.settings, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	existing := newDexRuntimeSecret("dex", "astronomer-dex-runtime", rendered, 1)
	existing.Metadata.ResourceVersion = "42"
	existingJSON, _ := json.Marshal(existing)
	mockK8s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/secrets/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(existingJSON)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/") {
			deployment := map[string]any{"metadata": map[string]any{"generation": 2}, "spec": map[string]any{"replicas": 1, "template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"astronomer.io/dex-runtime-resource-version": "42", dexRuntimeGenerationAnnotation: "1"}}, "spec": map[string]any{"volumes": []any{map[string]any{"name": "config", "secret": map[string]any{"secretName": "astronomer-dex-runtime"}}}}}}, "status": map[string]any{"observedGeneration": 2, "updatedReplicas": 1, "readyReplicas": 1, "availableReplicas": 1, "conditions": []any{map[string]any{"type": "Available", "status": "True"}}}}
			raw, _ := json.Marshal(deployment)
			_, _ = w.Write(raw)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/services/") {
			_, _ = w.Write([]byte("ok"))
			return
		}
		t.Fatalf("fixed point unexpectedly mutated Kubernetes with %s %s", r.Method, r.URL.Path)
	}))
	defer mockK8s.Close()
	h.SetK8sRequester(&stubK8sRequester{respFn: proxyToHTTPTest(mockK8s)})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/apply/", nil)
	w := httptest.NewRecorder()
	h.Apply(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(requests) != 4 || requests[0] != "GET /api/v1/namespaces/dex/secrets/astronomer-dex-runtime" {
		t.Fatalf("fixed point requests=%v", requests)
	}
}

func TestApplyRuntimeSecret_FailsClosedWhenChartIdentityMissing(t *testing.T) {
	h := NewDexHandler(newFakeDexQuerier())
	h.SetK8sRequester(&stubK8sRequester{respFn: func(_ stubReq) (*protocol.K8sResponsePayload, error) {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	}})
	changed, version, err := h.applyRuntimeSecret(context.Background(), uuid.NewString(), "dex", "astronomer-dex-runtime", []byte("secret: never-log-me"))
	if err == nil || !strings.Contains(err.Error(), "install or upgrade the chart") {
		t.Fatalf("error=%v, want operator guidance", err)
	}
	if changed || version != "" || strings.Contains(err.Error(), "never-log-me") {
		t.Fatalf("missing identity leaked or reported mutation: changed=%v version=%q err=%v", changed, version, err)
	}
}

func TestApplyRuntimeSecret_RefusesUnownedNameCollision(t *testing.T) {
	unowned := dexRuntimeSecret{APIVersion: "v1", Kind: "Secret", Type: "Opaque"}
	unowned.Metadata.Name = "astronomer-dex-runtime"
	unowned.Metadata.ResourceVersion = "7"
	unowned.Data = map[string]string{"config.yaml": base64.StdEncoding.EncodeToString([]byte("operator-owned"))}
	raw, _ := json.Marshal(unowned)
	h := NewDexHandler(newFakeDexQuerier())
	h.SetK8sRequester(&stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected mutation %s", req.Method)
		}
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode(raw)}, nil
	}})
	changed, _, err := h.applyRuntimeSecret(context.Background(), uuid.NewString(), "dex", "astronomer-dex-runtime", []byte("replacement"))
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

func TestApplyRuntimeSecret_ReconcilesOwnedMetadataAndPreservesForeignFields(t *testing.T) {
	existing := newDexRuntimeSecret("dex", "astronomer-dex-runtime", []byte("config"))
	existing.Metadata.ResourceVersion = "8"
	delete(existing.Metadata.Labels, "astronomer.io/backup-reconstruction")
	existing.Metadata.Labels["operator.example/foreign"] = "preserve"
	existing.Metadata.Annotations["operator.example/note"] = "preserve"
	existing.Data["operator-extra"] = base64.StdEncoding.EncodeToString([]byte("preserve"))
	raw, _ := json.Marshal(existing)
	var patchBody []byte
	h := NewDexHandler(newFakeDexQuerier())
	h.SetK8sRequester(&stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		if req.Method == http.MethodGet {
			return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode(raw)}, nil
		}
		patchBody = append([]byte(nil), req.Body...)
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode([]byte(`{"metadata":{"resourceVersion":"9"}}`))}, nil
	}})
	changed, version, err := h.applyRuntimeSecret(context.Background(), uuid.NewString(), "dex", "astronomer-dex-runtime", []byte("config"))
	if err != nil || changed || version != "9" {
		t.Fatalf("changed=%v version=%q err=%v", changed, version, err)
	}
	text := string(patchBody)
	if !strings.Contains(text, "backup-reconstruction") || strings.Contains(text, "operator.example/foreign") || strings.Contains(text, "operator-extra") {
		t.Fatalf("unsafe metadata patch: %s", text)
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
	createAudit := q.auditRowAt(t, 0)
	assertDexAudit(t, createAudit, "dex.connector.create", "dex_connector")
	if createAudit.ResourceID != stored.ID.String() || createAudit.ResourceName != "azure-prod" {
		t.Fatalf("connector create audit target=(%q,%q), want (%q,azure-prod)", createAudit.ResourceID, createAudit.ResourceName, stored.ID.String())
	}
	assertAuditDetail(t, createAudit.Detail, "type", "microsoft")
	assertAuditDetailOmit(t, createAudit.Detail, "clientSecret")
	assertAuditDetailOmit(t, createAudit.Detail, "client_secret")

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

func TestDexConnectorUpdateAndDeleteAreAudited(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	connectorID := uuid.New()
	cfg, _ := json.Marshal(map[string]any{
		"tenant":       "t",
		"clientID":     "id",
		"clientSecret": "encrypted",
	})
	q.connectors[connectorID] = sqlc.DexConnector{
		ID:          connectorID,
		Name:        "azure-prod",
		Type:        "microsoft",
		DisplayName: "Azure AD",
		Config:      cfg,
		Enabled:     true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	raw, _ := json.Marshal(map[string]any{
		"display_name": "Azure AD Prod",
		"enabled":      false,
	})
	updateReq := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/dex/connectors/"+connectorID.String()+"/", bytes.NewReader(raw))
	updateCtx := chi.NewRouteContext()
	updateCtx.URLParams.Add("id", connectorID.String())
	updateReq = updateReq.WithContext(context.WithValue(updateReq.Context(), chi.RouteCtxKey, updateCtx))
	updateRec := httptest.NewRecorder()
	h.UpdateConnector(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", updateRec.Code, updateRec.Body.String())
	}
	updateAudit := q.auditRowAt(t, 0)
	assertDexAudit(t, updateAudit, "dex.connector.update", "dex_connector")
	if updateAudit.ResourceID != connectorID.String() || updateAudit.ResourceName != "azure-prod" {
		t.Fatalf("connector update audit target=(%q,%q), want (%q,azure-prod)", updateAudit.ResourceID, updateAudit.ResourceName, connectorID.String())
	}
	assertAuditDetail(t, updateAudit.Detail, "type", "microsoft")
	assertAuditDetailOmit(t, updateAudit.Detail, "clientSecret")
	assertAuditDetailOmit(t, updateAudit.Detail, "client_secret")

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/dex/connectors/"+connectorID.String()+"/", nil)
	deleteCtx := chi.NewRouteContext()
	deleteCtx.URLParams.Add("id", connectorID.String())
	deleteReq = deleteReq.WithContext(context.WithValue(deleteReq.Context(), chi.RouteCtxKey, deleteCtx))
	deleteRec := httptest.NewRecorder()
	h.DeleteConnector(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	deleteAudit := q.auditRowAt(t, 1)
	assertDexAudit(t, deleteAudit, "dex.connector.delete", "dex_connector")
	if deleteAudit.ResourceID != connectorID.String() || deleteAudit.ResourceName != "azure-prod" {
		t.Fatalf("connector delete audit target=(%q,%q), want (%q,azure-prod)", deleteAudit.ResourceID, deleteAudit.ResourceName, connectorID.String())
	}
	assertAuditDetail(t, deleteAudit.Detail, "type", "microsoft")
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
		ServerUrl:    "https://astronomer.example.com",
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
	configureVerifiedDexRuntime(t, q, h)

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
	plainClients, err := enc.Decrypt(q.settings.PublicClientsEncrypted)
	if err != nil {
		t.Fatalf("decrypt public_clients: %v", err)
	}
	if err := json.Unmarshal([]byte(plainClients), &clients); err != nil {
		t.Fatalf("unmarshal public_clients: %v", err)
	}
	if len(clients) != 1 {
		t.Fatalf("public clients len=%d want 1", len(clients))
	}
	gotURIs := mergeStringList(clients[0]["redirectURIs"], nil)
	if !containsString(gotURIs, "https://astronomer.example.com/api/v1/auth/callback/dex") {
		t.Fatalf("missing normalized callback URI: %v", gotURIs)
	}
	if !containsString(gotURIs, "https://astronomer.example.com/api/v1/auth/callback/dex/") {
		t.Fatalf("missing slash callback URI: %v", gotURIs)
	}
	if clients[0]["secret"] != "shared-secret" {
		t.Fatalf("public client secret not synchronized")
	}
	rendered, err := h.renderDexConfig(*q.settings, clients, nil)
	if err != nil {
		t.Fatal(err)
	}
	var dexDoc map[string]any
	if err := yaml.Unmarshal(rendered, &dexDoc); err != nil {
		t.Fatal(err)
	}
	staticClients, _ := dexDoc["staticClients"].([]any)
	dexClient, _ := staticClients[0].(map[string]any)
	serverSecret, err := enc.Decrypt(row.ClientSecretEncrypted)
	if err != nil || dexClient["secret"] != serverSecret {
		t.Fatalf("Dex/server credential pair diverged: dex=%v server=%q err=%v", dexClient["secret"], serverSecret, err)
	}
	auditRow := q.auditRowAt(t, 0)
	assertDexAudit(t, auditRow, "dex.register_sso", "sso_configuration")
	if auditRow.ResourceID != row.ID.String() || auditRow.ResourceName != "dex" {
		t.Fatalf("register SSO audit target=(%q,%q), want (%q,dex)", auditRow.ResourceID, auditRow.ResourceName, row.ID.String())
	}
	assertAuditDetail(t, auditRow.Detail, "client_id", "astronomer")
	assertAuditDetail(t, auditRow.Detail, "issuer_url", "https://dex.example.com")
	assertAuditDetailOmit(t, auditRow.Detail, "client_secret")

	rotateBody, _ := json.Marshal(map[string]any{
		"client_id": "astronomer", "client_secret": "rotated-shared-secret", "display_name": "Sign in with Dex",
	})
	rotateRecorder := httptest.NewRecorder()
	h.RegisterAsSSO(rotateRecorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/register-as-sso/", bytes.NewReader(rotateBody)))
	if rotateRecorder.Code != http.StatusOK {
		t.Fatalf("rotation status=%d body=%s", rotateRecorder.Code, rotateRecorder.Body.String())
	}
	rotatedServerSecret, err := enc.Decrypt(q.ssoByProv["dex"].ClientSecretEncrypted)
	if err != nil {
		t.Fatal(err)
	}
	rotatedClientsJSON, err := enc.Decrypt(q.settings.PublicClientsEncrypted)
	if err != nil {
		t.Fatal(err)
	}
	var rotatedClients []map[string]any
	if err := json.Unmarshal([]byte(rotatedClientsJSON), &rotatedClients); err != nil {
		t.Fatal(err)
	}
	if rotatedServerSecret != "rotated-shared-secret" || rotatedClients[0]["secret"] != rotatedServerSecret {
		t.Fatalf("atomic rotation pair diverged: server=%q clients=%#v", rotatedServerSecret, rotatedClients)
	}
}

func TestRegisterAsSSO_AtomicFailurePreservesCredentialPair(t *testing.T) {
	q := newFakeDexQuerier()
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	oldCipher, _ := enc.Encrypt("old-paired-secret")
	oldClients, _ := enc.Encrypt(`[{"id":"astronomer","secret":"old-paired-secret"}]`)
	q.platform = &sqlc.PlatformConfiguration{ID: 1, ServerUrl: "https://astronomer.example.com"}
	q.settings = &sqlc.DexSetting{
		ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example.com", Namespace: "dex",
		ReleaseName: "dex", RuntimeSecretName: "astronomer-dex-runtime",
		PublicClients: json.RawMessage(`[]`), PublicClientsEncrypted: oldClients,
		Expiry: json.RawMessage(`{}`), Extra: json.RawMessage(`{}`),
	}
	q.ssoByProv["dex"] = sqlc.SsoConfiguration{
		ID: uuid.New(), Provider: "dex", ClientID: "astronomer", ClientSecretEncrypted: oldCipher,
		AllowedOrganizations: json.RawMessage(`[]`), AllowedDomains: json.RawMessage(`[]`), AutoCreateUsers: true,
	}
	q.upsertErr = errors.New("atomic statement failed")
	h := NewDexHandler(q)
	h.SetEncryptor(enc)
	configureVerifiedDexRuntime(t, q, h)
	body, _ := json.Marshal(map[string]any{"client_id": "astronomer", "client_secret": "new-secret"})
	recorder := httptest.NewRecorder()
	h.RegisterAsSSO(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/register-as-sso/", bytes.NewReader(body)))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if q.settings.PublicClientsEncrypted != oldClients || q.ssoByProv["dex"].ClientSecretEncrypted != oldCipher ||
		strings.Contains(recorder.Body.String(), "new-secret") {
		t.Fatalf("failed atomic rotation changed or leaked the pair")
	}
}

func TestRegisterAsSSORolloutFailureLeavesProviderDisabledAndRetryConverges(t *testing.T) {
	q := newFakeDexQuerier()
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	oldCipher, _ := enc.Encrypt("old-paired-secret")
	oldClients, _ := enc.Encrypt(`[{"id":"astronomer","name":"Astronomer","redirectURIs":["https://platform.example/api/v1/auth/callback/dex"],"secret":"old-paired-secret"}]`)
	q.platform = &sqlc.PlatformConfiguration{ID: 1, ServerUrl: "https://platform.example"}
	q.settings = &sqlc.DexSetting{
		ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example.com", Namespace: "dex",
		ReleaseName: "dex", RuntimeSecretName: "astronomer-dex-runtime",
		ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, PublicClients: json.RawMessage(`[]`),
		PublicClientsEncrypted: oldClients, PublicClientsCutoverAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		Expiry: json.RawMessage(`{}`), Extra: json.RawMessage(`{}`),
	}
	q.ssoByProv["dex"] = sqlc.SsoConfiguration{
		ID: uuid.New(), Provider: "dex", IsEnabled: true, DisplayName: "Sign in with Dex",
		Config: json.RawMessage(`{"issuer_url":"https://dex.example.com"}`), ClientID: "astronomer",
		ClientSecretEncrypted: oldCipher, AllowedOrganizations: json.RawMessage(`[]`), AllowedDomains: json.RawMessage(`[]`), AutoCreateUsers: true,
	}
	h := NewDexHandler(q)
	h.SetEncryptor(enc)
	h.rolloutTimeout = 5 * time.Millisecond
	h.rolloutPollInterval = time.Millisecond
	configureDexRuntime(t, q, h, false)
	body, _ := json.Marshal(map[string]any{"client_id": "astronomer", "client_secret": "new-paired-secret"})
	failed := httptest.NewRecorder()
	h.RegisterAsSSO(failed, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if failed.Code != http.StatusBadGateway || q.ssoByProv["dex"].IsEnabled {
		t.Fatalf("failed rollout must leave provider disabled: status=%d row=%#v body=%s", failed.Code, q.ssoByProv["dex"], failed.Body.String())
	}

	h.rolloutTimeout = time.Second
	configureDexRuntime(t, q, h, true)
	retried := httptest.NewRecorder()
	h.RegisterAsSSO(retried, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
	if retried.Code != http.StatusOK || !q.ssoByProv["dex"].IsEnabled || !strings.Contains(retried.Body.String(), `"verified":true`) {
		t.Fatalf("retry did not converge: status=%d row=%#v body=%s", retried.Code, q.ssoByProv["dex"], retried.Body.String())
	}
	serverSecret, err := enc.Decrypt(q.ssoByProv["dex"].ClientSecretEncrypted)
	if err != nil || serverSecret != "new-paired-secret" {
		t.Fatalf("retry enabled wrong server credential: %q err=%v", serverSecret, err)
	}
}

func TestRenderDexConfig_DefaultsRedirectURI(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	h.SetEncryptor(enc)
	ciphertext, _ := enc.Encrypt("secret")
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
			Config:      json.RawMessage(fmt.Sprintf(`{"issuer":"https://issuer.example.com","clientID":"id","clientSecret":%q}`, ciphertext)),
			Enabled:     true,
		},
	}
	rendered, err := h.renderDexConfig(settings, nil, connectors)
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

func TestGetSettings_BackfillsEnvelopeWithoutScrubbingCompatibilityCopy(t *testing.T) {
	q := newFakeDexQuerier()
	legacySecret := "legacy-low-entropy-secret"
	q.settings = &sqlc.DexSetting{
		ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example.com", Namespace: "dex",
		ReleaseName: "dex", ConfigmapName: "legacy-dex-config",
		PublicClients: json.RawMessage(`[{"id":"astronomer","name":"Astronomer","redirectURIs":["https://platform.example/callback"],"secret":"` + legacySecret + `"}]`),
		Expiry:        json.RawMessage(`{}`), Extra: json.RawMessage(`{}`), UpdatedAt: time.Now().UTC(),
	}
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	h := NewDexHandler(q)
	h.SetEncryptor(enc)
	configureVerifiedDexRuntime(t, q, h)
	recorder := httptest.NewRecorder()
	h.GetSettings(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/dex/settings/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(string(q.settings.PublicClients), legacySecret) || q.settings.PublicClientsEncrypted == "" || q.settings.PublicClientsCutoverAt.Valid {
		t.Fatalf("compatibility row was not safely dual-stored before cutover: %#v", q.settings)
	}
	if strings.Contains(q.settings.PublicClientsEncrypted, legacySecret) || strings.Contains(recorder.Body.String(), legacySecret) {
		t.Fatalf("legacy secret leaked after migration: row=%q response=%s", q.settings.PublicClientsEncrypted, recorder.Body.String())
	}
	var response struct {
		Data struct {
			RuntimeSecretName string `json:"runtime_secret_name"`
			PublicClients     []struct {
				Secret           string `json:"secret"`
				SecretConfigured bool   `json:"secret_configured"`
			} `json:"public_clients"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.RuntimeSecretName != "legacy-dex-config" || len(response.Data.PublicClients) != 1 ||
		response.Data.PublicClients[0].Secret != "" || !response.Data.PublicClients[0].SecretConfigured {
		t.Fatalf("response did not preserve alias/redaction contract: %#v", response.Data)
	}
}

func TestLegacyDexExtensionsFailClosedWithoutCanaryDisclosure(t *testing.T) {
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	clients, _ := enc.Encrypt(`[{"id":"app","redirectURIs":["https://platform.example/callback"],"secret":"synthetic"}]`)
	for name, fixture := range map[string]struct {
		expiry json.RawMessage
		extra  json.RawMessage
	}{
		"malformed expiry":    {expiry: json.RawMessage(`{"idTokens":"1h","futurePassword":"DORMANT-DEX-CANARY"}`), extra: json.RawMessage(`{}`)},
		"secret-shaped extra": {expiry: json.RawMessage(`{}`), extra: json.RawMessage(`{"logger":{"token":"DORMANT-DEX-CANARY"}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			q := newFakeDexQuerier()
			q.settings = &sqlc.DexSetting{ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example.com", PublicClients: json.RawMessage(`[]`), PublicClientsEncrypted: clients, PublicClientsCutoverAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, Expiry: fixture.expiry, Extra: fixture.extra}
			h := NewDexHandler(q)
			h.SetEncryptor(enc)
			recorder := httptest.NewRecorder()
			h.GetSettings(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
			if recorder.Code < 400 || strings.Contains(recorder.Body.String(), "DORMANT-DEX-CANARY") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if _, err := h.renderDexConfig(*q.settings, []map[string]any{{"id": "app", "redirectURIs": []string{"https://platform.example/callback"}, "secret": "synthetic"}}, nil); err == nil || strings.Contains(err.Error(), "DORMANT-DEX-CANARY") {
				t.Fatalf("render error=%v", err)
			}
		})
	}
}

func TestBundledSettingsBindPreservesChartIdentityAndRuntimeConfiguration(t *testing.T) {
	q := newFakeDexQuerier()
	q.settings = &sqlc.DexSetting{ID: dexSettingsSingletonID, IssuerUrl: "https://old.example.com/dex", Namespace: "platform-system", ReleaseName: "elite-dex", ChartReleaseName: "elite", DeploymentName: "elite-dex", ServiceName: "elite-dex", RuntimeSecretName: "elite-dex-runtime", PublicClients: json.RawMessage(`[]`), Expiry: json.RawMessage(`{"idTokens":"2h"}`), Extra: json.RawMessage(`{"logger":{"level":"info"}}`), RuntimeGeneration: 7}
	h := NewDexHandler(q)
	h.bundledIdentity = &dexRuntimeIdentity{Namespace: "platform-system", ChartReleaseName: "elite", DeploymentName: "elite-dex", ServiceName: "elite-dex", RuntimeSecretName: "elite-dex-runtime"}
	clusterID := uuid.NewString()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/dex/settings/", strings.NewReader(`{"issuer_url":"https://platform.example.com/dex","cluster_id":"`+clusterID+`"}`))
	w := httptest.NewRecorder()
	h.UpdateSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	row := q.settings
	if row.Namespace != "platform-system" || row.ChartReleaseName != "elite" || row.DeploymentName != "elite-dex" || row.ServiceName != "elite-dex" || row.RuntimeSecretName != "elite-dex-runtime" {
		t.Fatalf("bundled identity drifted: %#v", row)
	}
	if string(row.Expiry) != `{"idTokens":"2h"}` || string(row.Extra) != `{"logger":{"level":"info"}}` {
		t.Fatalf("bind overwrote runtime config: expiry=%s extra=%s", row.Expiry, row.Extra)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/auth/dex/settings/", strings.NewReader(`{"issuer_url":"https://platform.example.com/dex","cluster_id":"`+clusterID+`","namespace":"attacker"}`))
	w = httptest.NewRecorder()
	h.UpdateSettings(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("immutable bundled namespace accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestCustomDexRuntimeIdentityRoundTripsEndToEnd(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	clusterID := uuid.NewString()
	body := `{"issuer_url":"https://dex.customer.example/auth","cluster_id":"` + clusterID + `","namespace":"customer-auth","release_name":"customer-dex","chart_release_name":"customer-stack","deployment_name":"customer-dex-server","service_name":"customer-dex-http","runtime_secret_name":"customer-dex-runtime"}`
	w := httptest.NewRecorder()
	h.UpdateSettings(w, httptest.NewRequest(http.MethodPut, "/api/v1/auth/dex/settings/", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	row := q.settings
	if row.Namespace != "customer-auth" || row.ChartReleaseName != "customer-stack" || row.DeploymentName != "customer-dex-server" || row.ServiceName != "customer-dex-http" || row.RuntimeSecretName != "customer-dex-runtime" {
		t.Fatalf("custom identity did not round trip: %#v", row)
	}
}

func TestStaleGenerationCannotEnableDexSSOOrOverwriteNewerSecret(t *testing.T) {
	q := newFakeDexQuerier()
	q.settings = &sqlc.DexSetting{ID: dexSettingsSingletonID, RuntimeGeneration: 2, RuntimeAppliedGeneration: 2}
	if _, err := q.EnableDexSSOForGeneration(context.Background(), sqlc.EnableDexSSOForGenerationParams{RuntimeGeneration: 1, ClientID: "stale"}); err == nil {
		t.Fatal("stale generation enabled SSO")
	}
	if _, exists := q.ssoByProv["dex"]; exists {
		t.Fatal("stale generation mutated SSO")
	}

	secret := newDexRuntimeSecret("dex", "runtime", []byte("newer"), 2)
	secret.Metadata.ResourceVersion = "9"
	raw, _ := json.Marshal(secret)
	patched := false
	h := NewDexHandler(q)
	h.SetK8sRequester(&stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		if req.Method == http.MethodPatch {
			patched = true
		}
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64StdEncode(raw)}, nil
	}})
	if _, _, err := h.applyRuntimeSecret(context.Background(), uuid.NewString(), "dex", "runtime", []byte("older"), 1); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale Secret write err=%v", err)
	}
	if patched {
		t.Fatal("stale generation patched the newer Secret")
	}
}

func TestLoadPublicClientsMixedVersionAuthorityAndCutover(t *testing.T) {
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	h.SetEncryptor(enc)
	staleEnvelope, _ := enc.Encrypt(`[{"id":"stale","redirectURIs":["https://platform.example/stale"],"secret":"stale"}]`)
	freshEnvelope, _ := enc.Encrypt(`[{"id":"fresh","redirectURIs":["https://platform.example/fresh"],"secret":"fresh"}]`)

	t.Run("pre-cutover compatibility copy wins over stale envelope", func(t *testing.T) {
		row := sqlc.DexSetting{ID: dexSettingsSingletonID, PublicClients: json.RawMessage(`[{"id":"old-writer","redirectURIs":["https://platform.example/latest"],"secret":"latest"}]`), PublicClientsEncrypted: staleEnvelope}
		clients, _, err := h.loadPublicClients(context.Background(), row)
		if err != nil || len(clients) != 1 || clients[0]["id"] != "old-writer" {
			t.Fatalf("clients=%#v err=%v", clients, err)
		}
	})

	t.Run("post-cutover envelope is authoritative", func(t *testing.T) {
		row := sqlc.DexSetting{ID: dexSettingsSingletonID, PublicClients: json.RawMessage(`[]`), PublicClientsEncrypted: freshEnvelope, PublicClientsCutoverAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
		clients, _, err := h.loadPublicClients(context.Background(), row)
		if err != nil || len(clients) != 1 || clients[0]["id"] != "fresh" {
			t.Fatalf("clients=%#v err=%v", clients, err)
		}
	})

	t.Run("post-cutover empty envelope means empty client set", func(t *testing.T) {
		row := sqlc.DexSetting{ID: dexSettingsSingletonID, PublicClients: json.RawMessage(`[]`), PublicClientsCutoverAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
		clients, _, err := h.loadPublicClients(context.Background(), row)
		if err != nil || len(clients) != 0 {
			t.Fatalf("clients=%#v err=%v", clients, err)
		}
	})

	t.Run("post-cutover encrypted clients fail closed without encryptor", func(t *testing.T) {
		withoutKey := NewDexHandler(q)
		row := sqlc.DexSetting{ID: dexSettingsSingletonID, PublicClients: json.RawMessage(`[]`), PublicClientsEncrypted: freshEnvelope, PublicClientsCutoverAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
		if _, _, err := withoutKey.loadPublicClients(context.Background(), row); err == nil {
			t.Fatal("expected missing encryptor failure")
		}
	})
}

func TestCreateConnector_FailsClosedWithoutEncryptor(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	raw, _ := json.Marshal(map[string]any{
		"type": "oidc", "name": "unsafe", "config": map[string]any{
			"issuer": "https://idp.example.com", "clientID": "id", "clientSecret": "must-not-store",
		},
	})
	recorder := httptest.NewRecorder()
	h.CreateConnector(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/auth/dex/connectors/", bytes.NewReader(raw)))
	if recorder.Code != http.StatusInternalServerError || len(q.connectors) != 0 || strings.Contains(recorder.Body.String(), "must-not-store") {
		t.Fatalf("connector write did not fail closed: status=%d rows=%d body=%s", recorder.Code, len(q.connectors), recorder.Body.String())
	}
}

func TestDexClosedSchemasRejectSecretShapedBypassesAndTypeTransition(t *testing.T) {
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	h.SetEncryptor(enc)
	for name, body := range map[string]map[string]any{
		"connector unknown secret":     {"type": "oidc", "name": "unsafe", "config": map[string]any{"issuer": "https://idp.example", "clientID": "id", "clientSecret": "known", "futurePassword": "synthetic-canary"}},
		"connector top-level secret":   {"type": "oidc", "name": "unsafe", "config": map[string]any{"issuer": "https://idp.example", "clientID": "id", "clientSecret": "known"}, "futurePassword": "synthetic-canary"},
		"settings top-level secret":    {"issuer_url": "https://dex.example", "futurePassword": "synthetic-canary"},
		"static client unknown secret": {"issuer_url": "https://dex.example", "public_clients": []any{map[string]any{"id": "app", "secret": "known", "apiToken": "synthetic-canary"}}},
		"extra nested secret":          {"issuer_url": "https://dex.example", "extra": map[string]any{"logger": map[string]any{"token": "synthetic-canary"}}},
	} {
		raw, _ := json.Marshal(body)
		recorder := httptest.NewRecorder()
		if strings.HasPrefix(name, "connector") {
			h.CreateConnector(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)))
		} else {
			h.UpdateSettings(recorder, httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(raw)))
		}
		if recorder.Code != http.StatusBadRequest || strings.Contains(recorder.Body.String(), "synthetic-canary") {
			t.Fatalf("%s status=%d body=%s", name, recorder.Code, recorder.Body.String())
		}
	}

	cipher, _ := enc.Encrypt("existing-secret")
	id := uuid.New()
	config, _ := json.Marshal(map[string]any{"host": "ldap.example:636", "bindDN": "cn=svc", "bindPW": cipher, "userSearch": map[string]any{"baseDN": "dc=example", "username": "uid", "idAttr": "uid", "emailAttr": "mail"}})
	q.connectors[id] = sqlc.DexConnector{ID: id, Name: "ldap", Type: "ldap", Config: config, Enabled: true}
	raw, _ := json.Marshal(map[string]any{"type": "saml"})
	req := httptest.NewRequest(http.MethodPatch, "/", bytes.NewReader(raw))
	route := chi.NewRouteContext()
	route.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	recorder := httptest.NewRecorder()
	h.UpdateConnector(recorder, req)
	if recorder.Code != http.StatusBadRequest || q.connectors[id].Type != "ldap" {
		t.Fatalf("unsafe type transition status=%d", recorder.Code)
	}
}

func TestRegisterAsSSOPlatformConfigFailurePreservesPair(t *testing.T) {
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	q := newFakeDexQuerier()
	oldServer, _ := enc.Encrypt("old")
	oldClients, _ := enc.Encrypt(`[{"id":"astronomer","secret":"old"}]`)
	q.settings = &sqlc.DexSetting{ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example", PublicClients: json.RawMessage(`[]`), PublicClientsEncrypted: oldClients, PublicClientsCutoverAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, Expiry: json.RawMessage(`{}`), Extra: json.RawMessage(`{}`)}
	q.ssoByProv["dex"] = sqlc.SsoConfiguration{ID: uuid.New(), Provider: "dex", ClientSecretEncrypted: oldServer, AllowedOrganizations: json.RawMessage(`[]`), AllowedDomains: json.RawMessage(`[]`)}
	h := NewDexHandler(q)
	h.SetEncryptor(enc)
	configureVerifiedDexRuntime(t, q, h)
	raw, _ := json.Marshal(map[string]any{"client_secret": "new"})
	recorder := httptest.NewRecorder()
	h.RegisterAsSSO(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)))
	if recorder.Code != http.StatusInternalServerError || q.settings.PublicClientsEncrypted != oldClients || q.ssoByProv["dex"].ClientSecretEncrypted != oldServer || strings.Contains(recorder.Body.String(), "new") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRegisterAsSSORejectsEmptyPlatformURLMalformedEnvelopeAndDecryptFailure(t *testing.T) {
	key, _ := auth.GenerateKey()
	enc, _ := auth.NewEncryptor(key)
	for _, mode := range []string{"empty-url", "malformed-envelope", "bad-server-ciphertext"} {
		t.Run(mode, func(t *testing.T) {
			q := newFakeDexQuerier()
			q.platform = &sqlc.PlatformConfiguration{ID: 1, ServerUrl: "https://platform.example"}
			q.settings = &sqlc.DexSetting{ID: dexSettingsSingletonID, IssuerUrl: "https://dex.example", PublicClients: json.RawMessage(`[]`), Expiry: json.RawMessage(`{}`), Extra: json.RawMessage(`{}`)}
			q.ssoByProv["dex"] = sqlc.SsoConfiguration{ID: uuid.New(), Provider: "dex", ClientID: "astronomer", AllowedOrganizations: json.RawMessage(`[]`), AllowedDomains: json.RawMessage(`[]`)}
			body := map[string]any{"client_secret": "replacement"}
			switch mode {
			case "empty-url":
				q.platform.ServerUrl = ""
			case "malformed-envelope":
				q.settings.PublicClientsCutoverAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
				q.settings.PublicClientsEncrypted, _ = enc.Encrypt(`not-json`)
			case "bad-server-ciphertext":
				q.ssoByProv["dex"] = sqlc.SsoConfiguration{ID: uuid.New(), Provider: "dex", ClientSecretEncrypted: "invalid", AllowedOrganizations: json.RawMessage(`[]`), AllowedDomains: json.RawMessage(`[]`)}
				body = map[string]any{}
			}
			h := NewDexHandler(q)
			h.SetEncryptor(enc)
			configureVerifiedDexRuntime(t, q, h)
			beforeSettings := *q.settings
			beforeSSO := q.ssoByProv["dex"]
			raw, _ := json.Marshal(body)
			recorder := httptest.NewRecorder()
			h.RegisterAsSSO(recorder, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)))
			if recorder.Code < 400 || q.settings.PublicClientsEncrypted != beforeSettings.PublicClientsEncrypted ||
				q.ssoByProv["dex"].ClientSecretEncrypted != beforeSSO.ClientSecretEncrypted {
				t.Fatalf("mode=%s status=%d body=%s", mode, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestUpdateSettingsAuditsDexSettingsUpdate(t *testing.T) {
	q := newFakeDexQuerier()
	h := NewDexHandler(q)
	clusterID := uuid.New()
	body, _ := json.Marshal(map[string]any{
		"issuer_url":     "https://dex.example.com/",
		"cluster_id":     clusterID.String(),
		"namespace":      "dex",
		"release_name":   "dex",
		"configmap_name": "astronomer-dex-config",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/dex/settings/", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.UpdateSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	auditRow := q.auditRowAt(t, 0)
	assertDexAudit(t, auditRow, "dex.settings.update", "dex_settings")
	if auditRow.ResourceID != dexSettingsSingletonID.String() || auditRow.ResourceName != "dex" {
		t.Fatalf("settings audit target=(%q,%q), want (%q,dex)", auditRow.ResourceID, auditRow.ResourceName, dexSettingsSingletonID.String())
	}
	assertAuditDetail(t, auditRow.Detail, "issuer_url", "https://dex.example.com")
	assertAuditDetail(t, auditRow.Detail, "namespace", "dex")
	assertAuditDetail(t, auditRow.Detail, "runtime_secret_name", "astronomer-dex-config")
}

// base64StdEncode mirrors what the agent does so the handler's
// ensureSuccess / decodeResponseBody helpers see the right shape.
func base64StdEncode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
