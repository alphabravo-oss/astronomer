package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/jackc/pgx/v5/pgconn"
)

// This driver models a valid first row followed by a connection/iterator error.
// A partial page must never be interpreted as completed rotation evidence.
type interruptedDriver struct {
	ciphertext string
	queryError error
}

var interruptedDriverSequence atomic.Uint64

type interruptedConn struct {
	ciphertext string
	queryError error
}
type interruptedRows struct {
	ciphertext string
	delivered  bool
}

func (d interruptedDriver) Open(string) (driver.Conn, error) {
	return interruptedConn{d.ciphertext, d.queryError}, nil
}
func (c interruptedConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c interruptedConn) Close() error { return nil }
func (c interruptedConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c interruptedConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.queryError != nil {
		return nil, c.queryError
	}
	return &interruptedRows{ciphertext: c.ciphertext}, nil
}
func (r *interruptedRows) Columns() []string { return []string{"id", "ciphertext"} }
func (r *interruptedRows) Close() error      { return nil }
func (r *interruptedRows) Next(values []driver.Value) error {
	if r.delivered {
		return errors.New("synthetic iterator interruption")
	}
	r.delivered = true
	values[0], values[1] = "synthetic-row", r.ciphertext
	return nil
}

func TestRotationRejectsIncompleteCiphertextPage(t *testing.T) {
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal("generate synthetic key")
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal("initialize synthetic encryptor")
	}
	ciphertext, err := enc.Encrypt("synthetic")
	if err != nil {
		t.Fatal("encrypt synthetic value")
	}
	name := fmt.Sprintf("keyrotate-interrupted-page-%d", interruptedDriverSequence.Add(1))
	sql.Register(name, interruptedDriver{ciphertext: ciphertext})
	db, err := sql.Open(name, "synthetic")
	if err != nil {
		t.Fatal("open synthetic iterator")
	}
	defer db.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	result := rewriteColumn(context.Background(), log, db, enc,
		target{"synthetic_rows", "id", "ciphertext"}, 2, false)
	if result.failed != 1 || result.scanned != 0 || result.rewrote != 0 || result.skipped != 0 {
		t.Fatalf("incomplete page reported rotation progress: %+v", result)
	}
}

func TestRotationDatabaseErrorLogsExcludeUntrustedContent(t *testing.T) {
	const secret = "synthetic-rotation-private-value"
	name := fmt.Sprintf("keyrotate-interrupted-page-%d", interruptedDriverSequence.Add(1))
	sql.Register(name, interruptedDriver{queryError: &pgconn.PgError{Code: "23514", Message: secret, Detail: "password_encrypted=" + secret}})
	db, err := sql.Open(name, "synthetic")
	if err != nil {
		t.Fatal("open synthetic error driver")
	}
	defer db.Close()
	var output bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&output, nil))
	result := rewriteColumn(context.Background(), log, db, nil, target{"synthetic_rows", "id", "ciphertext"}, 2, false)
	if result.failed != 1 {
		t.Fatalf("database failure progress=%+v", result)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("rotation emitted untrusted database error content")
	}
	if !strings.Contains(output.String(), "23514") {
		t.Fatal("rotation lost bounded SQLSTATE diagnosis")
	}
}
