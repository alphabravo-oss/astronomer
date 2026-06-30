package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

type ExtensionQuerier interface {
	ListUIExtensions(ctx context.Context) ([]sqlc.UIExtension, error)
	UpsertUIExtension(ctx context.Context, arg sqlc.UpsertUIExtensionParams) (sqlc.UIExtension, error)
	SetUIExtensionEnabled(ctx context.Context, arg sqlc.SetUIExtensionEnabledParams) (sqlc.UIExtension, error)
	SetUIExtensionBundleVerified(ctx context.Context, arg sqlc.SetUIExtensionBundleVerifiedParams) (sqlc.UIExtension, error)
}

type ExtensionHandler struct {
	queries ExtensionQuerier
	auditor any
	current string
	// trustedKey is the Ed25519 public key extension bundles must be
	// signed with. Nil means no key is configured: bundle verification
	// fails closed (executable bundles are gated) until an operator
	// supplies a trusted key via SetTrustedBundleKey.
	trustedKey ed25519.PublicKey
	// engine + bindings power §DataProxy's per-call RBAC re-check against
	// the REQUESTING user's own bindings (never the extension's). Wired via
	// SetRBAC; when nil the data proxy fails closed (503).
	engine   *rbac.Engine
	bindings ExtensionBindingsQuerier
	// upstream dispatches a resolved DataSourceRef to the in-process handler
	// the UI already uses (the second RBAC gate). Wired via SetUpstream; nil
	// => the proxy reports the upstream is not configured (502) AFTER both
	// the RBAC and allowlist gates have already passed, so a denied caller
	// can never reach it.
	upstream ExtensionUpstream
	// tickets validates Tier-2 X-Extension-Ticket headers (§BridgeProtocol).
	// Wired via SetExtensionTickets by the bridge phase; nil => only the
	// browser-session (Tier-1) auth path is available.
	tickets ExtensionTicketValidator
	// issuer mints the short-lived, scoped bridge ticket the iframe asks for
	// via ext/token.request. Wired via SetExtensionTickets alongside the
	// validator; nil => the ticket-issuance endpoint fails closed (503).
	issuer ExtensionTicketIssuer
}

// ExtensionBindingsQuerier resolves the caller's RBAC bindings. Same shape as
// middleware.RBACQuerier; declared locally so the handler package needn't import
// the middleware package.
type ExtensionBindingsQuerier interface {
	GetUserBindings(ctx context.Context, userID string) ([]rbac.RoleBinding, error)
}

// ExtensionTicketValidator consumes a single-use Tier-2 data ticket, returning
// the bound user id. It is the §BridgeProtocol ExtensionTicketStore; declared as
// an interface so the data proxy compiles before the bridge phase wires a store.
type ExtensionTicketValidator interface {
	Validate(token, extension, dataSourceID string, clusterID uuid.UUID) (uuid.UUID, error)
}

// ExtensionTicketIssuer mints a short-lived, narrowly-scoped Tier-2 bridge
// ticket (§BridgeProtocol). It backs ext/token.request: the bridge handler
// RBAC-checks the user for the dataSource first, then issues. The store hashes
// the opaque token at rest, makes it single-use, and TTL-bounds it (≤60s).
type ExtensionTicketIssuer interface {
	IssueToken(userID uuid.UUID, extension, dataSourceID string, clusterID uuid.UUID) (token string, expiresAt time.Time, err error)
}

// ExtensionUpstreamRequest is the server-built, validated upstream call. Every
// field is derived from the STORED manifest + validated context — no raw client
// field redirects it (no SSRF / traversal).
type ExtensionUpstreamRequest struct {
	Proxy     string
	Method    string
	Path      string            // placeholders already filled
	Query     map[string]string // declared keys only
	Body      json.RawMessage   // POST form submit only
	UserID    uuid.UUID
	ClusterID uuid.UUID
	ProjectID uuid.UUID
	Namespace string
}

// ExtensionUpstream dispatches a resolved upstream request in-process and
// returns the raw decoded payload (rows for a list, an object, or a series).
// The proxy projects/truncates the result; the upstream itself re-runs the
// host's own RBAC middleware as a second, independent gate.
type ExtensionUpstream func(ctx context.Context, req ExtensionUpstreamRequest) (any, error)

// SetRBAC wires the RBAC engine + bindings querier the data proxy uses to
// re-check the requesting user's own permissions on every call.
func (h *ExtensionHandler) SetRBAC(engine *rbac.Engine, bindings ExtensionBindingsQuerier) {
	if h == nil {
		return
	}
	h.engine = engine
	h.bindings = bindings
}

// SetUpstream wires the in-process upstream dispatcher (the second RBAC gate).
func (h *ExtensionHandler) SetUpstream(u ExtensionUpstream) {
	if h == nil {
		return
	}
	h.upstream = u
}

// SetExtensionTickets wires the Tier-2 data-ticket validator (§BridgeProtocol).
func (h *ExtensionHandler) SetExtensionTickets(v ExtensionTicketValidator) {
	if h == nil {
		return
	}
	h.tickets = v
}

// SetExtensionTicketIssuer wires the Tier-2 bridge ticket issuer backing
// ext/token.request (§BridgeProtocol). Typically the same store as the
// validator, set together at construction.
func (h *ExtensionHandler) SetExtensionTicketIssuer(i ExtensionTicketIssuer) {
	if h == nil {
		return
	}
	h.issuer = i
}

func NewExtensionHandler(queries ExtensionQuerier) *ExtensionHandler {
	return &ExtensionHandler{queries: queries, auditor: queries, current: version.Version}
}

