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
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/server"
)

const databasePublicationUsage = "astronomer-server database-update-publish --main-repository HOST/REPOSITORY --java-repository HOST/REPOSITORY --cosign-binary /ABSOLUTE/PATH\n"

func parseDatabasePublicationCommand(args []string, getenv func(string) string) (server.DatabasePublicationCommandOptions, error) {
	var options server.DatabasePublicationCommandOptions
	flags := flag.NewFlagSet("database-update-publish", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.MainRepository, "main-repository", "", "operator-owned immutable main database destination")
	flags.StringVar(&options.JavaRepository, "java-repository", "", "operator-owned immutable Java database destination")
	flags.StringVar(&options.CosignBinary, "cosign-binary", "", "qualified absolute Cosign executable")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, flag.ErrHelp
		}
		return options, errors.New("database publication command arguments invalid")
	}
	if flags.NArg() != 0 || (distribution.DatabasePublicationRepositories{Main: options.MainRepository, Java: options.JavaRepository}).Validate() != nil || !filepath.IsAbs(options.CosignBinary) || filepath.Clean(options.CosignBinary) != options.CosignBinary || getenv == nil {
		return options, errors.New("database publication command requires exact role repositories and absolute signer")
	}
	// Protected job environment supplies scoped registry authentication and the
	// exact GitHub request. Raw OIDC values stay in an opaque, nonpersisted object.
	// No command flag/feed URL or ambient Docker credential store supplies it.
	options.Authentication = resolver.DatabaseRegistryAuthentication{RealmURL: getenv("ASTRONOMER_DATABASE_PUBLICATION_REALM"), Service: getenv("ASTRONOMER_DATABASE_PUBLICATION_SERVICE"), Username: getenv("ASTRONOMER_DATABASE_PUBLICATION_USERNAME"), Password: getenv("ASTRONOMER_DATABASE_PUBLICATION_PASSWORD")}
	request, err := distribution.CaptureDatabasePublicationGitHubRequest(getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	if err != nil {
		return options, errors.New("database publication protected GitHub request unavailable")
	}
	options.SigningRequest = request
	return options, nil
}

func handleDatabasePublicationCommand(args []string, stdout, stderr io.Writer) int {
	options, err := parseDatabasePublicationCommand(args, os.Getenv)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = io.WriteString(stdout, databasePublicationUsage)
		return 0
	}
	if err != nil {
		_, _ = io.WriteString(stderr, "database-update-publish: invalid command arguments\n")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		_, _ = io.WriteString(stderr, "database-update-publish: configuration unavailable\n")
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	result, err := server.PublishDatabaseUpdate(ctx, cfg, options)
	if err != nil {
		if result.Published {
			_, _ = fmt.Fprintf(stderr, "database-update-publish: immutable update published (generation %d); discovery update failed\n", result.Generation)
		} else {
			_, _ = io.WriteString(stderr, "database-update-publish: publication failed\n")
		}
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "database-update-publish: published generation %d, feed %s\n", result.Generation, result.FeedDigest)
	return 0
}
