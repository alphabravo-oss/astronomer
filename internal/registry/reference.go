// Package registry implements the air-gap layered registry policy: every
// runtime image or OCI artifact reference the management plane renders or
// pulls is re-hosted onto the cluster-local registry (layer 1), optionally
// resolved against an operator-configured fallback list (layer 2, opt-in), and
// otherwise fails closed (layer 3). No layer ever reaches a public default
// registry on its own; public hosts appear only when an operator configures
// them as fallbacks or runs the explicit online profile (no local registry).
package registry

import (
	"errors"
	"fmt"
	"strings"
)

// dockerHubHosts are the spellings of Docker Hub. Short names such as
// "busybox:1.36" or "dexidp/dex:v2" implicitly live there.
var dockerHubHosts = map[string]bool{"docker.io": true, "index.docker.io": true, "registry-1.docker.io": true}

// SplitHost separates a reference ("[oci://]host/path[:tag][@digest]") into
// its registry host and the repository path with tag/digest. References with
// no explicit host are Docker Hub references; their "library/" namespace is
// dropped so the path matches how the chart spells short names.
func SplitHost(ref string) (host, path string, err error) {
	ref = strings.TrimPrefix(ref, "oci://")
	if ref == "" || strings.ContainsAny(ref, " \t\r\n\\") || strings.Contains(ref, "://") {
		return "", "", fmt.Errorf("image reference %q is invalid", ref)
	}
	first, rest, found := strings.Cut(ref, "/")
	if found && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, path = strings.ToLower(first), rest
	} else {
		host, path = "docker.io", ref
	}
	if dockerHubHosts[host] {
		host = "docker.io"
		path = strings.TrimPrefix(path, "library/")
	}
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "//") {
		return "", "", fmt.Errorf("image reference %q has no repository", ref)
	}
	return host, path, nil
}

// Rehost replaces the registry of ref with base, which is a registry host
// optionally followed by a path prefix ("zot.ns.svc:5000" or
// "mirror.corp/astronomer"). An oci:// scheme on ref is preserved. Tags and
// digests are preserved byte-for-byte, so content identity never changes.
func Rehost(ref, base string) (string, error) {
	base = strings.TrimSuffix(strings.TrimSpace(base), "/")
	if base == "" {
		return "", errors.New("registry base is empty")
	}
	_, path, err := SplitHost(ref)
	if err != nil {
		return "", err
	}
	scheme := ""
	if strings.HasPrefix(ref, "oci://") {
		scheme = "oci://"
	}
	return scheme + base + "/" + path, nil
}

// IsLocal reports whether ref already names the given local registry.
func IsLocal(ref, local string) bool {
	if local == "" {
		return false
	}
	trimmed := strings.TrimPrefix(ref, "oci://")
	return strings.HasPrefix(trimmed, strings.TrimSuffix(local, "/")+"/")
}

// ValidateBase checks one REGISTRY_LOCAL / REGISTRY_FALLBACKS entry: a
// lowercase host[:port] with an optional path prefix and no scheme,
// credentials, tag or digest.
func ValidateBase(base string) error {
	if base == "" || base != strings.TrimSpace(base) || strings.Contains(base, "://") || strings.ContainsAny(base, "@ \t\\?#") || strings.HasSuffix(base, "/") {
		return fmt.Errorf("registry %q must be host[:port][/prefix] without scheme, credentials or tag", base)
	}
	host, _, _ := strings.Cut(base, "/")
	if host != strings.ToLower(host) || (!strings.ContainsAny(host, ".:") && host != "localhost") {
		return fmt.Errorf("registry %q must start with a lowercase registry host", base)
	}
	return nil
}

// ParseList parses a comma-separated REGISTRY_FALLBACKS value. Empty input
// yields an empty list, which means fail closed after layer 1.
func ParseList(raw string) ([]string, error) {
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if err := ValidateBase(item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