// SetTrustedBundleKey installs the base64 (std encoding) Ed25519 public key
// that executable extension bundles must be signed with. An empty string is a
// no-op (leaves verification failing closed). Returns an error if the key is
// present but not a valid 32-byte Ed25519 public key.
func (h *ExtensionHandler) SetTrustedBundleKey(b64 string) error {
	if h == nil {
		return nil
	}
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("trusted bundle key is not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return fmt.Errorf("trusted bundle key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	h.trustedKey = ed25519.PublicKey(raw)
	return nil
}

func (h *ExtensionHandler) SetAuditWriter(a any) {
	if h == nil {
		return
	}
	h.auditor = a
}

func (h *ExtensionHandler) SetCurrentVersion(v string) {
	if h == nil {
		return
	}
	h.current = strings.TrimSpace(v)
}

type ExtensionManifest struct {
	APIVersion           string                  `json:"apiVersion"`
	Name                 string                  `json:"name"`
	DisplayName          string                  `json:"displayName"`
	Version              string                  `json:"version"`
	CompatibleAstronomer string                  `json:"compatibleAstronomer"`
	Entry                string                  `json:"entry"`
	Permissions          []string                `json:"permissions"`
	BackendAPIScopes     []string                `json:"backendApiScopes,omitempty"`
	CSP                  ExtensionCSP            `json:"csp,omitempty"`
	ExtensionPoints      ExtensionManifestPoints `json:"extensionPoints"`
}

type ExtensionCSP struct {
	ScriptSrc  []string `json:"scriptSrc,omitempty"`
	ConnectSrc []string `json:"connectSrc,omitempty"`
	FrameSrc   []string `json:"frameSrc,omitempty"`
	ImageSrc   []string `json:"imageSrc,omitempty"`
}

type ExtensionManifestPoints struct {
	Sidebar     []ExtensionSidebarPoint `json:"sidebar,omitempty"`
	Widgets     []ExtensionWidgetPoint  `json:"widgets,omitempty"`
	ClusterTabs []ExtensionClusterTab   `json:"clusterTabs,omitempty"`
	Settings    []ExtensionSettingsPage `json:"settings,omitempty"`
}

type ExtensionSidebarPoint struct {
	Label       string           `json:"label"`
	Path        string           `json:"path"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

type ExtensionWidgetPoint struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

type ExtensionClusterTab struct {
	Label       string           `json:"label"`
	Component   string           `json:"component"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

type ExtensionSettingsPage struct {
	Label       string           `json:"label"`
	Component   string           `json:"component"`
	Render      *ExtensionRender `json:"render,omitempty"`
	DataSources []DataSourceRef  `json:"dataSources,omitempty"`
}

// ExtensionRender is attached per extension point. Exactly one of Declarative
// (Tier 1) or Bundle (Tier 2) is set. Absent => legacy entry, mounts nothing.
type ExtensionRender struct {
	Declarative *DeclarativeWidget `json:"declarative,omitempty"` // Tier 1
	Bundle      *BundleDescriptor  `json:"bundle,omitempty"`      // Tier 2
}

// DataSourceRef is a named, RBAC-allowlisted data source (never a raw URL). It
// is referenced by widgets and bridge calls and re-checked at call time.
type DataSourceRef struct {
	ID              string            `json:"id"`
	Proxy           string            `json:"proxy"`  // host allowlist: "astronomer-api"|"k8s"|"prometheus"
	Method          string            `json:"method"` // "GET" | "POST"
	Path            string            `json:"path"`   // host template, e.g. "/api/v1/clusters/{clusterId}/pods"
	Query           map[string]string `json:"query,omitempty"`
	RBAC            RBACRequirement   `json:"rbac"`
	Shape           string            `json:"shape"`            // "list" | "object" | "series"
	Fields          []string          `json:"fields,omitempty"` // response projection allowlist (dot-paths; "*" rejected)
	MaxRows         int               `json:"maxRows,omitempty"`
	CacheTTLSeconds int               `json:"cacheTtlSeconds,omitempty"`
}

type RBACRequirement struct {
	Resource string `json:"resource"` // MUST be IsCanonicalResource (no "*")
	Verb     string `json:"verb"`     // MUST be IsCanonicalVerb (no "*")
	Scope    string `json:"scope"`    // "global" | "cluster" | "project"
}

// DeclarativeWidget is a Tier 1 widget spec (zero third-party JS).
type DeclarativeWidget struct {
	Kind       string         `json:"kind"`       // "table" | "chart" | "stat" | "form"
	DataSource string         `json:"dataSource"` // ref into the point's DataSources[].ID
	Fields     []FieldBinding `json:"fields,omitempty"`
	Chart      *ChartSpec     `json:"chart,omitempty"` // kind=chart
	Form       *FormSpec      `json:"form,omitempty"`  // kind=form
	Stat       *StatSpec      `json:"stat,omitempty"`  // kind=stat
	EmptyText  string         `json:"emptyText,omitempty"`
}

type FieldBinding struct {
	Path   string `json:"path"` // JSONPath-lite into a proxy row, "metadata.name"
	Label  string `json:"label"`
	Format string `json:"format,omitempty"` // closed enum: text|number|bytes|datetime|duration|badge|currency
}

type ChartSpec struct {
	Type string   `json:"type"` // "line" | "bar" | "area"
	X    string   `json:"x"`
	Y    []string `json:"y"`
}

type StatSpec struct {
	Value FieldBinding  `json:"value"`
	Delta *FieldBinding `json:"delta,omitempty"`
	Label string        `json:"label"`
}

type FormSpec struct {
	Submit      string      `json:"submit"` // a DataSources[].ID with Method=POST + write verb
	Inputs      []FormInput `json:"inputs"`
	SubmitLabel string      `json:"submitLabel"`
}

type FormInput struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Type      string   `json:"type"`              // "text"|"number"|"select"|"toggle"
	Options   []string `json:"options,omitempty"` // type=select
	MaxLength int      `json:"maxLength,omitempty"`
	Required  bool     `json:"required"`
}

// BundleDescriptor is a Tier 2 signed-bundle / iframe descriptor.
type BundleDescriptor struct {
	URL           string          `json:"url"`           // https; host on operator allowlist
	SHA256        string          `json:"sha256"`        // "sha256:<64hex>"
	Integrity     string          `json:"integrity"`     // SRI "sha384-..."
	Signature     string          `json:"signature"`     // base64 Ed25519 over raw bundle
	Entry         string          `json:"entry"`         // relative .js (safeExtensionEntry rules)
	SandboxOrigin string          `json:"sandboxOrigin"` // per-extension origin; MUST NOT equal host origin
	Component     string          `json:"component"`     // logical view name passed in handshake
	CSP           ExtensionCSP    `json:"csp"`           // per-iframe CSP; intersected with manifest.CSP
	DataSources   []DataSourceRef `json:"dataSources"`   // the ONLY routes the iframe may request via bridge
}

type ExtensionValidationFinding struct {
	Field    string `json:"field,omitempty"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type ExtensionValidationResponse struct {
	Valid               bool                         `json:"valid"`
	CompatibilityStatus string                       `json:"compatibility_status"`
	Checksum            string                       `json:"checksum"`
	Manifest            ExtensionManifest            `json:"manifest"`
	Warnings            []ExtensionValidationFinding `json:"warnings"`
	Errors              []ExtensionValidationFinding `json:"errors"`
}

type ExtensionRecordResponse struct {
	ID                  uuid.UUID         `json:"id"`
	Name                string            `json:"name"`
	DisplayName         string            `json:"display_name"`
	Version             string            `json:"version"`
	Source              string            `json:"source"`
	Checksum            string            `json:"checksum"`
	Enabled             bool              `json:"enabled"`
	CompatibilityStatus string            `json:"compatibility_status"`
	Manifest            ExtensionManifest `json:"manifest"`
	InstalledAt         string            `json:"installed_at"`
	UpdatedAt           string            `json:"updated_at"`
}

type ExtensionListResponse struct {
	Items          []ExtensionRecordResponse `json:"items"`
	SampleManifest ExtensionManifest         `json:"sample_manifest"`
}

type InstallExtensionRequest struct {
	Manifest ExtensionManifest `json:"manifest"`
	Source   string            `json:"source,omitempty"`
	Enable   bool              `json:"enable,omitempty"`
}

// VerifyBundleRequest carries an executable extension bundle for signature +
// checksum verification. Bundle and Signature are base64 (std encoding); the
// signature is an Ed25519 signature over the raw bundle bytes. Checksum, if
// supplied, is the "sha256:<hex>" digest the caller expects and is checked
// against the bundle so a tampered bundle is rejected even before the
// signature is examined.
type VerifyBundleRequest struct {
	Bundle    string `json:"bundle"`
	Signature string `json:"signature"`
	Checksum  string `json:"checksum,omitempty"`
	// Name, when supplied, lifts the §HostMounts Tier-2 gate for that stored
	// extension: on a successful signed+trusted verification whose checksum
	// matches a bundle descriptor in the extension's manifest, bundle_verified
	// is set true so the descriptor may mount. Omit to verify a bundle without
	// touching any stored row (the original verify-only behaviour).
	Name string `json:"name,omitempty"`
}

type VerifyBundleResponse struct {
	Verified bool   `json:"verified"`
	Checksum string `json:"checksum"`
	// Gated reports whether the named extension's §HostMounts gate was lifted
	// (bundle_verified set true) as a result of this verification.
	Gated bool `json:"gated,omitempty"`
}

var (
	errBundleNoTrustedKey = errors.New("no trusted extension bundle key is configured")
	errBundleChecksum     = errors.New("bundle checksum does not match")
	errBundleSignature    = errors.New("bundle signature is not valid for the trusted key")
)

var extensionNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// VerifyBundle verifies an executable extension bundle's SHA-256 checksum and
// Ed25519 signature against the configured trusted public key. It fails closed
// when no trusted key is configured.
func (h *ExtensionHandler) VerifyBundle(w http.ResponseWriter, r *http.Request) {
	var req VerifyBundleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	bundle, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Bundle))
	if err != nil || len(bundle) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFormat, "bundle must be non-empty base64")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.Signature))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFormat, "signature must be base64")
		return
	}
	checksum := bundleChecksum(bundle)
	if vErr := verifyExtensionBundle(bundle, sig, req.Checksum, h.trustedKey); vErr != nil {
		switch {
		case errors.Is(vErr, errBundleNoTrustedKey):
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension bundle verification is not configured")
		case errors.Is(vErr, errBundleChecksum):
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidFormat, "Bundle checksum mismatch")
		default:
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidSignature, "Bundle signature verification failed")
		}
		return
	}

	// Gate lift (§Security / §HostMounts): a signed+trusted bundle becomes
	// mountable. Only when a name is supplied AND the verified checksum matches
	// a bundle descriptor in that extension's STORED manifest do we flip
	// bundle_verified. An unsigned/tampered bundle never reaches this point, so
	// it stays gated (bundle_verified=false) and /mounts/ refuses it.
	gated := false
	if name := strings.TrimSpace(req.Name); name != "" {
		ok, gErr := h.markBundleVerified(r, name, checksum)
		if gErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to record bundle verification")
			return
		}
		gated = ok
	}
	RespondJSON(w, http.StatusOK, VerifyBundleResponse{Verified: true, Checksum: checksum, Gated: gated})
}

// markBundleVerified flips the §HostMounts Tier-2 gate for one extension when
// the just-verified bundle checksum matches a bundle descriptor in that
// extension's STORED manifest. It returns ok=true when the flag was set. A
// missing extension, a non-Tier-2 manifest, or a checksum that matches no stored
// descriptor is a no-op (ok=false) — never an error — so verify-bundle can only
// LIFT the gate for a descriptor the extension actually shipped, never an
// arbitrary one.
func (h *ExtensionHandler) markBundleVerified(r *http.Request, name, checksum string) (bool, error) {
	if h.queries == nil || !extensionNameRE.MatchString(name) {
		return false, nil
	}
	row, err := h.findExtension(r.Context(), name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	var manifest ExtensionManifest
	if json.Unmarshal(row.Manifest, &manifest) != nil {
		return false, nil
	}
	if !manifestBundleHasChecksum(manifest, checksum) {
		return false, nil
	}
	updated, err := h.queries.SetUIExtensionBundleVerified(r.Context(), sqlc.SetUIExtensionBundleVerifiedParams{
		Name:           name,
		BundleVerified: true,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	recordAudit(r, h.auditor, "admin.extension.bundle_verified", "ui_extension", updated.ID.String(), updated.Name, map[string]any{
		"name":     updated.Name,
		"version":  updated.Version,
		"checksum": checksum,
	})
	return true, nil
}

// manifestBundleHasChecksum reports whether any Tier-2 bundle descriptor in the
// manifest declares the given "sha256:<hex>" checksum (case-insensitive on the
// hex). This binds a verify-bundle call to a descriptor the extension shipped.
func manifestBundleHasChecksum(m ExtensionManifest, checksum string) bool {
	want := strings.ToLower(strings.TrimSpace(checksum))
	match := func(r *ExtensionRender) bool {
		return r != nil && r.Bundle != nil && strings.ToLower(strings.TrimSpace(r.Bundle.SHA256)) == want
	}
	for _, p := range m.ExtensionPoints.Sidebar {
		if match(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.Widgets {
		if match(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.ClusterTabs {
		if match(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.Settings {
		if match(p.Render) {
			return true
		}
	}
	return false
}

// verifyExtensionBundle checks a bundle's expected checksum (if supplied) and
// its Ed25519 signature against trustedKey. It returns nil only when the
// bundle is trusted. A nil trustedKey always fails (gated).
func verifyExtensionBundle(bundle, signature []byte, expectedChecksum string, trustedKey ed25519.PublicKey) error {
	if len(trustedKey) != ed25519.PublicKeySize {
		return errBundleNoTrustedKey
	}
	if expected := strings.TrimSpace(expectedChecksum); expected != "" {
		// Constant-time compare of the hex digests guards against
		// timing oracles on the checksum path.
		got := bundleChecksum(bundle)
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(expected)), []byte(got)) != 1 {
			return errBundleChecksum
		}
	}
	if !ed25519.Verify(trustedKey, bundle, signature) {
		return errBundleSignature
	}
	return nil
}

// bundleChecksum returns the "sha256:<hex>" digest of raw bundle bytes.
func bundleChecksum(bundle []byte) string {
	sum := sha256.Sum256(bundle)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (h *ExtensionHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension registry is not configured")
		return
	}
	rows, err := h.queries.ListUIExtensions(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list extensions")
		return
	}
	items := make([]ExtensionRecordResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, extensionRecordResponse(row))
	}
	RespondJSON(w, http.StatusOK, ExtensionListResponse{
		Items:          items,
		SampleManifest: sampleExtensionManifest(),
	})
}

func (h *ExtensionHandler) SampleManifest(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, sampleExtensionManifest())
}

func (h *ExtensionHandler) Validate(w http.ResponseWriter, r *http.Request) {
	manifest, err := decodeExtensionManifest(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidManifest, err.Error())
		return
	}
	validation := validateExtensionManifest(manifest, h.current)
	h.warnBundleGated(&validation)
	RespondJSON(w, http.StatusOK, validation)
}

// warnBundleGated emits the fail-closed signing warning when a manifest ships
// an executable (Tier-2) bundle but no trusted key is configured. The mounting
// gate is enforced at install (enabled forced false) and in /mounts/.
func (h *ExtensionHandler) warnBundleGated(v *ExtensionValidationResponse) {
	if h.trustedKey == nil && manifestHasBundle(v.Manifest) {
		v.Warnings = append(v.Warnings, extensionWarning("extensionPoints", "executable bundles gated: no trusted key"))
	}
}

// manifestHasBundle reports whether any extension point declares a Tier-2
// bundle render.
func manifestHasBundle(m ExtensionManifest) bool {
	has := func(r *ExtensionRender) bool { return r != nil && r.Bundle != nil }
	for _, p := range m.ExtensionPoints.Sidebar {
		if has(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.Widgets {
		if has(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.ClusterTabs {
		if has(p.Render) {
			return true
		}
	}
	for _, p := range m.ExtensionPoints.Settings {
		if has(p.Render) {
			return true
		}
	}
	return false
}

func (h *ExtensionHandler) Install(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension registry is not configured")
		return
	}
	var req InstallExtensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	validation := validateExtensionManifest(req.Manifest, h.current)
	h.warnBundleGated(&validation)
	if !validation.Valid {
		RespondJSON(w, http.StatusBadRequest, validation)
		return
	}
	enabled := req.Enable && validation.CompatibilityStatus == "compatible"
	// Fail-closed: an executable bundle cannot reach enabled=true until a
	// trusted key is configured (and, in a later phase, bundle_verified).
	if enabled && h.trustedKey == nil && manifestHasBundle(validation.Manifest) {
		enabled = false
	}
	manifestBytes, _ := json.Marshal(validation.Manifest)
	row, err := h.queries.UpsertUIExtension(r.Context(), sqlc.UpsertUIExtensionParams{
		Name:                validation.Manifest.Name,
		DisplayName:         extensionDisplayName(validation.Manifest),
		Version:             validation.Manifest.Version,
		Source:              sanitizeExtensionSource(req.Source),
		Checksum:            validation.Checksum,
		Enabled:             enabled,
		CompatibilityStatus: validation.CompatibilityStatus,
		Manifest:            manifestBytes,
		InstalledBy:         currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InstallFailed, "Failed to install extension")
		return
	}
	recordAudit(r, h.auditor, "admin.extension.installed", "ui_extension", row.ID.String(), row.Name, map[string]any{
		"name":                 row.Name,
		"version":              row.Version,
		"enabled":              row.Enabled,
		"compatibility_status": row.CompatibilityStatus,
	})
	RespondJSON(w, http.StatusOK, extensionRecordResponse(row))
}

func (h *ExtensionHandler) Enable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, true)
}

func (h *ExtensionHandler) Disable(w http.ResponseWriter, r *http.Request) {
	h.setEnabled(w, r, false)
}

func (h *ExtensionHandler) setEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if h.queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Extension registry is not configured")
		return
	}
	name := strings.TrimSpace(chi.URLParam(r, "name"))
	if !extensionNameRE.MatchString(name) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "Invalid extension name")
		return
	}
	if enabled {
		existing, err := h.findExtension(r.Context(), name)
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Extension not found")
			return
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to read extension")
			return
		}
		if existing.CompatibilityStatus != "compatible" {
			RespondRequestError(w, r, http.StatusConflict, apierror.IncompatibleExtension, "Incompatible extensions cannot be enabled")
			return
		}
	}
	row, err := h.queries.SetUIExtensionEnabled(r.Context(), sqlc.SetUIExtensionEnabledParams{Name: name, Enabled: enabled})
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Extension not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update extension")
		return
	}
	action := "admin.extension.disabled"
	if enabled {
		action = "admin.extension.enabled"
	}
	recordAudit(r, h.auditor, action, "ui_extension", row.ID.String(), row.Name, map[string]any{
		"name":    row.Name,
		"version": row.Version,
	})
	RespondJSON(w, http.StatusOK, extensionRecordResponse(row))
}

func (h *ExtensionHandler) findExtension(ctx context.Context, name string) (sqlc.UIExtension, error) {
	rows, err := h.queries.ListUIExtensions(ctx)
	if err != nil {
		return sqlc.UIExtension{}, err
	}
	for _, row := range rows {
		if row.Name == name {
			return row, nil
		}
	}
	return sqlc.UIExtension{}, pgx.ErrNoRows
}

func decodeExtensionManifest(r *http.Request) (ExtensionManifest, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return ExtensionManifest{}, fmt.Errorf("invalid JSON body")
	}
	var wrapped struct {
		Manifest ExtensionManifest `json:"manifest"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Manifest.Name != "" {
		return wrapped.Manifest, nil
	}
	var manifest ExtensionManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return ExtensionManifest{}, fmt.Errorf("manifest must be a JSON object")
	}
	return manifest, nil
}

