// Cloud-credentials provider-validity test endpoint (migration 053).
//
// The /test/ endpoint dials each provider's "am I valid?" SDK call:
//   - AWS:    STS GetCallerIdentity
//   - GCP:    parse the service-account JSON + mint an ID token
//   - Azure:  client-credentials grant against the tenant's OAuth endpoint
//
// We avoid pulling in the full cloud-provider SDKs at this layer — they
// each add tens of MB of dependencies for one HTTP call. Instead, each
// provider gets a tiny HTTP-based implementation that uses the
// well-documented public endpoint shapes. A future migration can swap
// these for the SDK-native equivalents without changing the CloudTester
// interface (the handler is provider-agnostic by design).
//
// All three implementations run inside a 10s timeout — the operator is
// watching a "Test" button and a slow upstream shouldn't block the UI.

package handler

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// defaultCloudTesterTimeout is the wall-clock budget for any single
// provider call. Hard-coded rather than per-handler-tunable because
// "operator clicks Test → result in <10s" is the UX contract.
const defaultCloudTesterTimeout = 10 * time.Second

// DefaultCloudTester is the production implementation of CloudTester.
// Constructed once at server startup (or once per request — it's
// stateless) and wired via CloudCredentialHandler.SetTester.
type DefaultCloudTester struct {
	HTTPClient *http.Client
}

// NewDefaultCloudTester builds a tester with a shared http.Client
// configured for the 10s budget.
func NewDefaultCloudTester() *DefaultCloudTester {
	return &DefaultCloudTester{
		HTTPClient: &http.Client{Timeout: defaultCloudTesterTimeout},
	}
}

// TestAWS exercises sts.GetCallerIdentity via the v4-signed query API.
// A 200 response (with valid XML body) confirms the access key + secret
// pair is recognised by AWS and not disabled.
//
// We use SigV4 inline because importing the full aws-sdk-go-v2 chain
// for one signed GET would double the binary size. The signing routine
// here is the canonical "Query API" path documented in the AWS Signing
// Process Guide.
func (t *DefaultCloudTester) TestAWS(ctx context.Context, blob map[string]string) (CloudTestResult, error) {
	accessKey := strings.TrimSpace(blob["access_key_id"])
	secretKey := strings.TrimSpace(blob["secret_access_key"])
	if accessKey == "" || secretKey == "" {
		return CloudTestResult{OK: false, Message: "access_key_id and secret_access_key are required"}, nil
	}
	region := strings.TrimSpace(blob["region"])
	if region == "" {
		// STS is one of the few services that has a global "us-east-1"
		// endpoint that accepts SigV4 signed with us-east-1 as the
		// region. We pick that as the default so an operator who hasn't
		// configured a region still gets a meaningful test.
		region = "us-east-1"
	}
	body, statusCode, err := signedGet(ctx, t.httpClient(), accessKey, secretKey, region, "sts",
		"https://sts.amazonaws.com/",
		url.Values{
			"Action":  []string{"GetCallerIdentity"},
			"Version": []string{"2011-06-15"},
		})
	if err != nil {
		return CloudTestResult{OK: false, Message: err.Error()}, nil
	}
	if statusCode != http.StatusOK {
		// AWS returns a structured XML error; surface the gist.
		return CloudTestResult{OK: false, Message: fmt.Sprintf("STS returned status %d: %s", statusCode, summariseAWSError(body))}, nil
	}
	// The canonical success body has an Arn element under
	// GetCallerIdentityResult. Extract it for a friendly OK message.
	type stsResult struct {
		XMLName xml.Name `xml:"GetCallerIdentityResponse"`
		Result  struct {
			Arn     string `xml:"Arn"`
			Account string `xml:"Account"`
			UserId  string `xml:"UserId"`
		} `xml:"GetCallerIdentityResult"`
	}
	var parsed stsResult
	if err := xml.Unmarshal(body, &parsed); err == nil && parsed.Result.Arn != "" {
		return CloudTestResult{OK: true, Message: fmt.Sprintf("authenticated as %s", parsed.Result.Arn)}, nil
	}
	// 200 but unrecognised body — still treat as success since the
	// signed request was accepted.
	return CloudTestResult{OK: true, Message: "STS GetCallerIdentity succeeded"}, nil
}

