// Package airgap implements `astro airgap plan` and `astro airgap mirror`:
// the complete image + artifact inventory for one release and a digest-
// preserving, idempotent copy of it into a cluster-local registry. It
// replaces scripts/airgap-kit.py and scripts/mirror-release.py.
package airgap

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/registry"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// Item kinds.
const (
	KindImage    = "container_image"
	KindChart    = "helm_chart"
	KindArtifact = "oci_artifact"
)

// Item is one subject to mirror. Target is set when a destination registry
// is known and is always registry.Rehost(Source, destination).
type Item struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Target string `json:"target,omitempty"`
	// Origin names where the inventory learned about the subject.
	Origin string `json:"origin"`
}

// Plan is the deterministic inventory, sorted by source.
type Plan struct {
	SchemaVersion int    `json:"schema_version"`
	Release       string `json:"release,omitempty"`
	Destination   string `json:"destination,omitempty"`
	Items         []Item `json:"items"`
}

// Inputs selects the inventory sources. Empty paths are skipped; the
// embedded Flux controller set and built-in bundle catalog are always added.
type Inputs struct {
	// ImagesFile is deploy/chart/images.txt (`make images.txt`): every image
	// the chart renders across its profiles, the agent image, the Flux
	// controllers and the built-in bundle images.
	ImagesFile string
	// ChartValues is deploy/chart/values.yaml; it contributes the zot image,
	// which is not rendered by the default profile.
	ChartValues string
	// ReleaseManifest is an optional signed release-manifest.json; it adds
	// the digest-pinned chart, images, Flux distribution, built-in bundle
	// distribution and Charlie artifacts of that exact release.
	ReleaseManifest string
	// Destination, when set, fills every Item.Target.
	Destination string
}

// BuildPlan assembles the inventory.
func BuildPlan(in Inputs) (Plan, error) {
	items := map[string]Item{}
	add := func(kind, ref, origin string) error {
		ref = strings.TrimPrefix(strings.TrimSpace(ref), "oci://")
		if ref == "" {
			return nil
		}
		if _, _, err := registry.SplitHost(ref); err != nil {
			return fmt.Errorf("%s: %w", origin, err)
		}
		if existing, found := items[ref]; found && existing.Kind != kind && existing.Kind != KindImage {
			return fmt.Errorf("%s: %s has conflicting kinds %s/%s", origin, ref, existing.Kind, kind)
		}
		if _, found := items[ref]; !found || kind != KindImage {
			items[ref] = Item{Kind: kind, Source: ref, Origin: origin}
		}
		return nil
	}
	plan := Plan{SchemaVersion: 1, Destination: in.Destination}
	if in.ImagesFile != "" {
		lines, err := readImageList(in.ImagesFile)
		if err != nil {
			return Plan{}, err
		}
		for _, line := range lines {
			if err := add(KindImage, line, "images.txt"); err != nil {
				return Plan{}, err
			}
		}
	}
	if in.ChartValues != "" {
		zot, err := zotImage(in.ChartValues)
		if err != nil {
			return Plan{}, err
		}
		if err := add(KindImage, zot, "values.yaml:registry.zot.image"); err != nil {
			return Plan{}, err
		}
	}
	controllers, err := fluxdistribution.ControllerImages()
	if err != nil {
		return Plan{}, err
	}
	for _, controller := range controllers {
		repository, _, _ := strings.Cut(controller.Reference, "@")
		if index := strings.LastIndex(repository, ":"); index > strings.LastIndex(repository, "/") {
			repository = repository[:index]
		}
		if err := add(KindImage, repository+"@"+controller.Digest, "flux:"+controller.Component); err != nil {
			return Plan{}, err
		}
	}
	catalog, err := builtinbundles.Load()
	if err != nil {
		return Plan{}, err
	}
	for _, component := range catalog.Components {
		for _, image := range component.Images {
			if err := add(KindImage, image, "builtin:"+component.Slug); err != nil {
				return Plan{}, err
			}
		}
	}
	if in.ReleaseManifest != "" {
		release, err := releaseSubjects(in.ReleaseManifest, add)
		if err != nil {
			return Plan{}, err
		}
		plan.Release = release
	}
	for _, item := range items {
		if in.Destination != "" {
			target, err := registry.Rehost(item.Source, in.Destination)
			if err != nil {
				return Plan{}, err
			}
			item.Target = target
		}
		plan.Items = append(plan.Items, item)
	}
	slices.SortFunc(plan.Items, func(a, b Item) int { return strings.Compare(a.Source, b.Source) })
	return plan, nil
}

