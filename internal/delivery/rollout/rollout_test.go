package rollout

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
)

var (
	testProjectID = uuid.MustParse("10000000-0000-0000-0000-000000000001")
	testTargetID  = uuid.MustParse("20000000-0000-0000-0000-000000000001")
	testBundleID  = uuid.MustParse("30000000-0000-0000-0000-000000000001")
	testSourceID  = uuid.MustParse("40000000-0000-0000-0000-000000000001")
	testRolloutID = uuid.MustParse("50000000-0000-0000-0000-000000000001")
	testNow       = time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
)

func TestBuildCohortsAllStrategiesDeterministic(t *testing.T) {
	t.Parallel()
	candidates := testCandidates(10)
	reversed := append([]placement.Candidate(nil), candidates...)
	slices.Reverse(reversed)

	tests := []struct {
		name      string
		strategy  model.RolloutStrategy
		wantSizes []int
		wantNames []string
	}{
		{"all at once", testStrategy(model.StrategyAllAtOnce, 3), []int{10}, []string{"all"}},
		{"rolling", testStrategy(model.StrategyRolling, 3), []int{3, 3, 3, 1}, []string{"rolling-0000", "rolling-0001", "rolling-0002", "rolling-0003"}},
		{"seeded rolling", withSeed(testStrategy(model.StrategyRolling, 4), "release-42"), []int{4, 4, 2}, nil},
		{"canary", canaryStrategy(model.Amount{Type: model.AmountPercent, Value: 20}, 3, true, time.Minute), []int{2, 3, 3, 2}, []string{"canary", "rolling-0001", "rolling-0002", "rolling-0003"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			first, firstClusters, err := BuildCohorts(test.strategy, candidates)
			if err != nil {
				t.Fatal(err)
			}
			second, secondClusters, err := BuildCohorts(test.strategy, reversed)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.EqualFunc(first, second, equalCohort) || !slices.Equal(firstClusters, secondClusters) {
				t.Fatalf("cohorts depend on input order:\n%+v\n%+v", first, second)
			}
			gotSizes := make([]int, len(first))
			gotNames := make([]string, len(first))
			for index := range first {
				gotSizes[index], gotNames[index] = len(first[index].ClusterIDs), first[index].Name
			}
			if !slices.Equal(gotSizes, test.wantSizes) {
				t.Fatalf("sizes = %v, want %v", gotSizes, test.wantSizes)
			}
			if test.wantNames != nil && !slices.Equal(gotNames, test.wantNames) {
				t.Fatalf("names = %v, want %v", gotNames, test.wantNames)
			}
			if test.strategy.Type == model.StrategyCanary && !first[1].ApprovalRequired {
				t.Fatal("post-canary approval gate is not on the first rolling cohort")
			}
		})
	}
}

