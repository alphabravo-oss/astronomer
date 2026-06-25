package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeTicketIssuer records the scope it was asked to mint for so a test can
// assert the issuance is bound to exactly {user, extension, dataSource, cluster}.
type fakeTicketIssuer struct {
	called       bool
	user         uuid.UUID
	extension    string
	dataSourceID string
	clusterID    uuid.UUID
}

func (f *fakeTicketIssuer) IssueToken(userID uuid.UUID, extension, dataSourceID string, clusterID uuid.UUID) (string, time.Time, error) {
	f.called = true
	f.user = userID
	f.extension = extension
	f.dataSourceID = dataSourceID
	f.clusterID = clusterID
	return "opaque-ticket-token", time.Now().Add(time.Minute), nil
}

// bridgeHandler wires an ExtensionHandler with a seeded, ENABLED + COMPATIBLE +
// bundle-VERIFIED Tier-2 extension, the given bindings, and a recording issuer.
func bridgeHandler(t *testing.T, bindings []rbac.RoleBinding) (*ExtensionHandler, *fakeTicketIssuer) {
	t.Helper()
	q := newFakeExtensionQuerier()
	q.seedExtension(t, tier2Manifest(), true, "compatible", true)
	h := NewExtensionHandler(q)
	h.SetRBAC(rbac.NewEngine(), stubBindings{bindings: bindings})
	issuer := &fakeTicketIssuer{}
	h.SetExtensionTicketIssuer(issuer)
	return h, issuer
}

func tokenRequest(t *testing.T, userID uuid.UUID, dataSource string, ctx map[string]any) *http.Request {
	t.Helper()
	body := map[string]any{"dataSource": dataSource}
	if ctx != nil {
		body["context"] = ctx
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/extensions/cost-insights/token/", strings.NewReader(string(raw)))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", "cost-insights")
	c := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if userID != uuid.Nil {
		c = middleware.SetAuthenticatedUserForTest(c, &middleware.AuthenticatedUser{ID: userID.String(), AuthMethod: "jwt"})
	}
	return req.WithContext(c)
}

// TestIssueTicket_GrantedWithinUserRBAC proves the happy path: a user who holds
// monitoring:read on cluster A gets a ticket scoped to exactly that call.
func TestIssueTicket_GrantedWithinUserRBAC(t *testing.T) {
	clusterA := uuid.New()
	userID := uuid.New()
	h, issuer := bridgeHandler(t, monitoringReaderOn(clusterA))

	req := tokenRequest(t, userID, "podCost", map[string]any{"clusterId": clusterA.String()})
	rr := httptest.NewRecorder()
	h.IssueTicket(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s, want 201", rr.Code, rr.Body.String())
	}
	if !issuer.called {
		t.Fatal("issuer was not invoked on an allowed request")
	}
	if issuer.user != userID || issuer.extension != "cost-insights" || issuer.dataSourceID != "podCost" || issuer.clusterID != clusterA {
		t.Fatalf("ticket scope mismatch: %+v", issuer)
	}
	resp := decodeExtensionResp[extTokenResponse](t, rr)
	if resp.Token == "" {
		t.Fatal("expected a non-empty token")
	}
	if resp.Scope != "ext:cost-insights:data:podCost" {
		t.Fatalf("scope=%q", resp.Scope)
	}
}

// TestIssueTicket_DeniedOutsideUserRBAC is the load-bearing test: a user who can
// read cluster A cannot mint a ticket for cluster B. The bridge applies the SAME
// RBAC gate as §DataProxy, so a leaked ticket can never exceed the user.
func TestIssueTicket_DeniedOutsideUserRBAC(t *testing.T) {
	clusterA := uuid.New()
	clusterB := uuid.New()
	userID := uuid.New()
	h, issuer := bridgeHandler(t, monitoringReaderOn(clusterA))

	req := tokenRequest(t, userID, "podCost", map[string]any{"clusterId": clusterB.String()})
	rr := httptest.NewRecorder()
	h.IssueTicket(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s, want 403", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "extension_rbac_denied") {
		t.Fatalf("expected extension_rbac_denied, got %s", rr.Body.String())
	}
	if issuer.called {
		t.Fatal("issuer must NOT be invoked when the user's RBAC denies the call")
	}
}

