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
	"regexp"
	"sort"
	"strings"
)

//go:embed catalog.json
var catalogJSON []byte

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var imagePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?/[a-z0-9][a-z0-9._/-]*@sha256:[0-9a-f]{64}$`)

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
	var catalog Catalog
	decoder := json.NewDecoder(strings.NewReader(string(catalogJSON)))
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

func (c Catalog) Validate() error {
	if c.SchemaVersion != 1 || !strings.HasPrefix(c.Release, "v1.") || len(c.Components) == 0 {
		return errors.New("built-in bundle catalog has invalid release metadata")
	}
	seen := make(map[string]struct{}, len(c.Components))
	for index, component := range c.Components {
		if component.Slug == "" || component.Name == "" || component.Source.Kind != "helm_http" ||
			component.Source.URL != "https://prometheus-community.github.io/helm-charts" || component.Source.Chart == "" ||
			component.Source.Version == "" || !digestPattern.MatchString(component.Source.ChartDigest) ||
			component.Scope != "platform" || component.TargetNamespace == "" || component.ReleaseName == "" ||
			len(component.Images) == 0 || component.Requirements.KubernetesMinimum == "" ||
			component.Requirements.KubernetesMaximum == "" || len(component.Requirements.Capabilities) == 0 {
			return fmt.Errorf("built-in component %d is incomplete or unpinned", index)
		}
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
