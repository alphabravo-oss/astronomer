package charlie

import (
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestFindingProjectionSessionEligibilitySkipsNonApplicableHistory(t *testing.T) {
	connectionID := uuid.New()
	for _, test := range []struct {
		name    string
		session sqlc.CharlieSession
		err     error
		want    bool
		wantErr bool
	}{
		{name: "current", session: sqlc.CharlieSession{ConnectionID: connectionID, State: "active"}, want: true},
		{name: "missing", err: pgx.ErrNoRows},
		{name: "superseded connection", session: sqlc.CharlieSession{ConnectionID: uuid.New(), State: "active"}},
		{name: "aborted", session: sqlc.CharlieSession{ConnectionID: connectionID, State: "aborted"}},
		{name: "failed", session: sqlc.CharlieSession{ConnectionID: connectionID, State: "failed"}},
		{name: "database failure", err: errors.New("database unavailable"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := findingProjectionSessionEligible(connectionID, test.session, test.err)
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("eligible=%t err=%v", got, err)
			}
		})
	}
}