func validateExtensionManifest(manifest ExtensionManifest, currentVersion string) ExtensionValidationResponse {
	out := ExtensionValidationResponse{
		Manifest:            normalizeExtensionManifest(manifest),
		CompatibilityStatus: "unknown",
		Warnings:            []ExtensionValidationFinding{},
		Errors:              []ExtensionValidationFinding{},
	}
	out.Checksum = extensionChecksum(out.Manifest)
	if out.Manifest.APIVersion != "extensions.astronomer.io/v1alpha1" {
		out.Errors = append(out.Errors, extensionError("apiVersion", "apiVersion must be extensions.astronomer.io/v1alpha1"))
	}
	if !extensionNameRE.MatchString(out.Manifest.Name) {
		out.Errors = append(out.Errors, extensionError("name", "name must be DNS-label compatible"))
	}
	if out.Manifest.DisplayName == "" {
		out.Warnings = append(out.Warnings, extensionWarning("displayName", "displayName is empty; name will be shown in the UI"))
	}
	if _, err := semver.NewVersion(strings.TrimPrefix(out.Manifest.Version, "v")); err != nil {
		out.Errors = append(out.Errors, extensionError("version", "version must be valid semver"))
	}
	status, compatibilityWarning := extensionCompatibility(out.Manifest.CompatibleAstronomer, currentVersion)
	out.CompatibilityStatus = status
	if compatibilityWarning != "" {
		out.Warnings = append(out.Warnings, extensionWarning("compatibleAstronomer", compatibilityWarning))
	}
	if status == "incompatible" {
		out.Errors = append(out.Errors, extensionError("compatibleAstronomer", "extension is incompatible with this Astronomer version"))
	}
	if !safeExtensionEntry(out.Manifest.Entry) {
		out.Errors = append(out.Errors, extensionError("entry", "entry must be a relative JavaScript bundle path"))
	}
	if !hasExtensionPoints(out.Manifest.ExtensionPoints) {
		out.Errors = append(out.Errors, extensionError("extensionPoints", "at least one extension point is required"))
	}
	validateExtensionPoints(out.Manifest, &out)
	for _, permission := range out.Manifest.Permissions {
		if !validExtensionPermission(permission) {
			out.Errors = append(out.Errors, extensionError("permissions", "permission must use resource:verb format: "+permission))
		}
	}
	validateExtensionCSP(out.Manifest.CSP, &out)
	out.Valid = len(out.Errors) == 0
	return out
}

