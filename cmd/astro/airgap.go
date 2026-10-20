// airgap subcommands — offline release inventory and registry mirroring.
// These run on an operator host and never talk to the Astronomer API.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli/airgap"
)

type airgapInventoryFlags struct {
	imagesFile      string
	chartValues     string
	releaseManifest string
	planFile        string
}

func (f *airgapInventoryFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.imagesFile, "images-file", "deploy/chart/images.txt", "chart image inventory (make images.txt); empty to skip")
	cmd.Flags().StringVar(&f.chartValues, "chart-values", "deploy/chart/values.yaml", "chart values.yaml (adds the zot registry image); empty to skip")
	cmd.Flags().StringVar(&f.releaseManifest, "release-manifest", "", "signed release-manifest.json (adds the release's digest-pinned chart, images and OCI artifacts)")
}

func (f *airgapInventoryFlags) plan(destination string) (airgap.Plan, error) {
	if f.planFile != "" {
		raw, err := os.ReadFile(f.planFile)
		if err != nil {
			return airgap.Plan{}, fmt.Errorf("read plan: %w", err)
		}
		var plan airgap.Plan
		if err := json.Unmarshal(raw, &plan); err != nil || plan.SchemaVersion != 1 {
			return airgap.Plan{}, fmt.Errorf("plan %s is not an airgap plan (schema_version 1): %v", f.planFile, err)
		}
		plan.Destination = destination
		return plan, nil
	}
	return airgap.BuildPlan(airgap.Inputs{ImagesFile: f.imagesFile, ChartValues: f.chartValues, ReleaseManifest: f.releaseManifest, Destination: destination})
}

func newAirgapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "airgap",
		Short: "Air-gap release inventory and registry mirroring",
		Long: `Inventory and mirror everything an Astronomer release pulls at runtime.

  astro airgap plan                            # full image + artifact inventory
  astro airgap plan --to zot.example:5000 -o json > plan.json
  astro airgap mirror --to zot.example:5000    # copy every item, digests preserved

Targets keep the source path and replace the registry host
(ghcr.io/alphabravo-oss/astronomer-go-server -> <to>/alphabravo-oss/astronomer-go-server),
the same rewrite the chart (image.registry / registry.zot) and the server's
REGISTRY_LOCAL policy apply. Registry credentials come from the docker
credential store (docker login / oras login); they are never flags.`,
	}
	cmd.AddCommand(newAirgapPlanCmd(), newAirgapMirrorCmd())
	return cmd
}

func newAirgapPlanCmd() *cobra.Command {
	var flags airgapInventoryFlags
	var destination string
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Print the complete image and artifact inventory of a release",
		Long: `Assembles the inventory from deploy/chart/images.txt (every chart profile,
the agent image, Flux controllers and built-in bundle images), the zot registry
image from values.yaml, the Flux controllers and built-in bundle catalog
embedded in this binary, and optionally a signed release manifest (chart, Flux
distribution, built-in bundle distribution and Charlie OCI artifacts).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			plan, err := flags.plan(destination)
			if err != nil {
				return err
			}
			return render(cmd, plan, func(w io.Writer) error { return writePlanTable(w, plan) })
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVar(&destination, "to", "", "destination registry; fills each item's target")
	return cmd
}

func newAirgapMirrorCmd() *cobra.Command {
	var flags airgapInventoryFlags
	var destination string
	var plainHTTP []string
	var concurrency int
	cmd := &cobra.Command{
		Use:   "mirror --to <registry>",
		Short: "Copy every inventory item into a registry, preserving digests",
		Long: `Copies each item with oras-go: multi-arch indexes with every platform
manifest, source tags, cosign signature/attestation/SBOM tags and OCI referrers
when present. The destination digest is verified for every item. Items already
present are skipped and existing blobs are never re-sent, so the command is
idempotent and a rerun resumes an interrupted mirror.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if destination == "" {
				return fmt.Errorf("--to is required")
			}
			plan, err := flags.plan(destination)
			if err != nil {
				return err
			}
			plain := map[string]bool{}
			for _, host := range plainHTTP {
				for item := range strings.SplitSeq(host, ",") {
					if item = strings.TrimSpace(item); item != "" {
						plain[item] = true
					}
				}
			}
			results, err := airgap.Mirror(cmd.Context(), plan, airgap.MirrorOptions{Destination: destination, PlainHTTP: plain, Out: cmd.ErrOrStderr(), Concurrency: concurrency})
			copied, present := 0, 0
			for _, result := range results {
				switch result.Status {
				case airgap.StatusCopied:
					copied++
				case airgap.StatusPresent:
					present++
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "mirror %s: %d copied, %d already present, %d failed\n", destination, copied, present, len(results)-copied-present)
			return err
		},
	}
	flags.register(cmd)
	cmd.Flags().StringVar(&flags.planFile, "plan", "", "use a plan written by `astro airgap plan -o json` instead of building one")
	cmd.Flags().StringVar(&destination, "to", "", "destination registry host[:port][/prefix] (required)")
	cmd.Flags().StringSliceVar(&plainHTTP, "plain-http", nil, "registry hosts (source or destination) reached over plain HTTP")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "concurrent blob copies per item (default: oras default)")
	return cmd
}

func writePlanTable(w io.Writer, plan airgap.Plan) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "KIND\tSOURCE\tORIGIN"
	if plan.Destination != "" {
		header += "\tTARGET"
	}
	_, _ = fmt.Fprintln(tw, header)
	for _, item := range plan.Items {
		line := item.Kind + "\t" + item.Source + "\t" + item.Origin
		if plan.Destination != "" {
			line += "\t" + item.Target
		}
		_, _ = fmt.Fprintln(tw, line)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%d items\n", len(plan.Items))
	return err
}
