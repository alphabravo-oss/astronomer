// Chart-repository credential envelope (migration 145).
//
// `helm_repositories.auth_config` was bare JSONB from 001 until 145: the
// password for a private ChartMuseum/Artifactory/Nexus, and the bearer token
// or registry password for a private OCI registry, sat in the clear in the
// platform database and in every backup of it.
//
// The credential now lives in `auth_config_encrypted` as a Fernet token over
// the complete document; `auth_config` keeps only the non-secret projection.
// Everything that needs a chart-repository credential goes through this file,
// on purpose. There are five distinct consumers across two processes — the
// interactive Sync/Test-connection handler, the chart-archive hydrator, the
// OCI ingest, the scheduled index sweep and the scheduled asset fetch — and
// the way this change ships broken is one of them reading `repo.AuthConfig`
// directly and authenticating with a stripped document or a Fernet token.
// Resolve*() is the only supported way to read the field.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// Decryptor unwraps the Fernet envelope. *auth.Encryptor satisfies it; it is
// declared as an interface here so this package does not depend on the auth
// package (and so the worker can keep injecting a narrow dependency, the same
// shape as tasks.GitOpsDecryptor).
type Decryptor interface {
	Decrypt(token string) (string, error)
}

// Encryptor seals the envelope on the write path.
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
}

// AuthConfigSecretKeys are the auth_config keys whose values are credentials.
// They are stripped from the plaintext JSONB projection on write and replaced
// with a sentinel in API responses. Kept here so the write path, the redaction
// path and the sealing sweep cannot drift into disagreeing about what a secret
// is — a key that only one of them knows about is a key that leaks.
var AuthConfigSecretKeys = []string{
	"password", "token", "bearer", "secret",
	"client_secret", "access_token", "refresh_token",
}

// ErrAuthConfigUnavailable reports that a repository's stored credential could
// not be recovered. Callers must respond by sending NO credential — never by
// falling back to the ciphertext or to a partially-stripped document.
//
// The fallback-to-ciphertext shape is a real bug in this codebase
// (decryptGitAuth in internal/worker/tasks/gitops_sync.go returns the stored
// blob when Decrypt fails), and it is worth naming why it is wrong: sending a
// Fernet token as a password produces an upstream 401, which reads to the
// operator as "the repository rejected my credential" when what actually
// happened is that ASTRONOMER_ENCRYPTION_KEY is wrong or a rotation dropped a
// key too early. The hardened sibling is ArgoCDHandler.decryptInstanceToken.
var ErrAuthConfigUnavailable = errors.New("chart repository credential unavailable")

// ResolveAuthConfig returns the plaintext auth_config document for a
// repository.
//
// Disambiguation is by COLUMN, not by inspecting the bytes: a non-empty
// AuthConfigEncrypted means the envelope is authoritative; an empty one means
// the row predates migration 145 (or was written by a deployment with no
// Fernet key) and AuthConfig is still the whole document. There is no
// "does this look like a Fernet token" heuristic to get wrong.
//
// The error is terminal for credential purposes. It is never accompanied by a
// usable document.
func ResolveAuthConfig(repo sqlc.HelmRepository, dec Decryptor) (json.RawMessage, error) {
	sealed := strings.TrimSpace(repo.AuthConfigEncrypted)
	if sealed == "" {
		// Legacy / unsealed row: auth_config is the complete document. This
		// branch is what keeps an existing install working across the upgrade
		// until security:migrate_plaintext_credentials seals the row.
		return repo.AuthConfig, nil
	}
	if dec == nil {
		return nil, fmt.Errorf("%w: repository %q is encrypted but no Fernet key is configured (check ASTRONOMER_ENCRYPTION_KEY)",
			ErrAuthConfigUnavailable, repo.Name)
	}
	plaintext, err := dec.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt repository %q auth_config (check ASTRONOMER_ENCRYPTION_KEY / key rotation): %w",
			ErrAuthConfigUnavailable, repo.Name, err)
	}
	if strings.TrimSpace(plaintext) == "" {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(plaintext), nil
}

// ResolveIndexAuthConfig resolves and decodes the credential for a classic
// (index.yaml-served) Helm repository.
func ResolveIndexAuthConfig(repo sqlc.HelmRepository, dec Decryptor) (IndexAuthConfig, error) {
	raw, err := ResolveAuthConfig(repo, dec)
	if err != nil {
		return IndexAuthConfig{}, err
	}
	var cfg IndexAuthConfig
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	return cfg, nil
}

// ResolveOCIAuthConfig resolves and decodes the credential + chart selection
// for an OCI registry.
func ResolveOCIAuthConfig(repo sqlc.HelmRepository, dec Decryptor) (OCIAuthConfig, error) {
	raw, err := ResolveAuthConfig(repo, dec)
	if err != nil {
		return OCIAuthConfig{}, err
	}
	return ParseOCIAuthConfig(raw), nil
}

