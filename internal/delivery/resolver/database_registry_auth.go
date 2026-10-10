package resolver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"
)

// DatabaseRegistryAuthentication is constructor authority, never artifact data.
// Empty permits only anonymous same-registry HTTPS bearer authentication. A
// separate realm and private username/password require an explicit exact realm
// and service; the supplied canonical client's destination policy still applies.
// Credentials are never included in returned errors or public snapshots.
type DatabaseRegistryAuthentication struct {
	RealmURL string
	Service  string
	Username string
	Password string
}

// NewDatabaseRegistryClient keeps the caller's guarded transport and redirect
// policy beneath ORAS's challenge/token machinery. A constructor-owned cache is
// scoped to this repository only; there is no ambient/global credential cache.
func NewDatabaseRegistryClient(client *http.Client, repositoryName string, policy DatabaseRegistryAuthentication) (*http.Client, error) {
	return newDatabaseRegistryClient(client, repositoryName, policy, false)
}

// NewDatabaseRegistryPublicationClient is protected operator-constructor
// composition only. Runtime loaders, signed feeds and HTTP requests cannot
// select this constructor. It expands only the same exact repository to OCI
// push, retaining the identical realm/service/credential and destination guards.
func NewDatabaseRegistryPublicationClient(client *http.Client, repositoryName string, policy DatabaseRegistryAuthentication) (*http.Client, error) {
	return newDatabaseRegistryClient(client, repositoryName, policy, true)
}

func newDatabaseRegistryClient(client *http.Client, repositoryName string, policy DatabaseRegistryAuthentication, publication bool) (*http.Client, error) {
	repository, err := remote.NewRepository(repositoryName)
	if client == nil || client.Transport == nil || err != nil || repository.Reference.Reference != "" || !validDatabaseRegistryAuthentication(policy) {
		return nil, errors.New("database registry authentication constructor invalid")
	}
	host := repository.Reference.Registry
	guard := &databaseAuthenticationTransport{base: client.Transport, host: host, repository: repository.Reference.Repository, policy: policy, publication: publication}
	guarded := *client
	guarded.Transport = guard
	guarded.Jar = nil
	// Each registry/token request is finite independently of the whole transfer.
	maximumRequest := time.Minute
	if publication {
		maximumRequest = 15 * time.Minute
		guarded.Timeout = maximumRequest
	}
	if guarded.Timeout <= 0 || guarded.Timeout > maximumRequest {
		guarded.Timeout = maximumRequest
	}
	originalRedirect := guarded.CheckRedirect
	guarded.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) == 0 || len(via) > 2 || guard.isTokenRequest(via[0]) {
			return errors.New("database registry authentication redirect denied")
		}
		if originalRedirect == nil {
			return errors.New("database registry redirect policy missing")
		}
		return originalRedirect(request, via)
	}
	authentication := &remoteauth.Client{Client: &guarded, Cache: remoteauth.NewCache(), Credential: remoteauth.StaticCredential(host, remoteauth.Credential{Username: policy.Username, Password: policy.Password})}
	outer := *client
	outer.Jar = nil
	outer.Transport = &databaseAuthenticatedTransport{authentication: authentication, guarded: &guarded, host: host, repository: repository.Reference.Repository, publication: publication}
	// Redirects are resolved only by the guarded inner client; no second redirect
	// pass may interpret an already checked response.
	outer.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("database registry outer redirect denied")
	}
	return &outer, nil
}

func validDatabaseRegistryAuthentication(policy DatabaseRegistryAuthentication) bool {
	if (policy.Username == "") != (policy.Password == "") || strings.Contains(policy.Username, ":") || len(policy.Username) > 4096 || len(policy.Password) > 16384 {
		return false
	}
	if policy.RealmURL == "" {
		return policy.Service == "" && policy.Username == "" && policy.Password == ""
	}
	realm, err := url.Parse(policy.RealmURL)
	return err == nil && validDatabaseRealm(realm) && realm.String() == policy.RealmURL && policy.Service != "" && len(policy.Service) <= 256 && !strings.ContainsAny(policy.Service, "\r\n\t ,\"\\")
}

func validDatabaseRealm(realm *url.URL) bool {
	return realm != nil && realm.Scheme == "https" && realm.Host != "" && realm.User == nil && realm.Opaque == "" && realm.RawQuery == "" && !realm.ForceQuery && realm.Fragment == "" && realm.RawFragment == "" && realm.Path != "" && realm.RawPath == ""
}

type databaseAuthenticatedTransport struct {
	authentication *remoteauth.Client
	guarded        *http.Client
	host           string
	repository     string
	publication    bool
}

func (transport *databaseAuthenticatedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	if request.URL.Scheme != "https" || request.URL.Host != transport.host || request.URL.User != nil || !strings.HasPrefix(request.URL.Path, "/v2/"+transport.repository+"/") || !validDatabaseRegistryRequest(request, transport.host, transport.repository, transport.publication) || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
		return nil, errors.New("database registry request outside constructor scope")
	}
	// Replace, rather than append, caller scope hints: upstream ORAS merges hints
	// with challenges. Only this constructor's exact repository scope is authorized.
	ctx := remoteauth.WithScopes(request.Context())
	ctx = remoteauth.WithScopesForHost(ctx, transport.host, databaseRegistryScope(transport.repository, transport.publication))
	response, err := transport.authentication.Do(request.Clone(ctx))
	if err != nil {
		return nil, databaseAuthenticationError(request.Context(), err)
	}
	return response, nil
}

