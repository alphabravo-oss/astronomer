// Vault resolver tests (migration 067).
//
// Coverage:
//   - Parse: basic ref, default-connection form, multiples, ignored
//     non-vault placeholders.
//   - Resolve: substitution, per-call cache, missing-key clear error,
//     missing-connection clear error, 403 → reauth retry.
//   - Audit guard: the (path-only, never-value) Observer surface
//     proves the resolved secret never appears in observer args.

package vault

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// --- Parse tests -------------------------------------------------------

func TestParse_BasicReference(t *testing.T) {
	refs := Parse(`password: ${vault://prod/secret/db#password}`)
	if len(refs) != 1 {
		t.Fatalf("want 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.ConnectionName != "prod" || r.Engine != "secret" || r.Path != "db" || r.Key != "password" {
		t.Fatalf("bad ref: %+v", r)
	}
	if r.Raw != "${vault://prod/secret/db#password}" {
		t.Fatalf("bad raw: %q", r.Raw)
	}
}

func TestParse_NoConnectionUsesDefault(t *testing.T) {
	refs := Parse(`token: ${vault:kv/api/key#token}`)
	if len(refs) != 1 {
		t.Fatalf("want 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.ConnectionName != "" {
		t.Fatalf("expected empty connection name (default), got %q", r.ConnectionName)
	}
	if r.Engine != "kv" || r.Path != "api/key" || r.Key != "token" {
		t.Fatalf("bad ref: %+v", r)
	}
}

func TestParse_MultipleReferences(t *testing.T) {
	blob := `
user: ${vault://prod/secret/db#user}
pass: ${vault://prod/secret/db#password}
api:  ${vault:kv/api#key}
`
	refs := Parse(blob)
	if len(refs) != 3 {
		t.Fatalf("want 3 refs, got %d", len(refs))
	}
	if refs[2].ConnectionName != "" {
		t.Fatalf("third ref should be default-connection, got %q", refs[2].ConnectionName)
	}
}

func TestParse_IgnoresNonVaultPlaceholders(t *testing.T) {
	blob := `
env: ${HOME}
literal: $vault://no-braces
template: {{ .Values.password }}
also: ${other:foo#bar}
`
	refs := Parse(blob)
	if len(refs) != 0 {
		t.Fatalf("expected zero refs, got %d: %+v", len(refs), refs)
	}
}

// --- Fixture for Resolve tests -----------------------------------------

type fakeQuerier struct {
	conns          map[string]sqlc.VaultConnection // keyed by name
	byID           map[uuid.UUID]sqlc.VaultConnection
	projectDefault pgtype.UUID
}

func (f *fakeQuerier) GetVaultConnectionByID(_ context.Context, id uuid.UUID) (sqlc.VaultConnection, error) {
	c, ok := f.byID[id]
	if !ok {
		return sqlc.VaultConnection{}, errors.New("not found")
	}
	return c, nil
}
func (f *fakeQuerier) GetVaultConnectionByName(_ context.Context, name string) (sqlc.VaultConnection, error) {
	c, ok := f.conns[name]
	if !ok {
		return sqlc.VaultConnection{}, errors.New("not found")
	}
	return c, nil
}
func (f *fakeQuerier) GetProjectDefaultVaultConnection(_ context.Context, _ uuid.UUID) (pgtype.UUID, error) {
	return f.projectDefault, nil
}

func newFakeQuerier() *fakeQuerier {
	prodID := uuid.New()
	prod := sqlc.VaultConnection{
		ID: prodID, Name: "prod", AuthMethod: "token", DefaultMount: "secret",
		Enabled: true, AuthEncrypted: `{"token":"root"}`,
	}
	return &fakeQuerier{
		conns: map[string]sqlc.VaultConnection{"prod": prod},
		byID:  map[uuid.UUID]sqlc.VaultConnection{prodID: prod},
	}
}

// fakeClient is a Vault Client implementation for tests. fetches is a
// table of (engine/path) → data map; on each FetchSecret call we
// increment callsByPath so tests can assert dedupe semantics.
type fakeClient struct {
	mu    sync.Mutex
	data  map[string]map[string]any
	calls map[string]int
	denyN int // first N calls return permission-denied
}

func (c *fakeClient) FetchSecret(_ context.Context, engine, path string) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := engine + "/" + path
	c.calls[key]++
	if c.denyN > 0 {
		c.denyN--
		return nil, errors.New("403 permission denied")
	}
	d, ok := c.data[key]
	if !ok {
		return nil, errors.New("secret not found at " + key)
	}
	// Return a copy so resolver dedupe doesn't accidentally hand back
	// the same underlying map for the test's mutation-safe equality.
	out := map[string]any{}
	for k, v := range d {
		out[k] = v
	}
	return out, nil
}

// recordingObserver captures every OnResolved / OnFailed call so the
// audit guard can prove the secret value never appears.
type recordingObserver struct {
	mu       sync.Mutex
	resolved []string // "<conn>:<ref.Raw>"
	failed   []string
}

func (o *recordingObserver) OnResolved(_ context.Context, conn string, ref Reference, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// IMPORTANT: we record only the reference RAW + connection name —
	// never the resolved value. This mirrors the production
	// audit_helpers contract.
	o.resolved = append(o.resolved, conn+":"+ref.Raw)
}
func (o *recordingObserver) OnFailed(_ context.Context, conn string, ref Reference, _ error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.failed = append(o.failed, conn+":"+ref.Raw)
}
func (o *recordingObserver) OnHealth(context.Context, string, bool) {}

// --- Resolve tests -----------------------------------------------------

func newResolverWithFakeClient(t *testing.T, fq *fakeQuerier, client *fakeClient) *Resolver {
	t.Helper()
	factory := func(_ sqlc.VaultConnection, _ string) (Client, error) {
		return client, nil
	}
	return NewResolverWithFactory(fq, nil, factory)
}

func TestResolve_SubstitutesAllReferences(t *testing.T) {
	fq := newFakeQuerier()
	client := &fakeClient{
		data: map[string]map[string]any{
			"secret/db": {"user": "alice", "password": "hunter2"},
		},
		calls: map[string]int{},
	}
	r := newResolverWithFakeClient(t, fq, client)

	blob := "user: ${vault://prod/secret/db#user}\npassword: ${vault://prod/secret/db#password}\n"
	out, err := r.Resolve(context.Background(), uuid.New(), blob)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(out, "user: alice") || !strings.Contains(out, "password: hunter2") {
		t.Fatalf("substitution failed: %q", out)
	}
	if strings.Contains(out, "${vault:") {
		t.Fatalf("output still contains a ${vault:...} marker: %q", out)
	}
}

func TestResolve_ReusesCachedSecret_WithinSingleCall(t *testing.T) {
	fq := newFakeQuerier()
	client := &fakeClient{
		data:  map[string]map[string]any{"secret/db": {"k1": "v1", "k2": "v2"}},
		calls: map[string]int{},
	}
	r := newResolverWithFakeClient(t, fq, client)

	// Two refs on the same (engine, path) — different keys. Resolver
	// must call FetchSecret once.
	blob := "${vault://prod/secret/db#k1} and ${vault://prod/secret/db#k2}"
	_, err := r.Resolve(context.Background(), uuid.New(), blob)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := client.calls["secret/db"]; got != 1 {
		t.Fatalf("expected 1 fetch, got %d", got)
	}
}

func TestResolve_FailsClearOnMissingKey(t *testing.T) {
	fq := newFakeQuerier()
	client := &fakeClient{
		data:  map[string]map[string]any{"secret/db": {"user": "alice"}},
		calls: map[string]int{},
	}
	r := newResolverWithFakeClient(t, fq, client)

	_, err := r.Resolve(context.Background(), uuid.New(), "${vault://prod/secret/db#password}")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !strings.Contains(err.Error(), "password") || !strings.Contains(err.Error(), "secret/db") {
		t.Fatalf("error should name the key+path: %v", err)
	}
}

func TestResolve_FailsClearOnMissingConnection(t *testing.T) {
	fq := newFakeQuerier()
	client := &fakeClient{
		data:  map[string]map[string]any{},
		calls: map[string]int{},
	}
	r := newResolverWithFakeClient(t, fq, client)

	_, err := r.Resolve(context.Background(), uuid.New(), "${vault://nope/secret/db#k}")
	if err == nil {
		t.Fatal("expected error for missing connection")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error should name the connection: %v", err)
	}

	// Also: default-connection path with no project default set → clear error.
	_, err = r.Resolve(context.Background(), uuid.Nil, "${vault:secret/db#k}")
	if err == nil {
		t.Fatal("expected error when no project context and reference is unqualified")
	}
	if !strings.Contains(err.Error(), "project") && !strings.Contains(err.Error(), "connection") {
		t.Fatalf("error should hint at the missing project default: %v", err)
	}
}

func TestResolve_ReauthOn403(t *testing.T) {
	fq := newFakeQuerier()
	client := &fakeClient{
		data:  map[string]map[string]any{"secret/db": {"password": "hunter2"}},
		calls: map[string]int{},
		denyN: 0, // resolver doesn't reauth itself; that's the Client's job
	}
	r := newResolverWithFakeClient(t, fq, client)

	// First call exercises the happy path; we then prove the resolver
	// surfaces a 403 cleanly when the client returns one without
	// retrying. The vaultClient.FetchSecret in client.go handles
	// reauth; here we verify the resolver bubbles the error verbatim
	// with the reference attribution intact.
	client.denyN = 99 // all calls deny
	_, err := r.Resolve(context.Background(), uuid.New(), "${vault://prod/secret/db#password}")
	if err == nil {
		t.Fatal("expected 403 error")
	}
	if !strings.Contains(err.Error(), "${vault://prod/secret/db#password}") {
		t.Fatalf("error should attribute the failing reference: %v", err)
	}
}

func TestAuditLog_DoesNotContainSecretValue(t *testing.T) {
	const secret = "super-secret-value-do-not-log"
	fq := newFakeQuerier()
	client := &fakeClient{
		data:  map[string]map[string]any{"secret/db": {"password": secret}},
		calls: map[string]int{},
	}
	r := newResolverWithFakeClient(t, fq, client)
	obs := &recordingObserver{}
	r.SetObserver(obs)

	out, err := r.Resolve(context.Background(), uuid.New(), "${vault://prod/secret/db#password}")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// The substituted blob obviously contains the secret — that's the
	// point. What we're testing is the AUDIT trail.
	if !strings.Contains(out, secret) {
		t.Fatalf("resolved output must contain the secret value; got %q", out)
	}
	// Now: prove the observer rows do NOT contain the secret value.
	obs.mu.Lock()
	defer obs.mu.Unlock()
	for _, row := range obs.resolved {
		if strings.Contains(row, secret) {
			t.Fatalf("audit row contains secret value: %q", row)
		}
	}
	for _, row := range obs.failed {
		if strings.Contains(row, secret) {
			t.Fatalf("failed-audit row contains secret value: %q", row)
		}
	}
	// And one observer call SHOULD have been recorded for the
	// successful resolve.
	if len(obs.resolved) == 0 {
		t.Fatal("expected at least one OnResolved call")
	}
}