// TestGCP parses the service-account JSON, signs a JWT with the
// service-account's RSA private key, and exchanges it for an access
// token at oauth2.googleapis.com/token. A 200 response confirms the
// private key + client_email pair is recognised by Google.
//
// We avoid the oauth2/google library for the same dependency-size
// reason as AWS — the JWT shape is small and well-documented.
func (t *DefaultCloudTester) TestGCP(ctx context.Context, blob map[string]string) (CloudTestResult, error) {
	jsonBlob := strings.TrimSpace(blob["service_account_json"])
	if jsonBlob == "" {
		return CloudTestResult{OK: false, Message: "service_account_json is required"}, nil
	}
	var key struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
		ProjectID   string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(jsonBlob), &key); err != nil {
		return CloudTestResult{OK: false, Message: fmt.Sprintf("service_account_json is not valid JSON: %s", err.Error())}, nil
	}
	if key.Type != "service_account" {
		return CloudTestResult{OK: false, Message: fmt.Sprintf("expected service_account JSON, got type=%q", key.Type)}, nil
	}
	if key.ClientEmail == "" || key.PrivateKey == "" {
		return CloudTestResult{OK: false, Message: "service_account_json missing client_email or private_key"}, nil
	}
	tokenURI := strings.TrimSpace(key.TokenURI)
	if tokenURI == "" {
		tokenURI = "https://oauth2.googleapis.com/token"
	}
	// SSRF guard: token_uri comes from the operator-supplied service-account
	// JSON, so refuse to dial a loopback/internal/metadata address. Do not echo
	// the URI back to the caller.
	if err := httpclient.GuardPublicHost(tokenURI); err != nil {
		return CloudTestResult{OK: false, Message: "token_uri is not a permitted public address"}, nil
	}
	// Sign a real JWT assertion with the service-account's RSA private key
	// and exchange it at the token URI. Merely parsing the JSON is NOT
	// enough: a revoked/rotated key produces syntactically-valid JSON, and
	// reporting OK on that would greenlight a dead credential. Only a 200
	// with an access_token from the OAuth token endpoint proves the private
	// key + client_email pair is still recognised by Google.
	assertion, err := signGCPServiceAccountJWT(key.PrivateKey, key.ClientEmail, tokenURI)
	if err != nil {
		return CloudTestResult{OK: false, Message: fmt.Sprintf("failed to sign JWT with service-account private_key: %s", err.Error())}, nil
	}
	form := url.Values{
		"grant_type": []string{"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  []string{assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return CloudTestResult{OK: false, Message: err.Error()}, nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.httpClient().Do(req)
	if err != nil {
		return CloudTestResult{OK: false, Message: err.Error()}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return CloudTestResult{OK: false, Message: fmt.Sprintf("token endpoint returned status %d: %s", resp.StatusCode, summariseGCPError(body))}, nil
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.AccessToken == "" {
		return CloudTestResult{OK: false, Message: "token endpoint returned 200 but no access_token"}, nil
	}
	return CloudTestResult{OK: true, Message: fmt.Sprintf("acquired %s token for %s (expires in %ds)", parsed.TokenType, key.ClientEmail, parsed.ExpiresIn)}, nil
}

// signGCPServiceAccountJWT builds and RS256-signs the JWT-bearer assertion
// used in the OAuth 2.0 service-account flow: a JWT with iss=client_email,
// scope=cloud-platform, aud=tokenURI, iat/exp, signed with the SA's RSA
// private key. Uses stdlib crypto only (no oauth2/google dependency).
func signGCPServiceAccountJWT(privateKeyPEM, clientEmail, audience string) (string, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", fmt.Errorf("private_key is not valid PEM")
	}
	var rsaKey *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return "", fmt.Errorf("private_key is not an RSA key")
		}
		rsaKey = rk
	} else if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		rsaKey = k
	} else {
		return "", fmt.Errorf("private_key could not be parsed as PKCS#8 or PKCS#1")
	}
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims := map[string]any{
		"iss":   clientEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   audience,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := header + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// summariseGCPError extracts the "error_description" from a Google OAuth
// error body. Falls back to a snippet on parse failure.
func summariseGCPError(body []byte) string {
	var doc struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &doc); err == nil && doc.Error != "" {
		if doc.ErrorDescription != "" {
			return fmt.Sprintf("%s: %s", doc.Error, doc.ErrorDescription)
		}
		return doc.Error
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return string(body)
}

// TestAzure exchanges the client_id / client_secret for an access
// token via the tenant's OAuth v2 endpoint. A 200 with a valid
// access_token confirms the SP is recognised and the secret is
// current.
func (t *DefaultCloudTester) TestAzure(ctx context.Context, blob map[string]string) (CloudTestResult, error) {
	clientID := strings.TrimSpace(blob["client_id"])
	clientSecret := strings.TrimSpace(blob["client_secret"])
	tenantID := strings.TrimSpace(blob["tenant_id"])
	if clientID == "" || clientSecret == "" || tenantID == "" {
		return CloudTestResult{OK: false, Message: "client_id, client_secret, and tenant_id are required"}, nil
	}
	tokenURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", url.PathEscape(tenantID))
	form := url.Values{
		"grant_type":    []string{"client_credentials"},
		"client_id":     []string{clientID},
		"client_secret": []string{clientSecret},
		// management.azure.com is the canonical resource for cred-check
		// — any resource owned by the SP would work. Using the AAD
		// management plane minimises the chance of a "this SP isn't
		// scoped to anything" false negative.
		"scope": []string{"https://management.azure.com/.default"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return CloudTestResult{OK: false, Message: err.Error()}, nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.httpClient().Do(req)
	if err != nil {
		return CloudTestResult{OK: false, Message: err.Error()}, nil
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return CloudTestResult{OK: false, Message: fmt.Sprintf("AAD returned status %d: %s", resp.StatusCode, summariseAzureError(body))}, nil
	}
	var parsed struct {
		TokenType   string `json:"token_type"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.AccessToken == "" {
		return CloudTestResult{OK: false, Message: "AAD returned 200 but no access_token"}, nil
	}
	return CloudTestResult{OK: true, Message: fmt.Sprintf("acquired %s token (expires in %ds)", parsed.TokenType, parsed.ExpiresIn)}, nil
}

func (t *DefaultCloudTester) httpClient() *http.Client {
	if t.HTTPClient != nil {
		return t.HTTPClient
	}
	return &http.Client{Timeout: defaultCloudTesterTimeout}
}

// summariseAWSError extracts the <Message> element from an AWS XML
// error body. Falls back to a hex snippet so a really weird body is
// still readable in logs.
func summariseAWSError(body []byte) string {
	var doc struct {
		Error struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		} `xml:"Error"`
	}
	if err := xml.Unmarshal(body, &doc); err == nil && doc.Error.Code != "" {
		return fmt.Sprintf("%s: %s", doc.Error.Code, doc.Error.Message)
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return string(body)
}

// summariseAzureError extracts the "error_description" from an Azure
// OAuth error body. Falls back to a snippet on parse failure.
func summariseAzureError(body []byte) string {
	var doc struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &doc); err == nil && doc.Error != "" {
		return fmt.Sprintf("%s: %s", doc.Error, doc.ErrorDescription)
	}
	if len(body) > 200 {
		body = body[:200]
	}
	return string(body)
}

// --- AWS SigV4 -------------------------------------------------------
//
// The four-step Signature Version 4 process from the AWS Signing
// Process Guide:
//   1. Canonical request: METHOD\nPATH\nQUERY\nHEADERS\nSIGNED\nHASH
//   2. String to sign:    AWS4-HMAC-SHA256\nDATE\nSCOPE\nHASH(canon)
//   3. Signing key:       HMAC-SHA256-derived from secret + scope
//   4. Signature:         HMAC-SHA256(signing_key, string_to_sign)
//
// We use the query-string flavour (Authorization header carries the
// signature, body is empty) which is the natural fit for STS
// GetCallerIdentity.

func signedGet(ctx context.Context, client *http.Client, accessKey, secretKey, region, service, endpoint string, query url.Values) ([]byte, int, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, 0, fmt.Errorf("parse endpoint: %w", err)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	host := req.URL.Host
	req.Header.Set("Host", host)
	req.Header.Set("X-Amz-Date", amzDate)

	// 1. Canonical request. The helpers from backups.go (hashSHA256 +
	// hmacSHA256) are reused so the AWS SigV4 routine has exactly one
	// implementation in the package.
	signedHeaders := "host;x-amz-date"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-date:%s\n", host, amzDate)
	payloadHash := hashSHA256("")
	path := req.URL.Path
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{
		http.MethodGet,
		path,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	// 2. String to sign.
	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashSHA256(canonical),
	}, "\n")
	// 3. Signing key.
	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	// 4. Signature.
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))
	authorization := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authorization)

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}