func TestCanaryExplicitMembershipAndErrors(t *testing.T) {
	t.Parallel()
	candidates := testCandidates(5)
	strategy := canaryStrategy(model.Amount{}, 2, false, 0)
	strategy.Canary.ClusterIDs = []uuid.UUID{candidates[3].ID, candidates[1].ID}
	cohorts, _, err := BuildCohorts(strategy, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cohorts[0].ClusterIDs, []uuid.UUID{candidates[1].ID, candidates[3].ID}; !slices.Equal(got, want) {
		t.Fatalf("explicit canary = %v, want %v", got, want)
	}
	strategy.Canary.ClusterIDs[0] = uuid.New()
	if _, _, err := BuildCohorts(strategy, candidates); !HasCode(err, CodeInvalidCohorts) {
		t.Fatalf("missing explicit canary = %v", err)
	}
}

func TestPartitionCohortsRejectAmbiguityAndGaps(t *testing.T) {
	t.Parallel()
	candidates := testCandidates(4)
	candidates[0].Labels = map[string]string{"environment": "dev", "region": "east"}
	candidates[1].Labels = map[string]string{"environment": "dev", "region": "west"}
	candidates[2].Labels = map[string]string{"environment": "prod", "region": "east"}
	candidates[3].Labels = map[string]string{"environment": "prod", "region": "west"}
	strategy := testStrategy(model.StrategyPartitioned, 2)
	strategy.Partitions = []model.Partition{
		{Name: "development", Selector: model.Placement{MatchLabels: map[string]string{"environment": "dev"}}, Soak: model.Duration(time.Minute)},
		{Name: "production", Selector: model.Placement{MatchLabels: map[string]string{"environment": "prod"}}, ApprovalRequired: true},
	}
	cohorts, _, err := BuildCohorts(strategy, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(cohorts) != 2 || len(cohorts[0].ClusterIDs) != 2 || !cohorts[1].ApprovalRequired {
		t.Fatalf("partition cohorts = %+v", cohorts)
	}

	overlap := strategy
	overlap.Partitions = append([]model.Partition(nil), strategy.Partitions...)
	overlap.Partitions[1].Selector = model.Placement{MatchExpressions: []model.LabelExpression{{Key: "region", Operator: model.OperatorExists}}}
	if _, _, err := BuildCohorts(overlap, candidates); !HasCode(err, CodeInvalidCohorts) {
		t.Fatalf("overlap = %v", err)
	}
	gap := strategy
	gap.Partitions = gap.Partitions[:1]
	if _, _, err := BuildCohorts(gap, candidates); !HasCode(err, CodeInvalidCohorts) {
		t.Fatalf("gap = %v", err)
	}
}

func TestBuildCohortsTenThousandStableUnique(t *testing.T) {
	candidates := testCandidates(10_000)
	strategy := canaryStrategy(model.Amount{Type: model.AmountPercent, Value: 1}, 100, true, 0)
	strategy.ShuffleSeed = "enterprise-release"
	cohorts, clusters, err := BuildCohorts(strategy, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(cohorts) != 100 || len(cohorts[0].ClusterIDs) != 100 || len(clusters) != 10_000 {
		t.Fatalf("unexpected 10k shape: cohorts=%d canary=%d clusters=%d", len(cohorts), len(cohorts[0].ClusterIDs), len(clusters))
	}
	seen := make(map[uuid.UUID]struct{}, len(clusters))
	for order, cluster := range clusters {
		if cluster.Order != order {
			t.Fatalf("order[%d] = %d", order, cluster.Order)
		}
		seen[cluster.ClusterID] = struct{}{}
	}
	if len(seen) != 10_000 {
		t.Fatalf("unique clusters = %d", len(seen))
	}
}

func TestPlannerCreatesAtomicImmutablePlanAndIdempotentRetry(t *testing.T) {
	t.Parallel()
	snapshot, preview := testSnapshot(t, 8)
	store := newMemoryPlanningStore(snapshot)
	planner := mustPlanner(t, store)
	request := testCreateRequest(preview, testStrategy(model.StrategyRolling, 3))
	first, err := planner.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(first.Clusters) != 8 || first.Clusters[0].Previous == nil || first.Deadline != testNow.Add(30*time.Minute) {
		t.Fatalf("plan did not freeze expected fields: %+v", first)
	}
	if store.insertCount != 1 || store.eventCount != 1 || store.enqueueCount != 1 {
		t.Fatalf("transaction effects = insert:%d event:%d enqueue:%d", store.insertCount, store.eventCount, store.enqueueCount)
	}

	store.snapshot.TargetGeneration++ // committed retries do not depend on mutable target state.
	second, err := planner.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.PlanDigest != first.PlanDigest || store.insertCount != 1 {
		t.Fatalf("idempotent retry changed plan: first=%s second=%s inserts=%d", first.ID, second.ID, store.insertCount)
	}

	request.Actor = "different@example.test"
	if _, err := planner.Create(context.Background(), request); !HasCode(err, CodeIdempotencyConflict) {
		t.Fatalf("reused key with changed request = %v", err)
	}
}

func TestPlannerRejectsStalePreviewTargetRaceAndRollsBackFailure(t *testing.T) {
	t.Parallel()
	snapshot, preview := testSnapshot(t, 3)
	tests := []struct {
		name   string
		mutate func(*PlanningSnapshot, *CreateRequest, *memoryPlanningStore)
		code   ErrorCode
	}{
		{"preview membership drift", func(snapshot *PlanningSnapshot, _ *CreateRequest, _ *memoryPlanningStore) {
			snapshot.PlacementRequest.Candidates = append(snapshot.PlacementRequest.Candidates, testCandidates(4)[3])
		}, CodePreviewStale},
		{"target generation race", func(snapshot *PlanningSnapshot, _ *CreateRequest, _ *memoryPlanningStore) {
			snapshot.TargetGeneration++
		}, CodeTargetChanged},
		{"transaction append failure", func(_ *PlanningSnapshot, _ *CreateRequest, store *memoryPlanningStore) {
			store.failEvent = errors.New("event storage unavailable")
		}, ""},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			snapshotCopy := snapshot
			snapshotCopy.PlacementRequest.Candidates = append([]placement.Candidate(nil), snapshot.PlacementRequest.Candidates...)
			store := newMemoryPlanningStore(snapshotCopy)
			request := testCreateRequest(preview, testStrategy(model.StrategyRolling, 2))
			test.mutate(&store.snapshot, &request, store)
			_, err := mustPlanner(t, store).Create(context.Background(), request)
			if err == nil || (test.code != "" && !HasCode(err, test.code)) {
				t.Fatalf("Create() error = %v, want %s", err, test.code)
			}
			if len(store.plans) != 0 || store.insertCount != 0 || store.eventCount != 0 || store.enqueueCount != 0 {
				t.Fatalf("failed transaction leaked effects: %+v", store)
			}
		})
	}
}

func TestPlannerConcurrentSameKeyCreatesExactlyOne(t *testing.T) {
	snapshot, preview := testSnapshot(t, 20)
	store := newMemoryPlanningStore(snapshot)
	planner := mustPlanner(t, store)
	request := testCreateRequest(preview, testStrategy(model.StrategyRolling, 5))
	const workers = 32
	results := make(chan FrozenRollout, workers)
	errorsCh := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			plan, err := planner.Create(context.Background(), request)
			results <- plan
			errorsCh <- err
		}()
	}
	group.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	for plan := range results {
		if plan.ID != testRolloutID {
			t.Fatalf("rollout ID = %s", plan.ID)
		}
	}
	if store.insertCount != 1 || len(store.plans) != 1 {
		t.Fatalf("concurrent inserts=%d plans=%d", store.insertCount, len(store.plans))
	}
}

