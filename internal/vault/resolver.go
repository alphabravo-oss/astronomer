// Package vault — operator-side HashiCorp Vault integration.
//
// The resolver is the runtime hook that turns "${vault://...}" markers in
// a helm values blob (or any string the operator hands us) into the
// real cleartext value pulled from Vault. The resolved blob is what we
// feed to the install task; the ORIGINAL pre-resolve blob is what we
// persist to Postgres, so secrets never sit at rest in our DB and a
// rotated Vault entry is picked up automatically on the next upgrade.
//
// Reference syntax (case-insensitive on the scheme):
//
//	${vault://<connection>/<engine>/<path>#<key>}
//	${vault:<engine>/<path>#<key>}                — project default connection
//
// Defaults & resolution rules:
//
//   - <engine> defaults to the connection's default_mount (typically
//     "secret" / "kv") when omitted.
//   - <key> is mandatory. We fetch the secret then return data[<key>].
//   - KV v2 detection is automatic: the client tries
//     "<mount>/data/<path>" first; on 404 it falls back to
//     "<mount>/<path>" for KV v1. The operator never has to declare
//     which engine version their mount runs.
//   - References are deduped within a single Resolve call: the same
//     "secret/data/db#password" is fetched once even when it appears
//     N times in the same blob.
//
// Errors fail the call clean — we never substitute a literal
// "${vault://...}" into the resolved output, and we never silently
// skip a reference. The error message names the path so the operator
// can find the typo immediately. Resolved values never appear in
// errors, audit rows, or logs — see the TestAuditLog_DoesNotContainSecretValue
// guard in resolver_test.go for the regression check.

package vault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SentinelEncrypted is the redaction marker the handler sends in place
// of the cleartext auth blob on every GET / list. The PUT path treats
// an incoming value equal to this sentinel as "preserve the stored
// value" so a natural GET → edit → PUT loop doesn't wipe the
// connection's auth blob.
const SentinelEncrypted = "<encrypted>"

// Reference is one parsed "${vault://...}" marker. Raw is the original
// text including the "${" / "}" delimiters; the substituter uses Raw as
// the search key to do exact-match string replacement so adjacent
// references aren't ambiguous.
type Reference struct {
	ConnectionName string // empty = use project default
	Engine         string // e.g. "secret"; resolved against connection.default_mount when empty
	Path           string // "prod/db"
	Key            string // "password"
	Raw            string // original "${vault://...}"
}

// referenceRE matches either reference form:
//
//	${vault://<connection>/<engine>/<path>#<key>}
//	${vault:<engine>/<path>#<key>}
//
// The path component allows letters, digits, and "/_-.": anything
// stricter trips operators who use date-suffixed secret paths.
var referenceRE = regexp.MustCompile(`(?i)\$\{vault:(/{0,2})([^}#]*)#([^}]+)\}`)

// Parse walks a values blob and returns every Vault reference. Idempotent:
// safe to call on already-resolved blobs (returns zero refs). Returns
// references in document order; downstream Resolve dedupes per-call.
//
// The grammar is intentionally permissive — Parse returns a Reference
// for anything that LOOKS like a vault ref; Resolve is responsible for
// rejecting empty paths/keys with a clear error attributing the bad ref.
func Parse(blob string) []Reference {
	matches := referenceRE.FindAllStringSubmatchIndex(blob, -1)
	if len(matches) == 0 {
		return nil
	}
	refs := make([]Reference, 0, len(matches))
	for _, m := range matches {
		// m = [start, end, slashes_start, slashes_end, mid_start, mid_end, key_start, key_end]
		raw := blob[m[0]:m[1]]
		slashes := blob[m[2]:m[3]]
		mid := blob[m[4]:m[5]]
		key := strings.TrimSpace(blob[m[6]:m[7]])

		ref := Reference{
			Raw: raw,
			Key: key,
		}
		if slashes == "//" {
			// ${vault://<connection>/<engine>/<path>}
			conn, rest, _ := strings.Cut(mid, "/")
			ref.ConnectionName = strings.TrimSpace(conn)
			engine, path := splitEnginePath(rest)
			ref.Engine = engine
			ref.Path = path
		} else {
			// ${vault:<engine>/<path>}  — project default connection
			engine, path := splitEnginePath(mid)
			ref.Engine = engine
			ref.Path = path
		}
		refs = append(refs, ref)
	}
	return refs
}

