// docs subcommand — prints the OpenAPI URL and (when requested) the
// full spec. Handy for AI agents that want to introspect the API
// surface without round-tripping a Swagger UI.

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDocsCmd() *cobra.Command {
	var print bool
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Show where to find the OpenAPI spec / Swagger UI",
		Long: `Without flags, prints the URLs for the live OpenAPI spec and
Swagger UI on the configured server. With --print, fetches the spec
and writes it to stdout (useful as input to AI agents or codegen
tools — pipe into oapi-codegen, swagger-codegen, openapi-typescript,
etc.).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, cfg, err := authedClient(cmd)
			if err != nil {
				return err
			}
			if !print {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "OpenAPI spec: %s/api/v1/openapi.yaml\nSwagger UI:   %s/api/v1/docs/\n",
					cfg.ServerURL, cfg.ServerURL)
				return err
			}
			body, err := client.GetRaw(cmd.Context(), "/api/v1/openapi.yaml")
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		},
	}
	cmd.Flags().BoolVar(&print, "print", false, "fetch and print the full OpenAPI spec to stdout")
	return cmd
}
