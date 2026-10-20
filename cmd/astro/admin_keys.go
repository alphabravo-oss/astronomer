package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/astronomer-go/internal/db/sealedcols"
	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// newAdminRotateKeysCmd re-encrypts every sealed column value under the
// current age key. Unlike the other admin commands it talks to PostgreSQL
// directly: rotation must work when the API is down or the key is wrong.
func newAdminRotateKeysCmd() *cobra.Command {
	var (
		databaseURL string
		fromKeyID   string
		batchSize   int
		dryRun      bool
		keyFile     string
	)
	cmd := &cobra.Command{
		Use:   "rotate-keys",
		Short: "Re-encrypt every sealed column under the current encryption key",
		Long: `Re-encrypt every age-sealed column value that is not sealed under the current
key, in transactions of --batch-size rows, until no row references an old key.

The keyring comes from ASTRONOMER_ENCRYPTION_KEY_FILE or ASTRONOMER_ENCRYPTION_KEY
(the same variables the server and worker read): the NEW key first, then every
previous key that rows may still reference. The command refuses to write
anything if a referenced key id is not loaded. It is resumable and idempotent:
rerunning it after an interruption picks up the remaining rows, and a finished
run rewrites nothing. Remove the previous key from the keyring only after a run
reports 0 remaining rows.`,
		Example: `  # Rotate: new key first, old key second, then restart server+worker with the same keyring
  export ASTRONOMER_ENCRYPTION_KEY="k2026b:AGE-SECRET-KEY-1...NEW,k2026a:AGE-SECRET-KEY-1...OLD"
  astro admin rotate-keys --database-url "$DATABASE_URL" --dry-run
  astro admin rotate-keys --database-url "$DATABASE_URL"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(databaseURL) == "" {
				return errors.New("--database-url (or DATABASE_URL) is required")
			}
			var enc *crypto.Encryptor
			var err error
			if keyFile != "" {
				enc, err = crypto.NewEncryptorFromFile(keyFile)
			} else {
				enc, err = crypto.NewEncryptorFromEnv("ASTRONOMER_ENCRYPTION_KEY")
			}
			if err != nil {
				return fmt.Errorf("load keyring: %w", err)
			}
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			conn, err := pgx.Connect(ctx, databaseURL)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer func() { _ = conn.Close(context.Background()) }()

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "current key: %s; loaded: %s\n", enc.CurrentKeyID(), strings.Join(enc.KeyIDs(), ", "))
			results, err := sealedcols.Rotate(ctx, conn, enc, sealedcols.RotateOptions{
				FromKeyID: fromKeyID,
				BatchSize: batchSize,
				DryRun:    dryRun,
			})
			if werr := sealedcols.WriteReport(out, results, dryRun); werr != nil && err == nil {
				err = werr
			}
			return err
		},
	}
	cmd.Flags().StringVar(&databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL DSN (env: DATABASE_URL)")
	cmd.Flags().StringVar(&fromKeyID, "from-key-id", "", "only rewrite rows sealed under this key id (default: every non-current key)")
	cmd.Flags().IntVar(&batchSize, "batch-size", 100, "rows rewritten per transaction")
	cmd.Flags().StringVar(&keyFile, "key-file", os.Getenv("ASTRONOMER_ENCRYPTION_KEY_FILE"), "keyring file, new key first (env: ASTRONOMER_ENCRYPTION_KEY_FILE; else ASTRONOMER_ENCRYPTION_KEY is read)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report how many rows would be rewritten, write nothing")
	return cmd
}

// newAdminGenerateEncryptionKeyCmd prints a fresh age X25519 identity for
// ASTRONOMER_ENCRYPTION_KEY / secrets.encryptionKey.
func newAdminGenerateEncryptionKeyCmd() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "generate-encryption-key",
		Short: "Print a new age identity for ASTRONOMER_ENCRYPTION_KEY",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			key, err := crypto.GenerateKey()
			if err != nil {
				return err
			}
			entry := key
			if label != "" {
				entry = label + ":" + key
			}
			id, err := crypto.KeyID(entry)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "# key id: %s\n%s\n", id, entry)
			return err
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "optional key id label (default: fingerprint of the public key)")
	return cmd
}
