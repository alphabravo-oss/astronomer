package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestDatabaseOfflineTransferCommandClosedArgumentsAndHelp(t *testing.T) {
	subject := "sha256:" + strings.Repeat("a", 64)
	options, err := parseDatabaseExportCommand([]string{"--feed-digest", subject, "--output", "/private/new-kit"})
	if err != nil || options.FeedDigest != subject || options.OutputDirectory != "/private/new-kit" {
		t.Fatalf("valid export DATA unavailable: %v", err)
	}
	for _, args := range [][]string{{}, {"--feed-digest", "latest", "--output", "/private/new-kit"}, {"--feed-digest", subject, "--output", "relative"}, {"--feed-digest", subject, "--output", "/private/../kit"}, {"--feed-digest", subject, "--output", "/private/new-kit", "--repository", "https://credential.invalid"}, {"--feed-digest", subject, "--output", "/private/new-kit", "extra"}, {"--feed-digest", "sha512:" + strings.Repeat("a", 128), "--output", "/private/new-kit"}} {
		if _, err := parseDatabaseExportCommand(args); err == nil {
			t.Fatal("invalid export args accepted")
		}
	}
	if _, err := parseDatabaseExportCommand([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal("export help failed")
	}
	var stdout, stderr bytes.Buffer
	handled, exit := handleCLI([]string{"database-update-export", "--help"}, &stdout, &stderr)
	if !handled || exit != 0 || stdout.String() != databaseExportUsage || stderr.Len() != 0 {
		t.Fatal("export dispatch bypassed supplied streams or configuration-free help")
	}
}

func TestDatabaseOfflineImportSeparatelyCapturesOnlyOwnedEphemeralInputs(t *testing.T) {
	values := map[string]string{"ASTRONOMER_DATABASE_IMPORT_REALM": "https://registry.test/token", "ASTRONOMER_DATABASE_IMPORT_SERVICE": "registry.test", "ASTRONOMER_DATABASE_IMPORT_USERNAME": "import-only", "ASTRONOMER_DATABASE_IMPORT_PASSWORD": "import-password"}
	var requested []string
	options, err := parseDatabaseImportCommand([]string{"--kit", "/private/kit"}, func(key string) string { requested = append(requested, key); return values[key] })
	if err != nil || options.KitDirectory != "/private/kit" || len(requested) != 4 || options.Authentication.Password != "import-password" {
		t.Fatal("distinct import credential bootstrap failed")
	}
	for _, key := range requested {
		if _, ok := values[key]; !ok {
			t.Fatal("ambient publisher/OIDC/Docker authority requested")
		}
	}
	for _, args := range [][]string{{}, {"--kit", "relative"}, {"--kit", "/private/../kit"}, {"--kit", "/private/kit", "--allow-unsigned"}, {"--kit", "/private/kit", "--attempt", "external"}, {"--kit", "/private/kit", "extra"}} {
		if _, err := parseDatabaseImportCommand(args, func(string) string { return "" }); err == nil {
			t.Fatal("invalid import arguments accepted")
		}
	}
	var stdout, stderr bytes.Buffer
	handled, exit := handleCLI([]string{"database-update-import", "--help"}, &stdout, &stderr)
	if !handled || exit != 0 || stdout.String() != databaseImportUsage || stderr.Len() != 0 {
		t.Fatal("import help bypassed supplied streams or read config")
	}
}
