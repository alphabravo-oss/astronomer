package quota

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeQuerier is the in-memory QuotaQuerier used by the enforcer tests.
// Counts are stored in maps so each test case can pre-seed the
// "current usage" view independently. Plan + override blobs are
// addressed by the project/user UUID the test uses for the call.
type fakeQuerier struct {
	planByProject map[uuid.UUID]sqlc.GetEffectiveQuotaForProjectRow
	planByUser    map[uuid.UUID]sqlc.GetEffectiveQuotaForUserRow
	planByName    map[string]sqlc.QuotaPlan

	clusterCountInProject map[uuid.UUID]int64
	memberCountInProject  map[uuid.UUID]int64
	projectCountForUser   map[uuid.UUID]int64
	activeTokensForUser   map[uuid.UUID]int64
	totalClusters         int64
	totalActiveUsers      int64
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		planByProject:         map[uuid.UUID]sqlc.GetEffectiveQuotaForProjectRow{},
		planByUser:            map[uuid.UUID]sqlc.GetEffectiveQuotaForUserRow{},
		planByName:            map[string]sqlc.QuotaPlan{},
		clusterCountInProject: map[uuid.UUID]int64{},
		memberCountInProject:  map[uuid.UUID]int64{},
		projectCountForUser:   map[uuid.UUID]int64{},
		activeTokensForUser:   map[uuid.UUID]int64{},
	}
}

func (f *fakeQuerier) GetQuotaPlan(_ context.Context, name string) (sqlc.QuotaPlan, error) {
	p, ok := f.planByName[name]
	if !ok {
		return sqlc.QuotaPlan{}, errors.New("not found")
	}
	return p, nil
}
func (f *fakeQuerier) GetEffectiveQuotaForUser(_ context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForUserRow, error) {
	p, ok := f.planByUser[id]
	if !ok {
		return sqlc.GetEffectiveQuotaForUserRow{}, errors.New("not found")
	}
	return p, nil
}
func (f *fakeQuerier) GetEffectiveQuotaForProject(_ context.Context, id uuid.UUID) (sqlc.GetEffectiveQuotaForProjectRow, error) {
	p, ok := f.planByProject[id]
	if !ok {
		return sqlc.GetEffectiveQuotaForProjectRow{}, errors.New("not found")
	}
	return p, nil
}
func (f *fakeQuerier) CountClustersInProject(_ context.Context, id uuid.UUID) (int64, error) {
	return f.clusterCountInProject[id], nil
}
func (f *fakeQuerier) CountMembersInProject(_ context.Context, id uuid.UUID) (int64, error) {
	return f.memberCountInProject[id], nil
}
func (f *fakeQuerier) CountProjectsForUser(_ context.Context, id uuid.UUID) (int64, error) {
	return f.projectCountForUser[id], nil
}
func (f *fakeQuerier) CountActiveTokensForUser(_ context.Context, id uuid.UUID) (int64, error) {
	return f.activeTokensForUser[id], nil
}
func (f *fakeQuerier) CountTotalClusters(_ context.Context) (int64, error) {
	return f.totalClusters, nil
}
func (f *fakeQuerier) CountTotalActiveUsers(_ context.Context) (int64, error) {
	return f.totalActiveUsers, nil
}

func TestEnforcer_AllowsBelowLimit(t *testing.T) {
	projectID := uuid.New()
	q := newFakeQuerier()
	q.planByProject[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID,
		PlanName:              "free",
		Enforcement:           "hard",
		MaxClustersPerProject: 5,
	}
	q.clusterCountInProject[projectID] = 3

	e := New(q, nil)
	if err := e.CheckProjectClusterAdd(context.Background(), projectID); err != nil {
		t.Fatalf("expected nil under cap, got %v", err)
	}
}

func TestEnforcer_RejectsAtHardLimit(t *testing.T) {
	projectID := uuid.New()
	q := newFakeQuerier()
	q.planByProject[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID,
		PlanName:              "free",
		Enforcement:           "hard",
		MaxClustersPerProject: 5,
	}
	q.clusterCountInProject[projectID] = 5

	e := New(q, nil)
	err := e.CheckProjectClusterAdd(context.Background(), projectID)
	qe, ok := IsQuotaExceeded(err)
	if !ok {
		t.Fatalf("expected QuotaExceededError, got %v", err)
	}
	if qe.Maximum != 5 || qe.Current != 5 {
		t.Errorf("unexpected current/maximum: %d/%d", qe.Current, qe.Maximum)
	}
	if qe.Limit != "max_clusters_per_project" {
		t.Errorf("unexpected limit: %s", qe.Limit)
	}
	if qe.Enforcement != "hard" {
		t.Errorf("expected hard enforcement, got %s", qe.Enforcement)
	}
}

func TestEnforcer_SoftLogsButAllows(t *testing.T) {
	projectID := uuid.New()
	q := newFakeQuerier()
	q.planByProject[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID,
		PlanName:              "enterprise",
		Enforcement:           "soft",
		MaxClustersPerProject: 5,
	}
	q.clusterCountInProject[projectID] = 50

	e := New(q, nil)
	if err := e.CheckProjectClusterAdd(context.Background(), projectID); err != nil {
		t.Fatalf("soft mode should not return error, got %v", err)
	}
}