// splitEnginePath splits "engine/path/to/secret" into ("engine",
// "path/to/secret"). When the input has no slash we treat the whole
// thing as the path and leave the engine empty (the resolver will
// fall back to the connection's default_mount).
func splitEnginePath(s string) (engine, path string) {
	s = strings.TrimLeft(s, "/")
	idx := strings.Index(s, "/")
	if idx < 0 {
		return "", s
	}
	return s[:idx], s[idx+1:]
}

// Querier is the slice of *sqlc.Queries Resolve needs. Narrow so tests
// can pass a fake without spinning up Postgres.
type Querier interface {
	GetVaultConnectionByID(ctx context.Context, id uuid.UUID) (sqlc.VaultConnection, error)
	GetVaultConnectionByName(ctx context.Context, name string) (sqlc.VaultConnection, error)
	GetProjectDefaultVaultConnection(ctx context.Context, projectID uuid.UUID) (pgtype.UUID, error)
}

// Decryptor is the surface for unsealing the auth_encrypted blob on a
// vault_connections row. *auth.Encryptor satisfies this; tests pass a
// stub that returns its input unchanged.
type Decryptor interface {
	Decrypt(token string) (string, error)
}

// Client is the per-connection Vault HTTP client surface. Defined as an
// interface so resolver_test.go can swap in a fake without standing up
// the real Vault HTTP client. The production implementation lives in
// internal/vault/client.go.
type Client interface {
	// FetchSecret returns the raw map[string]any of data on the
	// secret at "<engine>/<path>". The client handles KV v1 vs v2
	// detection internally.
	FetchSecret(ctx context.Context, engine, path string) (map[string]any, error)
}

// Resolver owns per-process state: the in-memory Client cache keyed on
// vault_connections.id, the Decryptor, the Querier, and the optional
// audit/metrics hooks.
//
// One Resolver per server. Safe for concurrent use — the client cache
// is mutex-guarded; each Resolve call holds its own per-call dedupe
// table separate from cross-request state.
type Resolver struct {
	q        Querier
	decrypt  Decryptor
	newClient func(conn sqlc.VaultConnection, authBlob string) (Client, error)
	observer Observer

	mu      sync.Mutex
	clients map[uuid.UUID]Client
}

// Observer is the hook surface for audit + metrics callbacks. Nil is
// fine; the resolver does nothing on a nil observer.
//
// IMPORTANT: implementations MUST NOT include the resolved secret VALUE
// in any record. Only the reference path is loggable. The
// TestAuditLog_DoesNotContainSecretValue guard in resolver_test.go
// proves this for the default observer.
type Observer interface {
	OnResolved(ctx context.Context, connectionName string, ref Reference, duration time.Duration)
	OnFailed(ctx context.Context, connectionName string, ref Reference, err error)
	OnHealth(ctx context.Context, connectionName string, ok bool)
}

// NoopObserver is the default — does nothing. Useful for tests that
// don't care about observability hooks.
type NoopObserver struct{}

func (NoopObserver) OnResolved(context.Context, string, Reference, time.Duration) {}
func (NoopObserver) OnFailed(context.Context, string, Reference, error)           {}
func (NoopObserver) OnHealth(context.Context, string, bool)                       {}

// NewResolver constructs a Resolver wired to real production clients. Use
// NewResolverWithFactory in tests to inject a fake.
func NewResolver(q Querier, decrypt Decryptor) *Resolver {
	return NewResolverWithFactory(q, decrypt, DefaultClientFactory)
}

// NewResolverWithFactory is the tested seam: factory is invoked once per
// unique connection_id observed during the resolver's lifetime, and the
// returned Client is cached for subsequent calls. The factory receives
// the decrypted auth blob (NOT the encrypted form) so the client can
// pre-populate auth state without re-doing the decrypt dance.
func NewResolverWithFactory(q Querier, decrypt Decryptor, factory func(sqlc.VaultConnection, string) (Client, error)) *Resolver {
	return &Resolver{
		q:         q,
		decrypt:   decrypt,
		newClient: factory,
		observer:  NoopObserver{},
		clients:   map[uuid.UUID]Client{},
	}
}

// SetObserver wires the audit/metrics observer. Nil resets to noop.
func (r *Resolver) SetObserver(o Observer) {
	if r == nil {
		return
	}
	if o == nil {
		o = NoopObserver{}
	}
	r.observer = o
}

// ClearCache drops every cached Client. Called from the handler's
// DELETE / UPDATE paths so a connection's stale token isn't re-used
// after the operator rotates auth.
func (r *Resolver) ClearCache(id uuid.UUID) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, id)
}

