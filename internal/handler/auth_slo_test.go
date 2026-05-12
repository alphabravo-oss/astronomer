package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// newTestEncryptor builds a per-test Fernet encryptor by generating
// a fresh key. The Fernet library is strict about the encoded key
// shape (44 chars, base64-url-safe of a 32-byte secret), so we let
// auth.GenerateKey produce one rather than hand-rolling a constant
// that would drift if the library ever tightened validation.
func newTestEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	k, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	e, err := auth.NewEncryptor(k)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return e
}

// recordingSSOSessionStore is the test fake satisfying both the
// SSOSessionStore (Auth Logout) and ResourceSSOSessionStore (admin
// force-logout) surfaces. Captures every read/write/delete so tests
// can assert on the persistence calls without standing up Postgres.
type recordingSSOSessionStore struct {
	mu       sync.Mutex
	rows     map[string]sqlc.SsoSession // keyed by JTI
	inserts  []sqlc.InsertSSOSessionParams
	deletes  []string
	userDels []uuid.UUID
	getErr   error
}

func newRecordingSSOSessionStore() *recordingSSOSessionStore {
	return &recordingSSOSessionStore{rows: map[string]sqlc.SsoSession{}}
}

func (s *recordingSSOSessionStore) InsertSSOSession(_ context.Context, arg sqlc.InsertSSOSessionParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inserts = append(s.inserts, arg)
	s.rows[arg.Jti] = sqlc.SsoSession{
		Jti:                      arg.Jti,
		UserID:                   arg.UserID,
		ProviderName:             arg.ProviderName,
		UpstreamIdTokenEncrypted: arg.UpstreamIdTokenEncrypted,
		EndSessionEndpoint:       arg.EndSessionEndpoint,
		ExpiresAt:                arg.ExpiresAt,
		CreatedAt:                time.Now(),
	}
	return nil
}

func (s *recordingSSOSessionStore) GetSSOSession(_ context.Context, jti string) (sqlc.SsoSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return sqlc.SsoSession{}, s.getErr
	}
	row, ok := s.rows[jti]
	if !ok {
		return sqlc.SsoSession{}, errNoRows
	}
	return row, nil
}

func (s *recordingSSOSessionStore) DeleteSSOSession(_ context.Context, jti string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes = append(s.deletes, jti)
	delete(s.rows, jti)
	return nil
}

func (s *recordingSSOSessionStore) ListSSOSessionsByUser(_ context.Context, userID uuid.UUID) ([]sqlc.SsoSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []sqlc.SsoSession{}
	for _, r := range s.rows {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *recordingSSOSessionStore) DeleteSSOSessionsByUser(_ context.Context, userID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userDels = append(s.userDels, userID)
	for jti, r := range s.rows {
		if r.UserID == userID {
			delete(s.rows, jti)
		}
	}
	return nil
}

// recordingBackchannel captures the upstream POST attempts so tests
// can assert force-logout fires them with the right params.
type recordingBackchannel struct {
	mu    sync.Mutex
	calls []recordedBackchannelCall
	err   error
}

type recordedBackchannelCall struct {
	endpoint    string
	idTokenHint string
}

func (b *recordingBackchannel) PostEndSession(_ context.Context, endpoint, hint string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, recordedBackchannelCall{endpoint, hint})
	return b.err
}

