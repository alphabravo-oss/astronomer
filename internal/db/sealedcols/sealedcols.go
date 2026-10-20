// Package sealedcols is the registry of every age-sealed column family in the
// schema and the key-rotation job that rewrites them.
//
// A sealed column family <c> on table T is four columns: <c>_key_id,
// <c>_algorithm, <c>_ciphertext and <c>_nonce (see pkg/crypto and
// docs/decisions/0010-age-column-encryption.md). Columns is the single Go
// table of those families; an integration test fails when information_schema
// has a *_ciphertext column that is not registered here, so rotation can never
// silently skip a secret.
package sealedcols

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"
)

// Column names one sealed column family.
type Column struct {
	Table string // table name in the public schema
	Name  string // family base name: <Name>_key_id, <Name>_ciphertext, ...
}

func (c Column) String() string { return c.Table + "." + c.Name }

// KeyIDColumn is the <Name>_key_id column.
func (c Column) KeyIDColumn() string { return c.Name + "_key_id" }

// Columns is every sealed column family, in table order.
var Columns = []Column{
	{"backup_storage_configs", "credentials"},
	{"charlie_action_receipts", "arguments"},
	{"charlie_action_receipts", "result"},
	{"charlie_connections", "local_trust_material"},
	{"cloud_credentials", "data"},
	{"cluster_registry_configs", "registry_password"},
	{"deferred_operations", "sealed_spec"},
	{"delivery_sources", "ca_bundle"},
	{"delivery_sources", "credential"},
	{"delivery_system_releases", "registry_credential"},
	{"dex_connectors", "secrets"},
	{"dex_operations", "payload"},
	{"dex_settings", "public_clients"},
	{"gitops_registration_sources", "auth"},
	{"gitops_registration_sources", "webhook_secret"},
	{"helm_repositories", "auth_config"},
	{"loki_ingest_tokens", "token"},
	{"management_backup_destinations", "credentials"},
	{"monitoring_backends", "auth_config"},
	{"node_operations", "parameters"},
	{"prometheus_datasources", "auth"},
	{"resource_operations", "manifest"},
	{"siem_forwarders", "auth"},
	{"smtp_settings", "password"},
	{"sso_configurations", "client_secret"},
	{"sso_sessions", "upstream_id_token"},
	{"user_totp_enrollments", "secret"},
	{"vault_connections", "auth"},
	{"webhook_subscriptions", "secret"},
}

// DB is the subset of pgx.Conn / pgxpool.Pool the rotation uses.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// KeyUsage counts rows per key id for one column family.
type KeyUsage struct {
	Column Column
	KeyID  string
	Rows   int64
}

// Usage reports, for every registered family, how many rows reference each
// key id. Rows with no secret are not counted.
func Usage(ctx context.Context, db DB) ([]KeyUsage, error) {
	var out []KeyUsage
	for _, c := range Columns {
		kid := pgx.Identifier{c.KeyIDColumn()}.Sanitize()
		sql := fmt.Sprintf(`SELECT %s, count(*) FROM %s WHERE %s <> '' GROUP BY 1 ORDER BY 1`,
			kid, pgx.Identifier{"public", c.Table}.Sanitize(), kid)
		rows, err := db.Query(ctx, sql)
		if err != nil {
			return nil, fmt.Errorf("sealedcols: usage of %s: %w", c, err)
		}
		for rows.Next() {
			u := KeyUsage{Column: c}
			if err := rows.Scan(&u.KeyID, &u.Rows); err != nil {
				rows.Close()
				return nil, fmt.Errorf("sealedcols: usage of %s: %w", c, err)
			}
			out = append(out, u)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("sealedcols: usage of %s: %w", c, err)
		}
	}
	return out, nil
}

// RotateOptions controls a rotation run.
type RotateOptions struct {
	// FromKeyID limits the run to rows sealed under this key id. Empty means
	// every key id other than the encryptor's current key.
	FromKeyID string
	// BatchSize is the number of rows rewritten per transaction (default 100).
	BatchSize int
	// DryRun reports what would be rewritten without writing.
	DryRun bool
	// Progress, when set, is called after every committed batch.
	Progress func(c Column, rewritten int64)
}

// ColumnResult is the outcome for one registered family.
type ColumnResult struct {
	Column    Column
	Rewritten int64
	// Remaining rows still sealed under a non-current key (0 after a
	// successful non-dry run).
	Remaining int64
}

// ErrMissingKey reports rows that reference a key id the keyring does not hold.
var ErrMissingKey = errors.New("sealedcols: rows reference key ids not in the keyring")