func (transport *databaseAuthenticatedTransport) CloseIdleConnections() {
	transport.guarded.CloseIdleConnections()
}

type databaseAuthenticationTransport struct {
	base        http.RoundTripper
	host        string
	repository  string
	policy      DatabaseRegistryAuthentication
	publication bool
}

func (transport *databaseAuthenticationTransport) isTokenRequest(request *http.Request) bool {
	return request.URL.Query().Has("scope") || request.URL.Query().Has("service")
}

func (transport *databaseAuthenticationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if transport.isTokenRequest(request) {
		if !transport.validTokenRequest(request) {
			return nil, errors.New("database registry token request denied")
		}
	} else if request.Header.Get("Authorization") != "" && (request.URL.Scheme != "https" || request.URL.Host != transport.host || !strings.HasPrefix(request.URL.Path, "/v2/"+transport.repository+"/") || !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ")) {
		return nil, errors.New("database registry credential destination denied")
	}
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if transport.isTokenRequest(request) {
		if err := boundDatabaseTokenResponse(response); err != nil {
			return nil, err
		}
	}
	if response.StatusCode == http.StatusUnauthorized {
		if transport.isTokenRequest(request) || request.URL.Scheme != "https" || request.URL.Host != transport.host || !transport.validChallenge(response.Header) {
			_ = response.Body.Close()
			return nil, errors.New("database registry challenge outside constructor authority")
		}
	}
	return response, nil
}

func (transport *databaseAuthenticationTransport) CloseIdleConnections() {
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// This deliberately supports the selected registry's closed bearer challenge,
// not arbitrary auth schemes, escapes, multi-challenges or scope expansion.
var databaseBearerToken = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

var databaseBearerParameter = regexp.MustCompile(`^([a-z_]+)="([^"\\\r\n]*)"$`)

func (transport *databaseAuthenticationTransport) validChallenge(header http.Header) bool {
	challenges := header.Values("Www-Authenticate")
	if len(challenges) != 1 || len(challenges[0]) > 8192 || !strings.HasPrefix(challenges[0], "Bearer ") {
		return false
	}
	parameters := make(map[string]string, 3)
	parts := strings.Split(strings.TrimPrefix(challenges[0], "Bearer "), ",")
	if transport.publication {
		parts = databasePublicationBearerParts(strings.TrimPrefix(challenges[0], "Bearer "))
	}
	for _, raw := range parts {
		match := databaseBearerParameter.FindStringSubmatch(strings.TrimSpace(raw))
		if len(match) != 3 || (match[1] != "realm" && match[1] != "service" && match[1] != "scope") {
			return false
		}
		if _, exists := parameters[match[1]]; exists {
			return false
		}
		parameters[match[1]] = match[2]
	}
	if len(parameters) != 3 || parameters["scope"] != databaseRegistryScope(transport.repository, transport.publication) && (!transport.publication || parameters["scope"] != remoteauth.ScopeRepository(transport.repository, remoteauth.ActionPull)) {
		return false
	}
	realm, err := url.Parse(parameters["realm"])
	if err != nil || !validDatabaseRealm(realm) {
		return false
	}
	return transport.allowedRealm(realm, parameters["service"])
}

func (transport *databaseAuthenticationTransport) allowedRealm(realm *url.URL, service string) bool {
	if transport.policy.RealmURL != "" {
		return realm.String() == transport.policy.RealmURL && service == transport.policy.Service
	}
	return realm.Host == transport.host && service == transport.host
}

func (transport *databaseAuthenticationTransport) validTokenRequest(request *http.Request) bool {
	if request.Method != http.MethodGet || request.URL.User != nil || request.Header.Get("Cookie") != "" || request.Header.Get("Proxy-Authorization") != "" {
		return false
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(query) != 2 || len(query["scope"]) != 1 || len(query["service"]) != 1 || query.Get("scope") != databaseRegistryScope(transport.repository, transport.publication) {
		return false
	}
	realm := *request.URL
	realm.RawQuery = ""
	if !validDatabaseRealm(&realm) || !transport.allowedRealm(&realm, query.Get("service")) {
		return false
	}
	username, password, basic := request.BasicAuth()
	if transport.policy.Username == "" {
		return request.Header.Get("Authorization") == ""
	}
	return basic && username == transport.policy.Username && password == transport.policy.Password
}

func databaseAuthenticationError(ctx context.Context, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(cause, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New("database registry authenticated request unavailable")
}

// ORAS owns challenge mechanics; this wrapper closes the selected token JSON
// response and limits both bytes and credential header size before ORAS reads it.
func boundDatabaseTokenResponse(response *http.Response) error {
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return errors.New("database registry token response unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	_ = response.Body.Close()
	var token struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in,omitempty"`
		IssuedAt    string `json:"issued_at,omitempty"`
	}
	if err != nil || len(data) > 128<<10 || jsoncontract.Decode(data, &token) != nil || (token.Token == "" && token.AccessToken == "") || len(token.Token) > 16384 || len(token.AccessToken) > 16384 || (token.Token != "" && !databaseBearerToken.MatchString(token.Token)) || (token.AccessToken != "" && !databaseBearerToken.MatchString(token.AccessToken)) || (token.Token != "" && token.AccessToken != "" && token.Token != token.AccessToken) || token.ExpiresIn < 0 {
		return errors.New("database registry token response invalid")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	return nil
}
