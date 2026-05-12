package migrations_test

// Static content test for migration 048_webhook_subscriptions.
//
// We don't run the migration against a real Postgres here — the helm-test
// path in CI handles that. What we DO check is the shape of the SQL so a
// well-meaning future edit can't:
//
//   - Drop the partial index on pending deliveries (the dispatcher's
//     hot path scans this every 15s, on every replica).
//   - Drop the unique index on (name) — uniqueness keeps the admin
//     wizard's "name already taken" check correct.
//   - Forget the ON DELETE CASCADE that wipes delivery rows when an
//     operator deletes the subscription.
//   - Switch the encrypted column from TEXT to something narrower —
//     Fernet ciphertext grows with the plaintext + 57 byte overhead and
//     would clip an oversized secret silently.

import (
	"strings"
	"testing"
)

func TestMigration_WebhookSubscriptions_UpContent(t *testing.T) {
	up := loadMigrationFile(t, "048_webhook_subscriptions.up.sql")

	for _, want := range []string{
		"CREATE TABLE webhook_subscriptions",
		"CREATE TABLE webhook_deliveries",
		// Encrypted-at-rest HMAC signing secret.
		"secret_encrypted TEXT NOT NULL",
		// Filter shape: a JSON array of fnmatch globs.
		"event_filters   JSONB NOT NULL DEFAULT '[]'",
		// Optional Go template applied to the event payload.
		"payload_template TEXT NOT NULL DEFAULT ''",
		// Default headers + per-subscription retry tunables.
		"extra_headers   JSONB NOT NULL DEFAULT '{}'",
		"max_retries     INTEGER NOT NULL DEFAULT 5",
		"timeout_seconds INTEGER NOT NULL DEFAULT 10",
		// Subscription deletion must cascade to deliveries so the admin
		// delete handler doesn't have to do that explicitly.
		"REFERENCES webhook_subscriptions(id) ON DELETE CASCADE",
		// Unique name keeps the wizard's "name already taken" check
		// honest.
		"CREATE UNIQUE INDEX uidx_webhook_subscriptions_name",
		// Recent-deliveries hot path on the admin view.
		"CREATE INDEX idx_webhook_deliveries_sub_recent",
		// Dispatcher partial index — keeps the pending scan O(pending)
		// even after millions of delivered rows accumulate.
		"CREATE INDEX idx_webhook_deliveries_pending",
		"WHERE status IN ('queued', 'failed')",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing required text %q", want)
		}
	}
}

func TestMigration_WebhookSubscriptions_DownContent(t *testing.T) {
	down := loadMigrationFile(t, "048_webhook_subscriptions.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS webhook_deliveries",
		"DROP TABLE IF EXISTS webhook_subscriptions",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("down migration missing %q", want)
		}
	}
}
