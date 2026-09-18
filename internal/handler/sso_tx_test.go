package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type ssoTestInvalidator struct {
	calls     int
	committed *bool
	t         *testing.T
}

func (c *ssoTestInvalidator) Invalidate(string) {
	if c.committed != nil && !*c.committed {
		c.t.Fatal("invalidation before commit")
	}
	c.calls++
}

func (f *fakeTTLSSOQueries) GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error) {
	return f.user, nil
}
func (f *fakeTTLSSOQueries) InsertSSOSession(context.Context, sqlc.InsertSSOSessionParams) error {
	return nil
}
func (f *fakeTTLSSOQueries) CreateRefreshSession(context.Context, sqlc.CreateRefreshSessionParams) error {
	return nil
}
func (f *fakeTTLSSOQueries) UpsertAuditOutbox(context.Context, sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	return sqlc.AuditOutbox{}, nil
}

// Every mutation is staged on a transaction copy; only a successful commit
// replaces durable state. A failed phase therefore tests earlier writes too.
type stagedSSOTx struct {
	*fakeTTLSSOQueries
	fail      string
	mode      string
	writes    []string
	audits    []string
	connector uuid.UUID
	session   sqlc.InsertSSOSessionParams
}

func (q *stagedSSOTx) step(name string) error {
	if q.fail == name {
		return errors.New("injected " + name)
	}
	q.writes = append(q.writes, name)
	return nil
}
func (q *stagedSSOTx) GetDexConnectorByName(_ context.Context, name string) (sqlc.DexConnector, error) {
	if name != "employees" {
		return sqlc.DexConnector{}, errors.New("wrong connector identity")
	}
	return sqlc.DexConnector{ID: q.connector, Enabled: q.fail != "disabled_connector"}, q.step("connector")
}
func (q *stagedSSOTx) ClaimExternalPrincipal(context.Context, sqlc.ClaimExternalPrincipalParams) (sqlc.User, error) {
	if q.mode != "claim" {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return q.user, q.step("claim")
}
func (q *stagedSSOTx) GetUserByEmail(context.Context, string) (sqlc.User, error) {
	if q.mode == "provision" {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return q.user, q.step("lookup")
}
func (q *stagedSSOTx) GetUserByUsername(context.Context, string) (sqlc.User, error) {
	return sqlc.User{}, pgx.ErrNoRows
}
func (q *stagedSSOTx) CreateUser(context.Context, sqlc.CreateUserParams) (sqlc.User, error) {
	return q.user, q.step("provision")
}
func (q *stagedSSOTx) GetUserByIDForUpdate(context.Context, uuid.UUID) (sqlc.User, error) {
	user := q.user
	if q.fail == "disabled_user" {
		user.IsActive = false
	}
	return user, q.step("lock")
}
func (q *stagedSSOTx) CreateRefreshSession(context.Context, sqlc.CreateRefreshSessionParams) error {
	return q.step("refresh_session")
}
func (q *stagedSSOTx) UpsertUserIDPGroups(_ context.Context, p sqlc.UpsertUserIDPGroupsParams) (sqlc.UserIdpGroup, error) {
	if !p.ConnectorID.Valid || p.ConnectorID.Bytes != q.connector {
		return sqlc.UserIdpGroup{}, errors.New("wrong connector scope")
	}
	return sqlc.UserIdpGroup{}, q.step("snapshot")
}
func (q *stagedSSOTx) ListGroupMappingsForConnector(context.Context, pgtype.UUID) ([]sqlc.IdentityGroupMapping, error) {
	return []sqlc.IdentityGroupMapping{
		{Scope: "global", RoleID: uuid.New(), GroupName: "operators"},
		{Scope: "cluster", RoleID: uuid.New(), ClusterID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, GroupName: "operators"},
		{Scope: "project", RoleID: uuid.New(), ProjectID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, GroupName: "operators"},
	}, q.step("mappings")
}
func (q *stagedSSOTx) ListGroupSyncGlobalBindingsForConnector(context.Context, sqlc.ListGroupSyncGlobalBindingsForConnectorParams) ([]sqlc.GlobalRoleBinding, error) {
	return []sqlc.GlobalRoleBinding{{ID: uuid.New(), RoleID: uuid.New()}}, q.step("list_global")
}
func (q *stagedSSOTx) ListGroupSyncClusterBindingsForConnector(context.Context, sqlc.ListGroupSyncClusterBindingsForConnectorParams) ([]sqlc.ClusterRoleBinding, error) {
	return []sqlc.ClusterRoleBinding{{ID: uuid.New(), RoleID: uuid.New()}}, q.step("list_cluster")
}
func (q *stagedSSOTx) ListGroupSyncProjectBindingsForConnector(context.Context, sqlc.ListGroupSyncProjectBindingsForConnectorParams) ([]sqlc.ProjectRoleBinding, error) {
	return []sqlc.ProjectRoleBinding{{ID: uuid.New(), RoleID: uuid.New()}}, q.step("list_project")
}
func (q *stagedSSOTx) CreateGroupSyncGlobalBindingForConnector(context.Context, sqlc.CreateGroupSyncGlobalBindingForConnectorParams) (sqlc.GlobalRoleBinding, error) {
	return sqlc.GlobalRoleBinding{ID: uuid.New()}, q.step("add_global")
}
func (q *stagedSSOTx) CreateGroupSyncClusterBindingForConnector(context.Context, sqlc.CreateGroupSyncClusterBindingForConnectorParams) (sqlc.ClusterRoleBinding, error) {
	return sqlc.ClusterRoleBinding{ID: uuid.New()}, q.step("add_cluster")
}
func (q *stagedSSOTx) CreateGroupSyncProjectBindingForConnector(context.Context, sqlc.CreateGroupSyncProjectBindingForConnectorParams) (sqlc.ProjectRoleBinding, error) {
	return sqlc.ProjectRoleBinding{ID: uuid.New()}, q.step("add_project")
}
func (q *stagedSSOTx) DeleteGroupSyncGlobalBinding(context.Context, uuid.UUID) error {
	return q.step("remove_global")
}
func (q *stagedSSOTx) DeleteGroupSyncClusterBinding(context.Context, uuid.UUID) error {
	return q.step("remove_cluster")
}
func (q *stagedSSOTx) DeleteGroupSyncProjectBinding(context.Context, uuid.UUID) error {
	return q.step("remove_project")
}
func (q *stagedSSOTx) UpdateUserLastLogin(context.Context, uuid.UUID) error {
	return q.step("last_login")
}
func (q *stagedSSOTx) InsertSSOSession(_ context.Context, p sqlc.InsertSSOSessionParams) error {
	q.session = p
	return q.step("session")
}
func (q *stagedSSOTx) UpsertAuditOutbox(_ context.Context, p sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	q.audits = append(q.audits, p.Action)
	phase := "audit:" + p.Action
	if p.Action == "auth.group_sync.binding_added" || p.Action == "auth.group_sync.binding_removed" {
		phase += ":" + string(rune('0'+len(q.audits)))
	}
	return sqlc.AuditOutbox{}, q.step(phase)
}

func newSSOCallbackRequest(t *testing.T, h *SSOHandler) *http.Request {
	t.Helper()
	state, err := h.signStateCookie("dex", "state")
	if err != nil {
		t.Fatal(err)
	}
	r := withProvider(httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/dex/?code=ok&state=state", nil), "dex")
	r.AddCookie(&http.Cookie{Name: "astro_sso_state", Value: state})
	return r
}

func TestSSOCallbackTransactionRollback(t *testing.T) {
	phases := []string{"begin", "connector", "disabled_connector", "provision", "lock", "disabled_user", "snapshot", "mappings",
		"list_global", "remove_global", "add_global", "list_cluster", "remove_cluster", "add_cluster",
		"list_project", "remove_project", "add_project", "last_login", "session",
		"audit:sso.user_provisioned", "audit:sso.callback", "audit:auth.group_sync.binding_added:3",
		"audit:auth.group_sync.binding_removed:8", "commit", ""}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) { testSSOCallbackTransaction(t, phase, "provision") })
	}
	for _, phase := range []string{"claim", "snapshot", "audit:principal.linked", "commit", ""} {
		t.Run("claim/"+phase, func(t *testing.T) { testSSOCallbackTransaction(t, phase, "claim") })
	}
	for _, phase := range []string{"lookup", "snapshot", "audit:sso.callback", "commit", ""} {
		t.Run("existing/"+phase, func(t *testing.T) { testSSOCallbackTransaction(t, phase, "existing") })
	}
}