// Resolve walks the blob, fetches every reference, and returns the
// substituted blob. If any reference fails the call returns ("", err)
// and the install path treats it as a fatal error — we never partial-
// install with literal "${vault://...}" left in the manifest.
//
// Dedupe: within one Resolve call the same (connection, engine, path)
// triple is fetched at most once; subsequent references with the same
// triple read from the per-call cache.
func (r *Resolver) Resolve(ctx context.Context, projectID uuid.UUID, blob string) (string, error) {
	if r == nil {
		return blob, errors.New("vault: resolver not configured")
	}
	refs := Parse(blob)
	if len(refs) == 0 {
		return blob, nil
	}

	// Per-call dedupe cache. Keyed by "<conn.ID>:<engine>/<path>" so
	// two references that disagree on key but agree on path share one
	// fetch. Value is the data map; key extraction is per-ref.
	type cacheKey struct {
		connID       uuid.UUID
		engine, path string
	}
	cache := map[cacheKey]map[string]any{}

	// Per-call connection cache so we resolve the project default once
	// and re-use it across every "default" reference in the blob.
	conns := map[string]sqlc.VaultConnection{} // keyed by ref.ConnectionName ("" = default)

	// Substitution map: ref.Raw → resolved string. We build this map
	// then do all replacements at the end so an error part-way through
	// fails the whole call without partial state.
	subs := map[string]string{}

	for _, ref := range refs {
		if ref.Key == "" {
			return "", fmt.Errorf("vault: reference %q is missing a key (#key)", ref.Raw)
		}
		if ref.Path == "" {
			return "", fmt.Errorf("vault: reference %q is missing a path", ref.Raw)
		}

		// Resolve connection (cached per-call).
		conn, ok := conns[ref.ConnectionName]
		if !ok {
			c, err := r.resolveConnection(ctx, projectID, ref.ConnectionName)
			if err != nil {
				r.observer.OnFailed(ctx, ref.ConnectionName, ref, err)
				return "", fmt.Errorf("vault: reference %q: %w", ref.Raw, err)
			}
			conn = c
			conns[ref.ConnectionName] = conn
		}
		if !conn.Enabled {
			err := fmt.Errorf("connection %q is disabled", conn.Name)
			r.observer.OnFailed(ctx, conn.Name, ref, err)
			return "", fmt.Errorf("vault: reference %q: %w", ref.Raw, err)
		}

		engine := ref.Engine
		if engine == "" {
			engine = conn.DefaultMount
		}

		ck := cacheKey{connID: conn.ID, engine: engine, path: ref.Path}
		data, ok := cache[ck]
		if !ok {
			client, err := r.clientFor(conn)
			if err != nil {
				r.observer.OnFailed(ctx, conn.Name, ref, err)
				return "", fmt.Errorf("vault: reference %q: %w", ref.Raw, err)
			}
			start := time.Now()
			d, err := client.FetchSecret(ctx, engine, ref.Path)
			if err != nil {
				r.observer.OnFailed(ctx, conn.Name, ref, err)
				return "", fmt.Errorf("vault: reference %q: %w", ref.Raw, err)
			}
			cache[ck] = d
			data = d
			r.observer.OnResolved(ctx, conn.Name, ref, time.Since(start))
		} else {
			// Cache hit: still record an OnResolved with zero duration so
			// the metrics count every reference resolved (including
			// dedupe hits).
			r.observer.OnResolved(ctx, conn.Name, ref, 0)
		}

		val, ok := data[ref.Key]
		if !ok {
			err := fmt.Errorf("key %q not found in secret at %s/%s", ref.Key, engine, ref.Path)
			r.observer.OnFailed(ctx, conn.Name, ref, err)
			return "", fmt.Errorf("vault: reference %q: %w", ref.Raw, err)
		}
		subs[ref.Raw] = fmt.Sprint(val)
	}

	// Apply all substitutions. We iterate refs again (not subs) to
	// preserve textual order — but since Strings.Replace operates
	// on the whole blob, order only matters for performance.
	resolved := blob
	for raw, val := range subs {
		resolved = strings.ReplaceAll(resolved, raw, val)
	}
	return resolved, nil
}

