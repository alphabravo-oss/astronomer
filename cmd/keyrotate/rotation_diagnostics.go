package main

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5/pgconn"
)

var rotationSQLState = regexp.MustCompile(`^[0-9A-Z]{5}$`)

// Never emit database/parse/scan error messages: they can contain connection
// material, ciphertext or a server-supplied detail. Keep bounded typed metadata.
func rotationErrorAttrs(err error) []any {
	var postgres *pgconn.PgError
	switch {
	case errors.Is(err, context.Canceled):
		return []any{"error_kind", "cancelled"}
	case errors.Is(err, context.DeadlineExceeded):
		return []any{"error_kind", "deadline"}
	case errors.As(err, &postgres):
		attrs := []any{"error_kind", "postgres"}
		if rotationSQLState.MatchString(postgres.Code) {
			attrs = append(attrs, "sqlstate", postgres.Code)
		}
		return attrs
	default:
		return []any{"error_kind", "operation_failed"}
	}
}

type ciphertextRow struct{ id, ct string }

// No row from an incomplete page may be counted or updated. Earlier complete
// pages have already committed separately and remain resumable.
func readCiphertextBatch(rows *sql.Rows) (batch []ciphertextRow, err error) {
	defer func() {
		closed := rows.Close()
		if err == nil {
			err = closed
		}
		if err != nil {
			batch = nil
		}
	}()
	for rows.Next() {
		var row ciphertextRow
		if err := rows.Scan(&row.id, &row.ct); err != nil {
			return nil, err
		}
		batch = append(batch, row)
	}
	return batch, rows.Err()
}
