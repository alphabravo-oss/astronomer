package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// helper to create an SSOManager with a real Encryptor for tests.
func newTestSSOManager(t *testing.T) (*SSOManager, *Encryptor) {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	jwt := NewJWTManager("test-secret", 60)
	mgr := NewSSOManager(enc, jwt, "https://app.example.com")
	return mgr, enc
}

func TestSSORegisterProviderSuccess(t *testing.T) {
	mgr, enc := newTestSSOManager(t)

	secret, err := enc.Encrypt("my-github-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	err = mgr.RegisterProvider("github", "client-id-123", secret, "", nil)
	if err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	mgr.mu.RLock()
	p, ok := mgr.providers["github"]
	mgr.mu.RUnlock()

	if !ok {
		t.Fatal("expected provider 'github' to be registered")
	}
	if p.ClientID != "client-id-123" {
		t.Errorf("ClientID = %q, want %q", p.ClientID, "client-id-123")
	}
	if p.ClientSecret != "my-github-secret" {
		t.Errorf("ClientSecret = %q, want %q", p.ClientSecret, "my-github-secret")
	}
	if p.Config == nil {
		t.Fatal("expected Config to be non-nil")
	}
	// Default scopes for GitHub
	if len(p.Scopes) != 2 || p.Scopes[0] != "user:email" {
		t.Errorf("Scopes = %v, want [user:email read:org]", p.Scopes)
	}
	// Default redirect URL
	want := "https://app.example.com/auth/callback/github"
	if p.RedirectURL != want {
		t.Errorf("RedirectURL = %q, want %q", p.RedirectURL, want)
	}
}

func TestSSORegisterProviderWithEncryptedSecret(t *testing.T) {
	mgr, enc := newTestSSOManager(t)

	plainSecret := "super-secret-google-client"
	encrypted, err := enc.Encrypt(plainSecret)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	err = mgr.RegisterProvider("google", "google-client-id", encrypted, "https://custom.example.com/callback", []string{"profile", "email"})
	if err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	mgr.mu.RLock()
	p := mgr.providers["google"]
	mgr.mu.RUnlock()

	if p.ClientSecret != plainSecret {
		t.Errorf("ClientSecret = %q, want %q", p.ClientSecret, plainSecret)
	}
	if p.RedirectURL != "https://custom.example.com/callback" {
		t.Errorf("RedirectURL = %q, want custom callback URL", p.RedirectURL)
	}
}

func TestSSORegisterProviderInvalidSecret(t *testing.T) {
	mgr, _ := newTestSSOManager(t)

	err := mgr.RegisterProvider("github", "client-id", "not-a-valid-fernet-token", "", nil)
	if err == nil {
		t.Fatal("expected error for invalid encrypted secret, got nil")
	}
}

func TestSSORegisterProviderEmptyName(t *testing.T) {
	mgr, enc := newTestSSOManager(t)
	secret, _ := enc.Encrypt("secret")

	err := mgr.RegisterProvider("", "client-id", secret, "", nil)
	if err == nil {
		t.Fatal("expected error for empty provider name, got nil")
	}
}

func TestSSORegisterProviderEmptyClientID(t *testing.T) {
	mgr, enc := newTestSSOManager(t)
	secret, _ := enc.Encrypt("secret")

	err := mgr.RegisterProvider("github", "", secret, "", nil)
	if err == nil {
		t.Fatal("expected error for empty client ID, got nil")
	}
}

func TestSSOGetAuthorizationURL(t *testing.T) {
	mgr, enc := newTestSSOManager(t)

	secret, _ := enc.Encrypt("secret")
	if err := mgr.RegisterProvider("github", "client-id", secret, "", nil); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	url, state, err := mgr.GetAuthorizationURL("github")
	if err != nil {
		t.Fatalf("GetAuthorizationURL: %v", err)
	}

	if state == "" {
		t.Error("expected non-empty state")
	}

	if !strings.Contains(url, "client_id=client-id") {
		t.Errorf("URL missing client_id: %s", url)
	}
	if !strings.Contains(url, "state=") {
		t.Errorf("URL missing state parameter: %s", url)
	}
	if !strings.Contains(url, "redirect_uri=") {
		t.Errorf("URL missing redirect_uri: %s", url)
	}
}

func TestSSOGetAuthorizationURLUnknownProvider(t *testing.T) {
	mgr, _ := newTestSSOManager(t)

	_, _, err := mgr.GetAuthorizationURL("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error = %q, want it to mention 'unknown provider'", err.Error())
	}
}

