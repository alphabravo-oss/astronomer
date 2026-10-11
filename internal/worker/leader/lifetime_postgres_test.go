package leader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// This test only terminates the dedicated session it creates. It does not
// modify application tables or terminate unrelated database connections.
func TestPostgresLeadershipSessionLoss(t *testing.T) {
	dsn := os.Getenv("LEADER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("LEADER_TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	applicationName := fmt.Sprintf("leader-session-test-%d", time.Now().UnixNano())
	config.ConnConfig.RuntimeParams["application_name"] = applicationName
	config.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	e := New(pool, nil)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := e.RunWhileLeader(ctx, t.Name(), func(lease context.Context) error {
			close(started)
			<-lease.Done()
			return lease.Err()
		})
		result <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	competitor := New(admin, nil)
	release, held, err := competitor.TryLeader(ctx, t.Name())
	if held {
		release()
	}
	if err != nil || held {
		t.Fatalf("competing leader acquired live lock: held=%v err=%v", held, err)
	}
	var pid uint32
	if err := admin.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE application_name=$1 AND datname=current_database()`, applicationName).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrLeadershipLost) {
			t.Fatalf("loss = %v", err)
		}
	case <-ctx.Done():
		t.Fatal("old leader did not stop")
	}
	release, held, err = competitor.TryLeader(ctx, t.Name())
	if err != nil || !held {
		t.Fatalf("cannot reacquire after loss: held=%v err=%v", held, err)
	}
	release()
}
