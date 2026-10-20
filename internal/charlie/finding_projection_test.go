package charlie

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie/contract"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
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

func TestFindingProjectionResourceEligibilitySkipsUnboundCentralFinding(t *testing.T) {
	resources := []sqlc.CharlieSessionResource{{ResourceType: "installation", ResourceID: "local", RequiredVerb: "read"}}
	if _, ok := findingProjectionResourceEligible(resources, findingResourceDigest("unbound-central-resource")); ok {
		t.Fatal("unbound Central finding resource was accepted")
	}
	resource, ok := findingProjectionResourceEligible(resources, findingResourceDigest("local"))
	if !ok || resource.ResourceID != "local" {
		t.Fatalf("exact local resource=%#v ok=%t", resource, ok)
	}
}

func TestProjectedFindingRepeatCountBounds(t *testing.T) {
	for _, tc := range []struct {
		count int
		valid bool
	}{{1, true}, {2147483647, true}, {2147483648, false}, {4294967297, false}, {0, false}, {-1, false}} {
		change := contract.FindingChange{FindingId: "finding-a", Summary: &contract.FindingProjectionSummary{
			FindingId: "finding-a", SessionId: "session-a", InvestigationId: "investigation-a", DeduplicationKey: strings.Repeat("a", 64),
			ResourceDigest: strings.Repeat("b", 64), RecommendedCapability: "cluster.read", RepeatCount: tc.count,
			Severity: "medium", Status: "open", WorkflowState: "manual_remediation_required", BlockCode: "read_only", UpdatedAt: time.Now(),
		}}
		got, err := projectedFindingSummary(change)
		if (err == nil) != tc.valid {
			t.Fatalf("count=%d: %v", tc.count, err)
		}
		if tc.valid && int(got.RepeatCount) != tc.count {
			t.Fatalf("count changed: %d -> %d", tc.count, got.RepeatCount)
		}
	}
}
