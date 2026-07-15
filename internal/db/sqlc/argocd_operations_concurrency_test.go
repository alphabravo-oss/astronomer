package sqlc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// This opt-in test exercises the Argo operation state machine against real
// PostgreSQL. Release rehearsals set ARGOCD_OPERATION_CONCURRENCY_TEST_DATABASE_URL
// to a disposable database; the test owns and drops an isolated schema.
func TestArgoCDOperationClaimsAreAtMostOnceAndHAPollSafe(t *testing.T) {
	dsn := os.Getenv("ARGOCD_OPERATION_CONCURRENCY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ARGOCD_OPERATION_CONCURRENCY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const replicas = 3
	connections := make([]*pgx.Conn, replicas)
	for i := range connections {
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		connections[i] = conn
		defer conn.Close(context.Background())
	}

	schema := "argocd_operations_" + uuid.NewString()[:8]
	if _, err := connections[0].Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatal(err)
	}
	defer connections[0].Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, schema))
	for _, conn := range connections {
		if _, err := conn.Exec(ctx, fmt.Sprintf(`SET search_path TO %s`, schema)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := connections[0].Exec(ctx, `
CREATE TABLE argocd_operations (
    id uuid PRIMARY KEY,
    target_type varchar(64) NOT NULL,
    target_key varchar(255) NOT NULL,
    operation_type varchar(32) NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}',
    status varchar(32) NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    started_at timestamptz,
    completed_at timestamptz,
    error_message text NOT NULL DEFAULT '',
    created_by_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    revision varchar(128) NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    operation_id varchar(128) NOT NULL DEFAULT '',
    phase varchar(32) NOT NULL DEFAULT '',
    poll_attempts integer NOT NULL DEFAULT 0,
    last_polled_at timestamptz
)`); err != nil {
		t.Fatal(err)
	}

	id := uuid.New()
	if _, err := connections[0].Exec(ctx, `INSERT INTO argocd_operations
        (id,target_type,target_key,operation_type,status)
        VALUES ($1,'application','self-manage','sync','pending')`, id); err != nil {
		t.Fatal(err)
	}

	claimErrors := runArgoCDOperationRace(replicas, func(i int) error {
		_, err := New(connections[i]).MarkArgoCDOperationRunning(ctx, id)
		return err
	})
	assertOneWinner(t, claimErrors, "dispatch claim")
	var status string
	var attempts int
	if err := connections[0].QueryRow(ctx, `SELECT status,attempt_count FROM argocd_operations WHERE id=$1`, id).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "running" || attempts != 1 {
		t.Fatalf("dispatch state = %s attempts=%d, want running/1", status, attempts)
	}

	pollCounts := make([]int, replicas)
	pollErrors := runArgoCDOperationRace(replicas, func(i int) error {
		rows, err := New(connections[i]).ClaimRunningArgoCDOperationsForPoll(ctx, 10)
		pollCounts[i] = len(rows)
		return err
	})
	for _, err := range pollErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	totalClaims := 0
	for _, count := range pollCounts {
		totalClaims += count
	}
	if totalClaims != 1 {
		t.Fatalf("HA poll claims = %d, want exactly 1", totalClaims)
	}
	rows, err := New(connections[0]).ClaimRunningArgoCDOperationsForPoll(ctx, 10)
	if err != nil || len(rows) != 0 {
		t.Fatalf("poll lease immediately reclaimed: rows=%d err=%v", len(rows), err)
	}
	var polls int
	if err := connections[0].QueryRow(ctx, `SELECT poll_attempts FROM argocd_operations WHERE id=$1`, id).Scan(&polls); err != nil {
		t.Fatal(err)
	}
	if polls != 1 {
		t.Fatalf("poll attempts = %d, want 1 independent of replica count", polls)
	}

	if _, err := New(connections[0]).CompleteArgoCDOperationWithResult(ctx, CompleteArgoCDOperationWithResultParams{ID: id, Phase: "Succeeded", Revision: "deadbeef"}); err != nil {
		t.Fatal(err)
	}
	if _, err := New(connections[1]).UpdateArgoCDOperationProgress(ctx, UpdateArgoCDOperationProgressParams{ID: id, Phase: "Running"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("late progress error = %v, want pgx.ErrNoRows", err)
	}
	if _, err := New(connections[2]).FailArgoCDOperationWithResult(ctx, FailArgoCDOperationWithResultParams{ID: id, Phase: "Failed", ErrorMessage: "late"}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("late failure error = %v, want pgx.ErrNoRows", err)
	}
	var phase string
	if err := connections[0].QueryRow(ctx, `SELECT status,phase FROM argocd_operations WHERE id=$1`, id).Scan(&status, &phase); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || phase != "Succeeded" {
		t.Fatalf("terminal state resurrected: %s/%s", status, phase)
	}
}

func runArgoCDOperationRace(workers int, call func(int) error) []error {
	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = call(index)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

func assertOneWinner(t *testing.T, errs []error, operation string) {
	t.Helper()
	winners := 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, pgx.ErrNoRows):
		default:
			t.Fatalf("%s unexpected error: %v", operation, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%s winners = %d, want exactly 1", operation, winners)
	}
}
