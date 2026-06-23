package handler

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestSignedManifestURLRoundTrip exercises the HMAC sign/verify path:
// a freshly-minted URL verifies, while tampered, expired, and
// wrong-secret variants are all rejected.
func TestSignedManifestURLRoundTrip(t *testing.T) {
	h := NewClusterHandler(nil)
	h.SetManifestSigningSecret("test-secret")

	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	signed := h.SignManifestURL(id, 15*time.Minute)
	if signed == "" {
		t.Fatal("SignManifestURL returned empty with a secret configured")
	}
	if !strings.HasPrefix(signed, "/api/v1/register/signed/"+id.String()+"?") {
		t.Fatalf("unexpected signed URL shape: %s", signed)
	}

	u, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("parse signed URL: %v", err)
	}
	expiry, err := strconv.ParseInt(u.Query().Get("expires"), 10, 64)
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	sig := u.Query().Get("sig")

	// Valid signature verifies.
	if err := h.verifyManifestSignature(id, expiry, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}

	// Tampered signature rejected.
	if err := h.verifyManifestSignature(id, expiry, sig+"00"); err == nil {
		t.Fatal("tampered signature accepted")
	}

	// Different cluster_id under the same sig is rejected (HMAC binds id).
	other := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if err := h.verifyManifestSignature(other, expiry, sig); err == nil {
		t.Fatal("signature reused for a different cluster accepted")
	}

	// Expired URL rejected even with a correct signature for that expiry.
	pastExpiry := time.Now().Add(-time.Minute).Unix()
	pastSig := h.manifestSignature(id, pastExpiry)
	if err := h.verifyManifestSignature(id, pastExpiry, pastSig); err == nil {
		t.Fatal("expired signature accepted")
	}

	// Wrong secret cannot verify a URL signed with the real secret.
	other2 := NewClusterHandler(nil)
	other2.SetManifestSigningSecret("different-secret")
	if err := other2.verifyManifestSignature(id, expiry, sig); err == nil {
		t.Fatal("signature verified under a different secret")
	}
}

// TestVerifyManifestSignatureRejectsOverLongExpiry checks the upper bound
// on the attacker-presented expiry: an HMAC that is otherwise valid but
// claims a window past maxSignedManifestTTL is rejected.
func TestVerifyManifestSignatureRejectsOverLongExpiry(t *testing.T) {
	h := NewClusterHandler(nil)
	h.SetManifestSigningSecret("test-secret")
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	// Just inside the ceiling verifies.
	okExpiry := time.Now().Add(maxSignedManifestTTL - time.Minute).Unix()
	if err := h.verifyManifestSignature(id, okExpiry, h.manifestSignature(id, okExpiry)); err != nil {
		t.Fatalf("in-window expiry rejected: %v", err)
	}

	// Past the ceiling is rejected even with a correct signature.
	farExpiry := time.Now().Add(maxSignedManifestTTL + time.Hour).Unix()
	if err := h.verifyManifestSignature(id, farExpiry, h.manifestSignature(id, farExpiry)); err == nil {
		t.Fatal("over-long expiry accepted")
	}
}

// TestManifestSigningKeyIsDerived ensures the manifest HMAC key is
// domain-separated from the raw secret it was seeded with (which may be
// the JWT signing secret), so the two are never identical bytes.
func TestManifestSigningKeyIsDerived(t *testing.T) {
	const secret = "production-jwt-signing-key"
	h := NewClusterHandler(nil)
	h.SetManifestSigningSecret(secret)

	if string(h.manifestSigningSecret) == secret {
		t.Fatal("manifest signing key equals the raw secret; not domain-separated")
	}
	if len(h.manifestSigningSecret) == 0 {
		t.Fatal("derived key is empty")
	}
}

// TestSignManifestURLDisabledWithoutSecret ensures the feature stays off
// when no secret is wired.
func TestSignManifestURLDisabledWithoutSecret(t *testing.T) {
	h := NewClusterHandler(nil)
	id := uuid.New()
	if got := h.SignManifestURL(id, 15*time.Minute); got != "" {
		t.Fatalf("expected empty URL without secret, got %q", got)
	}
	if err := h.verifyManifestSignature(id, time.Now().Add(time.Minute).Unix(), "x"); err == nil {
		t.Fatal("verify succeeded with no secret configured")
	}
}