// SealAuthConfig splits an operator-supplied auth_config document into the
// pair of columns migration 145 defines: the Fernet envelope over the whole
// document, and the non-secret projection that stays queryable as JSONB.
//
// With no encryptor (development; config.ValidateProductionSecurity refuses to
// start a production server in this state) it returns an empty envelope and
// the document unchanged, which is exactly the pre-145 row shape and is what
// the resolver's empty-ciphertext branch expects. Encrypting is not silently
// skipped in production because there is no such thing as a production server
// without an encryptor.
func SealAuthConfig(full json.RawMessage, enc Encryptor) (ciphertext string, public json.RawMessage, err error) {
	if len(full) == 0 {
		full = json.RawMessage(`{}`)
	}
	if enc == nil {
		return "", full, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(full, &doc); err != nil || doc == nil {
		// Unparseable or JSON null: there is nothing to protect and nothing
		// worth storing. Normalising to {} keeps the column's NOT NULL
		// JSONB contract and matches ParseOCIAuthConfig's permissiveness.
		return "", json.RawMessage(`{}`), nil
	}
	if !hasAuthConfigSecret(doc) {
		// Nothing secret in the document (the common case: a public repo, or
		// an OCI repo with only a `charts` list). Sealing it anyway would put
		// the chart list out of reach of the catalog API for no gain and make
		// the repository list depend on a successful decrypt.
		return "", full, nil
	}
	ciphertext, err = enc.Encrypt(string(full))
	if err != nil {
		return "", nil, fmt.Errorf("encrypt chart repository auth_config: %w", err)
	}
	public = StripAuthConfigSecrets(full)
	return ciphertext, public, nil
}

// StripAuthConfigSecrets removes — not blanks — every secret-valued key, so no
// secret-shaped value survives in the plaintext column. Blanking would leave
// `"password": ""` behind, which ApplyIndexAuth would then treat as a real
// (empty) credential and send.
func StripAuthConfigSecrets(raw json.RawMessage) json.RawMessage {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return json.RawMessage(`{}`)
	}
	for _, k := range AuthConfigSecretKeys {
		delete(doc, k)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

// InferAuthType derives the auth_type a credential document implies, or "" if
// it implies none.
//
// auth_type and auth_config are two halves of one credential: ApplyIndexAuth
// returns early on an empty/none auth_type, so a repository that stores a
// perfectly good username and password but no auth_type sends no Authorization
// header at all. That is the same silent failure as dropping the credential —
// the operator sees a 401 from the registry and reads it as a bad password.
//
// Callers use this only to fill an auth_type the client left EMPTY, never to
// override one it stated: the same shape as the repo_type OCI auto-detect in
// CatalogHandler.CreateRepo.
func InferAuthType(raw json.RawMessage) string {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
		return ""
	}
	str := func(k string) string {
		s, _ := doc[k].(string)
		return s
	}
	if str("username") != "" || str("password") != "" {
		return "basic"
	}
	if str("token") != "" || str("bearer") != "" {
		return "bearer"
	}
	return ""
}

// hasAuthConfigSecret reports whether the document carries a non-empty secret.
func hasAuthConfigSecret(doc map[string]any) bool {
	for _, k := range AuthConfigSecretKeys {
		if s, ok := doc[k].(string); ok && s != "" {
			return true
		}
	}
	return false
}

// SameHost reports whether two URLs share scheme+host, i.e. whether the second
// is still "the repository" for credential purposes.
//
// This is the guard on every chart-ASSET fetch, and it lives here rather than
// beside either caller because both of them need it and neither may be the
// only one that has it. A repository's index.yaml carries absolute URLs that
// the repository itself chose: `urls: ["https://attacker.example/chart.tgz"]`
// is a well-formed index entry, and httpclient.GuardPublicHost will happily
// dial it because a third party is not a private address. Sending the stored
// credential to whatever host the index named would hand the operator's
// Artifactory password or registry bearer token to that host on the first
// fetch.
//
// This mirrors helm's own rule: `helm repo add` passes repository credentials
// to a chart URL only when it resolves to the repository's host, and requires
// --pass-credentials to go wider. Index entries routinely point at a CDN or a
// github release, which are not places to send a credential.
func SameHost(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Host, ub.Host)
}

// ApplyIndexAuth sets the Authorization header on an index.yaml (or
// test-connection, or chart-tarball) request from the repository's stored
// credentials, so private ChartMuseum / Artifactory / Nexus repos can be
// synced. Mirrors the OCI ingest branch, which already honours
// username/password. A repo with auth_type "" / "none" is left unauthenticated.
//
// When the credential cannot be recovered the request goes out with NO
// Authorization header and the reason is logged. The upstream will answer 401
// and the sweep records that against the repository — which is a worse error
// message than the log line, but it is a real one, and it is strictly better
// than presenting a key-management failure as an authentication failure by
// sending ciphertext.
func ApplyIndexAuth(req *http.Request, repo sqlc.HelmRepository, dec Decryptor, log *slog.Logger) {
	if req == nil {
		return
	}
	authType := strings.ToLower(strings.TrimSpace(repo.AuthType))
	if authType == "" || authType == "none" {
		return
	}
	cfg, err := ResolveIndexAuthConfig(repo, dec)
	if err != nil {
		if log == nil {
			log = slog.Default()
		}
		log.Error("chart repository request sent unauthenticated: credential could not be decrypted",
			"repository", repo.Name, "url", repo.Url, "error", err)
		return
	}
	SetIndexAuthHeader(req, repo.AuthType, cfg)
}

// SetIndexAuthHeader applies an ALREADY-RESOLVED credential to a request.
//
// Split out of ApplyIndexAuth for the callers that must handle a decrypt
// failure themselves rather than degrade to an anonymous request — currently
// TestRepoConnection, where reporting the upstream's 401 would tell the
// operator their password is wrong when the actual fault is the Fernet key.
// Keeping the header rules in one place means those callers cannot support a
// different set of auth_types than the unattended sweep does.
func SetIndexAuthHeader(req *http.Request, authType string, cfg IndexAuthConfig) {
	if req == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(authType)) {
	case "basic":
		if cfg.Username != "" || cfg.Password != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
	case "bearer", "token":
		token := cfg.Token
		if token == "" {
			token = cfg.Bearer
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
}
