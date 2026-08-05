package charlie

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type findingStoreFake struct {
	connection  sqlc.CharlieConnection
	prior       sqlc.CharlieFinding
	priorErr    error
	upsert      sqlc.UpsertCharlieFindingParams
	upserts     []sqlc.UpsertCharlieFindingParams
	row         sqlc.CharlieFinding
	resource    sqlc.AddCharlieFindingResourceParams
	resourceErr error
}

func (f *findingStoreFake) AddCharlieFindingResource(_ context.Context, p sqlc.AddCharlieFindingResourceParams) error {
	f.resource = p
	return f.resourceErr
}

func (f *findingStoreFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	return f.connection, nil
}
func (f *findingStoreFake) GetActiveCharlieFindingByFingerprint(context.Context, sqlc.GetActiveCharlieFindingByFingerprintParams) (sqlc.CharlieFinding, error) {
	return f.prior, f.priorErr
}
func (f *findingStoreFake) UpsertCharlieFinding(_ context.Context, p sqlc.UpsertCharlieFindingParams) (sqlc.CharlieFinding, error) {
	f.upsert = p
	f.upserts = append(f.upserts, p)
	return f.row, nil
}

func TestDBFindingStoreDeduplicatesRedactsAndAlertsAfterDurability(t *testing.T) {
	now := time.Unix(10000, 0).UTC()
	connection := readySessionConnection()
	fake := &findingStoreFake{
		connection: connection, priorErr: pgx.ErrNoRows,
		row: sqlc.CharlieFinding{ID: uuid.New(), DedupeFingerprint: stableFingerprint("finding"), Status: "open", RepeatCount: 1, UpdatedAt: now},
	}
	store, _ := NewDBFindingStore(fake)
	store.now = func() time.Time { return now }
	input := FindingInput{InstallationID: connection.InstallationID.String(), Severity: "critical", Mode: ModeReadOnly, RiskImpact: "token=secret could affect availability"}
	recommendation := FindingRecommendation{Title: "password=secret outage", Summary: "api_key=secret", RecommendedAction: "inspect safely", Verification: "recheck health", ExecutionBlockCode: DeniedReadOnlyWrite}
	durable, err := store.UpsertBlockedFinding(context.Background(), input, recommendation, fake.row.DedupeFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !durable.Notify || durable.RepeatCount != 1 || fake.upsert.ConnectionID != connection.ID || fake.upsert.EffectiveMode != string(ModeReadOnly) {
		t.Fatalf("unexpected durable finding: %#v upsert=%#v", durable, fake.upsert)
	}
	if fake.resource.FindingID != fake.row.ID || fake.resource.ResourceType != input.ResourceType || fake.resource.ResourceID != input.ResourceID || fake.resource.RequiredVerb != "read" {
		t.Fatalf("finding resource authorization scope was not persisted: %#v", fake.resource)
	}
	for _, value := range []string{fake.upsert.Title, fake.upsert.Summary, fake.upsert.RiskImpact} {
		if containsSecret(value) {
			t.Fatalf("finding persistence retained secret sentinel: %q", value)
		}
	}
}

func TestDBFindingStoreSuppressesRepeatAlertInsideCooldown(t *testing.T) {
	now := time.Unix(20000, 0).UTC()
	connection := readySessionConnection()
	fingerprint := stableFingerprint("same")
	fake := &findingStoreFake{
		connection: connection,
		prior:      sqlc.CharlieFinding{Severity: "warning", UpdatedAt: now.Add(-time.Minute)},
		row:        sqlc.CharlieFinding{ID: uuid.New(), DedupeFingerprint: fingerprint, Status: "open", RepeatCount: 4, UpdatedAt: now},
	}
	store, _ := NewDBFindingStore(fake)
	store.now = func() time.Time { return now }
	durable, err := store.UpsertBlockedFinding(context.Background(), FindingInput{InstallationID: connection.InstallationID.String(), Severity: "warning", Mode: ModeApproval}, FindingRecommendation{ExecutionBlockCode: DeniedApprovalRequired}, fingerprint)
	if err != nil || durable.Notify || durable.RepeatCount != 4 {
		t.Fatalf("repeat alert was not suppressed: durable=%#v err=%v", durable, err)
	}
}

func TestDBFindingStoreInactiveConnectionCreatesNothing(t *testing.T) {
	connection := readySessionConnection()
	connection.EmergencyDisabled = true
	fake := &findingStoreFake{connection: connection}
	store, _ := NewDBFindingStore(fake)
	_, err := store.UpsertBlockedFinding(context.Background(), FindingInput{InstallationID: connection.InstallationID.String()}, FindingRecommendation{}, stableFingerprint("x"))
	if err == nil || fake.upsert.ConnectionID != uuid.Nil {
		t.Fatal("emergency-disabled integration persisted a finding")
	}
}

func TestDBFindingStoreUsesUniqueLifecycleIDForRecurringClosedDiagnosis(t *testing.T) {
	connection := readySessionConnection()
	fingerprint := stableFingerprint("recurring")
	fake := &findingStoreFake{
		connection: connection, priorErr: pgx.ErrNoRows,
		row: sqlc.CharlieFinding{ID: uuid.New(), DedupeFingerprint: fingerprint, Status: "open", RepeatCount: 1, UpdatedAt: time.Now()},
	}
	store, _ := NewDBFindingStore(fake)
	input := FindingInput{InstallationID: connection.InstallationID.String(), ResourceType: "management_component", ResourceID: "server", Severity: "warning", Mode: ModeReadOnly}
	recommendation := FindingRecommendation{ExecutionBlockCode: DeniedReadOnlyWrite}
	if _, err := store.UpsertBlockedFinding(context.Background(), input, recommendation, fingerprint); err != nil {
		t.Fatal(err)
	}
	// Simulate a retained closed lifecycle: no active fingerprint row is found,
	// so the next occurrence must propose a different globally unique central ID.
	if _, err := store.UpsertBlockedFinding(context.Background(), input, recommendation, fingerprint); err != nil {
		t.Fatal(err)
	}
	if len(fake.upserts) != 2 || fake.upserts[0].CharlieFindingID == fake.upserts[1].CharlieFindingID {
		t.Fatalf("recurring finding reused globally unique lifecycle ID: %+v", fake.upserts)
	}
	for _, value := range fake.upserts {
		if len(value.CharlieFindingID) > 128 || !strings.HasPrefix(value.CharlieFindingID, "local-"+fingerprint[:24]+"-") {
			t.Fatalf("invalid local finding lifecycle ID %q", value.CharlieFindingID)
		}
	}
}
