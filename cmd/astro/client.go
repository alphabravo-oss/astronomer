// Shared SDK client + response rendering for the typed command groups.
//
// The legacy auth path (authedClient in auth.go) returns the lightweight
// internal/astrocli.Client. Newer command groups instead talk to the
// generated pkg/astroclient SDK, which gives them typed request bodies and
// response envelopes. newAstroClient bridges the two: it reuses the exact
// same server/--server and token/--token/$ASTRO_API_TOKEN plumbing and
// hands back a *astroclient.ClientWithResponses that injects an
// "Authorization: Bearer <token>" header on every request.
//
// renderSDK adapts any SDK typed response's JSON body to the global
// -o/--output flag using the existing output.go renderer, falling back to
// a generic key/value (or row) table when the caller has no bespoke table.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/pkg/astroclient"
)

// newAstroClient resolves the server URL and bearer token from the same
// config / --server / --token / $ASTRO_API_TOKEN precedence used by
// authedClient, then returns a generated SDK client wired to send the
// bearer token on every request.
//
// The base URL is the server root only; every generated method already
// prefixes /api/v1/..., so no path is appended here.
func newAstroClient(cmd *cobra.Command) (*astroclient.ClientWithResponses, error) {
	// Reuse the legacy resolver so server/token precedence stays in one
	// place. We only need the token + the resolved server URL from it.
	_, cfg, err := authedClient(cmd)
	if err != nil {
		return nil, err
	}

	server := cfg.ServerURL
	if override, _ := cmd.Root().PersistentFlags().GetString("server"); strings.TrimSpace(override) != "" {
		server = strings.TrimSpace(override)
	}

	token := cfg.AccessToken
	if override := bearerOverride(cmd); override != "" {
		token = override
	}

	bearer := func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}

	client, err := astroclient.NewClientWithResponses(server, astroclient.WithRequestEditorFn(bearer))
	if err != nil {
		return nil, fmt.Errorf("build SDK client: %w", err)
	}
	return client, nil
}

// renderSDK renders an SDK response body honoring the global -o/--output
// flag. For table output it uses a generic renderer: a list payload prints
// as rows, a map/struct prints as key/value pairs. Callers that want a
// bespoke table should call render() directly with their own closure.
func renderSDK(cmd *cobra.Command, body any) error {
	return render(cmd, body, func(w io.Writer) error {
		return writeGenericTable(w, body)
	})
}

// writeGenericTable prints v as a human-readable table without any
// schema knowledge. Slices become one row per element (with a column per
// shared key when the elements are maps); everything else becomes a
// two-column key/value listing. It is intentionally best-effort: the
// json/yaml formats remain the lossless options.
func writeGenericTable(w io.Writer, v any) error {
	if v == nil {
		_, err := fmt.Fprintln(w, "(empty)")
		return err
	}

	// SDK callers pass TYPED payloads ([]Project, *AgentFleetResponse,
	// typed structs, etc.). A Go type switch only matches exact dynamic
	// types, so those would never hit the []any / map[string]any cases.
	// Round-trip through JSON to normalize any typed slice/struct into the
	// dynamic []any / map[string]any shapes the table renderer understands.
	switch v.(type) {
	case []any, map[string]any:
		// Already dynamic; render directly below.
	default:
		raw, err := json.Marshal(v)
		if err == nil {
			var dyn any
			if json.Unmarshal(raw, &dyn) == nil {
				v = dyn
			}
		}
	}

	switch t := v.(type) {
	case nil:
		_, err := fmt.Fprintln(w, "(empty)")
		return err
	case []any:
		return writeRowsTable(w, t)
	case map[string]any:
		return writeKVTable(w, t)
	default:
		// Scalar or otherwise non-tabular value. Print it as a single
		// value rather than guessing its shape.
		_, err := fmt.Fprintf(w, "%v\n", v)
		return err
	}
}

// writeRowsTable renders a slice of maps as a column-per-key table. Rows
// that are not maps fall back to a single "value" column.
func writeRowsTable(w io.Writer, rows []any) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "(no results)")
		return err
	}

	// Collect the union of keys across all map rows, preserving a stable
	// (sorted) column order so output is deterministic.
	keySet := map[string]struct{}{}
	allMaps := true
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			allMaps = false
			break
		}
		for k := range m {
			keySet[k] = struct{}{}
		}
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()

	if !allMaps {
		if _, err := fmt.Fprintln(tw, "VALUE"); err != nil {
			return err
		}
		for _, r := range rows {
			if _, err := fmt.Fprintf(tw, "%v\n", r); err != nil {
				return err
			}
		}
		return nil
	}

	cols := make([]string, 0, len(keySet))
	for k := range keySet {
		cols = append(cols, k)
	}
	sort.Strings(cols)

	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = strings.ToUpper(c)
	}
	if _, err := fmt.Fprintln(tw, strings.Join(header, "\t")); err != nil {
		return err
	}
	for _, r := range rows {
		m := r.(map[string]any)
		cells := make([]string, len(cols))
		for i, c := range cols {
			if val, ok := m[c]; ok {
				cells[i] = scalarString(val)
			}
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return nil
}

// writeKVTable renders a map as a sorted two-column KEY / VALUE table.
func writeKVTable(w io.Writer, m map[string]any) error {
	if len(m) == 0 {
		_, err := fmt.Fprintln(w, "(empty)")
		return err
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	if _, err := fmt.Fprintln(tw, "KEY\tVALUE"); err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", k, scalarString(m[k])); err != nil {
			return err
		}
	}
	return nil
}

// scalarString renders a cell value compactly. Nested objects/arrays are
// summarized rather than dumped so the table stays readable; use json/yaml
// output to see them in full.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case map[string]any:
		return fmt.Sprintf("{%d fields}", len(t))
	case []any:
		return fmt.Sprintf("[%d items]", len(t))
	default:
		return fmt.Sprintf("%v", v)
	}
}