func readImageList(path string) ([]string, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-selected inventory file
	if err != nil {
		return nil, fmt.Errorf("open image list: %w", err)
	}
	defer func() { _ = file.Close() }()
	var out []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, scanner.Err()
}

func zotImage(valuesPath string) (string, error) {
	raw, err := os.ReadFile(valuesPath) // #nosec G304 -- operator-selected chart values
	if err != nil {
		return "", fmt.Errorf("read chart values: %w", err)
	}
	var values struct {
		Registry struct {
			Zot struct {
				Image struct {
					Registry   string `json:"registry"`
					Repository string `json:"repository"`
					Tag        string `json:"tag"`
					Digest     string `json:"digest"`
				} `json:"image"`
			} `json:"zot"`
		} `json:"registry"`
	}
	if err := yaml.Unmarshal(raw, &values); err != nil {
		return "", fmt.Errorf("parse chart values: %w", err)
	}
	image := values.Registry.Zot.Image
	if image.Repository == "" {
		return "", nil
	}
	ref := image.Repository
	if image.Registry != "" {
		ref = image.Registry + "/" + ref
	}
	if image.Tag != "" {
		ref += ":" + image.Tag
	}
	if image.Digest != "" {
		ref += "@" + image.Digest
	}
	return ref, nil
}

func releaseSubjects(path string, add func(kind, ref, origin string) error) (string, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-selected release manifest
	if err != nil {
		return "", fmt.Errorf("read release manifest: %w", err)
	}
	var manifest releasecontract.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("parse release manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || manifest.Release.Version == "" {
		return "", errors.New("release manifest has an unsupported schema or no version")
	}
	artifact := func(a releasecontract.Artifact, origin string) error {
		if a.Reference == "" {
			return nil
		}
		kind := a.Kind
		if kind == "" {
			kind = KindArtifact
		}
		if !strings.Contains(a.Reference, "@sha256:") {
			return fmt.Errorf("release manifest %s reference %q is not digest-pinned", origin, a.Reference)
		}
		return add(kind, a.Reference, origin)
	}
	steps := []func() error{
		func() error { return artifact(manifest.Astronomer.Chart, "release:chart") },
		func() error { return artifact(manifest.Flux.Distribution, "release:flux_distribution") },
		func() error { return artifact(manifest.BuiltInBundles.Artifact, "release:builtin_bundles") },
		func() error { return artifact(manifest.Charlie.Artifact, "release:charlie") },
	}
	for _, image := range manifest.Astronomer.Images {
		steps = append(steps, func() error { return artifact(image, "release:image:"+image.Name) })
	}
	for _, controller := range manifest.Flux.Controllers {
		steps = append(steps, func() error { return artifact(controller, "release:flux:"+controller.Name) })
	}
	for _, runtime := range manifest.Astronomer.RuntimeImages {
		steps = append(steps, func() error { return add(KindImage, runtime.Reference, "release:runtime") })
	}
	for _, component := range manifest.BuiltInBundles.Components {
		for _, image := range component.Images {
			steps = append(steps, func() error { return add(KindImage, image, "release:builtin:"+component.Slug) })
		}
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return "", err
		}
	}
	return manifest.Release.Version, nil
}