func TestFrozenRolloutValidationDetectsAnyPlanMutation(t *testing.T) {
	t.Parallel()
	plan := testPlan(t, 4, testStrategy(model.StrategyRolling, 2), false)
	mutations := []func(*FrozenRollout){
		func(plan *FrozenRollout) { plan.TargetGeneration++ },
		func(plan *FrozenRollout) { plan.Actor = "changed@example.test" },
		func(plan *FrozenRollout) {
			plan.Cohorts[0].ClusterIDs[0], plan.Cohorts[0].ClusterIDs[1] = plan.Cohorts[0].ClusterIDs[1], plan.Cohorts[0].ClusterIDs[0]
		},
		func(plan *FrozenRollout) { plan.Clusters[0].Previous.Generation++ },
	}
	for index, mutate := range mutations {
		copy := clonePlan(plan)
		mutate(&copy)
		if err := copy.Validate(); err == nil {
			t.Errorf("mutation %d was not detected", index)
		}
	}
}

func equalCohort(left, right Cohort) bool {
	return left.Index == right.Index && left.Name == right.Name && left.ApprovalRequired == right.ApprovalRequired &&
		left.SoakAfter == right.SoakAfter && slices.Equal(left.ClusterIDs, right.ClusterIDs)
}

func testStrategy(strategyType model.StrategyType, maxConcurrent uint32) model.RolloutStrategy {
	return model.RolloutStrategy{
		Type: strategyType, MaxConcurrent: maxConcurrent,
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: maxConcurrent},
		ProgressDeadline: model.Duration(30 * time.Minute),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
		OnFailure:        model.FailurePause,
	}
}

func withSeed(strategy model.RolloutStrategy, seed string) model.RolloutStrategy {
	strategy.ShuffleSeed = seed
	return strategy
}

func canaryStrategy(size model.Amount, maxConcurrent uint32, approval bool, soak time.Duration) model.RolloutStrategy {
	strategy := testStrategy(model.StrategyCanary, maxConcurrent)
	strategy.Canary = &model.CanarySpec{Size: size, ApprovalAfterCanary: approval, Soak: model.Duration(soak)}
	return strategy
}

func testCandidates(count int) []placement.Candidate {
	result := make([]placement.Candidate, count)
	for index := range result {
		result[index] = placement.Candidate{
			ID:        uuid.MustParse(fmt.Sprintf("60000000-0000-0000-0000-%012d", index+1)),
			ProjectID: testProjectID, Name: fmt.Sprintf("cluster-%05d", index+1), Connected: true,
			Compatibility: placement.CompatibilityCompatible,
		}
	}
	return result
}

func testVersion(bundleID uuid.UUID, fill byte) VersionIdentity {
	digest := model.Digest("sha256:" + strings.Repeat(string(fill), 64))
	return VersionIdentity{
		BundleVersionID: bundleID, SpecDigest: digest,
		Source: model.ResolvedSourceSpec{
			SourceID: testSourceID, Type: model.SourceGit, URL: "https://git.example.test/platform/config.git", AuthMode: model.AuthNone,
			Trust:    model.TrustPolicy{AllowUnsigned: true},
			Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: strings.Repeat("b", 40), ArtifactDigest: digest},
		},
	}
}

