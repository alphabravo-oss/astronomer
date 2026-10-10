//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	yaml "gopkg.in/yaml.v3"
)

// CNPGImage reads the digest-pinned standard operand from the fully verified
// target chart. It is separate from the six legacy mirrored runtime roles.
// This authenticates the chart's exact configured bytes, not an independent
// image signature or a running container. No observed source image or caller
// map can supply a substitute verifier image.
func (c *TargetChartContent) CNPGImage(ctx context.Context) (string, uint32, error) {
	if ctx == nil || c == nil {
		return "", 0, errors.New("explicit authenticated CNPG chart required")
	}
	if _, _, err := c.Verify(ctx); err != nil {
		return "", 0, err
	}
	manifest, err := c.metadata.Verify(ctx)
	if err != nil {
		return "", 0, err
	}
	raw, err := c.readArchive(ctx)
	if err != nil {
		return "", 0, err
	}
	var values []byte
	if _, err = targetChartCompatibilityWithValues(ctx, raw, manifest, &values); err != nil {
		return "", 0, err
	}
	image, major, err := cnpgChartImage(ctx, values)
	if err != nil {
		return "", 0, err
	}
	if _, err = c.readArchive(ctx); err != nil {
		return "", 0, err
	}
	if _, err = c.metadata.Verify(ctx); err != nil {
		return "", 0, err
	}
	return image, major, ctx.Err()
}
func cnpgChartImage(ctx context.Context, raw []byte) (string, uint32, error) {
	if ctx == nil || len(raw) == 0 || len(raw) > 1<<20 {
		return "", 0, errors.New("finite CNPG chart values required")
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	var document, trailing yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&document) != nil || len(document.Content) != 1 || decoder.Decode(&trailing) != io.EOF {
		return "", 0, errors.New("CNPG chart values document invalid")
	}
	root := document.Content[0]
	if validateRuntimeYAML(ctx, root, 0) != nil || root.Kind != yaml.MappingNode {
		return "", 0, errors.New("CNPG chart values ambiguous")
	}
	image := runtimeYAMLString(runtimeYAMLPath(root, []string{"postgres", "cloudNativePG"}), "imageName")
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil || named.Name() != "ghcr.io/cloudnative-pg/postgresql" {
		return "", 0, errors.New("authenticated CNPG standard operand repository required")
	}
	tagged, tagOK := named.(reference.Tagged)
	digested, digestOK := named.(reference.Digested)
	if !tagOK || !digestOK || digested.Digest().Algorithm().String() != "sha256" || digested.Digest().Validate() != nil || len(digested.Digest().Encoded()) != 64 || !strings.HasSuffix(tagged.Tag(), "-standard-trixie") {
		return "", 0, errors.New("exact digest-pinned CNPG standard-trixie operand required")
	}
	prefix, _, ok := strings.Cut(tagged.Tag(), ".")
	major, err := strconv.ParseUint(prefix, 10, 32)
	if !ok || err != nil || major == 0 || strconv.FormatUint(major, 10) != prefix {
		return "", 0, errors.New("CNPG same-major compatibility invalid")
	}
	return image, uint32(major), ctx.Err()
}