func testSSOCallbackTransaction(t *testing.T, phase, mode string) {
	h := testSSOHandler(t)
	h.manager = &fakeTTLSSOFlow{info: &auth.SSOUserInfo{
		Email: "user@example.test", ConnectorID: "employees", Subject: "stable-subject",
		Provider: "dex", Groups: []string{"operators"}, UpstreamIDToken: "secret-upstream-token",
	}}
	h.SetEncryptor(newTestEncryptor(t))
	committed := false
	inTransaction := false
	h.jwt.SetAccessTokenTTLProvider(func(context.Context) time.Duration {
		if inTransaction {
			t.Fatal("runtime TTL lookup holds the callback transaction open")
		}
		return time.Hour
	})
	cache := &ssoTestInvalidator{committed: &committed, t: t}
	h.SetRBACCacheInvalidator(cache)
	initial := stagedSSOTx{fakeTTLSSOQueries: &fakeTTLSSOQueries{user: makeTestUser(t, true)}, mode: mode, connector: uuid.New()}
	durable := initial
	rec := httptest.NewRecorder()
	h.SetRunTx(func(ctx context.Context, fn func(SSOCallbackTx) error) error {
		inTransaction = true
		defer func() { inTransaction = false }()
		if phase == "begin" {
			return errors.New("begin failed")
		}
		tx := initial
		tx.fail = phase
		if err := fn(&tx); err != nil {
			return err
		}
		if len(rec.Header().Values("Set-Cookie")) != 0 {
			t.Fatal("cookies issued before commit")
		}
		if phase == "commit" {
			return errors.New("commit failed")
		}
		durable = tx
		committed = true
		return nil
	})
	h.Callback(rec, newSSOCallbackRequest(t, h))
	if phase != "" {
		want := http.StatusServiceUnavailable
		if phase == "disabled_user" {
			want = http.StatusForbidden
		}
		if rec.Code != want {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		if !reflect.DeepEqual(durable, initial) || committed || cache.calls != 0 {
			t.Fatal("failed transaction leaked durable state or invalidation")
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Value != "" {
				t.Fatalf("failure issued cookie %s", cookie.Name)
			}
		}
		return
	}
	if rec.Code != http.StatusFound || !committed || cache.calls != 1 {
		t.Fatalf("status=%d committed=%v invalidations=%d body=%s", rec.Code, committed, cache.calls, rec.Body.String())
	}
	wantAudits := 8
	if mode == "existing" {
		wantAudits = 7
	}
	if len(durable.audits) != wantAudits {
		t.Fatalf("audits=%v", durable.audits)
	}
	if durable.session.UpstreamIDTokenEncrypted == "secret-upstream-token" || durable.session.Jti == "" {
		t.Fatal("invalid persisted session")
	}
	accessFound, refreshFound := false, false
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Value == "" {
			continue
		}
		claims, err := h.jwt.ValidateToken(cookie.Value)
		if err != nil {
			continue
		} // Browser CSRF cookie is not a JWT.
		if claims.TokenType == auth.AccessToken {
			accessFound = true
			if claims.ID != durable.session.Jti {
				t.Fatal("persisted JTI differs from issued JWT")
			}
		}
		if claims.TokenType == auth.RefreshToken {
			refreshFound = true
			if !claims.ExpiresAt.Equal(durable.session.ExpiresAt) {
				t.Fatal("session expires before refresh JWT")
			}
		}
	}
	if !accessFound || !refreshFound {
		t.Fatal("successful commit did not issue both session cookies")
	}
}

