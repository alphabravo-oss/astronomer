package sqlc

import (
	"strings"
	"testing"
)

func TestDeliverAuditOutboxPersistsAndAcknowledgesAtomically(t *testing.T) {
	for _, fragment := range []string{
		"WITH source AS (",
		"INSERT INTO audit_log",
		"ON CONFLICT (id, created_at) DO NOTHING",
		"UPDATE audit_outbox AS o",
		"status = 'delivered'",
		"SELECT id, dedupe_key",
	} {
		if !strings.Contains(deliverAuditOutbox, fragment) {
			t.Errorf("atomic audit delivery query missing %q", fragment)
		}
	}
}

func TestClaimAuditOutboxUsesLeaseAndSkipLocked(t *testing.T) {
	for _, fragment := range []string{
		"FOR UPDATE SKIP LOCKED",
		"locked_until IS NULL OR locked_until <=",
		"attempt_count = attempt_count + 1",
		"status = 'delivering'",
	} {
		if !strings.Contains(claimDueAuditOutbox, fragment) {
			t.Errorf("audit claim query missing %q", fragment)
		}
	}
}

func TestAuditOutboxFailureAndSIEMDeliveryAreDeduplicated(t *testing.T) {
	if !strings.Contains(markAuditOutboxFailed, "attempt_count >= max_attempts") ||
		!strings.Contains(markAuditOutboxFailed, "THEN 'dead'") {
		t.Fatal("audit outbox failure query must retain an explicit dead-letter state")
	}
	for _, fragment := range []string{
		"ON CONFLICT (forwarder_id, dedupe_key)",
		"WHERE dedupe_key IS NOT NULL",
	} {
		if !strings.Contains(enqueueSIEMEventDeduped, fragment) {
			t.Errorf("SIEM dedupe query missing %q", fragment)
		}
	}
}
