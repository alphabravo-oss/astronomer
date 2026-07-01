package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"golang.org/x/oauth2"
)

// Finding #4: an OIDC id_token whose email_verified claim is explicitly
// false must be rejected. The id_token's signature/issuer/audience all
// validate — email_verified is a content assertion, not a signing one — so
// an IdP that lets users pick an arbitrary, unverified email lets an
// attacker set the victim's (e.g. an admin's) address and, via email-based
// account linking, be issued that account's session. An absent claim is
// tolerated (many conformant IdPs omit it) so legitimate logins still work.
func TestFetchOIDCUserInfoRejectsUnverifiedEmail(t *testing.T) {
	mgr, _ := newTestSSOManager(t)
	makeToken := func(claims map[string]any) *oauth2.Token {
		payload, err := json.Marshal(claims)
		if err != nil {
			t.Fatalf("marshal claims: %v", err)
		}
		return (&oauth2.Token{}).WithExtra(map[string]any{
			"id_token": "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig",
		})
	}

	// Explicit boolean false → reject (account-hijack primitive).
	if _, err := mgr.fetchOIDCUserInfo(makeToken(map[string]any{
		"email":          "admin@example.com",
		"email_verified": false,
	})); err == nil {
		t.Fatal("expected rejection when email_verified=false, got nil error")
	}

	// String "false" — some IdPs emit the claim stringly → reject.
	if _, err := mgr.fetchOIDCUserInfo(makeToken(map[string]any{
		"email":          "admin@example.com",
		"email_verified": "false",
	})); err == nil {
		t.Fatal("expected rejection when email_verified=\"false\", got nil error")
	}

	// Absent claim → tolerated so IdPs that omit it keep working.
	if _, err := mgr.fetchOIDCUserInfo(makeToken(map[string]any{
		"email": "user@example.com",
	})); err != nil {
		t.Fatalf("absent email_verified must be tolerated, got %v", err)
	}

	// email_verified=true → accepted, identity mapped as usual.
	info, err := mgr.fetchOIDCUserInfo(makeToken(map[string]any{
		"email":              "user@example.com",
		"preferred_username": "user",
		"email_verified":     true,
	}))
	if err != nil {
		t.Fatalf("verified email must be accepted, got %v", err)
	}
	if info.Email != "user@example.com" || info.Username != "user" {
		t.Fatalf("unexpected identity: %#v", info)
	}
}
