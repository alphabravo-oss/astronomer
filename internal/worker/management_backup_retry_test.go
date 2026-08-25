package worker

import (
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
)

func TestManagementBackupObservationUsesBoundedRetryCadence(t *testing.T) {
	task, err := tasks.NewManagementBackupOperationTask(uuid.UUID{1})
	if err != nil {
		t.Fatal(err)
	}
	if got := retryDelay(19, errors.New("backup_in_progress"), task); got != managementBackupObservationRetryDelay {
		t.Fatalf("retry delay = %s, want %s", got, managementBackupObservationRetryDelay)
	}
}
