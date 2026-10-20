//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"github.com/distribution/reference"
	yaml "gopkg.in/yaml.v3"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

// RuntimeSources resolves the six historical argument roles from authenticated
// target-chart defaults. Operator values may still configure other chart fields;
// they cannot select a different runtime subject for a signed release upgrade.
// No local latest/default PostgreSQL or Valkey version is an authority here.
func (c *TargetChartContent) RuntimeSources(ctx context.Context) (map[string]string, error) {
	if ctx == nil || c == nil {
		return nil, errors.New("explicit authenticated runtime chart required")
	}
	if _, _, err := c.Verify(ctx); err != nil {
		return nil, err
	}
	manifest, err := c.metadata.Verify(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := c.readArchive(ctx)
	if err != nil {
		return nil, err
	}
	var values []byte
	if _, err := targetChartCompatibilityWithValues(ctx, raw, manifest, &values); err != nil {
		return nil, err
	}
	sources, err := chartRuntimeSources(ctx, values, manifest)
	if err != nil {
		return nil, err
	}
	if _, err := c.readArchive(ctx); err != nil {
		return nil, err
	}
	if _, err := c.metadata.Verify(ctx); err != nil {
		return nil, err
	}
	return sources, ctx.Err()
}

func chartRuntimeSources(ctx context.Context, raw []byte, manifest releasecontract.Manifest) (map[string]string, error) {
	if ctx == nil || len(raw) == 0 || len(raw) > 1<<20 {
		return nil, errors.New("bounded exact target runtime values required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&document) != nil || len(document.Content) != 1 {
		return nil, errors.New("target runtime values YAML differs")
	}
	root := document.Content[0]
	var trailing yaml.Node
	if decoder.Decode(&trailing) != io.EOF || validateRuntimeYAML(ctx, root, 0) != nil || root.Kind != yaml.MappingNode {
		return nil, errors.New("target runtime values are ambiguous")
	}
	// These are source references from the signed chart, not the rendered
	// registry/digest overrides. Canonical RuntimeBindings must resolve each
	// source exactly once into the independently signed manifest inventory.
	roles := map[string][]string{
		"utilities.busybox":              {"utilities", "busybox"},
		"postgres":                       {"postgres", "image"},
		"redis":                          {"redis", "image"},
		"dex":                            {"dex", "image"},
		"managementRestoreDrill.sidecar": {"managementRestoreDrill", "sidecar", "image"},
		"managementLogging":              {"managementLogging", "image"},
	}
	sources := make(map[string]string, len(roles))
	for role, keys := range roles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		image := runtimeYAMLPath(root, keys)
		repository := runtimeYAMLString(image, "repository")
		tag := runtimeYAMLString(image, "tag")
		if repository == "" || tag == "" || strings.ContainsAny(repository+tag, "@\t\r\n \x00") {
			return nil, errors.New("target runtime source is missing or malformed")
		}
		source := repository + ":" + tag
		parsed, err := reference.ParseNormalizedNamed(source)
		if err != nil {
			return nil, errors.New("target runtime source is not an OCI tag")
		}
		if _, ok := parsed.(reference.Tagged); !ok {
			return nil, errors.New("target runtime source tag missing")
		}
		if _, ok := parsed.(reference.Digested); ok {
			return nil, errors.New("target runtime source is not a tag-only reference")
		}
		sources[role] = source
	}
	if _, err := upgrade.RuntimeBindings(manifest, sources); err != nil {
		return nil, err
	}
	return sources, ctx.Err()
}

func validateRuntimeYAML(ctx context.Context, node *yaml.Node, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if node == nil || depth > 64 || node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("runtime YAML alias or depth unsupported")
	}
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool)
		if len(node.Content)%2 != 0 {
			return errors.New("runtime YAML mapping malformed")
		}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "" || seen[key.Value] {
				return errors.New("runtime YAML key ambiguous")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := validateRuntimeYAML(ctx, child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func runtimeYAMLPath(node *yaml.Node, keys []string) *yaml.Node {
	for _, key := range keys {
		if node == nil || node.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				next = node.Content[i+1]
				break
			}
		}
		node = next
	}
	return node
}

func runtimeYAMLString(node *yaml.Node, key string) string {
	value := runtimeYAMLPath(node, []string{key})
	if value == nil || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
		return ""
	}
	return value.Value
}
