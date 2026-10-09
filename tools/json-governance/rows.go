package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/jackc/pgx/v5"
)

type rowIssue struct {
	Table    string `json:"table"`
	ID       string `json:"id"`
	Category string `json:"category"`
}
type rowReceipt struct {
	SchemaVersion        int        `json:"schema_version"`
	DatabaseSchema       int        `json:"database_schema"`
	PostgresMajor        int        `json:"postgres_major"`
	ReadOnly             bool       `json:"read_only"`
	Checked              int        `json:"checked"`
	Invalid              int        `json:"invalid"`
	Issues               []rowIssue `json:"issues"`
	DiagnosticsTruncated bool       `json:"diagnostics_truncated"`
}
type rowSpec struct {
	table    string
	columns  []string
	validate func([][]byte) error
}

func rowSpecs() []rowSpec {
	return []rowSpec{
		{"alert_inhibitions", []string{"source_matchers", "target_matchers", "equal_labels"}, func(v [][]byte) error { _, err := jsoncontract.ReadInhibitionV1(v[0], v[1], v[2]); return err }},
		{"task_outbox", []string{"payload"}, func(v [][]byte) error { _, err := jsoncontract.ReadTaskPayloadV1(v[0]); return err }},
		{"audit_outbox", []string{"detail"}, func(v [][]byte) error { _, err := jsoncontract.ReadAuditDetailV1(v[0]); return err }},
		{"audit_log", []string{"detail"}, func(v [][]byte) error { _, err := jsoncontract.ReadAuditDetailV1(v[0]); return err }},
		{"logging_pipelines", []string{"labels"}, func(v [][]byte) error { _, err := jsoncontract.ReadStringMapV1("labels", v[0]); return err }},
		{"delivery_targets", []string{"placement"}, func(v [][]byte) error {
			_, err := model.ReadPlacementV1(v[0])
			return err
		}},
		{"delivery_rollouts", []string{"frozen_plan"}, func(v [][]byte) error {
			_, err := rollout.ReadFrozenRolloutV1(v[0])
			return err
		}},
		{"delivery_system_rollouts", []string{"strategy"}, func(v [][]byte) error {
			_, err := model.ReadRolloutStrategyV1(v[0])
			return err
		}},
	}
}

func inventoryRows(ctx context.Context, connection *pgx.Conn) (rowReceipt, error) {
	result := rowReceipt{SchemaVersion: 1, Issues: []rowIssue{}}
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return result, errors.New("cannot start read-only inventory")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var readOnly string
	if err := tx.QueryRow(ctx, "SHOW transaction_read_only").Scan(&readOnly); err != nil || readOnly != "on" {
		return result, errors.New("inventory transaction is not read-only")
	}
	result.ReadOnly = true
	if err := tx.QueryRow(ctx, "SELECT version FROM public.schema_migrations").Scan(&result.DatabaseSchema); err != nil {
		return result, errors.New("cannot identify inventory schema")
	}
	if err := tx.QueryRow(ctx, "SELECT current_setting('server_version_num')::int / 10000").Scan(&result.PostgresMajor); err != nil {
		return result, errors.New("cannot identify inventory PostgreSQL major")
	}
	for _, spec := range rowSpecs() {
		columns := []string{"id::text"}
		for _, column := range spec.columns {
			columns = append(columns, pgx.Identifier{column}.Sanitize())
		}
		rows, err := tx.Query(ctx, "SELECT "+strings.Join(columns, ",")+" FROM "+pgx.Identifier{"public", spec.table}.Sanitize()+" ORDER BY id")
		if err != nil {
			return result, fmt.Errorf("cannot inventory %s", spec.table)
		}
		for rows.Next() {
			var id string
			raw := make([][]byte, len(spec.columns))
			destinations := []any{&id}
			for i := range raw {
				destinations = append(destinations, &raw[i])
			}
			if err := rows.Scan(destinations...); err != nil {
				rows.Close()
				return result, fmt.Errorf("cannot read %s inventory row", spec.table)
			}
			result.Checked++
			if err := spec.validate(raw); err != nil {
				result.Invalid++
				category := "invalid_domain_schema"
				var schemaErr *jsoncontract.Error
				if errors.As(err, &schemaErr) {
					category = schemaErr.Field + ":" + schemaErr.Category
				}
				if len(result.Issues) < 1000 {
					result.Issues = append(result.Issues, rowIssue{spec.table, id, category})
				} else {
					result.DiagnosticsTruncated = true
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, fmt.Errorf("cannot finish %s inventory", spec.table)
		}
	}
	return result, tx.Commit(ctx)
}

func runRowInventory() error {
	// Tool-only environment input keeps credentials out of argv and receipts.
	dsn := os.Getenv("ASTRONOMER_JSON_INVENTORY_DATABASE_URL")
	if dsn == "" {
		return errors.New("ASTRONOMER_JSON_INVENTORY_DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return errors.New("cannot connect to inventory database")
	}
	defer func() { _ = conn.Close(context.Background()) }()
	result, err := inventoryRows(ctx, conn)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