func TestSSOCallbackMissingDependenciesFailsClosed(t *testing.T) {
	for _, missing := range []string{"transaction", "encryption", "cache", "jwt", "nil transaction querier"} {
		t.Run(missing, func(t *testing.T) {
			h := testSSOHandler(t)
			h.manager = &fakeTTLSSOFlow{info: &auth.SSOUserInfo{Email: "user@example.test", ConnectorID: "employees"}}
			h.SetEncryptor(newTestEncryptor(t))
			h.SetRBACCacheInvalidator(&ssoTestInvalidator{})
			h.SetRunTx(func(_ context.Context, fn func(SSOCallbackTx) error) error { return fn(nil) })
			req := newSSOCallbackRequest(t, h)
			switch missing {
			case "transaction":
				h.SetRunTx(nil)
			case "encryption":
				h.SetEncryptor(nil)
			case "cache":
				h.SetRBACCacheInvalidator(nil)
			case "jwt":
				h.jwt = nil
			}
			rec := httptest.NewRecorder()
			h.Callback(rec, req)
			if missing == "jwt" {
				if h.TransactionalAuditWired() || rec.Code != http.StatusForbidden {
					t.Fatalf("nil JWT accepted or unsafe response: wired=%v status=%d", h.TransactionalAuditWired(), rec.Code)
				}
				return // Missing signing key also prevents state-cookie validation.
			}
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