// TestIssueTicket_UnknownDataSourceRejected proves the manifest allowlist: an id
// not present in the bundle descriptor is a 404 — a ticket can only be minted
// for a Tier-2 source the extension shipped.
func TestIssueTicket_UnknownDataSourceRejected(t *testing.T) {
	userID := uuid.New()
	h, issuer := bridgeHandler(t, []rbac.RoleBinding{{IsSuperuser: true}})

	rr := httptest.NewRecorder()
	h.IssueTicket(rr, tokenRequest(t, userID, "doesNotExist", map[string]any{"clusterId": uuid.New().String()}))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", rr.Code, rr.Body.String())
	}
	if issuer.called {
		t.Fatal("issuer must not be reached for an unknown dataSource")
	}
}

// TestIssueTicket_UnverifiedBundleRejected proves the §HostMounts gate carries
// into issuance: an unverified bundle (bundle_verified=false) issues no ticket,
// even for a superuser.
func TestIssueTicket_UnverifiedBundleRejected(t *testing.T) {
	q := newFakeExtensionQuerier()
	q.seedExtension(t, tier2Manifest(), true, "compatible", false) // NOT verified
	h := NewExtensionHandler(q)
	h.SetRBAC(rbac.NewEngine(), stubBindings{bindings: []rbac.RoleBinding{{IsSuperuser: true}}})
	issuer := &fakeTicketIssuer{}
	h.SetExtensionTicketIssuer(issuer)

	rr := httptest.NewRecorder()
	h.IssueTicket(rr, tokenRequest(t, uuid.New(), "podCost", map[string]any{"clusterId": uuid.New().String()}))

	if rr.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", rr.Code, rr.Body.String())
	}
	if issuer.called {
		t.Fatal("issuer must not be reached for an unverified bundle")
	}
}

// TestIssueTicket_Unauthenticated rejects a request with no session.
func TestIssueTicket_Unauthenticated(t *testing.T) {
	h, issuer := bridgeHandler(t, []rbac.RoleBinding{{IsSuperuser: true}})
	rr := httptest.NewRecorder()
	h.IssueTicket(rr, tokenRequest(t, uuid.Nil, "podCost", map[string]any{"clusterId": uuid.New().String()}))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rr.Code)
	}
	if issuer.called {
		t.Fatal("issuer must not be reached unauthenticated")
	}
}

// TestIssueTicket_NoIssuerConfigured fails closed when no ticket store is wired.
func TestIssueTicket_NoIssuerConfigured(t *testing.T) {
	q := newFakeExtensionQuerier()
	q.seedExtension(t, tier2Manifest(), true, "compatible", true)
	h := NewExtensionHandler(q)
	h.SetRBAC(rbac.NewEngine(), stubBindings{bindings: []rbac.RoleBinding{{IsSuperuser: true}}})
	// No SetExtensionTicketIssuer.
	rr := httptest.NewRecorder()
	h.IssueTicket(rr, tokenRequest(t, uuid.New(), "podCost", map[string]any{"clusterId": uuid.New().String()}))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rr.Code)
	}
}

// --- Gate lift: verify-bundle flips bundle_verified for a signed+trusted bundle.

// signedTier2 returns a tier-2 manifest whose bundle descriptor checksum matches
// the given bundle, plus the raw bundle bytes and a valid signature.
func signedTier2(t *testing.T, priv ed25519.PrivateKey) (ExtensionManifest, []byte, []byte) {
	t.Helper()
	bundle := []byte("console.log('signed extension bundle');")
	sig := ed25519.Sign(priv, bundle)
	m := tier2Manifest()
	m.ExtensionPoints.ClusterTabs[0].Render.Bundle.SHA256 = bundleChecksum(bundle)
	return m, bundle, sig
}

