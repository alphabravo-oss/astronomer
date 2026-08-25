package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type stagedResourceSettingsStore struct {
	platform      sqlc.PlatformConfiguration
	providers     map[uuid.UUID]sqlc.SsoConfiguration
	audits        []sqlc.UpsertAuditOutboxParams
	events        []string
	auditErrFor   string
	commitErr     error
	settingsValue bool
}

type stagedResourceSettingsTx struct {
	store     *stagedResourceSettingsStore
	platform  sqlc.PlatformConfiguration
	providers map[uuid.UUID]sqlc.SsoConfiguration
	audits    []sqlc.UpsertAuditOutboxParams
	events    []string
}

func (s *stagedResourceSettingsStore) runTx(_ context.Context, fn func(ResourceSettingsMutationTx) error) error {
	tx := &stagedResourceSettingsTx{
		store:     s,
		platform:  s.platform,
		providers: cloneSSOProviders(s.providers),
		audits:    append([]sqlc.UpsertAuditOutboxParams(nil), s.audits...),
		events:    append([]string(nil), s.events...),
	}
	if err := fn(tx); err != nil {
		return err
	}
	if s.commitErr != nil {
		return s.commitErr
	}
	tx.events = append(tx.events, "commit")
	s.platform = tx.platform
	s.providers = tx.providers
	s.audits = tx.audits
	s.events = tx.events
	return nil
}

func cloneSSOProviders(in map[uuid.UUID]sqlc.SsoConfiguration) map[uuid.UUID]sqlc.SsoConfiguration {
	out := make(map[uuid.UUID]sqlc.SsoConfiguration, len(in))
	for id, row := range in {
		out[id] = row
	}
	return out
}

func (tx *stagedResourceSettingsTx) GetPlatformConfigForUpdate(context.Context) (sqlc.PlatformConfiguration, error) {
	tx.events = append(tx.events, "lock-platform")
	if tx.platform.ID == 0 {
		return sqlc.PlatformConfiguration{}, pgx.ErrNoRows
	}
	return tx.platform, nil
}

func (tx *stagedResourceSettingsTx) UpsertPlatformConfig(_ context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error) {
	tx.events = append(tx.events, "upsert-platform")
	tx.platform = sqlc.PlatformConfiguration{
		ID:               1,
		ServerUrl:        arg.ServerUrl,
		PlatformName:     arg.PlatformName,
		TelemetryEnabled: arg.TelemetryEnabled,
		BootstrappedAt:   arg.BootstrappedAt,
		InstanceID:       arg.InstanceID,
	}
	return tx.platform, nil
}

func (tx *stagedResourceSettingsTx) LockSSOProviderKey(_ context.Context, provider string) error {
	tx.events = append(tx.events, "lock-provider:"+provider)
	return nil
}

func (tx *stagedResourceSettingsTx) GetSSOConfigurationByProvider(_ context.Context, provider string) (sqlc.SsoConfiguration, error) {
	tx.events = append(tx.events, "read-provider:"+provider)
	for _, row := range tx.providers {
		if row.Provider == provider {
			return row, nil
		}
	}
	return sqlc.SsoConfiguration{}, pgx.ErrNoRows
}

func (tx *stagedResourceSettingsTx) GetSSOConfigurationByIDForUpdate(_ context.Context, id uuid.UUID) (sqlc.SsoConfiguration, error) {
	tx.events = append(tx.events, "lock-provider-id:"+id.String())
	row, ok := tx.providers[id]
	if !ok {
		return sqlc.SsoConfiguration{}, pgx.ErrNoRows
	}
	return row, nil
}

func (tx *stagedResourceSettingsTx) CreateSSOConfiguration(_ context.Context, arg sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error) {
	tx.events = append(tx.events, "create-provider:"+arg.Provider)
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
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	tx.providers[row.ID] = row
	return row, nil
}

func (tx *stagedResourceSettingsTx) DeleteSSOConfiguration(_ context.Context, id uuid.UUID) error {
	tx.events = append(tx.events, "delete-provider:"+id.String())
	delete(tx.providers, id)
	return nil
}

func (tx *stagedResourceSettingsTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	tx.events = append(tx.events, "audit:"+arg.Action)
	if tx.store.auditErrFor == arg.Action {
		return sqlc.AuditOutbox{}, errors.New("audit unavailable")
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action, Detail: arg.Detail}, nil
}

