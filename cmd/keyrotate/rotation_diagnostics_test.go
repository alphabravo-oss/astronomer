package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/jackc/pgx/v5/pgconn"
)

type diagnosticDriver struct{ stage, ciphertext string }
type diagnosticConn struct{ diagnosticDriver }
type diagnosticRows struct {
	diagnosticDriver
	delivered bool
}

const diagnosticSecret = "synthetic-private-database-detail"

func diagnosticFailure() error {
	return &pgconn.PgError{Code: "23514", Message: diagnosticSecret, Detail: "password=" + diagnosticSecret}
}
func (d diagnosticDriver) Open(string) (driver.Conn, error)  { return diagnosticConn{d}, nil }
func (c diagnosticConn) Prepare(string) (driver.Stmt, error) { return nil, diagnosticFailure() }
func (c diagnosticConn) Begin() (driver.Tx, error)           { return nil, diagnosticFailure() }
func (c diagnosticConn) Close() error                        { return nil }
func (c diagnosticConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.stage == "query" {
		return nil, diagnosticFailure()
	}
	return &diagnosticRows{diagnosticDriver: c.diagnosticDriver}, nil
}
func (c diagnosticConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, diagnosticFailure()
}
func (r *diagnosticRows) Columns() []string { return []string{"id", "ciphertext"} }
func (r *diagnosticRows) Close() error {
	if r.stage == "close" {
		return diagnosticFailure()
	}
	return nil
}
func (r *diagnosticRows) Next(values []driver.Value) error {
	if r.delivered {
		if r.stage == "iterator" {
			return diagnosticFailure()
		}
		return io.EOF
	}
	r.delivered = true
	values[0], values[1] = "synthetic-row", r.ciphertext
	if r.stage == "scan" {
		values[0] = map[string]string{"private": diagnosticSecret}
	}
	return nil
}

func TestRotationAllDatabaseErrorPathsKeepBoundedDiagnostics(t *testing.T) {
	oldKey, _ := auth.GenerateKey()
	newKey, _ := auth.GenerateKey()
	old, _ := auth.NewEncryptor(oldKey)
	current, _ := auth.NewEncryptor(newKey + "," + oldKey)
	ciphertext, _ := old.Encrypt("synthetic-credential")
	for _, stage := range []string{"query", "scan", "iterator", "close", "update"} {
		t.Run(stage, func(t *testing.T) {
			name := fmt.Sprintf("keyrotate-diagnostic-%d", interruptedDriverSequence.Add(1))
			sql.Register(name, diagnosticDriver{stage: stage, ciphertext: ciphertext})
			db, err := sql.Open(name, "synthetic")
			if err != nil {
				t.Fatal("open synthetic driver")
			}
			defer db.Close()
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			result := rewriteColumn(context.Background(), logger, db, current, target{"synthetic_rows", "id", "ciphertext"}, 2, false)
			if result.failed != 1 || result.rewrote != 0 {
				t.Fatalf("failure reported success: %+v", result)
			}
			text := output.String()
			if strings.Contains(text, diagnosticSecret) || strings.Contains(text, ciphertext) || strings.Contains(text, "synthetic-credential") {
				t.Fatal("rotation emitted private database or ciphertext content")
			}
			if stage != "scan" && !strings.Contains(text, "23514") {
				t.Fatal("bounded SQLSTATE omitted")
			}
			if stage != "update" && result.scanned != 0 {
				t.Fatal("incomplete page counted as scanned")
			}
		})
	}
}

func TestRotationDiagnosticRejectsUnboundedSQLState(t *testing.T) {
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Error("synthetic", rotationErrorAttrs(&pgconn.PgError{Code: diagnosticSecret, Message: diagnosticSecret})...)
	if strings.Contains(output.String(), diagnosticSecret) || strings.Contains(output.String(), "sqlstate") {
		t.Fatal("unvalidated database code emitted")
	}
}
