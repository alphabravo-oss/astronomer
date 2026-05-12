package migrations_test

// Static content test for migration 055_siem_forwarders.
//
// As with the other migration_*_test.go siblings, we DO NOT run the
// migration against Postgres — the CI helm-test path covers that via
// the migrate-job container. What we check here is the SHAPE of the
// SQL, so an unrelated future edit can't quietly:
//
//   - Drop the FK ON DELETE CASCADE on forwarder_id. Without it, deleting
//     a forwarder would either fail or leave dangling queue rows.
//   - Drop the (forwarder_id, id) index. The dispatcher's batch scan
//     uses it; losing the index would degrade into a seq-scan on a
//     potentially large queue table.
//   - Swap the auth_encrypted column to something narrower than TEXT.
//     Fernet ciphertext grows with plaintext + 57 bytes overhead so
//     clipping would silently truncate a long token.
//   - Forget the CHECK constraint on transport. The constraint is the
//     contract surface for the dispatcher dispatch table — a stray
//     "syslogd" or typoed value should fail at INSERT time, not at
//     runtime when the worker tries to find a sender.

import (
	"strings"
	"testing"
)

func TestMigration_SIEMForwarders_UpContent(t *testing.T) {
	up := loadMigrationFile(t, "055_siem_forwarders.up.sql")

	for _, want := range []string{
		"CREATE TABLE siem_forwarders",
		"CREATE TABLE siem_forward_queue",
		"CREATE TABLE siem_forwarder_status",
		// Encrypted-at-rest auth blob.
		"auth_encrypted  TEXT NOT NULL DEFAULT ''",
		// Filter shape: a JSON array of fnmatch globs.
		"event_filters   JSONB NOT NULL DEFAULT '[]'",
		// Connection knobs with safe defaults so re-runs don't break.
		"tls_skip_verify BOOLEAN NOT NULL DEFAULT false",
		"batch_size      INTEGER NOT NULL DEFAULT 100",
		"flush_interval_ms INTEGER NOT NULL DEFAULT 2000",
		"timeout_seconds INTEGER NOT NULL DEFAULT 10",
		// Transport allow-list — enforced at INSERT.
		"CONSTRAINT transport_valid CHECK (transport IN ('syslog_udp','syslog_tcp','syslog_tls','splunk_hec','ndjson_https'))",
		// Forwarder deletion must cascade to its queue + status rows.
		"REFERENCES siem_forwarders(id) ON DELETE CASCADE",
		// BIGSERIAL — queue can churn millions of rows over a day on a
		// busy stack; INTEGER would wrap.
		"id              BIGSERIAL PRIMARY KEY",
		// Dispatcher batch index.
		"CREATE INDEX idx_siem_forward_queue_forwarder ON siem_forward_queue (forwarder_id, id)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_SIEMForwarders_DownContent(t *testing.T) {
	down := loadMigrationFile(t, "055_siem_forwarders.down.sql")

	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_siem_forward_queue_forwarder",
		"DROP TABLE IF EXISTS siem_forwarder_status",
		"DROP TABLE IF EXISTS siem_forward_queue",
		"DROP TABLE IF EXISTS siem_forwarders",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing required text %q", want)
		}
	}

	// Order: drop child tables (CASCADE-referencing siem_forwarders)
	// before the parent. Not strictly required because of CASCADE, but
	// the explicit form keeps the rollback symmetric.
	posStatus := strings.Index(down, "DROP TABLE IF EXISTS siem_forwarder_status")
	posQueue := strings.Index(down, "DROP TABLE IF EXISTS siem_forward_queue")
	posForwarders := strings.Index(down, "DROP TABLE IF EXISTS siem_forwarders")
	if posStatus < 0 || posQueue < 0 || posForwarders < 0 {
		t.Fatalf("missing one of the expected DROP statements")
	}
	if posForwarders < posStatus || posForwarders < posQueue {
		t.Errorf("siem_forwarders dropped before its children; rollback would log warnings")
	}
}
