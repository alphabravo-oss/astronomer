package catalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// stubCryptor is a deterministic stand-in for the Fernet encryptor: it is
// enough to prove the column-boundary contract without pulling internal/auth
// into this package's tests.
type stubCryptor struct {
	prefix  string
	failing bool
}

func (s stubCryptor) Encrypt(plaintext string) (string, error) {
	return s.prefix + plaintext, nil
}

func (s stubCryptor) Decrypt(token string) (string, error) {
	if s.failing || !strings.HasPrefix(token, s.prefix) {
		return "", errors.New("no matching key")
	}
	return strings.TrimPrefix(token, s.prefix), nil
}

// TestSealAuthConfigSplitsSecretsFromTheQueryableProjection.
func TestSealAuthConfigSplitsSecretsFromTheQueryableProjection(t *testing.T) {
	enc := stubCryptor{prefix: "SEALED:"}
	doc := `{"username":"u","password":"s3cret","charts":["app"],"allow_catalog":true}`

	ciphertext, public, err := SealAuthConfig(json.RawMessage(doc), enc)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if ciphertext == "" {
		t.Fatal("a document with a password must produce an envelope")
	}
	if strings.Contains(string(public), "s3cret") {
		t.Fatalf("secret survived in the plaintext projection: %s", public)
	}
	var m map[string]any
	if err := json.Unmarshal(public, &m); err != nil {
		t.Fatalf("projection is not JSON: %v", err)
	}
	if _, present := m["password"]; present {
		// Removed, not blanked: a blank "password" would be indistinguishable
		// from a real empty credential and ApplyIndexAuth would send it.
		t.Fatalf("password key must be removed, not blanked: %s", public)
	}
	for _, keep := range []string{"username", "charts", "allow_catalog"} {
		if _, ok := m[keep]; !ok {
			t.Fatalf("non-secret key %q was stripped: %s", keep, public)
		}
	}
}

