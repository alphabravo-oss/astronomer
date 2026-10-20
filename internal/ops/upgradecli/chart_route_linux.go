//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"oras.land/oras-go/v2/registry"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgrade"
)

// captureOperatorRoute preserves CHART_REF's actual Helm credential/plugin
// contract while granting it no signing authority. The canonical signed OCI
// subject is verified separately first. Exact selected version bytes obtained
// by the joined bound Helm child must equal that authenticated archive. Later
// dry-run/apply consume the retained private copy, so a tag change cannot swap
// charts between this check and Helm rendering. No operator file is overwritten.
func (c *TargetChartContent) captureOperatorRoute(ctx context.Context, commands *Commands, helm Tool, plan upgrade.Plan, reference string) (string, error) {
	if ctx == nil || c == nil || commands == nil || helm.Kind != Helm || plan.Validate() != nil || c.metadata == nil || c.metadata.target != plan.Target {
		return "", errors.New("explicit owned chart routing required")
	}
	parsed, err := registry.ParseReference(strings.TrimPrefix(reference, "oci://"))
	if err != nil || !strings.HasPrefix(reference, "oci://") || parsed.Reference != "" || parsed.String() != strings.TrimPrefix(reference, "oci://") {
		return "", errors.New("CHART_REF requires exact OCI repository")
	}
	if _, _, err := c.Verify(ctx); err != nil {
		return "", err
	}
	canonical, err := c.readArchive(ctx)
	if err != nil {
		return "", err
	}
	if err := artifactCapacity(commands.options.CaptureDirectory, 2*targetChartCompressedMaximum); err != nil {
		return "", err
	}
	directory := filepath.Join(commands.options.CaptureDirectory, "operator-chart-route")
	if err := os.Mkdir(directory, 0700); err != nil {
		return "", errors.New("exclusive chart routing scope unavailable")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", errors.New("open owned chart route")
	}
	defer func(close func() error) { _ = close() }(root.Close)
	_, _, err = commands.Execute(ctx, helm, []string{"pull", reference, "--version", strings.TrimPrefix(plan.Target, "v"), "--destination", directory})
	if err != nil {
		return "", err
	}
	name := "astronomer-" + strings.TrimPrefix(plan.Target, "v") + ".tgz"
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != name {
		return "", errors.New("Helm route returned unexpected chart output")
	}
	source := filepath.Join(directory, name)
	raw, input, err := readOfflineReleaseInput(ctx, source, targetChartCompressedMaximum)
	if err != nil {
		return "", err
	}
	if err := recheckOfflineReleaseInputs(ctx, []offlineReleaseInput{input}); err != nil {
		return "", err
	}
	if !bytes.Equal(raw, canonical) {
		return "", errors.New("CHART_REF returned content different from signed release")
	}
	// Preserve the CLI-created original and publish a distinct private input.
	private := filepath.Join(commands.options.CaptureDirectory, "operator-verified-chart.tgz")
	if err := writeBootstrapBytes(ctx, commands.options.CaptureDirectory, filepath.Base(private), raw, 0600); err != nil {
		return "", err
	}
	if _, _, err := c.Verify(ctx); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return private, nil
}