func normalizeExtensionManifest(in ExtensionManifest) ExtensionManifest {
	in.APIVersion = strings.TrimSpace(in.APIVersion)
	in.Name = strings.TrimSpace(in.Name)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.Version = strings.TrimSpace(in.Version)
	in.CompatibleAstronomer = strings.TrimSpace(in.CompatibleAstronomer)
	in.Entry = strings.TrimSpace(in.Entry)
	in.Permissions = trimStringSlice(in.Permissions)
	in.BackendAPIScopes = trimStringSlice(in.BackendAPIScopes)
	return in
}

func extensionCompatibility(rangeExpr, currentVersion string) (string, string) {
	if strings.TrimSpace(rangeExpr) == "" {
		return "unknown", "compatibleAstronomer is empty; extension will fail closed until a range is declared"
	}
	if currentVersion == "" || currentVersion == "dev" {
		if _, err := semver.NewConstraint(rangeExpr); err != nil {
			return "unknown", "compatibleAstronomer is not a valid semver constraint"
		}
		return "compatible", ""
	}
	constraint, err := semver.NewConstraint(rangeExpr)
	if err != nil {
		return "unknown", "compatibleAstronomer is not a valid semver constraint"
	}
	current, err := semver.NewVersion(strings.TrimPrefix(currentVersion, "v"))
	if err != nil {
		return "unknown", "current Astronomer version is not semver; compatibility cannot be proven"
	}
	if !constraint.Check(current) {
		return "incompatible", ""
	}
	return "compatible", ""
}