// TestVerifyBundle_LiftsGateForSignedTrustedBundle is the gate lift: a signed +
// trusted bundle whose checksum matches the stored descriptor flips
// bundle_verified=true, so /mounts/ will surface the Tier-2 extension.
func TestVerifyBundle_LiftsGateForSignedTrustedBundle(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	m, bundle, sig := signedTier2(t, priv)

	q := newFakeExtensionQuerier()
	q.seedExtension(t, m, true, "compatible", false) // installed, but NOT yet verified
	h := NewExtensionHandler(q)
	if err := h.SetTrustedBundleKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("set key: %v", err)
	}

	raw, _ := json.Marshal(VerifyBundleRequest{
		Bundle:    base64.StdEncoding.EncodeToString(bundle),
		Signature: base64.StdEncoding.EncodeToString(sig),
		Checksum:  bundleChecksum(bundle),
		Name:      "cost-insights",
	})
	rr := httptest.NewRecorder()
	h.VerifyBundle(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/verify-bundle/", string(raw)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeExtensionResp[VerifyBundleResponse](t, rr)
	if !resp.Verified || !resp.Gated {
		t.Fatalf("expected verified+gated, got %+v", resp)
	}
	if !q.rows["cost-insights"].BundleVerified {
		t.Fatal("bundle_verified must be true after a signed+trusted verify-bundle")
	}

	// And now the Tier-2 mount appears in /mounts/.
	mounts := mountsResponse(t, h)
	if len(mounts.ClusterTabs) != 1 {
		t.Fatalf("expected the verified Tier-2 tab to mount, got %+v", mounts.ClusterTabs)
	}
}

// TestVerifyBundle_UnsignedStaysGated proves the negative: a bundle that does
// not verify against the trusted key never flips bundle_verified, so the Tier-2
// extension stays gated out of /mounts/.
func TestVerifyBundle_UnsignedStaysGated(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	m, bundle, _ := signedTier2(t, priv)
	// Sign with a DIFFERENT (untrusted) key.
	_, wrongPriv, _ := ed25519.GenerateKey(rand.Reader)
	badSig := ed25519.Sign(wrongPriv, bundle)

	q := newFakeExtensionQuerier()
	q.seedExtension(t, m, true, "compatible", false)
	h := NewExtensionHandler(q)
	if err := h.SetTrustedBundleKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("set key: %v", err)
	}

	raw, _ := json.Marshal(VerifyBundleRequest{
		Bundle:    base64.StdEncoding.EncodeToString(bundle),
		Signature: base64.StdEncoding.EncodeToString(badSig),
		Name:      "cost-insights",
	})
	rr := httptest.NewRecorder()
	h.VerifyBundle(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/verify-bundle/", string(raw)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rr.Code, rr.Body.String())
	}
	if q.rows["cost-insights"].BundleVerified {
		t.Fatal("an unsigned/untrusted bundle must NOT flip bundle_verified")
	}
	mounts := mountsResponse(t, h)
	if len(mounts.ClusterTabs) != 0 {
		t.Fatalf("an unverified Tier-2 bundle must stay out of /mounts/, got %+v", mounts.ClusterTabs)
	}
}

// TestVerifyBundle_ChecksumMustMatchStoredDescriptor proves verify-bundle can
// only lift the gate for a descriptor the extension actually shipped: a valid
// signature over a bundle whose checksum is NOT in the manifest leaves the gate
// down (gated=false) even though verification itself succeeds.
func TestVerifyBundle_ChecksumMustMatchStoredDescriptor(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	// Seed a manifest whose descriptor checksum is for SOME OTHER bundle.
	m := tier2Manifest() // descriptor sha256 = repeat("a",64), not our bundle
	q := newFakeExtensionQuerier()
	q.seedExtension(t, m, true, "compatible", false)
	h := NewExtensionHandler(q)
	if err := h.SetTrustedBundleKey(base64.StdEncoding.EncodeToString(pub)); err != nil {
		t.Fatalf("set key: %v", err)
	}

	bundle := []byte("a different bundle than the descriptor declares")
	sig := ed25519.Sign(priv, bundle)
	raw, _ := json.Marshal(VerifyBundleRequest{
		Bundle:    base64.StdEncoding.EncodeToString(bundle),
		Signature: base64.StdEncoding.EncodeToString(sig),
		Name:      "cost-insights",
	})
	rr := httptest.NewRecorder()
	h.VerifyBundle(rr, extensionReq(t, http.MethodPost, "/api/v1/extensions/verify-bundle/", string(raw)))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	resp := decodeExtensionResp[VerifyBundleResponse](t, rr)
	if !resp.Verified {
		t.Fatal("verification itself should succeed")
	}
	if resp.Gated {
		t.Fatal("gate must NOT lift for a checksum absent from the stored descriptor")
	}
	if q.rows["cost-insights"].BundleVerified {
		t.Fatal("bundle_verified must stay false for a non-matching checksum")
	}
}
