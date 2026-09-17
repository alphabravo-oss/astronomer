package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestNotificationDeliveryIdentityMigrationIsReversibleAndUnique(t *testing.T) {
	up, err := os.ReadFile("048_notification_delivery_identity.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("048_notification_delivery_identity.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	upSQL := strings.ToLower(string(up))
	downSQL := strings.ToLower(string(down))
	for _, fragment := range []string{"add column dedupe_key", "create unique index", "where dedupe_key is not null"} {
		if !strings.Contains(upSQL, fragment) {
			t.Fatalf("up migration missing %q", fragment)
		}
	}
	for _, fragment := range []string{"drop index", "drop column"} {
		if !strings.Contains(downSQL, fragment) {
			t.Fatalf("down migration missing %q", fragment)
		}
	}
}