func TestEnforcer_AppliesOverrides(t *testing.T) {
	projectID := uuid.New()
	q := newFakeQuerier()
	q.planByProject[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID,
		PlanName:              "free",
		Enforcement:           "hard",
		MaxClustersPerProject: 5,
		// Override raises the cap to 100 for this single project.
		Overrides: json.RawMessage(`{"max_clusters_per_project": 100}`),
	}
	q.clusterCountInProject[projectID] = 10

	e := New(q, nil)
	if err := e.CheckProjectClusterAdd(context.Background(), projectID); err != nil {
		t.Fatalf("override should permit 10/100, got %v", err)
	}

	// And confirm the override is honoured downward: same plan, low override.
	projectID2 := uuid.New()
	q.planByProject[projectID2] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID2,
		PlanName:              "free",
		Enforcement:           "hard",
		MaxClustersPerProject: 5,
		Overrides:             json.RawMessage(`{"max_clusters_per_project": 2}`),
	}
	q.clusterCountInProject[projectID2] = 2
	err := e.CheckProjectClusterAdd(context.Background(), projectID2)
	if _, ok := IsQuotaExceeded(err); !ok {
		t.Fatalf("override-lowered cap should reject at 2/2, got %v", err)
	}
}

func TestEnforcer_UserTokenCreate(t *testing.T) {
	userID := uuid.New()
	q := newFakeQuerier()
	q.planByUser[userID] = sqlc.GetEffectiveQuotaForUserRow{
		UserID:           userID,
		PlanName:         "free",
		Enforcement:      "hard",
		MaxTokensPerUser: 3,
	}
	q.activeTokensForUser[userID] = 3

	e := New(q, nil)
	if _, ok := IsQuotaExceeded(e.CheckUserTokenCreate(context.Background(), userID)); !ok {
		t.Fatalf("expected token-cap rejection at 3/3")
	}
	// And below the cap.
	q.activeTokensForUser[userID] = 2
	if err := e.CheckUserTokenCreate(context.Background(), userID); err != nil {
		t.Fatalf("expected allow at 2/3, got %v", err)
	}
}

func TestEnforcer_UserProjectAdd(t *testing.T) {
	userID := uuid.New()
	q := newFakeQuerier()
	q.planByUser[userID] = sqlc.GetEffectiveQuotaForUserRow{
		UserID:             userID,
		PlanName:           "free",
		Enforcement:        "hard",
		MaxProjectsPerUser: 3,
	}
	q.projectCountForUser[userID] = 3

	e := New(q, nil)
	if _, ok := IsQuotaExceeded(e.CheckUserProjectAdd(context.Background(), userID)); !ok {
		t.Fatalf("expected reject when at-cap")
	}
}

func TestEnforcer_GlobalClusterCreate(t *testing.T) {
	q := newFakeQuerier()
	q.planByName["global"] = sqlc.QuotaPlan{
		Name:             "global",
		Enforcement:      "hard",
		MaxTotalClusters: 10,
	}
	q.totalClusters = 10
	e := New(q, nil)
	if _, ok := IsQuotaExceeded(e.CheckGlobalClusterCreate(context.Background())); !ok {
		t.Fatalf("expected global cluster cap reject")
	}

	q.totalClusters = 9
	if err := e.CheckGlobalClusterCreate(context.Background()); err != nil {
		t.Fatalf("expected allow at 9/10, got %v", err)
	}
}

func TestEnforcer_ZeroMeansUnlimited(t *testing.T) {
	projectID := uuid.New()
	q := newFakeQuerier()
	q.planByProject[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID,
		PlanName:              "enterprise",
		Enforcement:           "hard",
		MaxClustersPerProject: 0, // unlimited
	}
	q.clusterCountInProject[projectID] = 1_000_000

	e := New(q, nil)
	if err := e.CheckProjectClusterAdd(context.Background(), projectID); err != nil {
		t.Fatalf("0 cap should be treated as unlimited, got %v", err)
	}
}

func TestEnforcer_MalformedOverridesFallsBackToBaseline(t *testing.T) {
	projectID := uuid.New()
	q := newFakeQuerier()
	q.planByProject[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:             projectID,
		PlanName:              "free",
		Enforcement:           "hard",
		MaxClustersPerProject: 5,
		Overrides:             json.RawMessage(`not-a-json-object`),
	}
	q.clusterCountInProject[projectID] = 5

	e := New(q, nil)
	if _, ok := IsQuotaExceeded(e.CheckProjectClusterAdd(context.Background(), projectID)); !ok {
		t.Fatalf("expected fallback to baseline cap of 5 to reject at 5")
	}
}

func TestEnforcer_ProjectMemberAdd(t *testing.T) {
	projectID := uuid.New()
	q := newFakeQuerier()
	q.planByProject[projectID] = sqlc.GetEffectiveQuotaForProjectRow{
		ProjectID:            projectID,
		PlanName:             "free",
		Enforcement:          "hard",
		MaxMembersPerProject: 10,
	}
	q.memberCountInProject[projectID] = 10
	e := New(q, nil)
	if _, ok := IsQuotaExceeded(e.CheckProjectMemberAdd(context.Background(), projectID)); !ok {
		t.Fatalf("expected member-cap reject at 10/10")
	}
}

func TestQuotaExceededError_String(t *testing.T) {
	e := &QuotaExceededError{Subject: "user:abc", Limit: "max_tokens_per_user", Current: 5, Maximum: 5, Enforcement: "hard"}
	if got := e.Error(); got == "" {
		t.Fatal("Error() returned empty string")
	}
}
