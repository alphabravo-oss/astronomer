package resolver

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// Install is constructor-only after the canonical TLS/destination client exists.
// Each exact repository owns its own ORAS token cache. Push credentials and
// ambient Docker/GitHub state are never consulted.
func (transport *DatabaseRegistryTransport) captureReadClients(ctx context.Context, options *DatabaseRegistryReadOptions) error {
	if options == nil {
		return nil
	}
	credential, err := readDatabaseRegistryCredential(ctx, transport.mounted, options.CredentialRef)
	if err != nil {
		return err
	}
	transport.readOptions = freezeDatabaseRegistryReadOptions(options)
	transport.readCredential = credential
	transport.readClients = make(map[string]*http.Client, len(options.Repositories))
	base := *transport.client
	base.Jar = nil
	base.Transport = databaseRegistryPhysicalRoundTripper{owner: transport}
	for _, repository := range transport.readOptions.Repositories {
		client, err := NewDatabaseRegistryClient(&base, repository, DatabaseRegistryAuthentication{RealmURL: options.RealmURL, Service: options.Service, Username: credential.username, Password: credential.password})
		if err != nil {
			return errors.New("database registry read client unavailable")
		}
		transport.readClients[strings.TrimPrefix(repository, options.Registry+"/")] = client
	}
	return nil
}

func (transport *DatabaseRegistryTransport) revalidateReadCredential(ctx context.Context) error {
	if transport.readOptions == nil {
		return nil
	}
	current, err := readDatabaseRegistryCredential(ctx, transport.mounted, transport.readOptions.CredentialRef)
	if err != nil {
		return err
	}
	defer func() { current.username, current.password = "", "" }()
	if transport.readCredential == nil || current.material != transport.readCredential.material {
		return errors.New("database registry mounted read credential changed")
	}
	return nil
}

func (transport *DatabaseRegistryTransport) readClientFor(request *http.Request) (*http.Client, error) {
	if transport.readOptions == nil {
		return nil, nil
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return nil, errors.New("database registry loader transport is pull-only")
	}
	if request.Header.Get("Authorization") != "" || request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Cookie") != "" {
		return nil, errors.New("database registry caller credentials denied")
	}
	if request.URL.Host != transport.readOptions.Registry {
		return nil, nil // Other explicitly approved origins stay anonymous.
	}
	if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.RawPath != "" || request.URL.Opaque != "" || request.URL.Fragment != "" || request.URL.ForceQuery {
		return nil, errors.New("database registry read request invalid")
	}
	if !strings.HasPrefix(request.URL.Path, "/v2/") {
		return nil, errors.New("database registry read repository not approved")
	}
	path := strings.TrimPrefix(request.URL.Path, "/v2/")
	// OCI read operations delimit the exact repository at manifests or blobs.
	// A prefix match would grant a sibling beneath an approved parent name.
	for _, marker := range []string{"/manifests/", "/blobs/"} {
		if index := strings.LastIndex(path, marker); index > 0 {
			if client := transport.readClients[path[:index]]; client != nil {
				return client, nil
			}
		}
	}
	return nil, errors.New("database registry read repository not approved")
}

// This inner forwarding path does not recursively route auth. It revalidates
// both physical roots and the mounted credential on every actual registry OR
// token request before delegating to the same canonical transport. Auth guards
// above it validate exact realm/service/pull scope and any authorization header.
type databaseRegistryPhysicalRoundTripper struct{ owner *DatabaseRegistryTransport }

func (round databaseRegistryPhysicalRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil || round.owner == nil {
		return nil, errors.New("database registry owned request invalid")
	}
	if err := round.owner.Revalidate(request.Context()); err != nil {
		return nil, err
	}
	round.owner.mutex.RLock()
	if round.owner.closed {
		round.owner.mutex.RUnlock()
		return nil, errors.New("database registry transport unavailable")
	}
	base := round.owner.client.Transport
	round.owner.mutex.RUnlock()
	response, err := base.RoundTrip(request)
	if err != nil {
		return nil, databaseAuthenticationError(request.Context(), err)
	}
	if err := round.owner.Revalidate(request.Context()); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	return response, nil
}

func (round databaseRegistryPhysicalRoundTripper) CloseIdleConnections() {
	round.owner.closeIdleConnections()
}

// PublicationAuthenticationClient is only a protected publisher composition
// input beneath NewDatabaseRegistryPublicationClient. It supplies this owner's
// canonical TLS/destination transport and per-use physical revalidation, but NO
// mounted pull credential/token cache or signing/write/decision grant. The
// distribution publisher separately requires its verified current base proof
// and explicit push-role authentication before using this client.
func (transport *DatabaseRegistryTransport) PublicationAuthenticationClient(ctx context.Context) (*http.Client, error) {
	if transport == nil || ctx == nil {
		return nil, errors.New("database publication transport owner unavailable")
	}
	if err := transport.Revalidate(ctx); err != nil {
		return nil, err
	}
	transport.mutex.RLock()
	defer transport.mutex.RUnlock()
	if transport.closed {
		return nil, errors.New("database publication transport owner closed")
	}
	client := *transport.client
	client.Jar = nil
	client.Transport = databaseRegistryPhysicalRoundTripper{owner: transport}
	return &client, nil
}
