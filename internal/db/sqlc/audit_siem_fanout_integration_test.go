package sqlc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuditOutboxDeliveryDurablyFansOutToMatchingSIEMForwarders(t *testing.T) {
	dsn := os.Getenv("AUDIT_OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUDIT_OUTBOX_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	eventID := uuid.New()
	now := time.Now().UTC()
	prefix := "audit-fanout-" + eventID.String()
	forwarders := []struct {
		id      uuid.UUID
		filters string
		enabled bool
	}{
		{uuid.New(), `["audit.*"]`, true},
		{uuid.New(), `["cluster.*, audit.cluster.?elete"]`, true},
		{uuid.New(), `[]`, true},
		{uuid.New(), `["audit.project.*"]`, true},
		{uuid.New(), `["audit.*"]`, false},
	}
	for index, forwarder := range forwarders {
		_, err = pool.Exec(ctx, `
			INSERT INTO siem_forwarders
				(id, name, transport, endpoint, event_filters, enabled)
			VALUES ($1, $2, 'ndjson_https', 'https://siem.example.test/events', $3::jsonb, $4)`,
			forwarder.id, fmt.Sprintf("%s-%d", prefix, index), forwarder.filters, forwarder.enabled)
		if err != nil {
			t.Fatalf("insert forwarder %d: %v", index, err)
		}
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM siem_forwarders WHERE name LIKE $1`, prefix+"%")
		_, _ = pool.Exec(ctx, `DELETE FROM audit_log WHERE id = $1`, eventID)
		_, _ = pool.Exec(ctx, `DELETE FROM audit_outbox WHERE id = $1`, eventID)
	}()

	_, err = pool.Exec(ctx, `
		INSERT INTO audit_outbox (
			id, dedupe_key, event_created_at, action, resource_type,
			resource_id, resource_name, status, attempt_count, locked_until, detail
		) VALUES (
			$1, $2, $3::timestamptz, 'cluster.delete', 'cluster', 'cluster-1', 'production',
			'delivering', 1, $3::timestamptz + interval '2 minutes', '{"phase":"committed"}'::jsonb
		)`, eventID, prefix, now)
	if err != nil {
		t.Fatalf("insert outbox: %v", err)
	}

	queries := New(pool)
	deliver := func() {
		t.Helper()
		if _, err := queries.DeliverAuditOutbox(ctx, DeliverAuditOutboxParams{
			OutboxID:    eventID,
			DeliveredAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}); err != nil {
			t.Fatalf("deliver audit outbox: %v", err)
		}
	}
	deliver()

	var receiptCount int
	var payload string
	err = pool.QueryRow(ctx, `
		SELECT count(*), (array_agg(payload ORDER BY id))[1]::text
		FROM siem_forward_queue
		WHERE dedupe_key = $1`, "audit:"+eventID.String()).Scan(&receiptCount, &payload)
	if err != nil {
		t.Fatalf("read SIEM receipts: %v", err)
	}
	if receiptCount != 3 {
		t.Fatalf("SIEM receipt count = %d, want 3 matching enabled forwarders", receiptCount)
	}
	var envelope struct {
		EventName string `json:"event_name"`
		EventID   string `json:"event_id"`
		Detail    struct {
			ResourceID string          `json:"resource_id"`
			Detail     json.RawMessage `json:"detail"`
		} `json:"detail"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		t.Fatalf("decode SIEM envelope: %v", err)
	}
	if envelope.EventName != "audit.cluster.delete" || envelope.EventID != eventID.String() || envelope.Detail.ResourceID != "cluster-1" {
		t.Fatalf("unexpected SIEM envelope: %#v", envelope)
	}

	// Simulate a crash after the statement committed but before the worker
	// observed its result. Lease recovery replays the same outbox row; the
	// per-forwarder stable key must preserve exactly one receipt each.
	_, err = pool.Exec(ctx, `
		UPDATE audit_outbox
		SET status = 'delivering', delivered_at = NULL,
			locked_until = now() + interval '2 minutes'
		WHERE id = $1`, eventID)
	if err != nil {
		t.Fatalf("reset outbox for replay: %v", err)
	}
	deliver()
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM siem_forward_queue WHERE dedupe_key = $1`,
		"audit:"+eventID.String()).Scan(&receiptCount); err != nil {
		t.Fatalf("count replayed SIEM receipts: %v", err)
	}
	if receiptCount != 3 {
		t.Fatalf("SIEM receipt count after replay = %d, want 3", receiptCount)
	}

	// Mandatory receipts remain protected from both retry exhaustion and the
	// ordinary seven-day queue retention policy. A disposable product event in
	// the same forwarder proves the cleanup paths still make progress.
	_, err = pool.Exec(ctx, `
		UPDATE siem_forward_queue
		SET attempts = 101, created_at = now() - interval '8 days'
		WHERE dedupe_key = $1`, "audit:"+eventID.String())
	if err != nil {
		t.Fatalf("age mandatory SIEM receipts: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO siem_forward_queue (
			forwarder_id, event_name, payload, severity, attempts, created_at
		) VALUES (
			$1, 'cluster.changed', '{}'::jsonb, 'info', 101,
			now() - interval '8 days'
		)`, forwarders[0].id)
	if err != nil {
		t.Fatalf("age SIEM receipts: %v", err)
	}
	exhausted, err := queries.ListSIEMQueueExhausted(ctx, ListSIEMQueueExhaustedParams{
		ForwarderID: forwarders[0].id,
		Attempts:    100,
		Limit:       100,
	})
	if err != nil {
		t.Fatalf("list exhausted SIEM receipts: %v", err)
	}
	if len(exhausted) != 1 || exhausted[0].DedupeKey.Valid {
		t.Fatalf("exhausted rows = %#v, want only disposable event", exhausted)
	}
	removed, err := queries.DeleteSIEMQueueOlderThan(ctx, time.Now().UTC().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("delete old SIEM rows: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed rows = %d, want one disposable event", removed)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM siem_forward_queue WHERE dedupe_key = $1`,
		"audit:"+eventID.String()).Scan(&receiptCount); err != nil {
		t.Fatalf("count retained audit receipts: %v", err)
	}
	if receiptCount != 3 {
		t.Fatalf("retained audit receipt count = %d, want 3", receiptCount)
	}
}