func testSnapshot(t *testing.T, count int) (PlanningSnapshot, model.Digest) {
	t.Helper()
	desired := testVersion(testBundleID, 'a')
	candidates := testCandidates(count)
	request := placement.Request{
		Placement: model.Placement{AllClusters: true}, AllowedProjectIDs: []uuid.UUID{testProjectID}, Candidates: candidates,
		Identity: placement.SnapshotIdentity{TargetGeneration: 7, BundleVersionID: desired.BundleVersionID, BundleSpecDigest: desired.SpecDigest, ResolvedRevision: desired.Source.Revision},
	}
	result, err := placement.Evaluate(request)
	if err != nil {
		t.Fatal(err)
	}
	previous := make(map[uuid.UUID]PreviousDeployment, count)
	previousVersion := testVersion(uuid.MustParse("30000000-0000-0000-0000-000000000000"), 'c')
	for index, candidate := range candidates {
		previous[candidate.ID] = PreviousDeployment{Version: previousVersion, Generation: int64(index + 1)}
	}
	return PlanningSnapshot{TargetID: testTargetID, ProjectID: testProjectID, TargetGeneration: 7, Desired: desired, PlacementRequest: request, PreviousByCluster: previous}, result.PreviewDigest
}

func testCreateRequest(preview model.Digest, strategy model.RolloutStrategy) CreateRequest {
	return CreateRequest{TargetID: testTargetID, ExpectedTargetGeneration: 7, PreviewDigest: preview, ConfirmAllClusters: true,
		Strategy: strategy, Actor: "operator@example.test", IdempotencyKey: "release-2026-08-17"}
}

func mustPlanner(t *testing.T, store PlanningStore) *Planner {
	t.Helper()
	planner, err := NewPlanner(store, func() time.Time { return testNow }, func() uuid.UUID { return testRolloutID })
	if err != nil {
		t.Fatal(err)
	}
	return planner
}

func testPlan(t *testing.T, count int, strategy model.RolloutStrategy, approval bool) FrozenRollout {
	t.Helper()
	snapshot, preview := testSnapshot(t, count)
	snapshot.InitialApproval = approval
	store := newMemoryPlanningStore(snapshot)
	plan, err := mustPlanner(t, store).Create(context.Background(), testCreateRequest(preview, strategy))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func clonePlan(input FrozenRollout) FrozenRollout {
	result := input
	result.Cohorts = append([]Cohort(nil), input.Cohorts...)
	for index := range result.Cohorts {
		result.Cohorts[index].ClusterIDs = append([]uuid.UUID(nil), input.Cohorts[index].ClusterIDs...)
	}
	result.Clusters = append([]PlannedCluster(nil), input.Clusters...)
	for index := range result.Clusters {
		if input.Clusters[index].Previous != nil {
			copy := *input.Clusters[index].Previous
			result.Clusters[index].Previous = &copy
		}
	}
	return result
}

type memoryPlanningStore struct {
	mu           sync.Mutex
	snapshot     PlanningSnapshot
	plans        map[string]FrozenRollout
	insertCount  int
	eventCount   int
	enqueueCount int
	failEvent    error
}

func newMemoryPlanningStore(snapshot PlanningSnapshot) *memoryPlanningStore {
	return &memoryPlanningStore{snapshot: snapshot, plans: make(map[string]FrozenRollout)}
}

func (store *memoryPlanningStore) InTransaction(ctx context.Context, fn func(PlanningTransaction) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	stage := &memoryPlanningStore{snapshot: store.snapshot, plans: make(map[string]FrozenRollout, len(store.plans)),
		insertCount: store.insertCount, eventCount: store.eventCount, enqueueCount: store.enqueueCount, failEvent: store.failEvent}
	for key, plan := range store.plans {
		stage.plans[key] = plan
	}
	if err := fn(stage); err != nil {
		return err
	}
	store.plans, store.insertCount, store.eventCount, store.enqueueCount = stage.plans, stage.insertCount, stage.eventCount, stage.enqueueCount
	return nil
}

func planKey(targetID uuid.UUID, key string) string { return targetID.String() + "\x00" + key }

func (store *memoryPlanningStore) FindByIdempotency(_ context.Context, targetID uuid.UUID, key string) (FrozenRollout, bool, error) {
	plan, exists := store.plans[planKey(targetID, key)]
	return plan, exists, nil
}

func (store *memoryPlanningStore) LoadSnapshotForUpdate(_ context.Context, _ uuid.UUID) (PlanningSnapshot, error) {
	return store.snapshot, nil
}

func (store *memoryPlanningStore) InsertRollout(_ context.Context, plan FrozenRollout) error {
	key := planKey(plan.TargetID, plan.IdempotencyKey)
	if _, exists := store.plans[key]; exists {
		return fail(CodeIdempotencyConflict, "idempotency_key", "duplicate insert")
	}
	store.plans[key] = plan
	store.insertCount++
	return nil
}

func (store *memoryPlanningStore) AppendRolloutCreated(_ context.Context, _ FrozenRollout) error {
	if store.failEvent != nil {
		return store.failEvent
	}
	store.eventCount++
	return nil
}

func (store *memoryPlanningStore) EnqueueRollout(_ context.Context, _ uuid.UUID) error {
	store.enqueueCount++
	return nil
}
