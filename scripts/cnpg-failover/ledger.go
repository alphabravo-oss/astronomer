package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ledger struct {
	pool   *pgxpool.Pool
	schema string
}

func (l ledger) create(ctx context.Context) error {
	_, err := l.pool.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s;
CREATE TABLE %s.ledger (sequence bigint PRIMARY KEY, tenant text NOT NULL,
 request_key text NOT NULL, payload_digest text NOT NULL, UNIQUE(tenant,request_key));
CREATE TABLE %s.audit (sequence bigint PRIMARY KEY REFERENCES %s.ledger(sequence),
 tenant text NOT NULL, previous_digest text NOT NULL, digest text NOT NULL);
COMMENT ON SCHEMA %s IS 'Astronomer Plan032 W12 owned synthetic failover fixture';`,
		l.schema, l.schema, l.schema, l.schema, l.schema))
	return err
}

func record(sequence int) (tenant, key, payload, previous, audit string) {
	tenant = fmt.Sprintf("synthetic-tenant-%d", sequence%2)
	key = fmt.Sprintf("idempotency-%d", sequence)
	payload = digest(fmt.Sprintf("%s:%s:synthetic-payload", tenant, key))
	previous = auditDigest(sequence - 1)
	audit = digest(fmt.Sprintf("%d:%s:%s:%s", sequence, tenant, previous, payload))
	return
}

func auditDigest(sequence int) string {
	previous := digest("owned-synthetic-audit-genesis")
	for current := 1; current <= sequence; current++ {
		tenant := fmt.Sprintf("synthetic-tenant-%d", current%2)
		payload := digest(fmt.Sprintf("%s:idempotency-%d:synthetic-payload", tenant, current))
		previous = digest(fmt.Sprintf("%d:%s:%s:%s", current, tenant, previous, payload))
	}
	return previous
}

func digest(value string) string {
	bytes := sha256.Sum256([]byte(value))
	return hex.EncodeToString(bytes[:])
}

func (l ledger) write(ctx context.Context, sequence int) (string, error) {
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var primary string
	err = tx.QueryRow(ctx, `SELECT inet_server_addr()::text || ':' || pg_postmaster_start_time()::text WHERE NOT pg_is_in_recovery()`).Scan(&primary)
	if err != nil {
		return "", err
	}
	tenant, key, payload, previous, audit := record(sequence)
	// A deliberate repeat of the same write in every transaction proves that
	// idempotency and the paired audit insert remain atomic through reconnect.
	for range 2 {
		_, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.ledger VALUES ($1,$2,$3,$4) ON CONFLICT (tenant,request_key) DO NOTHING`, l.schema), sequence, tenant, key, payload)
		if err != nil {
			return "", err
		}
		_, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.audit VALUES ($1,$2,$3,$4) ON CONFLICT (sequence) DO NOTHING`, l.schema), sequence, tenant, previous, audit)
		if err != nil {
			return "", err
		}
	}
	return primary, tx.Commit(ctx)
}

func (l ledger) verify(ctx context.Context, acknowledged int) (map[string]bool, int, error) {
	rows, err := l.pool.Query(ctx, fmt.Sprintf(`SELECT l.sequence,l.tenant,l.request_key,l.payload_digest,a.tenant,a.previous_digest,a.digest
FROM %s.ledger l LEFT JOIN %s.audit a USING(sequence) ORDER BY l.sequence`, l.schema, l.schema))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	count, correct := 0, true
	for rows.Next() {
		var sequence int
		var tenant, key, payload, auditTenant, previous, audit string
		if err := rows.Scan(&sequence, &tenant, &key, &payload, &auditTenant, &previous, &audit); err != nil {
			return nil, 0, err
		}
		count++
		t, k, p, prev, a := record(sequence)
		correct = correct && sequence == count && tenant == t && auditTenant == t && key == k && payload == p && previous == prev && audit == a
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var audits int
	if err := l.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s.audit", l.schema)).Scan(&audits); err != nil {
		return nil, 0, err
	}
	lost := max(0, acknowledged-count)
	return map[string]bool{"acknowledged-rows-preserved": lost == 0, "exact-ledger-count": count == acknowledged,
		"tenant-idempotency-payload-audit-integrity": correct, "exact-audit-count": audits == acknowledged}, lost, nil
}
