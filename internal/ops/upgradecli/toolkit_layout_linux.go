//go:build linux

package upgradecli

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// InstalledToolchainDirectory locates the fixed sibling data directory produced
// by InstallUpgradeToolkit. Location is not signing authority: BootstrapTools
// must independently verify the compiled root, signed resolution and each tuple.
// No PATH lookup, working directory or release-asset directory supplies defaults.
func InstalledToolchainDirectory(executable string) (string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || filepath.Base(executable) != "upgrade-release" || filepath.Base(filepath.Dir(executable)) != "bin" {
		return "", errors.New("exact installed upgrade executable layout required")
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0700 {
		return "", errors.New("private installed upgrade executable required")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return "", errors.New("installed upgrade executable ownership differs")
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || resolved != executable {
		return "", errors.New("installed upgrade path aliases another location")
	}
	root := filepath.Dir(filepath.Dir(executable))
	for _, directory := range []string{root, filepath.Join(root, "bin"), filepath.Join(root, "share"), filepath.Join(root, "share", "astronomer-upgrade"), filepath.Join(root, "share", "astronomer-upgrade", "toolchain")} {
		if err := privateBootstrapDirectory(directory); err != nil {
			return "", err
		}
	}
	return filepath.Join(root, "share", "astronomer-upgrade", "toolchain"), nil
}
