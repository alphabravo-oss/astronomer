package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/server"
)

const databaseImportUsage = "astronomer-server database-update-import --kit /ABSOLUTE/PRIVATE/DIRECTORY\n"

func parseDatabaseImportCommand(args []string, getenv func(string) string) (server.DatabaseImportCommandOptions, error) {
	var options server.DatabaseImportCommandOptions
	flags := flag.NewFlagSet("database-update-import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.KitDirectory, "kit", "", "closed signed feed/child/layer transfer directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, flag.ErrHelp
		}
		return options, errors.New("offline database import arguments invalid")
	}
	if flags.NArg() != 0 || getenv == nil || !filepath.IsAbs(options.KitDirectory) || filepath.Clean(options.KitDirectory) != options.KitDirectory {
		return options, errors.New("offline database import requires canonical absolute kit")
	}
	// The executable owns these exact ephemeral inputs. Neither publication push
	// credentials nor GitHub OIDC/Docker/ambient registry credentials are inherited.
	options.Authentication = resolver.DatabaseRegistryAuthentication{RealmURL: getenv("ASTRONOMER_DATABASE_IMPORT_REALM"), Service: getenv("ASTRONOMER_DATABASE_IMPORT_SERVICE"), Username: getenv("ASTRONOMER_DATABASE_IMPORT_USERNAME"), Password: getenv("ASTRONOMER_DATABASE_IMPORT_PASSWORD")}
	return options, nil
}

func handleDatabaseImportCommand(args []string, stdout, stderr io.Writer) int {
	options, err := parseDatabaseImportCommand(args, os.Getenv)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = io.WriteString(stdout, databaseImportUsage)
		return 0
	}
	if err != nil {
		_, _ = io.WriteString(stderr, "database-update-import: invalid arguments\n")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		_, _ = io.WriteString(stderr, "database-update-import: configuration unavailable\n")
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	result, err := server.ImportDatabaseUpdate(ctx, cfg, options)
	if err != nil {
		if result.Accepted {
			_, _ = fmt.Fprintf(stderr, "database-update-import: generation %d accepted; discovery update failed\n", result.Generation)
		} else {
			_, _ = io.WriteString(stderr, "database-update-import: import incomplete\n")
		}
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "database-update-import: imported generation %d, feed %s\n", result.Generation, result.FeedDigest)
	return 0
}