func safeExtensionEntry(entry string) bool {
	if entry == "" || strings.HasPrefix(entry, "/") || strings.Contains(entry, "..") {
		return false
	}
	if u, err := url.Parse(entry); err == nil && u.Scheme != "" {
		return false
	}
	return path.Ext(entry) == ".js"
}

func hasExtensionPoints(points ExtensionManifestPoints) bool {
	return len(points.Sidebar)+len(points.Widgets)+len(points.ClusterTabs)+len(points.Settings) > 0
}

func validateExtensionPoints(manifest ExtensionManifest, out *ExtensionValidationResponse) {
	points := manifest.ExtensionPoints
	perms := permissionSet(manifest.Permissions)
	for i, item := range points.Sidebar {
		field := fmt.Sprintf("extensionPoints.sidebar[%d]", i)
		if strings.TrimSpace(item.Label) == "" {
			out.Errors = append(out.Errors, extensionError(field+".label", "sidebar label is required"))
		}
		if !strings.HasPrefix(item.Path, "/dashboard/extensions/") {
			out.Errors = append(out.Errors, extensionError(field+".path", "sidebar path must live under /dashboard/extensions/"))
		}
		validateExtensionPointRender(field, item.Render, item.DataSources, perms, out)
	}
	for i, tab := range points.ClusterTabs {
		field := fmt.Sprintf("extensionPoints.clusterTabs[%d]", i)
		if strings.TrimSpace(tab.Label) == "" || strings.TrimSpace(tab.Component) == "" {
			out.Errors = append(out.Errors, extensionError(field, "cluster tab label and component are required"))
		}
		validateExtensionPointRender(field, tab.Render, tab.DataSources, perms, out)
	}
	for i, widget := range points.Widgets {
		field := fmt.Sprintf("extensionPoints.widgets[%d]", i)
		if strings.TrimSpace(widget.ID) == "" || strings.TrimSpace(widget.Title) == "" {
			out.Errors = append(out.Errors, extensionError(field, "widget id and title are required"))
		}
		validateExtensionPointRender(field, widget.Render, widget.DataSources, perms, out)
	}
	for i, page := range points.Settings {
		field := fmt.Sprintf("extensionPoints.settings[%d]", i)
		if strings.TrimSpace(page.Label) == "" || strings.TrimSpace(page.Component) == "" {
			out.Errors = append(out.Errors, extensionError(field, "settings label and component are required"))
		}
		validateExtensionPointRender(field, page.Render, page.DataSources, perms, out)
	}
}

