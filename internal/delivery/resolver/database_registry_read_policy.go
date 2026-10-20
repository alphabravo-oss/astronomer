package resolver

import (
	"errors"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"oras.land/oras-go/v2/registry/remote"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// DatabaseRegistryReadOptions is protected operator DATA. It selects one mounted
// pull credential and a finite exact repository allowlist, never push authority.
// Credential bytes are not configuration, public provenance or retained state.
type DatabaseRegistryReadOptions struct {
	CredentialRef string
	Registry      string
	RealmURL      string
	Service       string
	Repositories  []string
}

func freezeDatabaseRegistryReadOptions(options *DatabaseRegistryReadOptions) *DatabaseRegistryReadOptions {
	if options == nil {
		return nil
	}
	copy := *options
	copy.Repositories = append([]string(nil), options.Repositories...)
	sort.Strings(copy.Repositories)
	return &copy
}

// This is configuration-only validation. It opens no credential/root, resolves
// no DNS and constructs no client, so reduce-only restart inspection stays usable
// when the separate read credential is absent. Both endpoints remain subject to
// the same canonical destination policy on every actual connection.
func validateDatabaseRegistryReadOptions(options *DatabaseRegistryReadOptions, network httpclient.DestinationPolicy) error {
	if options == nil {
		return nil
	}
	if options.CredentialRef == "" || len(options.CredentialRef) > 128 || strings.ContainsAny(options.CredentialRef, "/\\\r\n\x00") || filepath.Base(options.CredentialRef) != options.CredentialRef || len(options.Repositories) == 0 || len(options.Repositories) > 32 {
		return errors.New("database registry read configuration invalid")
	}
	registry, err := remote.NewRepository(options.Registry + "/scope-check")
	if err != nil || registry.Reference.Registry != options.Registry || registry.Reference.Repository != "scope-check" || registry.Reference.Reference != "" {
		return errors.New("database registry read configuration invalid")
	}
	policy := DatabaseRegistryAuthentication{RealmURL: options.RealmURL, Service: options.Service}
	if options.RealmURL == "" || !validDatabaseRegistryAuthentication(policy) {
		return errors.New("database registry read authentication configuration invalid")
	}
	realm, _ := url.Parse(options.RealmURL)
	for _, origin := range []string{"https://" + options.Registry, realm.Scheme + "://" + realm.Host} {
		approved := false
		for _, entry := range network.Origins {
			approved = approved || entry.URL == origin
		}
		if !approved {
			return errors.New("database registry read endpoints require exact approved origins")
		}
	}
	seen := make(map[string]bool, len(options.Repositories))
	for _, name := range options.Repositories {
		repository, err := remote.NewRepository(name)
		if len(name) > 512 || err != nil || repository.Reference.Registry != options.Registry || repository.Reference.Reference != "" || name != repository.Reference.Registry+"/"+repository.Reference.Repository || seen[name] {
			return errors.New("database registry read repository scope invalid")
		}
		seen[name] = true
	}
	return nil
}

// The stable policy identity includes exact scope and mount origin, never the
// secret bytes/hash. A new capture can adopt credential rotation within this
// same scope; an old capture fails its per-use private material comparison.
func databaseRegistryReadBindings(verifier *ExecVerifier, options *DatabaseRegistryReadOptions, policy, origin string) (string, string) {
	if options == nil {
		return policy, origin
	}
	fields := []string{"astronomer-database-registry-read-policy-v1", policy, options.Registry, options.RealmURL, options.Service, "pull-only", "exact-bearer-realm-service", "per-repository-token-cache", "auth-redirects=2", "token-response=131072", "token-header=16384", "no-cookie-or-ambient-credential", "bounded-mounted-json=32768"}
	fields = append(fields, options.Repositories...)
	return registryTransportFingerprint(fields...), registryTransportFingerprint("astronomer-database-registry-read-origin-v1", origin, verifier.trustDir, options.CredentialRef)
}
