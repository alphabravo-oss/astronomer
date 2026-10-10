//go:build linux

// The typed executable remains separate from the authoritative old wrapper
// until all legacy external/replacement/network/resume/parity lanes pass.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgradecli"
)

// Bound by the canonical build target to the exact source that embeds the
// authority/trusted root. Operator environment never fills a missing build ID.
var sourceCommit string

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	parsed, err := upgradecli.ParseLegacyUpgradeArguments(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if parsed.Help {
		fmt.Fprint(os.Stdout, upgradecli.LegacyUpgradeHelp)
		return 0
	}
	directory, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade-release: initial directory unavailable")
		return 1
	}
	lookup, err := upgradecli.FreezeLegacyLookup(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade-release: invalid bounded named operator input")
		return 1
	}
	// New typed bootstrap consumes the signed offline toolchain asset set inside
	// the release kit. Existing network/replacement/external shell remains intact;
	// this executable does not silently fall back to ambient tools or trust.
	kit, _ := lookup("UPGRADE_TOOLCHAIN_KIT")
	if kit == "" {
		executable, e := os.Executable()
		if e != nil {
			fmt.Fprintln(os.Stderr, "upgrade-release: installed executable location unavailable")
			return 1
		}
		kit, e = upgradecli.InstalledToolchainDirectory(executable)
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
	}
	if !filepath.IsAbs(kit) {
		kit = filepath.Join(directory, kit)
	}
	config, _ := lookup("KUBECONFIG")
	if config == "" {
		home, _ := lookup("HOME")
		if !filepath.IsAbs(home) {
			fmt.Fprintln(os.Stderr, "upgrade-release: explicit kubeconfig or absolute captured HOME required")
			return 1
		}
		config = filepath.Join(home, ".kube", "config")
	}
	if !filepath.IsAbs(config) {
		config = filepath.Join(directory, config)
	}
	raw, _ := lookup("UPGRADE_MAX_BACKUP_BYTES")
	var maximum int64
	if raw != "" {
		maximum, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || maximum <= 0 {
			fmt.Fprintln(os.Stderr, "upgrade-release: explicit backup budget must be a representable positive byte count")
			return 1
		}
	}
	// Only the detached named operator snapshot reaches the child adapter.
	// Credential values become retained private files; pinned native Tools and
	// compiled signing roots never come from PATH/HOME or operator variables.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	result, err := upgradecli.BootstrapLegacyUpgrade(ctx, args, upgradecli.LegacyBootstrapInputs{Directory: directory, Lookup: lookup, Kubeconfig: config, ToolchainKit: kit, SourceCommit: sourceCommit, MaximumBackupBytes: maximum})
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade-release: bounded upgrade failed; retained private originals require assessment")
	}
	if result.Report.Published {
		fmt.Fprintln(os.Stdout, "Sanitized upgrade report retained.")
	}
	return result.ExitCode
}
