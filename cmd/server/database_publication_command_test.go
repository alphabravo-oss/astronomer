package main

import (
	"bytes"
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
)

func TestDatabasePublicationCommandHasOnlyProtectedRoleAndSignerInputs(t *testing.T) {
	environment := map[string]string{
		"ASTRONOMER_DATABASE_PUBLICATION_REALM":    "https://token.test/token",
		"ASTRONOMER_DATABASE_PUBLICATION_SERVICE":  "registry.test",
		"ASTRONOMER_DATABASE_PUBLICATION_USERNAME": "fixture-user",
		"ASTRONOMER_DATABASE_PUBLICATION_PASSWORD": "fixture-password",
		"ACTIONS_ID_TOKEN_REQUEST_URL":             "https://pipelines.actions.githubusercontent.com/owned/idtoken?api-version=2.0",
		"ACTIONS_ID_TOKEN_REQUEST_TOKEN":           "fixture-oidc-token",
		"COSIGN_KEY":                               "must-not-select-key", "DOCKER_CONFIG": "must-not-select-store",
	}
	var requested []string
	options, err := parseDatabasePublicationCommand([]string{"--main-repository", "registry.test/databases/main", "--java-repository", "registry.test/databases/java", "--cosign-binary", "/owned/cosign"}, func(key string) string { requested = append(requested, key); return environment[key] })
	if err != nil {
		t.Fatal(err)
	}
	if options.MainRepository != "registry.test/databases/main" || options.JavaRepository != "registry.test/databases/java" || options.CosignBinary != "/owned/cosign" || options.SigningRequest == nil || options.Authentication.RealmURL != environment[requested[0]] || options.Authentication.Username != "fixture-user" || options.Authentication.Password != "fixture-password" {
		t.Fatal("protected command did not preserve exact constructor inputs")
	}
	expected := []string{"ASTRONOMER_DATABASE_PUBLICATION_REALM", "ASTRONOMER_DATABASE_PUBLICATION_SERVICE", "ASTRONOMER_DATABASE_PUBLICATION_USERNAME", "ASTRONOMER_DATABASE_PUBLICATION_PASSWORD", "ACTIONS_ID_TOKEN_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_TOKEN"}
	if !reflect.DeepEqual(requested, expected) {
		t.Fatal("command consulted ambient signing or registry authority")
	}
}

func TestDatabasePublicationCommandRefusesCallerGrantsWithoutPrintingInputs(t *testing.T) {
	base := []string{"--main-repository", "registry.test/databases/main", "--java-repository", "registry.test/databases/java", "--cosign-binary", "/owned/cosign"}
	for _, test := range []struct {
		name string
		args []string
	}{
		{"caller-key", append(append([]string{}, base...), "--key", "private-key")},
		{"caller-approval", append(append([]string{}, base...), "--approved")},
		{"caller-attempt", append(append([]string{}, base...), "--attempt", "external-attempt")},
		{"credential-url", []string{"--main-repository", "https://fixture-user:fixture-password@registry.test/main", "--java-repository", "registry.test/java", "--cosign-binary", "/owned/cosign"}},
		{"mutable-reference", []string{"--main-repository", "registry.test/main:latest", "--java-repository", "registry.test/java", "--cosign-binary", "/owned/cosign"}},
		{"same-role", []string{"--main-repository", "registry.test/main", "--java-repository", "registry.test/main", "--cosign-binary", "/owned/cosign"}},
		{"relative-signer", []string{"--main-repository", "registry.test/main", "--java-repository", "registry.test/java", "--cosign-binary", "./cosign"}},
		{"extra-positional", append(append([]string{}, base...), "fixture-password")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output, failure bytes.Buffer
			code := handleDatabasePublicationCommand(test.args, &output, &failure)
			if code != 2 || output.Len() != 0 || failure.String() != "database-update-publish: invalid command arguments\n" {
				t.Fatal("invalid protected command reached config/runtime or printed caller input")
			}
			if strings.Contains(failure.String(), "fixture-password") {
				t.Fatal("command exposed credential input")
			}
		})
	}
	_, err := parseDatabasePublicationCommand([]string{"--help"}, nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatal("help requires runtime authority")
	}
	var output, failure bytes.Buffer
	if handleDatabasePublicationCommand([]string{"--help"}, &output, &failure) != 0 || output.String() != databasePublicationUsage || failure.Len() != 0 {
		t.Fatal("help did not remain read-only")
	}
}