func permissionSet(permissions []string) map[string]bool {
	set := make(map[string]bool, len(permissions))
	for _, p := range permissions {
		set[strings.TrimSpace(p)] = true
	}
	return set
}

// allowedProxies, allowedDataMethods, allowedScopes and allowedFormats are the
// closed enums the declarative spec is validated against.
var (
	allowedProxies      = map[string]bool{"astronomer-api": true, "k8s": true, "prometheus": true}
	allowedDataMethods  = map[string]bool{"GET": true, "POST": true}
	allowedScopes       = map[string]bool{"global": true, "cluster": true, "project": true}
	allowedWidgetKinds  = map[string]bool{"table": true, "chart": true, "stat": true, "form": true}
	allowedFieldFormats = map[string]bool{"text": true, "number": true, "bytes": true, "datetime": true, "duration": true, "badge": true, "currency": true}
	allowedInputTypes   = map[string]bool{"text": true, "number": true, "select": true, "toggle": true}
	allowedChartTypes   = map[string]bool{"line": true, "bar": true, "area": true}
	allowedDataShapes   = map[string]bool{"list": true, "object": true, "series": true}
	allowedPathTokens   = map[string]bool{"clusterId": true, "projectId": true, "namespace": true}
	writeVerbs          = map[string]bool{"create": true, "update": true, "delete": true}

	bundleSHA256RE    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pathPlaceholderRE = regexp.MustCompile(`\{([^}]*)\}`)
)

// validateExtensionPointRender validates the Tier-1/Tier-2 render spec and the
// data sources declared on a single extension point. dataSources are the
// Tier-1 sources living on the point; Tier-2 sources live inside the bundle
// descriptor and are validated there.
func validateExtensionPointRender(field string, render *ExtensionRender, dataSources []DataSourceRef, perms map[string]bool, out *ExtensionValidationResponse) {
	ids := map[string]bool{}
	for i, ds := range dataSources {
		id := validateDataSourceRef(fmt.Sprintf("%s.dataSources[%d]", field, i), ds, perms, out)
		if id != "" {
			ids[id] = true
		}
	}
	if render == nil {
		return
	}
	if render.Declarative != nil && render.Bundle != nil {
		out.Errors = append(out.Errors, extensionError(field+".render", "render: a point is either declarative or bundle, not both"))
		return
	}
	if render.Declarative != nil {
		validateDeclarativeWidget(field+".render.declarative", render.Declarative, ids, dataSources, out)
	}
	if render.Bundle != nil {
		validateBundleDescriptor(field+".render.bundle", render.Bundle, perms, out)
	}
}

