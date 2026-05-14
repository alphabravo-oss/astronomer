// astro — Astronomer CLI.
//
// Hand-curated wrapper over the management REST API. Driven by the
// OpenAPI spec we publish at /api/v1/openapi.yaml — every command
// here exists in the spec, so AI agents using the spec directly + a
// raw HTTP client see exactly the same surface this binary exposes.
//
// Subcommand layout mirrors `kubectl` / `gh` conventions: noun first,
// then verb (`astro cluster list`, `astro cluster delete <id>`). This
// reads better than verb-first for the discovery-oriented help text
// operators read first.
//
// Auth: `astro login` posts to /api/v1/auth/login/ and persists the
// returned JWTs under ~/.config/astronomer/config.yaml (chmod 0600).
// Every subsequent command reads that file at startup; commands fail
// with an actionable "run astro login first" message when the token
// is missing.

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func main() {
	root := &cobra.Command{
		Use:   "astro",
		Short: "Astronomer management plane CLI",
		Long: `astro is the command-line client for the Astronomer multi-cluster
management plane.

Run "astro login" first to authenticate. Then explore:

  astro cluster list                 # all managed clusters
  astro cluster manifest <id>        # download agent install YAML
  astro cluster delete <id>          # decommission a cluster
  astro cluster shell <id>           # interactive kubectl session
  astro k8s <id> get pods -A         # kubectl-equivalent passthrough
  astro docs                         # open the live API reference

The CLI talks to the same REST API the dashboard uses (see the
OpenAPI spec at <server>/api/v1/openapi.yaml). Any operation the
dashboard can perform, this CLI can — and vice versa.`,
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags. --server overrides config.yaml for this run; useful
	// for one-off commands against a different environment without
	// re-logging-in.
	root.PersistentFlags().String("server", "", "Astronomer server URL (overrides ~/.config/astronomer/config.yaml)")

	root.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newWhoamiCmd(),
		newClusterCmd(),
		newK8sCmd(),
		newDocsCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
