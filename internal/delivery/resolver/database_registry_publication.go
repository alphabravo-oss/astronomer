package resolver

import (
	"net/http"
	"net/url"
	"strings"

	digest "github.com/opencontainers/go-digest"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"
)

func databaseRegistryScope(repository string, publication bool) string {
	if publication {
		return remoteauth.ScopeRepository(repository, remoteauth.ActionPull, remoteauth.ActionPush)
	}
	return remoteauth.ScopeRepository(repository, remoteauth.ActionPull)
}

func validDatabaseRegistryRequest(request *http.Request, host, repository string, publication bool) bool {
	// Preserve the original pull-only outer method/query contract exactly. Its
	// existing guard and canonical destination client retain URL validation.
	if !publication {
		return (request.Method == http.MethodGet || request.Method == http.MethodHead) && request.URL.RawQuery == ""
	}
	if request == nil || request.URL == nil || request.URL.Scheme != "https" || request.URL.Host != host || request.URL.User != nil || request.URL.Fragment != "" || request.URL.RawFragment != "" || request.URL.Opaque != "" || request.URL.RawPath != "" || request.URL.ForceQuery {
		return false
	}
	base := "/v2/" + repository + "/"
	if !strings.HasPrefix(request.URL.Path, base) {
		return false
	}
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return request.URL.RawQuery == ""
	}
	if !publication || request.Method != http.MethodPost && request.Method != http.MethodPut {
		return false
	}
	path := strings.TrimPrefix(request.URL.Path, base)
	if strings.HasPrefix(path, "manifests/") {
		// The sole mutable locator is the fixed discovery tag, written only after
		// immutable canonical readback and the actual published reservation.
		reference := strings.TrimPrefix(path, "manifests/")
		return request.Method == http.MethodPut && request.URL.RawQuery == "" && (reference == "current" || databaseRegistryPublicationSignatureTag(reference) || databaseRegistryPublicationDigest(reference))
	}
	if !strings.HasPrefix(path, "blobs/uploads/") || len(path) > 512 || len(request.URL.RawQuery) > 8192 {
		return false
	}
	upload := strings.TrimPrefix(path, "blobs/uploads/")
	if strings.ContainsAny(upload, "/\\\x00") || request.Method == http.MethodPost && upload != "" || request.Method == http.MethodPut && upload == "" {
		return false
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || len(query) > 2 {
		return false
	}
	for key, values := range query {
		if len(values) != 1 || key != "digest" && key != "_state" || len(values[0]) > 4096 || strings.ContainsAny(values[0], "\r\n\x00") {
			return false
		}
		if key == "digest" && !databaseRegistryPublicationDigest(values[0]) {
			return false
		}
	}
	// A monolithic POST may provide digest; final PUT always must. Opaque
	// registry upload state is never interpreted as network or scope authority.
	return request.Method != http.MethodPut || query.Get("digest") != ""
}

func databaseRegistryPublicationDigest(value string) bool {
	parsed := digest.Digest(value)
	return parsed.Validate() == nil && parsed.Algorithm() == digest.SHA256
}

func databaseRegistryPublicationSignatureTag(value string) bool {
	if len(value) != len("sha256-")+64+len(".sig") || !strings.HasPrefix(value, "sha256-") || !strings.HasSuffix(value, ".sig") {
		return false
	}
	return databaseRegistryPublicationDigest("sha256:" + strings.TrimSuffix(strings.TrimPrefix(value, "sha256-"), ".sig"))
}

// The production pull parser remains unchanged. Only the protected publisher
// supports the literal comma in the quoted exact pull,push scope. Every part
// still passes the existing closed key/value parser; escapes are unsupported.
func databasePublicationBearerParts(value string) []string {
	parts := make([]string, 0, 3)
	start := 0
	quoted := false
	for index, character := range value {
		switch character {
		case '\\', '\r', '\n':
			return nil
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				parts = append(parts, value[start:index])
				start = index + 1
				if len(parts) > 2 {
					return nil
				}
			}
		}
	}
	if quoted {
		return nil
	}
	parts = append(parts, value[start:])
	if len(parts) != 3 {
		return nil
	}
	return parts
}