// validateDataSourceRef validates a single DataSourceRef and returns its ID
// (empty if the ref is unusable).
func validateDataSourceRef(field string, ds DataSourceRef, perms map[string]bool, out *ExtensionValidationResponse) string {
	id := strings.TrimSpace(ds.ID)
	if id == "" {
		out.Errors = append(out.Errors, extensionError(field+".id", "dataSource id is required"))
	}
	if !allowedProxies[ds.Proxy] {
		out.Errors = append(out.Errors, extensionError(field+".proxy", "proxy must be one of astronomer-api|k8s|prometheus"))
	}
	if !allowedDataMethods[ds.Method] {
		out.Errors = append(out.Errors, extensionError(field+".method", "method must be GET or POST"))
	}
	if !allowedDataShapes[ds.Shape] {
		out.Errors = append(out.Errors, extensionError(field+".shape", "shape must be one of list|object|series"))
	}
	validateDataSourcePath(field+".path", ds.Path, ds.Proxy, out)
	for _, f := range ds.Fields {
		if strings.TrimSpace(f) == "*" || f == "" {
			out.Errors = append(out.Errors, extensionError(field+".fields", "fields must be explicit dot-paths; '*' is rejected"))
			break
		}
	}
	// RBAC requirement: canonical, non-wildcard, valid scope.
	if !rbac.IsCanonicalResource(ds.RBAC.Resource) || ds.RBAC.Resource == "*" {
		out.Errors = append(out.Errors, extensionError(field+".rbac.resource", "rbac.resource must be a canonical resource and not '*'"))
	}
	if !rbac.IsCanonicalVerb(ds.RBAC.Verb) || ds.RBAC.Verb == "*" {
		out.Errors = append(out.Errors, extensionError(field+".rbac.verb", "rbac.verb must be a canonical verb and not '*'"))
	}
	if !allowedScopes[ds.RBAC.Scope] {
		out.Errors = append(out.Errors, extensionError(field+".rbac.scope", "rbac.scope must be one of global|cluster|project"))
	}
	// RBAC ceiling: resource:verb must be declared in permissions[].
	if ds.RBAC.Resource != "" && ds.RBAC.Verb != "" {
		if !perms[ds.RBAC.Resource+":"+ds.RBAC.Verb] {
			out.Errors = append(out.Errors, extensionError(field+".rbac", "not declared in permissions[]"))
		}
	}
	// Write sources require a write verb.
	if ds.Method == "POST" && !writeVerbs[ds.RBAC.Verb] {
		out.Errors = append(out.Errors, extensionError(field+".rbac.verb", "POST dataSource requires a write verb (create|update|delete)"))
	}
	return id
}

func validateDataSourcePath(field, p, proxy string, out *ExtensionValidationResponse) {
	if !strings.HasPrefix(p, "/") {
		out.Errors = append(out.Errors, extensionError(field, "path must start with /"))
		return
	}
	if strings.Contains(p, "..") {
		out.Errors = append(out.Errors, extensionError(field, "path must not contain '..'"))
		return
	}
	if proxy == "astronomer-api" && !strings.HasPrefix(p, "/api/v1/") {
		out.Errors = append(out.Errors, extensionError(field, "astronomer-api path must start with /api/v1/"))
	}
	for _, m := range pathPlaceholderRE.FindAllStringSubmatch(p, -1) {
		if !allowedPathTokens[m[1]] {
			out.Errors = append(out.Errors, extensionError(field, "path placeholder {"+m[1]+"} is not one of {clusterId},{projectId},{namespace}"))
		}
	}
}

func validateDeclarativeWidget(field string, w *DeclarativeWidget, ids map[string]bool, dataSources []DataSourceRef, out *ExtensionValidationResponse) {
	if !allowedWidgetKinds[w.Kind] {
		out.Errors = append(out.Errors, extensionError(field+".kind", "kind must be one of table|chart|stat|form"))
	}
	if !ids[strings.TrimSpace(w.DataSource)] {
		out.Errors = append(out.Errors, extensionError(field+".dataSource", "dataSource must reference a declared dataSources[].id"))
	}
	for i, fb := range w.Fields {
		if fb.Format != "" && !allowedFieldFormats[fb.Format] {
			out.Errors = append(out.Errors, extensionError(fmt.Sprintf("%s.fields[%d].format", field, i), "field format must be one of text|number|bytes|datetime|duration|badge|currency"))
		}
	}
	if w.Chart != nil && !allowedChartTypes[w.Chart.Type] {
		out.Errors = append(out.Errors, extensionError(field+".chart.type", "chart type must be one of line|bar|area"))
	}
	if w.Stat != nil && w.Stat.Value.Format != "" && !allowedFieldFormats[w.Stat.Value.Format] {
		out.Errors = append(out.Errors, extensionError(field+".stat.value.format", "field format must be one of text|number|bytes|datetime|duration|badge|currency"))
	}
	if w.Form != nil {
		submit := strings.TrimSpace(w.Form.Submit)
		if !ids[submit] {
			out.Errors = append(out.Errors, extensionError(field+".form.submit", "form.submit must reference a declared dataSources[].id"))
		} else {
			for _, ds := range dataSources {
				if ds.ID == submit && ds.Method != "POST" {
					out.Errors = append(out.Errors, extensionError(field+".form.submit", "form.submit dataSource must use method POST"))
				}
			}
		}
		for i, in := range w.Form.Inputs {
			if !allowedInputTypes[in.Type] {
				out.Errors = append(out.Errors, extensionError(fmt.Sprintf("%s.form.inputs[%d].type", field, i), "input type must be one of text|number|select|toggle"))
			}
		}
	}
}

