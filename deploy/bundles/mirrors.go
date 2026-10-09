package builtinbundles

import (
	"errors"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// NormalizeMirrorSourceURL is used only after vendor bundle verification and
// operator mirror endorsement. It validates spelling; runtime network access
// still requires independent exact-origin/CIDR policy.
func NormalizeMirrorSourceURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" || strings.ContainsAny(raw, "\\\x00%") || strings.Contains(parsed.Path, "//") || strings.HasSuffix(parsed.Hostname(), ".") {
		return "", errors.New("mirror source URL is invalid")
	}
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Port() == "443" {
		parsed.Host = parsed.Hostname()
		if strings.Contains(parsed.Host, ":") {
			parsed.Host = "[" + parsed.Host + "]"
		}
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 || strconv.Itoa(value) != port {
			return "", errors.New("mirror source port is invalid")
		}
	}
	clean := path.Clean(parsed.Path)
	if clean == "." || clean == "/" {
		clean = ""
	}
	parsed.Path = clean
	if parsed.String() != raw {
		return "", errors.New("mirror source URL is not canonical")
	}
	return raw, nil
}

func NormalizeOCISourceURL(raw string) (string, error) {
	if !strings.HasPrefix(raw, "oci://") {
		return "", errors.New("chart OCI source is invalid")
	}
	canonical, err := NormalizeMirrorSourceURL("https://" + strings.TrimPrefix(raw, "oci://"))
	if err != nil {
		return "", err
	}
	if parsed, _ := url.Parse(canonical); parsed.Path == "" {
		return "", errors.New("chart OCI source has no repository")
	}
	return raw, nil
}
