package tunnel

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
)

var ErrInvalidK8sProxyPath = errors.New("invalid kubernetes proxy path")

type k8sProxyPathContextKey struct{}

// CanonicalK8sProxyPath returns the single Kubernetes API path used by proxy
// authorization, audit, and forwarding. The server middleware computes and
// stores it before any security decision; downstream callers read that exact
// value instead of independently interpreting URL.Path, RawPath, or chi's
// wildcard parameter.
func CanonicalK8sProxyPath(r *http.Request) (string, error) {
	if r == nil || r.URL == nil {
		return "", ErrInvalidK8sProxyPath
	}
	if value, ok := r.Context().Value(k8sProxyPathContextKey{}).(string); ok && value != "" {
		return value, nil
	}

	decoded := extractK8sPath(r.URL.Path)
	escaped := extractK8sPath(r.URL.EscapedPath())
	unescaped, err := url.PathUnescape(escaped)
	if err != nil || unescaped != decoded {
		return "", ErrInvalidK8sProxyPath
	}

	// Kubernetes resource coordinates never require percent-encoded path
	// segments. Rejecting every alternate spelling removes the class of bugs
	// where one layer classifies "%73ecrets" while another forwards "secrets".
	// Query-string escaping remains untouched.
	if escaped != decoded || strings.Contains(decoded, "\\") {
		return "", ErrInvalidK8sProxyPath
	}
	canonicalShape := decoded
	if decoded != "/" {
		canonicalShape = strings.TrimSuffix(decoded, "/")
	}
	if decoded == "" || decoded[0] != '/' || path.Clean(decoded) != canonicalShape {
		return "", ErrInvalidK8sProxyPath
	}
	if decoded == "/" {
		return decoded, nil
	}
	for _, segment := range strings.Split(strings.Trim(decoded, "/"), "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", ErrInvalidK8sProxyPath
		}
	}
	return decoded, nil
}

// WithCanonicalK8sProxyPath stores the already-validated path for all
// downstream security and forwarding consumers.
func WithCanonicalK8sProxyPath(r *http.Request, canonical string) *http.Request {
	if r == nil {
		return nil
	}
	return r.WithContext(context.WithValue(r.Context(), k8sProxyPathContextKey{}, canonical))
}
