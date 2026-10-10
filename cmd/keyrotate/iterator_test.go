package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
)

// This driver models a valid first row followed by a connection/iterator error.
// A partial page must never be interpreted as completed rotation evidence.
type interruptedDriver struct{ ciphertext string }
type interruptedConn struct{ ciphertext string }
type interruptedRows struct {
	ciphertext string
	delivered  bool
}

func (d interruptedDriver) Open(string) (driver.Conn, error) {
	return interruptedConn{d.ciphertext}, nil
}
func (c interruptedConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c interruptedConn) Close() error { return nil }
func (c interruptedConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c interruptedConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
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
	sql.Register("keyrotate-interrupted-page", interruptedDriver{ciphertext})
	db, err := sql.Open("keyrotate-interrupted-page", "synthetic")
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
