//go:build linux

package upgradecli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledUpgradeKitDefaultUsesOnlyExactSiblingLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bin", "share", "share/astronomer-upgrade", "share/astronomer-upgrade/toolchain"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(root, "bin", "upgrade-release")
	if err := os.WriteFile(binary, []byte("synthetic private executable, never run"), 0700); err != nil {
		t.Fatal(err)
	}
	got, err := InstalledToolchainDirectory(binary)
	if err != nil || got != filepath.Join(root, "share/astronomer-upgrade/toolchain") {
		t.Fatal("default did not use fixed sibling signed-kit scope", err)
	}
	for _, kind := range []string{"relative", "other-name", "symlink", "public-mode"} {
		t.Run(kind, func(t *testing.T) {
			path := binary
			switch kind {
			case "relative":
				path = "bin/upgrade-release"
			case "other-name":
				path = filepath.Join(root, "bin", "other")
			case "symlink":
				path = filepath.Join(root, "alias", "bin", "upgrade-release")
				if err := os.Symlink(root, filepath.Join(root, "alias")); err != nil {
					t.Fatal(err)
				}
			case "public-mode":
				if err := os.Chmod(binary, 0755); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Chmod(binary, 0700); err != nil {
						t.Error(err)
					}
				})
			}
			if _, err := InstalledToolchainDirectory(path); err == nil {
				t.Fatal("unowned/default path alias admitted")
			}
		})
	}
}
