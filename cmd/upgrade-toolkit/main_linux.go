//go:build linux

// upgrade-toolkit is built from the same trusted source as upgrade-release.
// Its install mode is never run from an unverified member of the archive.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/ops/upgradecli"
)

var sourceCommit string

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 || (args[0] != "build" && args[0] != "install") {
		fmt.Fprintln(os.Stderr, "usage: upgrade-toolkit build|install --target v1.X.Y --output <existing empty private directory> [exact inputs]")
		return 2
	}
	parser := flag.NewFlagSet("upgrade-toolkit "+args[0], flag.ContinueOnError)
	parser.SetOutput(io.Discard)
	target := parser.String("target", "", "exact stable target")
	output := parser.String("output", "", "fresh private output")
	resolution := parser.String("resolution-directory", "", "signed resolution inputs")
	executable := parser.String("executable", "", "source-bound upgrade-release binary")
	archive := parser.String("archive", "", "exact signed toolkit archive")
	inventory := parser.String("inventory", "", "signed toolkit inventory")
	bundle := parser.String("bundle", "", "inventory Sigstore bundle")
	if parser.Parse(args[1:]) != nil || parser.NArg() != 0 || *target == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "upgrade-toolkit: invalid explicit arguments")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bounded, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var err error
	if args[0] == "build" {
		if *resolution == "" || *executable == "" || *archive != "" || *inventory != "" || *bundle != "" {
			return 2
		}
		_, err = upgradecli.BuildUpgradeToolkit(bounded, *resolution, *executable, sourceCommit, *target, *output)
	} else {
		if *archive == "" || *inventory == "" || *bundle == "" || *resolution != "" || *executable != "" {
			return 2
		}
		_, err = upgradecli.InstallUpgradeToolkit(bounded, *archive, *inventory, *bundle, *target, sourceCommit, *output)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade-toolkit: verification or bounded installation failed; partial outputs retained")
		if bounded.Err() == context.Canceled {
			return 130
		}
		if bounded.Err() == context.DeadlineExceeded {
			return 124
		}
		return 1
	}
	_, _ = fmt.Fprintln(os.Stdout, "Verified toolkit output retained.")
	return 0
}
