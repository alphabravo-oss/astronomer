// Package builtinbundles exposes the reviewed catalog that is embedded into
// every server/worker release. The OCI artifact contains these same bytes and
// release tests require their digest to match.
package builtinbundles

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var imagePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?/[a-z0-9][a-z0-9._/-]*@sha256:[0-9a-f]{64}$`)
var sourceHostnamePattern = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type Catalog struct {
	SchemaVersion int         `json:"schema_version"`
	Release       string      `json:"release"`
	Components    []Component `json:"components"`
}

type Component struct {
	Slug            string         `json:"slug"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	DefaultEnabled  bool           `json:"default_enabled"`
	Source          Source         `json:"source"`
	Scope           string         `json:"scope"`
	TargetNamespace string         `json:"target_namespace"`
	ReleaseName     string         `json:"release_name"`
	Images          []string       `json:"images"`
	Values          map[string]any `json:"values"`
	Requirements    Requirements   `json:"requirements"`
}

type Source struct {
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Chart       string `json:"chart"`
	Version     string `json:"version"`
	ChartDigest string `json:"chart_digest"`
}

type Requirements struct {
	KubernetesMinimum string   `json:"kubernetes_minimum"`
	KubernetesMaximum string   `json:"kubernetes_maximum"`
	Capabilities      []string `json:"capabilities"`
}

func Load() (Catalog, error) {
	return Parse(catalogJSON)
}

// Parse decodes and validates catalog bytes with the same strict contract used
// by the embedded release catalog. Release tooling uses this entry point before
// fetching any chart archive.
func Parse(data []byte) (Catalog, error) {
	var catalog Catalog
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode built-in bundle catalog: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Catalog{}, errors.New("built-in bundle catalog has trailing JSON")
	}
	if err := catalog.Validate(); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func Bytes() []byte { return append([]byte(nil), catalogJSON...) }

// NormalizeSourceURL returns the one canonical identity accepted for an
// immutable built-in Helm repository. Catalog validation requires callers to
// supply this exact form so spelling variants cannot create two source rows or
// silently rebind an existing deterministic identity.
func NormalizeSourceURL(raw string) (string, error) {
	if raw == "" || len(raw) > 2048 || strings.TrimSpace(raw) != raw {
		return "", errors.New("source URL must be a non-empty bounded canonical URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Opaque != "" {
		return "", errors.New("source URL must be an absolute HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", errors.New("source URL must not contain credentials, a query, or a fragment")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if net.ParseIP(hostname) != nil || !sourceHostnamePattern.MatchString(hostname) ||
		hostname == "localhost" || strings.HasSuffix(hostname, ".local") || strings.HasSuffix(hostname, ".internal") {
		return "", errors.New("source URL must use a public canonical DNS hostname")
	}
	port := parsed.Port()
	if port != "" {
		value, parseErr := strconv.Atoi(port)
		if parseErr != nil || value < 1 || value > 65535 {
			return "", errors.New("source URL has an invalid port")
		}
		if port == "443" {
			port = ""
		}
	}
	if parsed.RawPath != "" || strings.Contains(parsed.EscapedPath(), "%") || strings.Contains(parsed.Path, "//") {
		return "", errors.New("source URL path must not use escaped or ambiguous segments")
	}
	cleanPath := path.Clean(parsed.Path)
	if cleanPath == "." || cleanPath == "/" {
		cleanPath = ""
	}
	if cleanPath != "" && !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	authority := hostname
	if port != "" {
		authority = net.JoinHostPort(hostname, port)
	}
	return (&url.URL{Scheme: "https", Host: authority, Path: cleanPath}).String(), nil
}

func (c Catalog) Validate() error {
	if c.SchemaVersion != 1 || !strings.HasPrefix(c.Release, "v1.") || len(c.Components) == 0 {
		return errors.New("built-in bundle catalog has invalid release metadata")
	}
	seen := make(map[string]struct{}, len(c.Components))
	artifacts := make(map[string]string, len(c.Components))
	for index, component := range c.Components {
		if component.Slug == "" || component.Name == "" || component.Source.Kind != "helm_http" ||
			component.Source.Chart == "" ||
			component.Source.Version == "" || !digestPattern.MatchString(component.Source.ChartDigest) ||
			component.Scope != "platform" || component.TargetNamespace == "" || component.ReleaseName == "" ||
			len(component.Images) == 0 || component.Requirements.KubernetesMinimum == "" ||
			component.Requirements.KubernetesMaximum == "" || len(component.Requirements.Capabilities) == 0 {
			return fmt.Errorf("built-in component %d is incomplete or unpinned", index)
		}
		normalizedSource, err := NormalizeSourceURL(component.Source.URL)
		if err != nil {
			return fmt.Errorf("component %q has unsafe Helm source URL: %w", component.Slug, err)
		}
		if normalizedSource != component.Source.URL {
			return fmt.Errorf("component %q Helm source URL is ambiguous; canonical form is %q", component.Slug, normalizedSource)
		}
		artifactKey := strings.Join([]string{normalizedSource, component.Source.Chart, component.Source.Version}, "\x00")
		if digest, exists := artifacts[artifactKey]; exists && digest != component.Source.ChartDigest {
			return fmt.Errorf("component %q conflicts on immutable Helm artifact identity", component.Slug)
		}
		artifacts[artifactKey] = component.Source.ChartDigest
		imageSeen := make(map[string]struct{}, len(component.Images))
		for _, image := range component.Images {
			if !imagePattern.MatchString(image) {
				return fmt.Errorf("component %q image %q is not an immutable digest reference", component.Slug, image)
			}
			if _, duplicate := imageSeen[image]; duplicate {
				return fmt.Errorf("component %q repeats image %q", component.Slug, image)
			}
			imageSeen[image] = struct{}{}
		}
		if _, duplicate := seen[component.Slug]; duplicate {
			return fmt.Errorf("duplicate built-in component %q", component.Slug)
		}
		seen[component.Slug] = struct{}{}
		capabilities := append([]string(nil), component.Requirements.Capabilities...)
		sort.Strings(capabilities)
		for i := 1; i < len(capabilities); i++ {
			if capabilities[i] == capabilities[i-1] {
				return fmt.Errorf("component %q repeats capability %q", component.Slug, capabilities[i])
			}
		}
	}
	return nil
}
