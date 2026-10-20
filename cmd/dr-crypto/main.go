// dr-crypto is the DR image's crypto tool: authenticated backup envelopes,
// the authenticated backup manifest and the closed key bundle used by the
// management backup, retention and restore-drill CronJobs. See
// internal/drcrypto for the format.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/drcrypto"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: dr-crypto <command> [flags]

commands:
  encrypt                    --input --output --passphrase-file --purpose --context
  decrypt                    --input --output --passphrase-file --expect-purpose --expect-context
  manifest-create            --archive --archive-key --key-bundle --key-bundle-key --passphrase-file
                             --release-name --release-namespace --chart-version --source-identity
                             --database-schema-version --created-at --output
  manifest-verify            --input --passphrase-file [--archive-file --key-bundle-file
                             --expect-release-name --expect-release-namespace
                             --expect-source-identity --expect-prefix --print-field]
  key-bundle-create          --source --output
  key-bundle-extract         --input --output
  legacy-key-bundle-decrypt  --input --output --passphrase-file --sunset YYYY-MM-DD
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprint(stderr, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	command, rest := args[0], args[1:]
	flags := flag.NewFlagSet("dr-crypto "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	str := func(name string) *string { return flags.String(name, "", "") }
	var required []string
	need := func(names ...string) { required = append(required, names...) }
	var exec func() (string, error)
	switch command {
	case "encrypt":
		input, output, pass, purpose, context := str("input"), str("output"), str("passphrase-file"), str("purpose"), str("context")
		need("input", "output", "passphrase-file", "purpose", "context")
		exec = func() (string, error) {
			parsed, err := drcrypto.ParseContext(*context)
			if err != nil {
				return "", err
			}
			return "", drcrypto.EncryptFile(*input, *output, *pass, *purpose, parsed)
		}
	case "decrypt":
		input, output, pass, purpose, context := str("input"), str("output"), str("passphrase-file"), str("expect-purpose"), str("expect-context")
		need("input", "output", "passphrase-file", "expect-purpose", "expect-context")
		exec = func() (string, error) {
			parsed, err := drcrypto.ParseContext(*context)
			if err != nil {
				return "", err
			}
			return "", drcrypto.DecryptFile(*input, *output, *pass, *purpose, parsed)
		}
	case "manifest-create":
		var in drcrypto.ManifestInput
		flags.StringVar(&in.Archive, "archive", "", "")
		flags.StringVar(&in.ArchiveKey, "archive-key", "", "")
		flags.StringVar(&in.KeyBundle, "key-bundle", "", "")
		flags.StringVar(&in.KeyBundleKey, "key-bundle-key", "", "")
		flags.StringVar(&in.Passphrase, "passphrase-file", "", "")
		flags.StringVar(&in.Source.ReleaseName, "release-name", "", "")
		flags.StringVar(&in.Source.ReleaseNamespace, "release-namespace", "", "")
		flags.StringVar(&in.Source.ChartVersion, "chart-version", "", "")
		flags.StringVar(&in.Source.Identity, "source-identity", "", "")
		flags.Int64Var(&in.Source.DatabaseSchemaVersion, "database-schema-version", 0, "")
		flags.StringVar(&in.CreatedAt, "created-at", "", "")
		flags.StringVar(&in.Output, "output", "", "")
		need("archive", "archive-key", "key-bundle", "key-bundle-key", "passphrase-file", "release-name", "release-namespace", "chart-version", "source-identity", "database-schema-version", "created-at", "output")
		exec = func() (string, error) { return "", drcrypto.CreateManifest(in) }
	case "manifest-verify":
		var in drcrypto.VerifyInput
		flags.StringVar(&in.Input, "input", "", "")
		flags.StringVar(&in.Passphrase, "passphrase-file", "", "")
		flags.StringVar(&in.ArchiveFile, "archive-file", "", "")
		flags.StringVar(&in.KeyBundleFile, "key-bundle-file", "", "")
		flags.StringVar(&in.ExpectReleaseName, "expect-release-name", "", "")
		flags.StringVar(&in.ExpectReleaseNamespace, "expect-release-namespace", "", "")
		flags.StringVar(&in.ExpectSourceIdentity, "expect-source-identity", "", "")
		flags.StringVar(&in.Prefix, "expect-prefix", "", "")
		flags.StringVar(&in.PrintField, "print-field", "", "")
		need("input", "passphrase-file")
		exec = func() (string, error) { return drcrypto.VerifyManifest(in) }
	case "key-bundle-create":
		source, output := str("source"), str("output")
		need("source", "output")
		exec = func() (string, error) { return "", drcrypto.CreateKeyBundle(*source, *output) }
	case "key-bundle-extract":
		input, output := str("input"), str("output")
		need("input", "output")
		exec = func() (string, error) { return "", drcrypto.ExtractKeyBundle(*input, *output) }
	case "legacy-key-bundle-decrypt":
		input, output, pass, sunset := str("input"), str("output"), str("passphrase-file"), str("sunset")
		need("input", "output", "passphrase-file", "sunset")
		exec = func() (string, error) {
			date, err := time.Parse(time.DateOnly, *sunset)
			if err != nil {
				return "", errors.New("--sunset must be YYYY-MM-DD")
			}
			warning, err := drcrypto.LegacyDecryptKeyBundle(*input, *output, *pass, date, time.Now())
			if err == nil {
				_, _ = fmt.Fprintln(stderr, warning)
			}
			return "", err
		}
	default:
		_, _ = fmt.Fprintf(stderr, "dr-crypto: unknown command %q\n%s", command, usage)
		return 2
	}
	if err := flags.Parse(rest); err != nil {
		return 2
	}
	set := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, name := range required {
		if !set[name] {
			_, _ = fmt.Fprintf(stderr, "dr-crypto %s: --%s is required\n", command, name)
			return 2
		}
	}
	printed, err := exec()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "dr-crypto: %v\n", err)
		return 1
	}
	if printed != "" {
		_, _ = fmt.Fprintln(stdout, printed)
	}
	return 0
}