// TestSealAuthConfigLeavesSecretlessDocumentsAlone — an OCI repo with only a
// chart list has nothing to protect. Sealing it anyway would move the chart
// list out of the catalog API's reach and make the repository list depend on a
// successful decrypt for no security gain.
func TestSealAuthConfigLeavesSecretlessDocumentsAlone(t *testing.T) {
	ciphertext, public, err := SealAuthConfig(json.RawMessage(`{"charts":["app"]}`), stubCryptor{prefix: "SEALED:"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if ciphertext != "" {
		t.Fatalf("secretless document should not be sealed, got %q", ciphertext)
	}
	if !strings.Contains(string(public), "charts") {
		t.Fatalf("chart list lost: %s", public)
	}
}

// TestSealAuthConfigWithoutEncryptorKeepsPre145Shape — development deployments
// have no Fernet key (production cannot: ValidateProductionSecurity refuses to
// start). The row must come out in exactly the shape the legacy read branch
// expects, or dev would write rows neither branch can read.
func TestSealAuthConfigWithoutEncryptorKeepsPre145Shape(t *testing.T) {
	doc := `{"username":"u","password":"p"}`
	ciphertext, public, err := SealAuthConfig(json.RawMessage(doc), nil)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if ciphertext != "" {
		t.Fatalf("no encryptor must produce no envelope, got %q", ciphertext)
	}
	if string(public) != doc {
		t.Fatalf("document should pass through unchanged, got %s", public)
	}
}

// TestResolveAuthConfigDisambiguatesOnTheColumnNotTheBytes is the core of the
// transition design. There is no "does this look like a Fernet token"
// heuristic: an empty envelope means the JSONB is the whole document, a
// non-empty one means decrypt. A plaintext document that happens to look like
// ciphertext (or vice versa) cannot confuse it.
func TestResolveAuthConfigDisambiguatesOnTheColumnNotTheBytes(t *testing.T) {
	enc := stubCryptor{prefix: "SEALED:"}

	legacy := sqlc.HelmRepository{
		Name:       "pre-145",
		AuthConfig: json.RawMessage(`{"username":"u","password":"p"}`),
		// AuthConfigEncrypted empty.
	}
	got, err := ResolveAuthConfig(legacy, enc)
	if err != nil {
		t.Fatalf("legacy row must resolve: %v", err)
	}
	if string(got) != `{"username":"u","password":"p"}` {
		t.Fatalf("legacy row resolved to %s", got)
	}

	ciphertext, public, _ := SealAuthConfig(json.RawMessage(`{"username":"u","password":"p"}`), enc)
	sealed := sqlc.HelmRepository{Name: "post-145", AuthConfig: public, AuthConfigEncrypted: ciphertext}
	got, err = ResolveAuthConfig(sealed, enc)
	if err != nil {
		t.Fatalf("sealed row must resolve: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(got, &m)
	if m["password"] != "p" {
		t.Fatalf("sealed row resolved without its password: %s", got)
	}
}

// TestResolveAuthConfigNeverReturnsCiphertextOnFailure is the anti-regression
// for decryptGitAuth's shape (internal/worker/tasks/gitops_sync.go), which
// returns the stored blob when Decrypt fails.
func TestResolveAuthConfigNeverReturnsCiphertextOnFailure(t *testing.T) {
	sealed := sqlc.HelmRepository{
		Name:                "private",
		AuthConfig:          json.RawMessage(`{"username":"u"}`),
		AuthConfigEncrypted: "SEALED:{\"password\":\"p\"}",
	}
	for name, dec := range map[string]Decryptor{
		"wrong key":  stubCryptor{prefix: "OTHER:"},
		"no key":     nil,
		"broken key": stubCryptor{prefix: "SEALED:", failing: true},
	} {
		got, err := ResolveAuthConfig(sealed, dec)
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if !errors.Is(err, ErrAuthConfigUnavailable) {
			t.Fatalf("%s: error must wrap ErrAuthConfigUnavailable, got %v", name, err)
		}
		if got != nil {
			t.Fatalf("%s: a failed resolve must return no document, got %s", name, got)
		}
		if strings.Contains(string(got), sealed.AuthConfigEncrypted) {
			t.Fatalf("%s: ciphertext returned as a credential", name)
		}
	}
}

// TestApplyIndexAuthDropsCredentialsItCannotDecrypt — the outbound request
// must carry no Authorization header at all. Sending the username with an
// empty password (the shape you get from naively reading the stripped
// projection) would be an authentication attempt with a wrong credential,
// which upstream reports as a 401 and the operator reads as a rejected
// password rather than a key problem.
func TestApplyIndexAuthDropsCredentialsItCannotDecrypt(t *testing.T) {
	sealed := sqlc.HelmRepository{
		Name: "private", AuthType: "basic",
		AuthConfig:          json.RawMessage(`{"username":"u"}`),
		AuthConfigEncrypted: "SEALED:{\"username\":\"u\",\"password\":\"p\"}",
	}
	req := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	ApplyIndexAuth(req, sealed, stubCryptor{prefix: "OTHER:"}, nil)

	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("expected no Authorization header, got %q", got)
	}
	if _, _, ok := req.BasicAuth(); ok {
		t.Fatal("a username-only basic credential was sent from the stripped projection")
	}
}

// TestSameHostGatesCredentialsToTheRepositoryItself pins the rule that decides
// whether a chart-ASSET fetch carries the operator's credential.
//
// The URLs come from the repository's own index.yaml and are attacker-chosen
// in the case that matters: httpclient.GuardPublicHost blocks loopback,
// RFC-1918 and the metadata endpoint, but a third party on the public internet
// is none of those. Both readers of this rule — the scheduled sweep's
// fetchChartAssets and the handler's fetchHTTPChartArchive — call this
// function, so a regression here leaks an Artifactory password or a registry
// bearer token to whatever host an index entry names.
func TestSameHostGatesCredentialsToTheRepositoryItself(t *testing.T) {
	const repo = "https://charts.example.com/stable"

	for _, tc := range []struct {
		name  string
		chart string
		want  bool
	}{
		{"same host, deeper path", "https://charts.example.com/stable/app-1.0.0.tgz", true},
		{"same host, different path", "https://charts.example.com/downloads/app.tgz", true},
		{"case-insensitive host", "https://CHARTS.EXAMPLE.COM/app.tgz", true},
		{"third-party host", "https://attacker.example/chart.tgz", false},
		{"CDN", "https://cdn.jsdelivr.net/charts/app.tgz", false},
		{"github release", "https://github.com/o/r/releases/download/v1/app.tgz", false},
		{"sibling subdomain", "https://evil.example.com/app.tgz", false},
		{"suffix-extended host", "https://charts.example.com.evil.test/app.tgz", false},
		{"downgraded scheme", "http://charts.example.com/app.tgz", false},
		{"explicit port differs", "https://charts.example.com:8443/app.tgz", false},
		{"unparseable chart URL", "https://charts.example.com/\x7f\x00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameHost(repo, tc.chart); got != tc.want {
				t.Fatalf("SameHost(%q, %q) = %v, want %v", repo, tc.chart, got, tc.want)
			}
		})
	}
}
