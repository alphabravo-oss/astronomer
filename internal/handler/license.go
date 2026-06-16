// Read-only license/entitlement scaffold (T7.4).
//
// The user declined a full license-management feature. This handler
// ships only the read endpoint so future LicenseExpiringSoon /
// EntitlementMissing conditions and the eventual chart-of-features
// UI have a stable contract to wire against. The returned state is
// hard-coded "open-source": there is no expiry, no seat count, no
// feature gating — the field list is the API surface, the values are
// the open-source defaults.
//
//	GET /api/v1/license/   — returns {state, features_enabled}
//
// No auth gate beyond the standard /api/v1/* middleware. Anyone with
// an authed session can read the entitlement summary; that's the
// expected contract for a downstream feature gate on the frontend.
package handler

import "net/http"

// LicenseResponse is the API shape returned by GET /api/v1/license/.
type LicenseResponse struct {
	State           string   `json:"state"`
	FeaturesEnabled []string `json:"features_enabled"`
	ExpiresAt       *string  `json:"expires_at"`
	SeatLimit       *int     `json:"seat_limit"`
}

// LicenseHandler is the trivial read-only owner of /api/v1/license/.
// Constructed once, fields are constants in the open-source build.
type LicenseHandler struct{}

// NewLicenseHandler returns a fresh handler with the open-source
// default response wired in.
func NewLicenseHandler() *LicenseHandler {
	return &LicenseHandler{}
}

// openSourceFeatures enumerates the capabilities the OSS build ships.
// Listed alphabetically; future additions go in-place so the response
// stays diff-friendly. Kept in code (not the DB) because the OSS
// build does not gate any of these — the list is informational only.
var openSourceFeatures = []string{
	"alerting",
	"audit-export",
	"backups",
	"baseline-policies",
	"catalog",
	"cluster-templates",
	"compliance-posture",
	"cluster-explorer",
	"dex-sso",
	"fleet-operations",
	"image-vulnerability-scans",
	"kubectl-shell",
	"monitoring",
	"netpol-templates",
	"projects",
	"rbac",
	"siem-forwarding",
	"telemetry",
	"tunnels",
	"webhooks",
}

// Get returns the OSS entitlement summary.
func (h *LicenseHandler) Get(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, LicenseResponse{
		State:           "open-source",
		FeaturesEnabled: openSourceFeatures,
		ExpiresAt:       nil,
		SeatLimit:       nil,
	})
}