func validateBundleDescriptor(field string, b *BundleDescriptor, perms map[string]bool, out *ExtensionValidationResponse) {
	if !bundleSHA256RE.MatchString(b.SHA256) {
		out.Errors = append(out.Errors, extensionError(field+".sha256", "sha256 must match sha256:<64 hex chars>"))
	}
	if !strings.HasPrefix(b.Integrity, "sha384-") {
		out.Errors = append(out.Errors, extensionError(field+".integrity", "integrity must be an SRI sha384- digest"))
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b.Signature)); err != nil || strings.TrimSpace(b.Signature) == "" {
		out.Errors = append(out.Errors, extensionError(field+".signature", "signature must be base64 Ed25519"))
	}
	if u, err := url.Parse(b.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		out.Errors = append(out.Errors, extensionError(field+".url", "url must be an absolute https URL"))
	}
	if !safeExtensionEntry(b.Entry) {
		out.Errors = append(out.Errors, extensionError(field+".entry", "entry must be a relative JavaScript bundle path"))
	}
	hostOK := false
	if so, err := url.Parse(b.SandboxOrigin); err == nil && so.Scheme == "https" && so.Host != "" && so.Path == "" {
		hostOK = true
	}
	if !hostOK {
		out.Errors = append(out.Errors, extensionError(field+".sandboxOrigin", "sandboxOrigin must be an absolute https origin"))
	}
	if len(b.CSP.FrameSrc) == 0 {
		out.Errors = append(out.Errors, extensionError(field+".csp.frameSrc", "bundle requires csp.frameSrc"))
	}
	for _, src := range b.CSP.ConnectSrc {
		s := strings.TrimSpace(src)
		if s == "*" || strings.Contains(s, "/api/") {
			out.Errors = append(out.Errors, extensionError(field+".csp.connectSrc", "bundle connectSrc must not be '*' or any /api/ host"))
			break
		}
	}
	for _, source := range b.CSP.ScriptSrc {
		switch strings.TrimSpace(source) {
		case "'unsafe-eval'", "'unsafe-inline'", "*":
			out.Errors = append(out.Errors, extensionError(field+".csp.scriptSrc", "scriptSrc cannot include "+strings.TrimSpace(source)))
		}
	}
	// Tier-2 bundle data sources live inside the descriptor.
	for i, ds := range b.DataSources {
		validateDataSourceRef(fmt.Sprintf("%s.dataSources[%d]", field, i), ds, perms, out)
	}
}

func validExtensionPermission(permission string) bool {
	parts := strings.Split(strings.TrimSpace(permission), ":")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func validateExtensionCSP(csp ExtensionCSP, out *ExtensionValidationResponse) {
	for _, source := range csp.ScriptSrc {
		trimmed := strings.TrimSpace(source)
		switch trimmed {
		case "'unsafe-eval'", "'unsafe-inline'", "*":
			out.Errors = append(out.Errors, extensionError("csp.scriptSrc", "scriptSrc cannot include "+trimmed))
		}
	}
	for _, source := range append(append(csp.ConnectSrc, csp.FrameSrc...), csp.ImageSrc...) {
		if strings.TrimSpace(source) == "*" {
			out.Warnings = append(out.Warnings, extensionWarning("csp", "wildcard non-script CSP source should be narrowed before production use"))
		}
	}
}

func extensionChecksum(manifest ExtensionManifest) string {
	raw, _ := json.Marshal(manifest)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func extensionRecordResponse(row sqlc.UIExtension) ExtensionRecordResponse {
	var manifest ExtensionManifest
	_ = json.Unmarshal(row.Manifest, &manifest)
	return ExtensionRecordResponse{
		ID:                  row.ID,
		Name:                row.Name,
		DisplayName:         row.DisplayName,
		Version:             row.Version,
		Source:              row.Source,
		Checksum:            row.Checksum,
		Enabled:             row.Enabled,
		CompatibilityStatus: row.CompatibilityStatus,
		Manifest:            manifest,
		InstalledAt:         row.InstalledAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:           row.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func extensionDisplayName(manifest ExtensionManifest) string {
	if manifest.DisplayName != "" {
		return manifest.DisplayName
	}
	return manifest.Name
}

func sanitizeExtensionSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "manual"
	}
	if len(source) > 128 {
		return source[:128]
	}
	return source
}

func trimStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func extensionError(field, message string) ExtensionValidationFinding {
	return ExtensionValidationFinding{Field: field, Severity: "error", Message: message}
}

func extensionWarning(field, message string) ExtensionValidationFinding {
	return ExtensionValidationFinding{Field: field, Severity: "warning", Message: message}
}

func sampleExtensionManifest() ExtensionManifest {
	return ExtensionManifest{
		APIVersion:           "extensions.astronomer.io/v1alpha1",
		Name:                 "cost-insights",
		DisplayName:          "Cost Insights",
		Version:              "0.1.0",
		CompatibleAstronomer: ">=0.2.0 <1.0.0",
		Entry:                "index.js",
		Permissions:          []string{"clusters:read", "monitoring:read"},
		CSP: ExtensionCSP{
			ConnectSrc: []string{"'self'"},
			ImageSrc:   []string{"'self'", "data:"},
		},
		ExtensionPoints: ExtensionManifestPoints{
			Sidebar: []ExtensionSidebarPoint{{
				Label: "Cost",
				Path:  "/dashboard/extensions/cost-insights",
			}},
			Widgets: []ExtensionWidgetPoint{{
				ID:    "cost-summary",
				Title: "Cost summary",
			}},
			ClusterTabs: []ExtensionClusterTab{{
				Label:     "Cost",
				Component: "ClusterCostTab",
			}},
		},
	}
}
