package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func outputCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String(outputFlagName, string(outputTable), "")
	cmd.Flags().Bool(jsonFlagName, false, "")
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestResolveOutput(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    outputFormat
		wantErr string
	}{
		{name: "default", want: outputTable},
		{name: "json", args: []string{"--output=json"}, want: outputJSON},
		{name: "yaml normalized", args: []string{"--output", " YAML "}, want: outputYAML},
		{name: "legacy json alias", args: []string{"--json"}, want: outputJSON},
		{name: "explicit output wins", args: []string{"--json", "--output=yaml"}, want: outputYAML},
		{name: "invalid", args: []string{"--output=xml"}, wantErr: "invalid --output"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveOutput(outputCommand(t, tt.args...))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("format = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderStructuredAndTableOutput(t *testing.T) {
	payload := map[string]any{"name": "cluster-a", "ready": true}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "json", args: []string{"--output=json"}, want: []string{"\"name\": \"cluster-a\"", "\"ready\": true"}},
		{name: "yaml", args: []string{"--output=yaml"}, want: []string{"name: cluster-a", "ready: true"}},
		{name: "table", want: []string{"KEY", "VALUE", "name", "cluster-a", "ready", "true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := outputCommand(t, tt.args...)
			var output bytes.Buffer
			cmd.SetOut(&output)
			if err := renderSDK(cmd, payload); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output %q missing %q", output.String(), want)
				}
			}
		})
	}
}

func TestWriteGenericTableIsDeterministicAndHandlesTypedRows(t *testing.T) {
	type row struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var output bytes.Buffer
	if err := writeGenericTable(&output, []row{{Name: "alpha", Count: 2}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	if !strings.HasPrefix(lines[0], "COUNT") || !strings.Contains(lines[0], "NAME") {
		t.Fatalf("columns are not stable and sorted: %q", lines[0])
	}
	if !strings.Contains(lines[1], "2") || !strings.Contains(lines[1], "alpha") {
		t.Fatalf("row = %q", lines[1])
	}
}

func TestWriteGenericTableEmptyAndNestedValues(t *testing.T) {
	tests := []struct {
		value any
		want  string
	}{
		{value: nil, want: "(empty)"},
		{value: []any{}, want: "(no results)"},
		{value: map[string]any{"labels": map[string]any{"app": "api"}}, want: "{1 fields}"},
		{value: map[string]any{"items": []any{1, 2}}, want: "[2 items]"},
		{value: "plain", want: "plain"},
	}
	for _, tt := range tests {
		var output bytes.Buffer
		if err := writeGenericTable(&output, tt.value); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), tt.want) {
			t.Fatalf("output %q missing %q", output.String(), tt.want)
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWriteGenericTableReturnsWriterErrors(t *testing.T) {
	if err := writeGenericTable(failingWriter{}, map[string]any{"a": "b"}); err == nil {
		t.Fatal("expected writer error")
	}
	if err := writeYAML(failingWriter{}, map[string]any{"a": "b"}); err == nil {
		t.Fatal("expected writer error")
	}
}
