package distribution

import (
	"encoding/json"
	"errors"
	"strings"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/registry"
)

// locationTable maps an authenticated source subject to the location it is
// served from. Content digests never change.
type locationTable interface {
	target(source string) (string, bool)
}

// exactTable is an operator-endorsed mirror plan: only listed subjects move.
type exactTable map[string]string

func (t exactTable) target(source string) (string, bool) {
	target, found := t[source]
	return target, found
}

// rehostTable is the cluster-local registry policy (REGISTRY_LOCAL): every
// digest-pinned subject is re-hosted with registry.Rehost, the same rewrite
// the chart, config and `astro airgap mirror` apply.
type rehostTable string

func (t rehostTable) target(source string) (string, bool) {
	if !strings.Contains(source, "@sha256:") {
		return "", false
	}
	target, err := registry.Rehost(source, string(t))
	if err != nil {
		return "", false
	}
	return target, true
}

// projectLocalRegistry re-hosts every component chart repository, workload
// image and digest-pinned values subject of a verified catalog onto the
// cluster-local registry. A component whose chart is not an OCI repository
// cannot be served from the local registry and fails closed.
func projectLocalRegistry(catalog builtinbundles.Catalog, local string) (builtinbundles.Catalog, error) {
	if local == "" {
		return catalog, nil
	}
	// Network policy for the re-hosted child charts is enforced by the caller
	// (validateLocation) before any fetch, exactly as for mirror targets.
	table := rehostTable(local)
	for index := range catalog.Components {
		component := &catalog.Components[index]
		if !strings.HasPrefix(component.Source.URL, "oci://") {
			return builtinbundles.Catalog{}, blocked("network", "built-in chart source is not an OCI repository and cannot be served from the cluster-local registry")
		}
		chart, err := registry.Rehost(component.Source.URL, local)
		if err != nil {
			return builtinbundles.Catalog{}, err
		}
		component.Source.URL = chart
		for i, image := range component.Images {
			target, found := table.target(image)
			if !found {
				return builtinbundles.Catalog{}, errors.New("built-in workload image is not digest-pinned")
			}
			component.Images[i] = target
		}
		if component.Values == nil {
			return builtinbundles.Catalog{}, blocked("values", "distribution values profile must be a closed object")
		}
		valueBytes, err := json.Marshal(component.Values)
		if err != nil {
			return builtinbundles.Catalog{}, err
		}
		var values any
		if err := json.Unmarshal(valueBytes, &values); err != nil {
			return builtinbundles.Catalog{}, err
		}
		rewritten, err := rewriteValues(values, table)
		if err != nil {
			return builtinbundles.Catalog{}, err
		}
		projected, ok := rewritten.(map[string]any)
		if !ok {
			return builtinbundles.Catalog{}, blocked("values", "distribution values profile must be a closed object")
		}
		component.Values = projected
	}
	catalog.DistributionIdentity += ";local:" + local
	if err := catalog.Validate(); err != nil {
		return builtinbundles.Catalog{}, err
	}
	return catalog, nil
}
