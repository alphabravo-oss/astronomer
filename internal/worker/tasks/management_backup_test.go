package tasks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestManagementBackupTasksCarryIdentifiersOnly(t *testing.T) {
	destinationID := uuid.New()
	task, err := NewManagementBackupReconcileTask(destinationID, 7)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 2 || payload["destination_id"] != destinationID.String() || payload["generation"] != float64(7) {
		t.Fatalf("unexpected reconcile payload: %#v", payload)
	}
	encoded := string(task.Payload())
	for _, forbidden := range []string{"credential", "access_key", "secret_key", "bucket", "endpoint"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("task payload contains forbidden field %q: %s", forbidden, encoded)
		}
	}
}
