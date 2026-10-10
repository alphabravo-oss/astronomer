package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/server"
	digest "github.com/opencontainers/go-digest"
)

const databaseExportUsage = "astronomer-server database-update-export --feed-digest sha256:HEX --output /ABSOLUTE/NEW/PRIVATE/DIRECTORY\n"

func parseDatabaseExportCommand(args []string) (server.DatabaseExportCommandOptions, error) {
	var options server.DatabaseExportCommandOptions
	flags := flag.NewFlagSet("database-update-export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.FeedDigest, "feed-digest", "", "immutable existing signed feed digest")
	flags.StringVar(&options.OutputDirectory, "output", "", "new closed private transfer directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, flag.ErrHelp
		}
		return options, errors.New("offline database export arguments invalid")
	}
	if flags.NArg() != 0 || !filepath.IsAbs(options.OutputDirectory) || filepath.Clean(options.OutputDirectory) != options.OutputDirectory || digest.Digest(options.FeedDigest).Validate() != nil || digest.Digest(options.FeedDigest).Algorithm() != digest.SHA256 {
		return options, errors.New("offline database export requires immutable feed and canonical output")
	}
	return options, nil
}
func handleDatabaseExportCommand(args []string, stdout, stderr io.Writer) int {
	options, err := parseDatabaseExportCommand(args)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = io.WriteString(stdout, databaseExportUsage)
		return 0
	}
	if err != nil {
		_, _ = io.WriteString(stderr, "database-update-export: invalid arguments\n")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		_, _ = io.WriteString(stderr, "database-update-export: configuration unavailable\n")
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	result, err := server.ExportDatabaseUpdate(ctx, cfg, options)
	if err != nil {
		_, _ = io.WriteString(stderr, "database-update-export: export incomplete; retained owned outputs require verification\n")
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "database-update-export: exported generation %d, feed %s\n", result.Generation, result.FeedDigest)
	return 0
}