// resolveConnection looks up the vault_connections row to use for a
// reference. Empty name = chase the project default; non-empty = look
// up by name.
func (r *Resolver) resolveConnection(ctx context.Context, projectID uuid.UUID, name string) (sqlc.VaultConnection, error) {
	if name == "" {
		// Use the project default. A zero project ID (catalog installs
		// at cluster scope, no project tie) short-circuits to a clean
		// error rather than hitting the DB with uuid.Nil — the
		// operator must use the explicit ${vault://<connection>/...}
		// form when there's no project context.
		if projectID == uuid.Nil {
			return sqlc.VaultConnection{}, errors.New("no project context: qualify references with an explicit connection name (${vault://<connection>/...})")
		}
		ptr, err := r.q.GetProjectDefaultVaultConnection(ctx, projectID)
		if err != nil {
			return sqlc.VaultConnection{}, fmt.Errorf("look up project default vault connection: %w", err)
		}
		if !ptr.Valid {
			return sqlc.VaultConnection{}, errors.New("project has no default vault connection (set one or qualify references with a connection name)")
		}
		conn, err := r.q.GetVaultConnectionByID(ctx, ptr.Bytes)
		if err != nil {
			return sqlc.VaultConnection{}, fmt.Errorf("load default vault connection: %w", err)
		}
		return conn, nil
	}
	conn, err := r.q.GetVaultConnectionByName(ctx, name)
	if err != nil {
		return sqlc.VaultConnection{}, fmt.Errorf("vault connection %q not found", name)
	}
	return conn, nil
}

// clientFor returns the cached Client for a connection, building it on
// first use. Decrypts the auth blob once at build time and passes the
// cleartext to the factory.
func (r *Resolver) clientFor(conn sqlc.VaultConnection) (Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[conn.ID]; ok {
		return c, nil
	}
	auth, err := r.decryptAuth(conn)
	if err != nil {
		return nil, err
	}
	c, err := r.newClient(conn, auth)
	if err != nil {
		return nil, err
	}
	r.clients[conn.ID] = c
	return c, nil
}

func (r *Resolver) decryptAuth(conn sqlc.VaultConnection) (string, error) {
	if r.decrypt == nil {
		// Tests + early-wire paths sometimes leave the decryptor nil. We
		// treat the stored blob as already-cleartext in that case — the
		// tests rely on this. Production wiring always sets a real
		// decryptor.
		return conn.AuthEncrypted, nil
	}
	if conn.AuthEncrypted == "" {
		return "{}", nil
	}
	v, err := r.decrypt.Decrypt(conn.AuthEncrypted)
	if err != nil {
		return "", fmt.Errorf("decrypt auth blob for connection %q: %w", conn.Name, err)
	}
	return v, nil
}

// DecodeAuthBlob parses the auth_encrypted JSON shape for a given
// auth_method. Exposed for the handler's test/health endpoints.
func DecodeAuthBlob(authMethod, blob string) (map[string]string, error) {
	if blob == "" {
		return map[string]string{}, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(blob), &m); err != nil {
		return nil, fmt.Errorf("invalid auth blob: %w", err)
	}
	switch authMethod {
	case "token":
		if m["token"] == "" {
			return nil, errors.New("auth blob: token method requires {token: ...}")
		}
	case "approle":
		if m["role_id"] == "" || m["secret_id"] == "" {
			return nil, errors.New("auth blob: approle method requires {role_id, secret_id}")
		}
	case "kubernetes":
		if m["role"] == "" {
			return nil, errors.New("auth blob: kubernetes method requires {role}")
		}
		if m["jwt_path"] == "" {
			m["jwt_path"] = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		}
	default:
		return nil, fmt.Errorf("auth blob: unknown auth_method %q", authMethod)
	}
	return m, nil
}

// EncodeAuthBlob inverse of DecodeAuthBlob — turns the typed handler
// input back into the canonical JSON shape for storage. Defaults the
// jwt_path on kubernetes when blank.
func EncodeAuthBlob(authMethod string, m map[string]string) (string, error) {
	if m == nil {
		m = map[string]string{}
	}
	switch authMethod {
	case "token":
		if m["token"] == "" {
			return "", errors.New("token method requires {token: ...}")
		}
		m = map[string]string{"token": m["token"]}
	case "approle":
		if m["role_id"] == "" || m["secret_id"] == "" {
			return "", errors.New("approle method requires {role_id, secret_id}")
		}
		m = map[string]string{"role_id": m["role_id"], "secret_id": m["secret_id"]}
	case "kubernetes":
		if m["role"] == "" {
			return "", errors.New("kubernetes method requires {role}")
		}
		jwt := m["jwt_path"]
		if jwt == "" {
			jwt = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		}
		m = map[string]string{"role": m["role"], "jwt_path": jwt}
	default:
		return "", fmt.Errorf("unknown auth_method %q", authMethod)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