type stagedSSORegistrar struct {
	store         *stagedResourceSettingsStore
	providers     map[string]bool
	registerErr   error
	partialOnFail bool
}

func (r *stagedSSORegistrar) RegisterProvider(name, _, _, _ string, _ []string) error {
	return r.register(name)
}

func (r *stagedSSORegistrar) RegisterOIDCProvider(_ context.Context, name, _, _, _, _ string, _ []string) error {
	return r.register(name)
}

func (r *stagedSSORegistrar) register(name string) error {
	r.store.events = append(r.store.events, "register:"+name)
	if r.registerErr != nil {
		if r.partialOnFail {
			r.providers[name] = true
		}
		return r.registerErr
	}
	r.providers[name] = true
	return nil
}

func (r *stagedSSORegistrar) HasProvider(name string) bool { return r.providers[name] }

func (r *stagedSSORegistrar) RemoveProvider(name string) {
	r.store.events = append(r.store.events, "remove:"+name)
	delete(r.providers, name)
}

type stagedSettingsReader struct{ store *stagedResourceSettingsStore }

func (r stagedSettingsReader) GetPlatformSetting(context.Context, string) (sqlc.PlatformSetting, error) {
	raw, _ := json.Marshal(r.store.settingsValue)
	return sqlc.PlatformSetting{Key: "feature.test", Value: raw}, nil
}

func newTransactionalResourceHandler(t *testing.T, store *stagedResourceSettingsStore, registrar *stagedSSORegistrar) *ResourceHandler {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	if store.providers == nil {
		store.providers = map[uuid.UUID]sqlc.SsoConfiguration{}
	}
	if registrar == nil {
		registrar = &stagedSSORegistrar{store: store, providers: map[string]bool{}}
	}
	h := &ResourceHandler{
		sso:       &fakeSSOSettingsQuerier{}, // production guard; tx owns writes
		encryptor: enc,
		ssoMgr:    registrar,
	}
	h.SetRunTx(store.runTx)
	return h
}

func transactionalSSOCreateRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/v1/settings/sso/", strings.NewReader(`{
        "type":"oidc",
        "name":"Workforce",
        "enabled":true,
        "config":{
          "client_id":"private-client-id",
          "client_secret":"super-secret-value",
          "metadata_url":"https://issuer.example.test/tenant?private=query",
          "allowed_organizations":"secret-org"
        }
      }`))
}

func transactionalSSODeleteRequest(id uuid.UUID) *http.Request {
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/sso/"+id.String()+"/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestUpdateGeneralSettingsCommitsStateAndAuditBeforeCacheFlush(t *testing.T) {
	store := &stagedResourceSettingsStore{
		platform:      sqlc.PlatformConfiguration{ID: 1, PlatformName: "Old", TelemetryEnabled: false},
		providers:     map[uuid.UUID]sqlc.SsoConfiguration{},
		settingsValue: false,
	}
	h := newTransactionalResourceHandler(t, store, nil)
	h.settingsCache = NewSettingsCache(stagedSettingsReader{store: store}, time.Hour)
	if h.settingsCache.BoolValue(context.Background(), "feature.test", true) {
		t.Fatal("expected primed false cache entry")
	}
	store.settingsValue = true

	r := httptest.NewRequest(http.MethodPut, "/api/v1/settings/general/", strings.NewReader(`{"platformName":"Enterprise","metricsCollection":true}`))
	w := httptest.NewRecorder()
	h.UpdateGeneralSettings(w, r)

	if w.Code != http.StatusOK || store.platform.PlatformName != "Enterprise" || !store.platform.TelemetryEnabled {
		t.Fatalf("status=%d platform=%+v body=%s", w.Code, store.platform, w.Body.String())
	}
	if got := strings.Join(store.events, ","); got != "lock-platform,upsert-platform,audit:settings.general.update,commit" {
		t.Fatalf("transaction ordering=%s", got)
	}
	if len(store.audits) != 1 || !h.settingsCache.BoolValue(context.Background(), "feature.test", false) {
		t.Fatalf("audit=%d cache was not flushed after commit", len(store.audits))
	}
}

func TestUpdateGeneralSettingsAuditFailureRollsBackAndDoesNotFlushCache(t *testing.T) {
	store := &stagedResourceSettingsStore{
		platform:      sqlc.PlatformConfiguration{ID: 1, PlatformName: "Old"},
		providers:     map[uuid.UUID]sqlc.SsoConfiguration{},
		auditErrFor:   "settings.general.update",
		settingsValue: false,
	}
	h := newTransactionalResourceHandler(t, store, nil)
	h.settingsCache = NewSettingsCache(stagedSettingsReader{store: store}, time.Hour)
	_ = h.settingsCache.BoolValue(context.Background(), "feature.test", true)
	store.settingsValue = true

	w := httptest.NewRecorder()
	h.UpdateGeneralSettings(w, httptest.NewRequest(http.MethodPut, "/api/v1/settings/general/", strings.NewReader(`{"platformName":"Must Roll Back"}`)))

	if w.Code != http.StatusServiceUnavailable || store.platform.PlatformName != "Old" || len(store.audits) != 0 {
		t.Fatalf("status=%d platform=%+v audits=%d body=%s", w.Code, store.platform, len(store.audits), w.Body.String())
	}
	if h.settingsCache.BoolValue(context.Background(), "feature.test", false) {
		t.Fatal("cache was flushed despite transaction rollback")
	}
}

func TestCreateSSOProviderCommitsBeforeRegistrationAndRedactsAudit(t *testing.T) {
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{}}
	registrar := &stagedSSORegistrar{store: store, providers: map[string]bool{}}
	h := newTransactionalResourceHandler(t, store, registrar)
	w := httptest.NewRecorder()

	h.CreateSSOProvider(w, transactionalSSOCreateRequest())

	if w.Code != http.StatusCreated || len(store.providers) != 1 || !registrar.HasProvider("workforce") {
		t.Fatalf("status=%d providers=%d runtime=%v body=%s", w.Code, len(store.providers), registrar.providers, w.Body.String())
	}
	want := "lock-provider:workforce,read-provider:workforce,create-provider:workforce,audit:sso.provider.create,commit,register:workforce"
	if got := strings.Join(store.events, ","); got != want {
		t.Fatalf("ordering=%s want=%s", got, want)
	}
	if len(store.audits) != 1 {
		t.Fatalf("audit rows=%d", len(store.audits))
	}
	auditJSON, _ := json.Marshal(store.audits[0])
	for _, forbidden := range []string{"super-secret-value", "private-client-id", "issuer.example.test", "private=query", "secret-org", "client_secret", "issuer_url"} {
		if strings.Contains(string(auditJSON), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, auditJSON)
		}
	}
}

func TestCreateSSOProviderAuditFailureRollsBackBeforeRegistration(t *testing.T) {
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{}, auditErrFor: "sso.provider.create"}
	registrar := &stagedSSORegistrar{store: store, providers: map[string]bool{}}
	h := newTransactionalResourceHandler(t, store, registrar)
	w := httptest.NewRecorder()

	h.CreateSSOProvider(w, transactionalSSOCreateRequest())

	if w.Code != http.StatusServiceUnavailable || len(store.providers) != 0 || len(registrar.providers) != 0 {
		t.Fatalf("status=%d providers=%d runtime=%v body=%s", w.Code, len(store.providers), registrar.providers, w.Body.String())
	}
	if strings.Contains(strings.Join(store.events, ","), "register:") {
		t.Fatalf("runtime registration ran before failed commit: %v", store.events)
	}
}

func TestCreateSSOProviderConflictIsDecidedUnderProviderLock(t *testing.T) {
	id := uuid.New()
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{id: {ID: id, Provider: "workforce"}}}
	h := newTransactionalResourceHandler(t, store, nil)
	w := httptest.NewRecorder()

	h.CreateSSOProvider(w, transactionalSSOCreateRequest())

	if w.Code != http.StatusConflict || len(store.providers) != 1 {
		t.Fatalf("status=%d providers=%d body=%s", w.Code, len(store.providers), w.Body.String())
	}
}

func TestCreateSSOProviderRegistrationFailureCompensatesWithoutLeakingError(t *testing.T) {
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{}}
	registrar := &stagedSSORegistrar{
		store:         store,
		providers:     map[string]bool{},
		registerErr:   errors.New("issuer https://issuer.example.test/?secret=query rejected super-secret-value"),
		partialOnFail: true,
	}
	h := newTransactionalResourceHandler(t, store, registrar)
	w := httptest.NewRecorder()

	h.CreateSSOProvider(w, transactionalSSOCreateRequest())

	if w.Code != http.StatusBadGateway || len(store.providers) != 0 || registrar.HasProvider("workforce") {
		t.Fatalf("status=%d providers=%d runtime=%v body=%s", w.Code, len(store.providers), registrar.providers, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret=query") || strings.Contains(w.Body.String(), "super-secret-value") {
		t.Fatalf("response leaked registration failure: %s", w.Body.String())
	}
	if len(store.audits) != 2 || store.audits[1].Action != "sso.provider.create_compensated" {
		t.Fatalf("audits=%+v", store.audits)
	}
}

func TestCreateSSOProviderCompensationFailureReportsRepairRequired(t *testing.T) {
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{}, auditErrFor: "sso.provider.create_compensated"}
	registrar := &stagedSSORegistrar{store: store, providers: map[string]bool{}, registerErr: errors.New("runtime failed")}
	h := newTransactionalResourceHandler(t, store, registrar)
	w := httptest.NewRecorder()

	h.CreateSSOProvider(w, transactionalSSOCreateRequest())

	if w.Code != http.StatusServiceUnavailable || len(store.providers) != 1 || !strings.Contains(w.Body.String(), "repair is required") {
		t.Fatalf("status=%d providers=%d body=%s", w.Code, len(store.providers), w.Body.String())
	}
}

func TestDeleteSSOProviderLocksAndCommitsBeforeRuntimeRemoval(t *testing.T) {
	id := uuid.New()
	row := sqlc.SsoConfiguration{ID: id, Provider: "github", DisplayName: "GitHub", IsEnabled: true}
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{id: row}}
	registrar := &stagedSSORegistrar{store: store, providers: map[string]bool{"github": true}}
	h := newTransactionalResourceHandler(t, store, registrar)
	w := httptest.NewRecorder()

	h.DeleteSSOProvider(w, transactionalSSODeleteRequest(id))

	if w.Code != http.StatusNoContent || len(store.providers) != 0 || registrar.HasProvider("github") {
		t.Fatalf("status=%d providers=%d runtime=%v body=%s", w.Code, len(store.providers), registrar.providers, w.Body.String())
	}
	want := "lock-provider-id:" + id.String() + ",delete-provider:" + id.String() + ",audit:sso.provider.delete,commit,remove:github"
	if got := strings.Join(store.events, ","); got != want {
		t.Fatalf("ordering=%s want=%s", got, want)
	}
}

func TestDeleteSSOProviderAuditFailureRollsBackWithoutRuntimeRemoval(t *testing.T) {
	id := uuid.New()
	row := sqlc.SsoConfiguration{ID: id, Provider: "github", DisplayName: "GitHub", IsEnabled: true}
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{id: row}, auditErrFor: "sso.provider.delete"}
	registrar := &stagedSSORegistrar{store: store, providers: map[string]bool{"github": true}}
	h := newTransactionalResourceHandler(t, store, registrar)
	w := httptest.NewRecorder()

	h.DeleteSSOProvider(w, transactionalSSODeleteRequest(id))

	if w.Code != http.StatusServiceUnavailable || len(store.providers) != 1 || !registrar.HasProvider("github") {
		t.Fatalf("status=%d providers=%d runtime=%v body=%s", w.Code, len(store.providers), registrar.providers, w.Body.String())
	}
	if strings.Contains(strings.Join(store.events, ","), "remove:github") {
		t.Fatalf("runtime removal ran after rollback: %v", store.events)
	}
}

func TestDeleteSSOProviderMissingRowReturns404AfterLockedRead(t *testing.T) {
	store := &stagedResourceSettingsStore{providers: map[uuid.UUID]sqlc.SsoConfiguration{}}
	h := newTransactionalResourceHandler(t, store, nil)
	id := uuid.New()
	w := httptest.NewRecorder()

	h.DeleteSSOProvider(w, transactionalSSODeleteRequest(id))

	if w.Code != http.StatusNotFound || len(store.audits) != 0 {
		t.Fatalf("status=%d audits=%d body=%s", w.Code, len(store.audits), w.Body.String())
	}
}
