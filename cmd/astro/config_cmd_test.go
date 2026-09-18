package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/astrocli"
	"github.com/spf13/cobra"
)

func executeConfigCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := &cobra.Command{Use: "astro", SilenceUsage: true, SilenceErrors: true}
	command.PersistentFlags().StringP(outputFlagName, "o", string(outputTable), "")
	command.PersistentFlags().Bool(jsonFlagName, false, "")
	command.AddCommand(newConfigCmd())
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(append([]string{"config"}, args...))
	err := command.Execute()
	return output.String(), err
}

func TestSortedConfigKeysAreStable(t *testing.T) {
	keys := sortedConfigKeys()
	if !slices.IsSorted(keys) {
		t.Fatalf("config keys are not sorted: %v", keys)
	}
	want := []string{"access_token", "api_token_id", "refresh_token", "server_url", "username"}
	if !slices.Equal(keys, want) {
		t.Fatalf("config keys = %v, want %v", keys, want)
	}
}

func TestConfigSetGetAndCurrentRedaction(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("ASTRO_CONFIG_HOME", configHome)
	if err := astrocli.SaveConfig(&astrocli.Config{
		AccessToken:  "access-secret",
		APITokenID:   "token-id",
		RefreshToken: "refresh-secret",
	}); err != nil {
		t.Fatal(err)
	}

	output, err := executeConfigCommand(t, "set", "server_url", "https://console.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Set server_url = https://console.example") {
		t.Fatalf("set output = %q", output)
	}

	_, err = executeConfigCommand(t, "set", "username", "operator")
	if err != nil {
		t.Fatal(err)
	}
	output, err = executeConfigCommand(t, "get", "server_url")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "https://console.example" {
		t.Fatalf("get output = %q", output)
	}

	output, err = executeConfigCommand(t, "current")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"https://console.example",
		"operator",
		"Access token:  (set)",
		"API token ID:  token-id",
		"Legacy refresh: (set)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("current output %q missing %q", output, want)
		}
	}
	if strings.Contains(output, "access-secret") || strings.Contains(output, "refresh-secret") {
		t.Fatalf("current table leaked a token: %q", output)
	}
	output, err = executeConfigCommand(t, "get", "access_token")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "(set)" || strings.Contains(output, "access-secret") {
		t.Fatalf("config get leaked a token: %q", output)
	}

	info, err := os.Stat(filepath.Join(configHome, astrocli.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
}

func TestConfigRejectsUnknownAndReadOnlyKeys(t *testing.T) {
	t.Setenv("ASTRO_CONFIG_HOME", t.TempDir())

	if _, err := executeConfigCommand(t, "get", "missing"); err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Fatalf("unknown key error = %v", err)
	}
	if _, err := executeConfigCommand(t, "set", "access_token", "unsafe"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only key error = %v", err)
	}
}

func TestConfigCurrentStructuredOutputIsExplicit(t *testing.T) {
	t.Setenv("ASTRO_CONFIG_HOME", t.TempDir())
	if err := astrocli.SaveConfig(&astrocli.Config{
		ServerURL:   "https://console.example",
		AccessToken: "automation-token",
	}); err != nil {
		t.Fatal(err)
	}

	output, err := executeConfigCommand(t, "current", "--output=json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"access_token": "(set)"`) {
		t.Fatalf("structured output = %q", output)
	}
	if strings.Contains(output, "automation-token") {
		t.Fatalf("structured output leaked a token: %q", output)
	}
}

func TestRedactToken(t *testing.T) {
	if got := redactToken(" \t"); got != "—" {
		t.Fatalf("empty token = %q", got)
	}
	if got := redactToken("secret"); got != "(set)" {
		t.Fatalf("present token = %q", got)
	}
}
