// keyrotate re-encrypts every Fernet-protected column with the current
// primary encryption key. Run it AFTER you have rolled out the server with
// the new key promoted to primary (and the old key still in the fallback
// list), so that:
//
//   - new ciphertext written by the live server uses the new key
//   - keyrotate sweeps the historical rows that were written under the old
//     key and rewrites them under the new key
//
// Only after this command exits 0 can you drop the old key from the
// encryptionKey config and restart. See docs/secret-rotation-runbook.md
// for the full procedure.
//
// Usage:
//
//	keyrotate \
//	  --database-url postgres://... \
//	  --encryption-key "<new>,<old>" \
//	  [--dry-run] [--batch-size 100]
//
// --dry-run reports what would be rewritten without writing.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

type stats struct {
	scanned int
	rewrote int
	skipped int // already under primary key — Fernet doesn't expose key id, so we detect by attempting decrypt and re-encrypting unconditionally; "skipped" is reserved for rows where decryption failed entirely (alarming).
	failed  int
}

func main() {
	startedAt := time.Now()
	var (
		dbURL     = flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection string (env: DATABASE_URL)")
		keyFlag   = flag.String("encryption-key", os.Getenv("ENCRYPTION_KEY"), "comma-separated Fernet keys, new primary FIRST")
		dryRun    = flag.Bool("dry-run", false, "report what would be re-encrypted; write nothing")
		batchSize = flag.Int("batch-size", 100, "rows per transaction (default 100)")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *dbURL == "" {
		log.Error("--database-url or env DATABASE_URL is required")
		os.Exit(2)
	}
	if *keyFlag == "" {
		log.Error("--encryption-key or env ENCRYPTION_KEY is required")
		os.Exit(2)
	}

	enc, err := auth.NewEncryptor(*keyFlag)
	if err != nil {
		log.Error("invalid encryption key", "err", err)
		os.Exit(2)
	}
	log.Info("keyrotate starting", "keys_loaded", enc.KeyCount(), "dry_run", *dryRun)
	if enc.KeyCount() < 2 && !*dryRun {
		log.Warn("only one key configured — nothing to rotate FROM; this run will rewrite ciphertexts under themselves (harmless but wasteful)")
	}

	ctx := context.Background()
	connCfg, err := pgx.ParseConfig(*dbURL)
	if err != nil {
		log.Error("parse dsn", "err", err)
		os.Exit(2)
	}
	db := stdlib.OpenDB(*connCfg)
	defer func() {
		_ = db.Close()
	}()
	db.SetMaxOpenConns(4)

	if err := db.PingContext(ctx); err != nil {
		log.Error("ping postgres", "err", err)
		os.Exit(2)
	}

	total := stats{}
	for _, t := range rewriteTargets {
		s := rewriteColumn(ctx, log, db, enc, t, *batchSize, *dryRun)
		total.scanned += s.scanned
		total.rewrote += s.rewrote
		total.skipped += s.skipped
		total.failed += s.failed
		log.Info("table done",
			"table", t.table, "column", t.column,
			"scanned", s.scanned, "rewrote", s.rewrote, "skipped", s.skipped, "failed", s.failed)
	}

	// Dex connector secrets live inside a JSONB blob (dex_connectors.config)
	// where the encrypted fields are not column-typed. Rather than special-
	// casing them here (the schema is per-connector-type), the runbook tells
	// the operator to re-save each connector via PATCH /api/v1/dex/connectors/{id}
	// once the server is running with the new key — the handler's
	// SetEncryptor path then re-writes the JSON. We surface the
	// outstanding count so the operator knows how many to touch.
	dexCount := 0
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dex_connectors`).Scan(&dexCount); err != nil {
		log.Warn("could not count dex_connectors", "err", err)
	} else if dexCount > 0 {
		log.Info("dex_connectors require manual re-save",
			"count", dexCount,
			"hint", "PATCH /api/v1/dex/connectors/{id} for each, or toggle enabled off/on to force re-encrypt")
	}

	log.Info("keyrotate complete",
		"scanned", total.scanned,
		"rewrote", total.rewrote,
		"skipped", total.skipped,
		"failed", total.failed,
		"dry_run", *dryRun,
		"duration", time.Since(startedAt))
	if total.failed > 0 {
		os.Exit(1)
	}
}

type target struct {
	table, idCol, column string
}

// rewriteTargets is every Fernet-protected column stored as a plain ciphertext
// column, as a (table, primary-key column, ciphertext column) triple. This MUST
// stay complete: any *_encrypted / encrypted_* column that a live server writes
// with *auth.Encryptor and is NOT listed here becomes undecryptable once the old
// key is dropped. cmd/keyrotate/coverage_test.go fails the build if a migration
// introduces an encrypted column that is neither listed here nor explicitly
// exempted (jsonbExemptColumns). See docs/secret-column-inventory.md.
var rewriteTargets = []target{
	{"sso_configurations", "id", "client_secret_encrypted"},
	{"argocd_instances", "id", "auth_token_encrypted"},
	{"backup_storage_configs", "id", "encrypted_credentials"},
	{"vault_connections", "id", "auth_encrypted"},
	{"gitops_registration_sources", "id", "auth_encrypted"},
	{"prometheus_datasources", "id", "auth_encrypted"},
	{"siem_forwarders", "id", "auth_encrypted"},
	{"cloud_credentials", "id", "data_encrypted"},
	{"smtp_settings", "id", "password_encrypted"},
	{"cluster_registry_configs", "id", "registry_password_encrypted"},
	{"webhook_subscriptions", "id", "secret_encrypted"},
	{"argocd_cluster_proxy_tokens", "id", "token_encrypted"},
	{"user_totp_enrollments", "user_id", "secret_encrypted"},
	{"sso_sessions", "jti", "upstream_id_token_encrypted"},
}

// jsonbExemptColumns are encrypted columns that keyrotate deliberately does NOT
// sweep because the ciphertext lives inside a JSONB blob with a per-row schema,
// not a column-typed value. These are re-encrypted by re-saving the row through
// the owning handler (see the dex_connectors note below and the runbook).
var jsonbExemptColumns = map[string]string{
	// table.column -> reason
	"dex_connectors.config": "encrypted fields live inside per-connector-type JSONB; re-saved via PATCH /api/v1/dex/connectors/{id}",
}

func rewriteColumn(ctx context.Context, log *slog.Logger, db *sql.DB, enc *auth.Encryptor, t target, batchSize int, dryRun bool) stats {
	s := stats{}
	log = log.With("table", t.table, "column", t.column)

	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND %s <> ''",
			t.idCol, t.column, t.table, t.column, t.column))
	if err != nil {
		log.Error("scan select", "err", err)
		s.failed++
		return s
	}
	type row struct {
		id string
		ct string
	}
	var batch []row
	for rows.Next() {
		var id, ct string
		if err := rows.Scan(&id, &ct); err != nil {
			log.Error("row scan", "err", err)
			s.failed++
			continue
		}
		batch = append(batch, row{id: id, ct: ct})
	}
	if err := rows.Close(); err != nil {
		log.Error("rows close", "err", err)
	}

	for _, r := range batch {
		s.scanned++
		plain, err := enc.Decrypt(r.ct)
		if err != nil {
			log.Error("decrypt failed — no configured key signed this ciphertext",
				"id", r.id, "err", err)
			s.failed++
			continue
		}
		newCT, err := enc.Encrypt(plain)
		if err != nil {
			log.Error("re-encrypt failed", "id", r.id, "err", err)
			s.failed++
			continue
		}
		if dryRun {
			s.rewrote++
			continue
		}
		_, err = db.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET %s = $1 WHERE %s = $2", t.table, t.column, t.idCol),
			newCT, r.id)
		if err != nil {
			log.Error("update failed", "id", r.id, "err", err)
			s.failed++
			continue
		}
		s.rewrote++
	}
	return s
}