func TestSSOStateUniqueness(t *testing.T) {
	mgr, enc := newTestSSOManager(t)

	secret, _ := enc.Encrypt("secret")
	if err := mgr.RegisterProvider("github", "client-id", secret, "", nil); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		_, state, err := mgr.GetAuthorizationURL("github")
		if err != nil {
			t.Fatalf("GetAuthorizationURL iteration %d: %v", i, err)
		}
		if seen[state] {
			t.Fatalf("duplicate state generated at iteration %d: %s", i, state)
		}
		seen[state] = true
	}
}

func TestSSOSplitName(t *testing.T) {
	tests := []struct {
		name      string
		wantFirst string
		wantLast  string
	}{
		{"", "", ""},
		{"Alice", "Alice", ""},
		{"Alice Smith", "Alice", "Smith"},
		{"Alice van Smith", "Alice", "van Smith"},
	}
	for _, tt := range tests {
		first, last := splitName(tt.name)
		if first != tt.wantFirst || last != tt.wantLast {
			t.Errorf("splitName(%q) = (%q, %q), want (%q, %q)", tt.name, first, last, tt.wantFirst, tt.wantLast)
		}
	}
}

func TestSSODefaultScopes(t *testing.T) {
	gh := defaultScopes("github")
	if len(gh) != 2 || gh[0] != "user:email" {
		t.Errorf("GitHub default scopes = %v", gh)
	}

	gl := defaultScopes("google")
	if len(gl) != 2 || gl[0] != "profile" {
		t.Errorf("Google default scopes = %v", gl)
	}

	oidc := defaultScopes("oidc")
	if len(oidc) != 3 || oidc[0] != "openid" {
		t.Errorf("OIDC default scopes = %v", oidc)
	}
}

func TestSSORegisterProviderUnknown(t *testing.T) {
	mgr, enc := newTestSSOManager(t)
	secret, _ := enc.Encrypt("secret")

	err := mgr.RegisterProvider("bitbucket", "client-id", secret, "", nil)
	if err == nil {
		t.Fatal("expected error for unknown provider type, got nil")
	}
}

func TestFetchOIDCUserInfoFromIDToken(t *testing.T) {
	mgr, _ := newTestSSOManager(t)
	claims := map[string]any{
		"email":              "alice@example.com",
		"preferred_username": "alice",
		"given_name":         "Alice",
		"family_name":        "Smith",
		"picture":            "https://example.com/avatar.png",
		"groups":             []string{"devops", "platform"},
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal claims: %v", err)
	}
	token := &oauth2.Token{}
	token = token.WithExtra(map[string]any{
		"id_token": "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig",
	})

	info, err := mgr.fetchOIDCUserInfo(token)
	if err != nil {
		t.Fatalf("fetchOIDCUserInfo: %v", err)
	}
	if info.Provider != "oidc" {
		t.Fatalf("Provider = %q, want oidc", info.Provider)
	}
	if info.Email != "alice@example.com" || info.Username != "alice" {
		t.Fatalf("unexpected OIDC identity: %#v", info)
	}
	if len(info.Groups) != 2 || info.Groups[0] != "devops" {
		t.Fatalf("unexpected OIDC groups: %#v", info.Groups)
	}
}
