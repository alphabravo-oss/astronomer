//go:build linux

package upgradecli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyBootstrapLookupCapturesEachNamedInputOnceAndIgnoresUnlisted(t *testing.T) {
	calls := map[string]int{}
	lookup, err := FreezeLegacyLookup(func(name string) (string, bool) { calls[name]++; return "original", true })
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range legacyBootstrapNames {
		for i := 0; i < 3; i++ {
			value, set := lookup(name)
			if value != "original" || !set {
				t.Fatal("captured input changed")
			}
		}
		if calls[name] != 1 {
			t.Fatal("ambient input was recaptured")
		}
	}
	if value, set := lookup("UNLISTED_TRUST_ROOT"); value != "" || set {
		t.Fatal("unlisted environment was admitted")
	}
}
func TestLegacyBootstrapChildCredentialsArePrivateAndNeverLiterals(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	secret := "operator-secret-not-public"
	initial := t.TempDir()
	config := filepath.Join(initial, "config")
	lookup := func(name string) (string, bool) {
		if name == "GH_TOKEN" {
			return secret, true
		}
		if name == "PATH" {
			return "bin::/usr/bin", true
		}
		return "", false
	}
	bindings, err := CaptureLegacyChildEnvironment(context.Background(), lookup, directory, initial, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.Literal != "" || binding.SecretFile == "" {
			t.Fatal("operator credential retained as literal")
		}
		info, err := os.Lstat(binding.SecretFile)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("credential file not private")
		}
		raw, err := os.ReadFile(binding.SecretFile)
		if err != nil {
			t.Fatal(err)
		}
		if binding.Name == "GH_TOKEN" && string(raw) != secret {
			t.Fatal("token changed")
		}
		if binding.Name == "PATH" && string(raw) != strings.Join([]string{filepath.Join(initial, "bin"), initial, "/usr/bin"}, ":") {
			t.Fatal("relative plugin search path no longer resolves against original working directory")
		}
		if binding.Name == "KUBECONFIG" && string(raw) != config {
			t.Fatal("explicit observed kubeconfig changed")
		}
	}
}
func TestLegacyBootstrapCredentialCaptureCancellationAndUnsafeInputPublishNothingUsable(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lookup := func(string) (string, bool) { return "credential", true }
	bindings, err := CaptureLegacyChildEnvironment(ctx, lookup, directory, directory, filepath.Join(directory, "config"))
	if err == nil || len(bindings) != 0 {
		t.Fatal("cancelled capture returned usable credentials")
	}
	for _, value := range []string{"unsafe\nvalue", strings.Repeat("s", (64<<10)+1)} {
		if _, err := FreezeLegacyLookup(func(string) (string, bool) { return value, true }); err == nil {
			t.Fatal("unbounded or multi-line credential admitted")
		}
	}
}
