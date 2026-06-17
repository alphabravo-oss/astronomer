// Shared output rendering for the CLI.
//
// One global persistent --output flag (table|json|yaml) controls how
// every command emits structured data. Commands that have structured
// payloads call render() with both the parsed value (for json/yaml) and
// a table-printing closure (for the default human-readable form). The
// legacy per-command --json flag is preserved as an alias for
// --output json so existing scripts keep working.

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// outputFormat is the value of the global --output flag.
type outputFormat string

const (
	outputTable outputFormat = "table"
	outputJSON  outputFormat = "json"
	outputYAML  outputFormat = "yaml"
)

// outputFlagName / jsonFlagName are the persistent flag names registered
// on the root command.
const (
	outputFlagName = "output"
	jsonFlagName   = "json"
)

// resolveOutput determines the effective output format for a command.
// Precedence: an explicit --output wins; otherwise --json (when set on
// the command or any parent) maps to json; otherwise the default table.
func resolveOutput(cmd *cobra.Command) (outputFormat, error) {
	// --json is a back-compat alias. If the user explicitly passed it,
	// honor it unless --output was also explicitly set.
	jsonSet := false
	if f := cmd.Flags().Lookup(jsonFlagName); f != nil && f.Changed {
		jsonSet = true
	}

	outChanged := false
	out := string(outputTable)
	if f := cmd.Flags().Lookup(outputFlagName); f != nil {
		out = f.Value.String()
		outChanged = f.Changed
	}

	// --json alias only applies when --output was left at its default.
	if !outChanged && jsonSet {
		return outputJSON, nil
	}

	switch outputFormat(strings.ToLower(strings.TrimSpace(out))) {
	case outputTable, "":
		return outputTable, nil
	case outputJSON:
		return outputJSON, nil
	case outputYAML:
		return outputYAML, nil
	default:
		return "", fmt.Errorf("invalid --output %q: want one of table, json, yaml", out)
	}
}

// render emits v in the resolved structured format, or invokes the
// table closure for the human-readable default. tableFn may be nil for
// commands that have no table representation (those fall back to json).
func render(cmd *cobra.Command, v any, tableFn func(io.Writer) error) error {
	format, err := resolveOutput(cmd)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	switch format {
	case outputJSON:
		return writeJSON(w, v)
	case outputYAML:
		return writeYAML(w, v)
	default:
		if tableFn == nil {
			return writeJSON(w, v)
		}
		return tableFn(w)
	}
}

// writeYAML emits v as YAML to w.
func writeYAML(w io.Writer, v any) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}
