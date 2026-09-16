package handler

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/jackc/pgx/v5"
)

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

// VerifyBundleRequest carries an executable extension bundle for signature +
// checksum verification. Bundle and Signature are base64 (std encoding); the
// signature is an Ed25519 signature over the raw bundle bytes. Checksum, if
// supplied, is the "sha256:<hex>" digest the caller expects and is checked
// against the bundle so a tampered bundle is rejected even before the
// signature is examined.
// openapi:request-operation postExtensionsVerifyBundle
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
		if h.runTx == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "extension transaction runner is not configured")
			return
		}
		ok, gErr := h.markBundleVerified(r, name, checksum)
		if gErr != nil {
			respondTransactionalMutationError(w, r, gErr, http.StatusInternalServerError, apierror.UpdateError, "Failed to record bundle verification")
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
	if h.runTx == nil {
		return false, audit.ErrMandatoryPersistenceUnavailable
	}
	verifyAndPersist := func(q ExtensionQuerier, row sqlc.UIExtension) (sqlc.UIExtension, bool, error) {
		var manifest ExtensionManifest
		if json.Unmarshal(row.Manifest, &manifest) != nil || !manifestBundleHasChecksum(manifest, checksum) {
			return sqlc.UIExtension{}, false, nil
		}
		updated, err := q.SetUIExtensionBundleVerified(r.Context(), sqlc.SetUIExtensionBundleVerifiedParams{Name: name, BundleVerified: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.UIExtension{}, false, nil
		}
		return updated, err == nil, err
	}

	candidate := false
	err := h.runTx(r.Context(), func(q ExtensionMutationTx) error {
		locked, err := q.GetUIExtensionByNameForUpdate(r.Context(), name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		updated, ok, err := verifyAndPersist(q, locked)
		if err != nil || !ok {
			return err
		}
		candidate = true
		return recordAuditOutbox(r, q, "admin.extension.bundle_verified", "ui_extension", updated.ID.String(), updated.Name, http.StatusOK, map[string]any{
			"name": updated.Name, "version": updated.Version, "checksum": checksum,
		})
	})
	return candidate && err == nil, err
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