// Rotate re-encrypts every registered sealed value whose key id is not the
// encryptor's current key (or equals opts.FromKeyID) under the current key.
//
// Each batch reads up to BatchSize rows WHERE <c>_key_id = <old> ... FOR
// UPDATE, decrypts them, re-encrypts with the current key and updates
// key_id/algorithm/ciphertext/nonce in the same transaction; the UPDATE is a
// compare-and-set on the old key id. The walk is resumable (a rerun picks up
// whatever still references an old key) and idempotent (a finished run leaves
// nothing to select).
//
// Before writing anything Rotate checks that every key id still referenced by
// a row is loaded; if not it returns ErrMissingKey and writes nothing, so a
// keyring that dropped the previous key too early cannot leave a half-rotated
// database.
func Rotate(ctx context.Context, db DB, enc *crypto.Encryptor, opts RotateOptions) ([]ColumnResult, error) {
	if enc == nil || enc.KeyCount() == 0 {
		return nil, fmt.Errorf("sealedcols: rotate: %w", crypto.ErrNoKey)
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	current := enc.CurrentKeyID()
	if opts.FromKeyID == current {
		return nil, fmt.Errorf("sealedcols: rotate: --from-key-id %q is the current key", current)
	}
	usage, err := Usage(ctx, db)
	if err != nil {
		return nil, err
	}
	missing := map[string]bool{}
	for _, u := range usage {
		if u.KeyID != current && selects(opts, u.KeyID) && !enc.HasKey(u.KeyID) {
			missing[u.KeyID] = true
		}
	}
	if len(missing) > 0 {
		ids := make([]string, 0, len(missing))
		for id := range missing {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("%w: %s (load them as previous keys after the current one in ASTRONOMER_ENCRYPTION_KEY[_FILE]; loaded: %s)",
			ErrMissingKey, strings.Join(ids, ", "), strings.Join(enc.KeyIDs(), ", "))
	}

	results := make([]ColumnResult, 0, len(Columns))
	for _, c := range Columns {
		res := ColumnResult{Column: c}
		for _, u := range usage {
			if u.Column == c && u.KeyID != current && selects(opts, u.KeyID) {
				res.Remaining += u.Rows
			}
		}
		if !opts.DryRun && res.Remaining > 0 {
			for {
				n, err := rotateBatch(ctx, db, enc, c, opts)
				if err != nil {
					return results, err
				}
				if n == 0 {
					break
				}
				res.Rewritten += n
				if opts.Progress != nil {
					opts.Progress(c, res.Rewritten)
				}
			}
			res.Remaining = 0
		}
		results = append(results, res)
	}
	return results, nil
}

func selects(opts RotateOptions, keyID string) bool {
	return opts.FromKeyID == "" || keyID == opts.FromKeyID
}

func rotateBatch(ctx context.Context, db DB, enc *crypto.Encryptor, c Column, opts RotateOptions) (int64, error) {
	table := pgx.Identifier{"public", c.Table}.Sanitize()
	kid := pgx.Identifier{c.Name + "_key_id"}.Sanitize()
	alg := pgx.Identifier{c.Name + "_algorithm"}.Sanitize()
	ct := pgx.Identifier{c.Name + "_ciphertext"}.Sanitize()
	nonce := pgx.Identifier{c.Name + "_nonce"}.Sanitize()

	where := fmt.Sprintf(`%s <> '' AND %s <> $1`, kid, kid)
	args := []any{enc.CurrentKeyID()}
	if opts.FromKeyID != "" {
		where = fmt.Sprintf(`%s = $1`, kid)
		args = []any{opts.FromKeyID}
	}
	args = append(args, opts.BatchSize)
	selectSQL := fmt.Sprintf(`SELECT ctid, %s, %s, %s, %s FROM %s WHERE %s LIMIT $2 FOR UPDATE`,
		kid, alg, ct, nonce, table, where)
	updateSQL := fmt.Sprintf(`UPDATE %s SET %s = $1, %s = $2, %s = $3, %s = $4 WHERE ctid = $5 AND %s = $6`,
		table, kid, alg, ct, nonce, kid)

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("sealedcols: rotate %s: begin: %w", c, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	type row struct {
		ctid   pgtype.TID
		sealed crypto.Sealed
	}
	rows, err := tx.Query(ctx, selectSQL, args...)
	if err != nil {
		return 0, fmt.Errorf("sealedcols: rotate %s: select: %w", c, err)
	}
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ctid, &r.sealed.KeyID, &r.sealed.Algorithm, &r.sealed.Ciphertext, &r.sealed.Nonce); err != nil {
			rows.Close()
			return 0, fmt.Errorf("sealedcols: rotate %s: scan: %w", c, err)
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("sealedcols: rotate %s: select: %w", c, err)
	}

	for _, r := range batch {
		plain, err := enc.Decrypt(r.sealed)
		if err != nil {
			return 0, fmt.Errorf("sealedcols: rotate %s (key %q): %w", c, r.sealed.KeyID, err)
		}
		fresh, err := enc.Encrypt(plain)
		clear(plain)
		if err != nil {
			return 0, fmt.Errorf("sealedcols: rotate %s: %w", c, err)
		}
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, updateSQL, fresh.KeyID, fresh.Algorithm, fresh.Ciphertext, fresh.Nonce, r.ctid, r.sealed.KeyID)
		if err != nil {
			return 0, fmt.Errorf("sealedcols: rotate %s: update: %w", c, err)
		}
		if tag.RowsAffected() != 1 {
			return 0, fmt.Errorf("sealedcols: rotate %s: row changed underneath the rotation (affected %d)", c, tag.RowsAffected())
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("sealedcols: rotate %s: commit: %w", c, err)
	}
	return int64(len(batch)), nil
}

// WriteReport prints per-column rotation counts, one line per registered
// family, followed by a total.
func WriteReport(w io.Writer, results []ColumnResult, dryRun bool) error {
	verb := "rewritten"
	if dryRun {
		verb = "to rewrite"
	}
	var total int64
	for _, r := range results {
		n := r.Rewritten
		if dryRun {
			n = r.Remaining
		}
		total += n
		if _, err := fmt.Fprintf(w, "%-55s %8d %s\n", r.Column.String(), n, verb); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "%-55s %8d %s\n", "TOTAL", total, verb)
	return err
}