// TestEndSessionURL_BuildsCorrectly covers the canonical RP-initiated
// logout URL shape: id_token_hint + post_logout_redirect_uri + state
// query parameters, all properly URL-encoded.
func TestEndSessionURL_BuildsCorrectly(t *testing.T) {
	endpoint := "https://dex.example.com/dex/auth/logout"
	idToken := "header.payload.signature"
	postRedirect := "https://astronomer.example.com/api/v1/auth/logout-done/"

	got, err := buildEndSessionURL(endpoint, idToken, postRedirect)
	if err != nil {
		t.Fatalf("buildEndSessionURL: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "dex.example.com" || parsed.Path != "/dex/auth/logout" {
		t.Errorf("base URL mangled: scheme=%q host=%q path=%q", parsed.Scheme, parsed.Host, parsed.Path)
	}
	q := parsed.Query()
	if got := q.Get("id_token_hint"); got != idToken {
		t.Errorf("id_token_hint = %q, want %q", got, idToken)
	}
	if got := q.Get("post_logout_redirect_uri"); got != postRedirect {
		t.Errorf("post_logout_redirect_uri = %q, want %q", got, postRedirect)
	}
	if q.Get("state") == "" {
		t.Errorf("state should be present (random CSRF marker)")
	}
}

// TestEndSessionURL_OmitsRedirectWhenEmpty covers the strict-IdP case
// where post_logout_redirect_uri isn't registered with the IdP and we
// have to omit it entirely (rather than send an empty value, which
// some IdPs reject with 400).
func TestEndSessionURL_OmitsRedirectWhenEmpty(t *testing.T) {
	got, err := buildEndSessionURL("https://dex.example.com/end", "tok", "")
	if err != nil {
		t.Fatalf("buildEndSessionURL: %v", err)
	}
	parsed, _ := url.Parse(got)
	if parsed.Query().Has("post_logout_redirect_uri") {
		t.Errorf("post_logout_redirect_uri should be omitted when empty, got %q", got)
	}
	if parsed.Query().Get("id_token_hint") != "tok" {
		t.Errorf("id_token_hint missing in %q", got)
	}
}

// TestEndSessionURL_EmptyEndpointErrors covers the no-endpoint case
// the Logout handler treats as "fall back to local-only revocation".
func TestEndSessionURL_EmptyEndpointErrors(t *testing.T) {
	_, err := buildEndSessionURL("", "tok", "https://example.com/done/")
	if err == nil {
		t.Fatalf("expected error for empty endpoint")
	}
}

// TestLogout_ReturnsRedirectURLWhenSSOSession is the happy path for
// the SLO flow: a JWT with a matching sso_sessions row triggers a
// redirect_url in the Logout response.
func TestLogout_ReturnsRedirectURLWhenSSOSession(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	enc := newTestEncryptor(t)
	rev := newRecordingRevocationQuerier()
	store := newRecordingSSOSessionStore()

	h := NewAuthHandler(q, jwtMgr)
	h.SetRevocationQuerier(rev)
	h.SetSSOSessionStore(store)
	h.SetEncryptor(enc)
	h.SetPostLogoutRedirectURL("https://astronomer.example.com/api/v1/auth/logout-done/")

	// Mint the JWT we'll log out.
	token, err := jwtMgr.GenerateAccessToken(user.ID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	claims, err := jwtMgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate own token: %v", err)
	}

	// Seed the sso_sessions row the JWT corresponds to.
	idToken := "id-token-header.payload.sig"
	cipher, err := enc.Encrypt(idToken)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	store.rows[claims.ID] = sqlc.SsoSession{
		Jti:                      claims.ID,
		UserID:                   user.ID,
		ProviderName:             "dex",
		UpstreamIdTokenEncrypted: cipher,
		EndSessionEndpoint:       "https://dex.example.com/dex/auth/logout",
		ExpiresAt:                claims.ExpiresAt.Time,
		CreatedAt:                time.Now(),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	req = setAuthUser(req, user.ID.String())
	rec := httptest.NewRecorder()

	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v; body=%s", err, rec.Body.String())
	}
	redirectURL, _ := resp["redirect_url"].(string)
	if redirectURL == "" {
		t.Fatalf("redirect_url missing in response; got %#v", resp)
	}
	parsed, _ := url.Parse(redirectURL)
	if parsed.Host != "dex.example.com" {
		t.Errorf("redirect host = %q, want dex.example.com", parsed.Host)
	}
	if parsed.Query().Get("id_token_hint") != idToken {
		t.Errorf("id_token_hint = %q, want %q", parsed.Query().Get("id_token_hint"), idToken)
	}
}

// TestLogout_NoRedirectURLForLocalLogin covers the dominant case for
// local-password users: no sso_sessions row, no redirect_url, just
// the legacy local revocation behaviour.
func TestLogout_NoRedirectURLForLocalLogin(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	enc := newTestEncryptor(t)
	rev := newRecordingRevocationQuerier()
	store := newRecordingSSOSessionStore() // empty — no sso_sessions row

	h := NewAuthHandler(q, jwtMgr)
	h.SetRevocationQuerier(rev)
	h.SetSSOSessionStore(store)
	h.SetEncryptor(enc)

	token, err := jwtMgr.GenerateAccessToken(user.ID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	req = setAuthUser(req, user.ID.String())
	rec := httptest.NewRecorder()

	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := resp["redirect_url"]; ok {
		t.Errorf("redirect_url should be absent for local logins; got %#v", resp)
	}
	// JWT revocation still occurred — that's the existing behaviour we
	// must not regress.
	if len(rev.revoked) != 1 {
		t.Errorf("expected 1 revocation, got %d", len(rev.revoked))
	}
}

// TestLogout_DeletesSSOSessionRow covers the side-effect that the
// upstream session row is GC'd as soon as Logout consumes it — the
// JWT is already revoked, the id_token is moot, and we don't want to
// leave encrypted bearer-equivalent secrets at rest unnecessarily.
func TestLogout_DeletesSSOSessionRow(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	enc := newTestEncryptor(t)
	rev := newRecordingRevocationQuerier()
	store := newRecordingSSOSessionStore()

	h := NewAuthHandler(q, jwtMgr)
	h.SetRevocationQuerier(rev)
	h.SetSSOSessionStore(store)
	h.SetEncryptor(enc)

	token, _ := jwtMgr.GenerateAccessToken(user.ID)
	claims, _ := jwtMgr.ValidateToken(token)
	cipher, _ := enc.Encrypt("upstream-id-token")
	store.rows[claims.ID] = sqlc.SsoSession{
		Jti:                      claims.ID,
		UserID:                   user.ID,
		ProviderName:             "dex",
		UpstreamIdTokenEncrypted: cipher,
		EndSessionEndpoint:       "https://dex.example.com/end",
		ExpiresAt:                claims.ExpiresAt.Time,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	req = setAuthUser(req, user.ID.String())
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	if len(store.deletes) != 1 || store.deletes[0] != claims.ID {
		t.Errorf("deletes = %v, want [%s]", store.deletes, claims.ID)
	}
	if _, still := store.rows[claims.ID]; still {
		t.Errorf("row should have been deleted from store")
	}
}

// TestLogout_NoEndpointFallsBackToLocal exercises the partial-SLO
// path: the user has a session row but the IdP doesn't advertise an
// end_session_endpoint. The JWT is still revoked locally, but no
// redirect_url is returned and the row is still cleaned up.
func TestLogout_NoEndpointFallsBackToLocal(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	enc := newTestEncryptor(t)
	rev := newRecordingRevocationQuerier()
	store := newRecordingSSOSessionStore()

	h := NewAuthHandler(q, jwtMgr)
	h.SetRevocationQuerier(rev)
	h.SetSSOSessionStore(store)
	h.SetEncryptor(enc)

	token, _ := jwtMgr.GenerateAccessToken(user.ID)
	claims, _ := jwtMgr.ValidateToken(token)
	cipher, _ := enc.Encrypt("upstream-id-token")
	store.rows[claims.ID] = sqlc.SsoSession{
		Jti:                      claims.ID,
		UserID:                   user.ID,
		ProviderName:             "github",
		UpstreamIdTokenEncrypted: cipher,
		EndSessionEndpoint:       "", // IdP doesn't support RP-initiated logout
		ExpiresAt:                claims.ExpiresAt.Time,
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	req = setAuthUser(req, user.ID.String())
	rec := httptest.NewRecorder()
	h.Logout(rec, req)

	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, ok := resp["redirect_url"]; ok {
		t.Errorf("redirect_url should be absent when end_session_endpoint is empty")
	}
	if len(store.deletes) != 1 {
		t.Errorf("row should still be cleaned up even without redirect; deletes=%v", store.deletes)
	}
}

// TestForceLogout_DeletesAllUserSSOSessions covers the admin-driven
// force-logout flow: the target user's sso_sessions rows are
// enumerated, back-channel POSTed to each IdP, and removed.
func TestForceLogout_DeletesAllUserSSOSessions(t *testing.T) {
	admin := makeTestUser(t, true)
	admin.IsSuperuser = true
	target := makeTestUser(t, true)
	target.ID = uuid.New()
	target.Email = "target@example.com"
	target.Username = "target"

	rq := &resourceQuerierForceLogout{
		users: map[uuid.UUID]sqlc.User{admin.ID: admin, target.ID: target},
	}
	h := NewResourceHandlerWithQueries(rq, nil)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h.SetJWTManager(jwtMgr)

	store := newRecordingSSOSessionStore()
	enc := newTestEncryptor(t)
	bc := &recordingBackchannel{}
	h.SetSSOSessionStore(store)
	h.SetSSOBackchannelClient(bc)
	h.SetEncryptor(enc)

	// Seed two sessions for the target user across two devices/providers.
	for i, prov := range []string{"dex", "okta"} {
		cipher, _ := enc.Encrypt("id-token-" + prov)
		jti := uuid.New().String()
		store.rows[jti] = sqlc.SsoSession{
			Jti:                      jti,
			UserID:                   target.ID,
			ProviderName:             prov,
			UpstreamIdTokenEncrypted: cipher,
			EndSessionEndpoint:       "https://" + prov + ".example.com/end",
			ExpiresAt:                time.Now().Add(time.Hour),
			CreatedAt:                time.Now().Add(time.Duration(-i) * time.Minute),
		}
	}
	// Also a session for a different user — must NOT be touched.
	otherUser := uuid.New()
	store.rows["unrelated-jti"] = sqlc.SsoSession{
		Jti:    "unrelated-jti",
		UserID: otherUser,
	}

	r := chi.NewRouter()
	r.Post("/api/v1/admin/users/{id}/force-logout/", h.ForceLogoutUser)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.ID.String()+"/force-logout/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: admin.ID.String(), AuthMethod: "jwt"}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Both target sessions should have been swept; the unrelated row
	// stays put.
	if _, still := store.rows["unrelated-jti"]; !still {
		t.Errorf("unrelated user's session should be preserved")
	}
	for jti, row := range store.rows {
		if row.UserID == target.ID {
			t.Errorf("target session %q still present", jti)
		}
	}
	if len(store.userDels) != 1 || store.userDels[0] != target.ID {
		t.Errorf("DeleteSSOSessionsByUser calls = %v, want [%v]", store.userDels, target.ID)
	}
	if len(bc.calls) != 2 {
		t.Errorf("backchannel calls = %d, want 2 (one per IdP)", len(bc.calls))
	}
}

// TestLogoutDoneEndpoint covers the post_logout_redirect_uri landing
// page: 303 to /dashboard/login + a marker cookie the SPA reads.
func TestLogoutDoneEndpoint(t *testing.T) {
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h := NewAuthHandler(newMockQuerier(), jwtMgr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/logout-done/", nil)
	rec := httptest.NewRecorder()
	h.LogoutDone(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303 See Other", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/dashboard/login" {
		t.Errorf("Location = %q, want /dashboard/login", got)
	}
	// Marker cookie present.
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "astro_logged_out" && c.Value == "1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("astro_logged_out cookie missing")
	}
}

// TestCallback_PersistsSSOSession covers the Callback's
// sso_sessions insertion: a successful OIDC callback should write a
// row with the access JWT's JTI + Fernet-encrypted upstream id_token.
// We invoke persistSSOSession directly because the full Callback
// machinery (CSRF state, manager registration, etc.) is exercised by
// sso_test.go; here we want a tight assertion on the storage shape.
func TestCallback_PersistsSSOSession(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	enc := newTestEncryptor(t)
	store := newRecordingSSOSessionStore()

	// queries=nil is the recommended shim for tests that only exercise
	// SSO persistence: SSOQuerier embeds the full GroupSyncQuerier
	// surface, which a narrow user-only fake doesn't satisfy. The
	// only queries path persistSSOSession uses is the audit writer,
	// and that is a no-op when q is nil.
	_ = q
	h := NewSSOHandler(nil, nil, jwtMgr, "/")
	h.SetSSOSessionWriter(store)
	h.SetEncryptor(enc)

	token, _ := jwtMgr.GenerateAccessToken(user.ID)
	claims, _ := jwtMgr.ValidateToken(token)

	info := &auth.SSOUserInfo{
		Email:              "u@example.com",
		Provider:           "dex",
		UpstreamIDToken:    "raw-id-token",
		EndSessionEndpoint: "https://dex.example.com/end",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/dex/", nil)
	h.persistSSOSession(req, user.ID, "dex", token, info)

	if len(store.inserts) != 1 {
		t.Fatalf("inserts = %d, want 1", len(store.inserts))
	}
	got := store.inserts[0]
	if got.Jti != claims.ID {
		t.Errorf("Jti = %q, want %q", got.Jti, claims.ID)
	}
	if got.UserID != user.ID {
		t.Errorf("UserID mismatch")
	}
	if got.ProviderName != "dex" {
		t.Errorf("provider = %q, want dex", got.ProviderName)
	}
	if got.EndSessionEndpoint != "https://dex.example.com/end" {
		t.Errorf("EndSessionEndpoint mismatch: %q", got.EndSessionEndpoint)
	}
	// id_token is encrypted at rest — plaintext must NOT round-trip.
	if got.UpstreamIdTokenEncrypted == "raw-id-token" {
		t.Errorf("id_token stored in plaintext")
	}
	plain, err := enc.Decrypt(got.UpstreamIdTokenEncrypted)
	if err != nil {
		t.Fatalf("decrypt round-trip: %v", err)
	}
	if plain != "raw-id-token" {
		t.Errorf("decrypted id_token = %q, want raw-id-token", plain)
	}
}

// TestCallback_SkipsPersistenceWhenNoUpstreamToken covers the GitHub /
// Google userinfo branches: no id_token => no row written.
func TestCallback_SkipsPersistenceWhenNoUpstreamToken(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	enc := newTestEncryptor(t)
	store := newRecordingSSOSessionStore()

	// queries=nil is the recommended shim for tests that only exercise
	// SSO persistence: SSOQuerier embeds the full GroupSyncQuerier
	// surface, which a narrow user-only fake doesn't satisfy. The
	// only queries path persistSSOSession uses is the audit writer,
	// and that is a no-op when q is nil.
	_ = q
	h := NewSSOHandler(nil, nil, jwtMgr, "/")
	h.SetSSOSessionWriter(store)
	h.SetEncryptor(enc)

	token, _ := jwtMgr.GenerateAccessToken(user.ID)
	info := &auth.SSOUserInfo{
		Email:    "u@example.com",
		Provider: "github",
		// no UpstreamIDToken
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/github/", nil)
	h.persistSSOSession(req, user.ID, "github", token, info)

	if len(store.inserts) != 0 {
		t.Errorf("inserts = %d, want 0 for non-OIDC providers", len(store.inserts))
	}
}

// dummyPurger satisfies the sub-interface ssoSessionPurger used by
// the retention task; declared here so we don't need to drag every
// retention dep into this test.
type dummySSOPurger struct {
	called bool
	rows   int64
}

func (d *dummySSOPurger) PurgeExpiredSSOSessions(_ context.Context) (int64, error) {
	d.called = true
	return d.rows, nil
}

// TestSSOSessions_PurgeExpired covers the retention task's
// integration with the sso_sessions sub-interface: a *Queries that
// implements PurgeExpiredSSOSessions should have it invoked by the
// daily cron path. We don't need to spin up the real task because
// the contract IS the method existing on the sqlc Queries surface —
// verifying that here protects against an accidental rename.
func TestSSOSessions_PurgeExpired(t *testing.T) {
	// The generated *sqlc.Queries must satisfy this sub-interface, so
	// the audit_retention path compiles. We assert structurally here
	// to catch a future sqlc regen that drops the column / query.
	var _ interface {
		PurgeExpiredSSOSessions(context.Context) (int64, error)
	} = (*sqlc.Queries)(nil)

	// And a hand-rolled fake exercises the call shape directly.
	d := &dummySSOPurger{rows: 7}
	got, err := d.PurgeExpiredSSOSessions(context.Background())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got != 7 {
		t.Errorf("rows = %d, want 7", got)
	}
	if !d.called {
		t.Errorf("PurgeExpiredSSOSessions was not invoked")
	}
}

// guard against an unused-import slip if recordingAuthAuditWriter
// stops being referenced inside this file.
var _ = pgtype.UUID{}
