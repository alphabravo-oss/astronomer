package handler

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gcpServiceAccountJSON builds a service_account_json blob with a freshly
// generated RSA key and a token_uri pointing at the given URL.
func gcpServiceAccountJSON(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen rsa key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	sa := map[string]string{
		"type":         "service_account",
		"client_email": "svc@my-project.iam.gserviceaccount.com",
		"private_key":  string(pemBytes),
		"token_uri":    tokenURI,
		"project_id":   "my-project",
	}
	b, _ := json.Marshal(sa)
	return string(b)
}

// TestGCP_ValidKeyExchangeReturnsOK proves the endpoint now does a real
// token exchange and reports OK only when the token endpoint returns an
// access_token.
func TestGCP_ValidKeyExchangeReturnsOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.FormValue("assertion") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	tester := NewDefaultCloudTester()
	res, err := tester.TestGCP(context.Background(), map[string]string{
		"service_account_json": gcpServiceAccountJSON(t, srv.URL),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK=true on successful token exchange, got %+v", res)
	}
}

// TestGCP_RevokedKeyReturnsNotOK is the core regression: a syntactically
// valid service-account JSON whose key the token endpoint rejects
// (invalid_grant, as a revoked/rotated key produces) must report OK=false.
// Before the fix, parseable JSON alone returned OK=true.
func TestGCP_RevokedKeyReturnsNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
	}))
	defer srv.Close()

	tester := NewDefaultCloudTester()
	res, err := tester.TestGCP(context.Background(), map[string]string{
		"service_account_json": gcpServiceAccountJSON(t, srv.URL),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected OK=false for a rejected key, got %+v", res)
	}
	if !strings.Contains(res.Message, "invalid_grant") {
		t.Fatalf("expected message to surface the token error, got %q", res.Message)
	}
}

// TestGCP_UnparseablePrivateKeyReturnsNotOK proves the endpoint no longer
// greenlights valid-JSON-but-broken-key blobs: a service_account_json with a
// bogus private_key can't be signed, so OK must be false and no OK=true stub
// answer leaks through.
func TestGCP_UnparseablePrivateKeyReturnsNotOK(t *testing.T) {
	blob := `{"type":"service_account","client_email":"svc@x.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----\n","token_uri":"https://oauth2.googleapis.com/token","project_id":"x"}`
	tester := NewDefaultCloudTester()
	res, err := tester.TestGCP(context.Background(), map[string]string{"service_account_json": blob})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected OK=false when the private_key cannot be signed with, got %+v", res)
	}
}
