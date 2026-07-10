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
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	// The server/worker read the Fernet key from ASTRONOMER_ENCRYPTION_KEY; older
	// tooling used bare ENCRYPTION_KEY. Prefer ENCRYPTION_KEY when set (explicit
	// override for a rotation run), else fall back to the canonical name so an
	// operator whose Secret only sets ASTRONOMER_ENCRYPTION_KEY isn't stuck.
	envKey := os.Getenv("ENCRYPTION_KEY")
	if envKey == "" {
		envKey = os.Getenv("ASTRONOMER_ENCRYPTION_KEY")
	}
	var (
		dbURL      = flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres connection string (env: DATABASE_URL)")
		keyFlag    = flag.String("encryption-key", envKey, "comma-separated Fernet keys, new primary FIRST (env: ENCRYPTION_KEY or ASTRONOMER_ENCRYPTION_KEY)")
		dryRun     = flag.Bool("dry-run", false, "report what would be re-encrypted; write nothing")
		batchSize  = flag.Int("batch-size", 100, "rows per transaction (default 100)")
		dexCutover = flag.Bool("dex-public-clients-cutover-confirmed", false, "after all old server replicas are quiesced, encrypt and scrub legacy Dex public_clients")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *dbURL == "" {
		log.Error("--database-url or env DATABASE_URL is required")
		os.Exit(2)
	}
	if *keyFlag == "" {
		log.Error("--encryption-key or env ENCRYPTION_KEY / ASTRONOMER_ENCRYPTION_KEY is required")
		os.Exit(2)
	}

	enc, err := auth.NewEncryptor(*keyFlag)
	if err != nil {
		log.Error("invalid encryption key", "err", err)
		os.Exit(2)
	}
	primaryKey := strings.TrimSpace(strings.Split(*keyFlag, ",")[0])
	primaryOnly, err := auth.NewEncryptor(primaryKey)
	if err != nil {
		log.Error("invalid primary encryption key")
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
	dexStats := rewriteDexConnectorConfigs(ctx, log, db, enc, *batchSize, *dryRun)
	total.scanned += dexStats.scanned
	total.rewrote += dexStats.rewrote
	total.skipped += dexStats.skipped
	total.failed += dexStats.failed
	if *dexCutover {
		cutoverStats := cutoverDexPublicClients(ctx, log, db, enc, *batchSize, *dryRun)
		total.scanned += cutoverStats.scanned
		total.rewrote += cutoverStats.rewrote
		total.skipped += cutoverStats.skipped
		total.failed += cutoverStats.failed
	}
	if !*dryRun {
		verified := verifyPrimaryOnly(ctx, log, db, primaryOnly, *batchSize)
		total.scanned += verified.scanned
		total.failed += verified.failed
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
	{"dex_settings", "id", "public_clients_encrypted"},
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
	"dex_connectors.config": "encrypted fields live inside typed JSONB and are CAS-rewritten by rewriteDexConnectorConfigs",
}

var dexConnectorSecretFields = map[string][]string{
	"oidc": {"clientSecret"}, "okta": {"clientSecret"}, "microsoft": {"clientSecret"},
	"github": {"clientSecret"}, "gitlab": {"clientSecret"}, "bitbucket": {"clientSecret"},
	"google": {"clientSecret"}, "ldap": {"bindPW"}, "oauth": {"clientSecret"}, "saml": {},
}

// selectBatchSQL builds the paged SELECT used by rewriteColumn (CORR-04).
// Exported for tests so LIMIT/OFFSET batching cannot silently regress.
func selectBatchSQL(t target) string {
	return fmt.Sprintf(
		"SELECT %s, %s FROM %s WHERE %s IS NOT NULL AND %s <> '' ORDER BY %s LIMIT $1 OFFSET $2",
		t.idCol, t.column, t.table, t.column, t.column, t.idCol,
	)
}

// casUpdateSQL builds the ciphertext CAS UPDATE (WHERE id AND col = old_ct).
func casUpdateSQL(t target) string {
	return fmt.Sprintf(
		"UPDATE %s SET %s = $1 WHERE %s = $2 AND %s = $3",
		t.table, t.column, t.idCol, t.column,
	)
}

func rewriteColumn(ctx context.Context, log *slog.Logger, db *sql.DB, enc *auth.Encryptor, t target, batchSize int, dryRun bool) stats {
	s := stats{}
	log = log.With("table", t.table, "column", t.column)
	if batchSize < 1 {
		batchSize = 100
	}

	// CORR-04: page with LIMIT/OFFSET so --batch-size is honored and large
	// tables (sso_sessions) do not load entirely into memory. Each rewrite
	// CAS-updates on the old ciphertext so a concurrent server write is not
	// silently overwritten with re-encrypted stale plaintext.
	offset := 0
	for {
		rows, err := db.QueryContext(ctx, selectBatchSQL(t), batchSize, offset)
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
		if len(batch) == 0 {
			break
		}
		offset += len(batch)

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
			res, err := db.ExecContext(ctx, casUpdateSQL(t), newCT, r.id, r.ct)
			if err != nil {
				log.Error("update failed", "id", r.id, "err", err)
				s.failed++
				continue
			}
			n, _ := res.RowsAffected()
			if err := requireCASUpdate(n); err != nil {
				// A concurrent writer may have used a fallback key. Treat every miss
				// as fatal so the final primary-only verification cannot be skipped.
				log.Error("cas miss — row changed during rotation; retry required", "id", r.id)
				s.failed++
				continue
			}
			s.rewrote++
		}
		if len(batch) < batchSize {
			break
		}
	}
	return s
}

func requireCASUpdate(rowsAffected int64) error {
	if rowsAffected != 1 {
		return fmt.Errorf("CAS update affected %d rows", rowsAffected)
	}
	return nil
}

func rewriteDexConnectorConfigs(ctx context.Context, log *slog.Logger, db *sql.DB, enc *auth.Encryptor, batchSize int, dryRun bool) stats {
	result := stats{}
	if batchSize < 1 {
		batchSize = 100
	}
	offset := 0
	for {
		rows, err := db.QueryContext(ctx, `SELECT id::text, type, config::text FROM dex_connectors ORDER BY id LIMIT $1 OFFSET $2`, batchSize, offset)
		if err != nil {
			result.failed++
			return result
		}
		type item struct{ id, connectorType, raw string }
		batch := []item{}
		for rows.Next() {
			var v item
			if err := rows.Scan(&v.id, &v.connectorType, &v.raw); err != nil {
				result.failed++
				continue
			}
			batch = append(batch, v)
		}
		if err := rows.Err(); err != nil {
			result.failed++
		}
		_ = rows.Close()
		if len(batch) == 0 {
			break
		}
		offset += len(batch)
		for _, row := range batch {
			result.scanned++
			updated, changed, err := rotateDexConnectorConfig(row.raw, row.connectorType, enc)
			if err != nil {
				log.Error("Dex connector rotation failed", "id", row.id)
				result.failed++
				continue
			}
			if !changed {
				continue
			}
			if dryRun {
				result.rewrote++
				continue
			}
			res, err := db.ExecContext(ctx, `WITH bypass AS MATERIALIZED (SELECT set_config('astronomer.dex_connector_stage_bypass','1',true)) UPDATE dex_connectors SET config = $1::jsonb, updated_at = now() WHERE id = $2::uuid AND config = $3::jsonb AND EXISTS (SELECT 1 FROM bypass)`, updated, row.id, row.raw)
			if err != nil {
				result.failed++
				continue
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				result.failed++
				continue
			}
			result.rewrote++
		}
		if len(batch) < batchSize {
			break
		}
	}
	return result
}

func rotateDexConnectorConfig(raw, connectorType string, enc *auth.Encryptor) (string, bool, error) {
	fields, known := dexConnectorSecretFields[strings.ToLower(connectorType)]
	if !known {
		return "", false, fmt.Errorf("unknown Dex connector type")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return "", false, err
	}
	changed := false
	allowedSecret := map[string]struct{}{}
	for _, field := range fields {
		allowedSecret[field] = struct{}{}
	}
	if err := rejectUnexpectedDexSecretFields(config, allowedSecret, true); err != nil {
		return "", false, err
	}
	for _, field := range fields {
		ciphertext, _ := config[field].(string)
		if ciphertext == "" {
			continue
		}
		plain, err := enc.Decrypt(ciphertext)
		if err != nil {
			return "", false, fmt.Errorf("decrypt connector field")
		}
		rotated, err := enc.Encrypt(plain)
		if err != nil {
			return "", false, fmt.Errorf("encrypt connector field")
		}
		config[field], changed = rotated, true
	}
	updated, err := json.Marshal(config)
	if err != nil {
		return "", false, err
	}
	return string(updated), changed, nil
}

func rejectUnexpectedDexSecretFields(value any, allowed map[string]struct{}, top bool) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(key))
			sensitive := false
			for _, fragment := range []string{"secret", "password", "passwd", "token", "apikey", "privatekey", "bindpw", "credential"} {
				sensitive = sensitive || strings.Contains(normalized, fragment)
			}
			_, admitted := allowed[key]
			if sensitive && (!top || !admitted) {
				return fmt.Errorf("unexpected secret-shaped Dex connector field")
			}
			if err := rejectUnexpectedDexSecretFields(item, nil, false); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := rejectUnexpectedDexSecretFields(item, nil, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func cutoverDexPublicClients(ctx context.Context, log *slog.Logger, db *sql.DB, enc *auth.Encryptor, batchSize int, dryRun bool) stats {
	result := stats{}
	if batchSize < 1 {
		batchSize = 100
	}
	offset := 0
	for {
		rows, err := db.QueryContext(ctx, `SELECT id::text, public_clients::text FROM dex_settings WHERE public_clients_cutover_at IS NULL ORDER BY id LIMIT $1 OFFSET $2`, batchSize, offset)
		if err != nil {
			result.failed++
			return result
		}
		type item struct{ id, raw string }
		batch := []item{}
		for rows.Next() {
			var v item
			if err := rows.Scan(&v.id, &v.raw); err != nil {
				result.failed++
				continue
			}
			batch = append(batch, v)
		}
		if err := rows.Err(); err != nil {
			result.failed++
		}
		_ = rows.Close()
		if len(batch) == 0 {
			break
		}
		for _, row := range batch {
			result.scanned++
			var clients []map[string]any
			if err := json.Unmarshal([]byte(row.raw), &clients); err != nil {
				result.failed++
				continue
			}
			canonical, _ := json.Marshal(clients)
			envelope := ""
			if len(clients) > 0 {
				var err error
				envelope, err = enc.Encrypt(string(canonical))
				if err != nil {
					result.failed++
					continue
				}
			}
			if dryRun {
				result.rewrote++
				continue
			}
			res, err := db.ExecContext(ctx, `UPDATE dex_settings SET public_clients_encrypted=$1, public_clients='[]'::jsonb, public_clients_cutover_at=now(), updated_at=now() WHERE id=$2::uuid AND public_clients_cutover_at IS NULL AND public_clients=$3::jsonb`, envelope, row.id, row.raw)
			if err != nil {
				result.failed++
				continue
			}
			n, _ := res.RowsAffected()
			if n == 0 {
				result.failed++
				continue
			}
			result.rewrote++
		}
		if dryRun {
			offset += len(batch)
		}
		if len(batch) < batchSize {
			break
		}
	}
	log.Info("Dex public-client cutover complete", "scanned", result.scanned, "rewrote", result.rewrote, "failed", result.failed, "dry_run", dryRun)
	return result
}

func verifyPrimaryOnly(ctx context.Context, log *slog.Logger, db *sql.DB, primary *auth.Encryptor, batchSize int) stats {
	result := stats{}
	for _, target := range rewriteTargets {
		verified := verifyPrimaryColumn(ctx, log, db, primary, target, batchSize)
		result.scanned += verified.scanned
		result.failed += verified.failed
	}
	connectors := verifyPrimaryDexConnectors(ctx, log, db, primary, batchSize)
	result.scanned += connectors.scanned
	result.failed += connectors.failed
	clients := verifyPrimaryDexStaticClients(ctx, log, db, primary, batchSize)
	result.scanned += clients.scanned
	result.failed += clients.failed
	log.Info("primary-only verification complete", "scanned", result.scanned, "failed", result.failed)
	return result
}

func verifyPrimaryColumn(ctx context.Context, log *slog.Logger, db *sql.DB, primary *auth.Encryptor, target target, batchSize int) stats {
	result := stats{}
	if batchSize < 1 {
		batchSize = 100
	}
	for offset := 0; ; offset += batchSize {
		rows, err := db.QueryContext(ctx, selectBatchSQL(target), batchSize, offset)
		if err != nil {
			result.failed++
			return result
		}
		count := 0
		for rows.Next() {
			var id, ciphertext string
			if err := rows.Scan(&id, &ciphertext); err != nil {
				result.failed++
				continue
			}
			count++
			result.scanned++
			if _, err := primary.Decrypt(ciphertext); err != nil {
				log.Error("primary-only verification failed", "table", target.table, "column", target.column, "id", id)
				result.failed++
			}
		}
		if err := rows.Err(); err != nil {
			result.failed++
		}
		_ = rows.Close()
		if count < batchSize {
			break
		}
	}
	return result
}

func verifyPrimaryDexConnectors(ctx context.Context, log *slog.Logger, db *sql.DB, primary *auth.Encryptor, batchSize int) stats {
	result := stats{}
	if batchSize < 1 {
		batchSize = 100
	}
	for offset := 0; ; offset += batchSize {
		rows, err := db.QueryContext(ctx, `SELECT id::text, type, config::text FROM dex_connectors ORDER BY id LIMIT $1 OFFSET $2`, batchSize, offset)
		if err != nil {
			result.failed++
			return result
		}
		count := 0
		for rows.Next() {
			var id, connectorType, raw string
			if err := rows.Scan(&id, &connectorType, &raw); err != nil {
				result.failed++
				continue
			}
			count++
			result.scanned++
			if err := verifyDexConnectorPrimary(raw, connectorType, primary); err != nil {
				log.Error("Dex connector primary-only verification failed", "id", id)
				result.failed++
			}
		}
		if err := rows.Err(); err != nil {
			result.failed++
		}
		_ = rows.Close()
		if count < batchSize {
			break
		}
	}
	return result
}

func verifyDexConnectorPrimary(raw, connectorType string, primary *auth.Encryptor) error {
	fields, known := dexConnectorSecretFields[strings.ToLower(connectorType)]
	if !known {
		return fmt.Errorf("unknown connector type")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return err
	}
	allowed := map[string]struct{}{}
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	if err := rejectUnexpectedDexSecretFields(config, allowed, true); err != nil {
		return err
	}
	for _, field := range fields {
		value, exists := config[field]
		if !exists || value == "" {
			continue
		}
		ciphertext, ok := value.(string)
		if !ok {
			return fmt.Errorf("secret field is not a string")
		}
		if _, err := primary.Decrypt(ciphertext); err != nil {
			return fmt.Errorf("secret field is not encrypted by primary")
		}
	}
	return nil
}

func verifyPrimaryDexStaticClients(ctx context.Context, log *slog.Logger, db *sql.DB, primary *auth.Encryptor, batchSize int) stats {
	result := stats{}
	if batchSize < 1 {
		batchSize = 100
	}
	for offset := 0; ; offset += batchSize {
		rows, err := db.QueryContext(ctx, `SELECT id::text, public_clients::text, public_clients_encrypted, public_clients_cutover_at IS NOT NULL FROM dex_settings ORDER BY id LIMIT $1 OFFSET $2`, batchSize, offset)
		if err != nil {
			result.failed++
			return result
		}
		count := 0
		for rows.Next() {
			var id, plaintext, envelope string
			var cutover bool
			if err := rows.Scan(&id, &plaintext, &envelope, &cutover); err != nil {
				result.failed++
				continue
			}
			count++
			result.scanned++
			if err := verifyDexStaticClientRow(plaintext, envelope, cutover, primary); err != nil {
				log.Error("Dex static-client primary-only verification failed", "id", id)
				result.failed++
			}
		}
		if err := rows.Err(); err != nil {
			result.failed++
		}
		_ = rows.Close()
		if count < batchSize {
			break
		}
	}
	return result
}

func verifyDexStaticClientRow(plaintext, envelope string, cutover bool, primary *auth.Encryptor) error {
	canonical := func(raw string) ([]byte, int, error) {
		var clients []map[string]any
		if err := json.Unmarshal([]byte(raw), &clients); err != nil {
			return nil, 0, err
		}
		encoded, err := json.Marshal(clients)
		return encoded, len(clients), err
	}
	plainCanonical, plainCount, err := canonical(plaintext)
	if err != nil {
		return err
	}
	if cutover && plainCount != 0 {
		return fmt.Errorf("cutover row retains plaintext clients")
	}
	if envelope == "" {
		if plainCount != 0 {
			return fmt.Errorf("non-empty clients have no envelope")
		}
		return nil
	}
	decrypted, err := primary.Decrypt(envelope)
	if err != nil {
		return err
	}
	envelopeCanonical, _, err := canonical(decrypted)
	if err != nil {
		return err
	}
	if !cutover && string(plainCanonical) != string(envelopeCanonical) {
		return fmt.Errorf("pre-cutover envelope differs from compatibility copy")
	}
	return nil
}
