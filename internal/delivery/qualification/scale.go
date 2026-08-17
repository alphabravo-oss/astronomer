package qualification

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	agentdelivery "github.com/alphabravocompany/astronomer-go/internal/agent/delivery"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	CapacityClusters        = 10_000
	CapacityConnectedAgents = 5_000
	CapacityDeployments     = 100_000
	CapacityReplicas        = 3
	CapacityStatusEvents    = 10
)

type ScaleConfig struct {
	Clusters               int
	ConnectedAgents        int
	ServerReplicas         int
	WorkerReplicas         int
	Deployments            int
	StatusEventsPerCluster int
	Iterations             int
	Warmup                 int
	WarmPreviewP95         time.Duration
	ColdPreviewP95         time.Duration
	MinimumReleasesMinute  float64
	Commit                 string
	Dirty                  bool
	Command                []string
	CapacityPath           string
}

func DefaultScaleConfig() ScaleConfig {
	return ScaleConfig{
		Clusters: CapacityClusters, ConnectedAgents: CapacityConnectedAgents,
		ServerReplicas: CapacityReplicas, WorkerReplicas: CapacityReplicas,
		Deployments: CapacityDeployments, StatusEventsPerCluster: CapacityStatusEvents,
		Iterations: 25, Warmup: 3, WarmPreviewP95: 2 * time.Second,
		ColdPreviewP95: 5 * time.Second, MinimumReleasesMinute: 1_000,
		CapacityPath: ".",
	}
}

func (config ScaleConfig) Dataset() Dataset {
	return Dataset{Clusters: config.Clusters, ConnectedAgents: config.ConnectedAgents,
		ServerReplicas: config.ServerReplicas, WorkerReplicas: config.WorkerReplicas,
		Deployments: config.Deployments, StatusEventsPerCluster: config.StatusEventsPerCluster}
}

func (config ScaleConfig) Validate() error {
	switch {
	case config.Clusters < 2 || config.Clusters > 100_000:
		return errors.New("clusters must be in 2..100000")
	case config.ConnectedAgents < 1 || config.ConnectedAgents > config.Clusters:
		return errors.New("connected agents must be in 1..clusters")
	case config.ServerReplicas < 1 || config.ServerReplicas > 100 || config.WorkerReplicas < 1 || config.WorkerReplicas > 100:
		return errors.New("server and worker replicas must be in 1..100")
	case config.Deployments < config.Clusters || config.Deployments > 1_000_000:
		return errors.New("deployments must be in clusters..1000000")
	case config.StatusEventsPerCluster < 1 || config.StatusEventsPerCluster > 100:
		return errors.New("status events per cluster must be in 1..100")
	case config.Iterations < 1 || config.Iterations > 1_000 || config.Warmup < 0 || config.Warmup > 100:
		return errors.New("iterations must be in 1..1000 and warmup in 0..100")
	case config.WarmPreviewP95 <= 0 || config.ColdPreviewP95 <= 0 || config.MinimumReleasesMinute <= 0:
		return errors.New("SLO thresholds must be positive")
	}
	return nil
}

func RunScale(ctx context.Context, config ScaleConfig) ScaleReport {
	started := time.Now().UTC()
	report := ScaleReport{
		SchemaVersion: ReportSchemaVersion, Kind: "scale", EvidenceScope: "deterministic_control_plane_simulation",
		Status: "running", StartedAt: started, Command: append([]string(nil), config.Command...),
		Environment: CurrentEnvironment(config.Commit, config.Dirty), Dataset: config.Dataset(),
		Metrics: make(map[string]Distribution), Invariants: make(map[string]bool),
		Limitations: []string{
			"This report exercises production placement, rollout, protocol validation, and status coalescing code in-process.",
			"It does not prove PostgreSQL query plans, queue age, network throughput, three-replica failover, or downstream Flux convergence.",
			"Release eligibility requires a separate 24-hour representative soak and owned-cluster resilience/restore evidence.",
		},
	}
	if err := config.Validate(); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return finishScaleReport(report, started, config.CapacityPath)
	}
	report.Resources = sampleResources(config.CapacityPath)

	candidates, request := makePlacementRequest(config.Clusters)
	for index := 0; index < config.Warmup; index++ {
		if _, err := placement.Evaluate(request); err != nil {
			report.Errors = append(report.Errors, "placement warmup: "+err.Error())
			return finishScaleReport(report, started, config.CapacityPath)
		}
	}
	warm := make([]time.Duration, 0, config.Iterations)
	for index := 0; index < config.Iterations; index++ {
		if err := ctx.Err(); err != nil {
			report.Errors = append(report.Errors, err.Error())
			return finishScaleReport(report, started, config.CapacityPath)
		}
		iterationStarted := time.Now()
		result, err := placement.Evaluate(request)
		warm = append(warm, time.Since(iterationStarted))
		if err != nil || result.SelectedCount != config.Clusters {
			report.Errors = append(report.Errors, fmt.Sprintf("warm placement selected %d/%d: %v", result.SelectedCount, config.Clusters, err))
			break
		}
	}
	cold := make([]time.Duration, 0, config.Iterations)
	for index := 0; index < config.Iterations && len(report.Errors) == 0; index++ {
		iterationStarted := time.Now()
		_, coldRequest := makePlacementRequest(config.Clusters)
		result, err := placement.Evaluate(coldRequest)
		cold = append(cold, time.Since(iterationStarted))
		if err != nil || result.SelectedCount != config.Clusters {
			report.Errors = append(report.Errors, fmt.Sprintf("cold placement selected %d/%d: %v", result.SelectedCount, config.Clusters, err))
		}
	}
	report.Metrics["placement_warm"] = distribution("milliseconds", warm)
	report.Metrics["placement_cold"] = distribution("milliseconds", cold)
	report.Invariants["placement_selected_exact_cluster_count"] = len(report.Errors) == 0
	report.Invariants["placement_warm_p95_within_slo"] = report.Metrics["placement_warm"].P95 <= float64(config.WarmPreviewP95.Milliseconds())
	report.Invariants["placement_cold_p95_within_slo"] = report.Metrics["placement_cold"].P95 <= float64(config.ColdPreviewP95.Milliseconds())

	deploymentStarted := time.Now()
	deployments, deploymentDigest := buildDeploymentDataset(config.Deployments, config.Clusters)
	report.Metrics["deployment_dataset_build"] = distribution("milliseconds", []time.Duration{time.Since(deploymentStarted)})
	report.Invariants["deployment_dataset_exact_count"] = len(deployments) == config.Deployments
	report.Invariants["deployment_dataset_content_addressed"] = len(deploymentDigest) == sha256.Size*2

	rolloutStarted := time.Now()
	rolloutResult, rolloutErr := simulateRollouts(ctx, candidates)
	rolloutElapsed := time.Since(rolloutStarted)
	if rolloutErr != nil {
		report.Errors = append(report.Errors, "rollout simulation: "+rolloutErr.Error())
	} else {
		releaseRate := float64(rolloutResult.Releases) / rolloutElapsed.Minutes()
		report.Metrics["rollout_scheduler"] = distribution("milliseconds", []time.Duration{rolloutElapsed})
		report.Metrics["assignment_release_rate"] = numberDistribution("releases_per_minute", []float64{releaseRate})
		report.Invariants["rollout_success_exact_once"] = rolloutResult.Success && !rolloutResult.DuplicateRelease
		report.Invariants["failure_budget_rollback_completed"] = rolloutResult.Rollback
		report.Invariants["assignment_release_rate_within_slo"] = releaseRate >= config.MinimumReleasesMinute
	}

	statusStarted := time.Now()
	statusResult, statusErr := simulateStatusStorm(ctx, config.Clusters, config.StatusEventsPerCluster)
	if statusErr != nil {
		report.Errors = append(report.Errors, "status storm: "+statusErr.Error())
	} else {
		report.Metrics["status_storm"] = distribution("milliseconds", []time.Duration{time.Since(statusStarted)})
		report.Metrics["status_coalescing_ratio"] = numberDistribution("input_events_per_output", []float64{float64(statusResult.Input) / float64(statusResult.Output)})
		report.Invariants["status_storm_bounded"] = statusResult.Input == config.Clusters*config.StatusEventsPerCluster && statusResult.Output == config.Clusters
	}

	reconnectStarted := time.Now()
	reconnectResult, reconnectErr := simulateReconnectStorm(ctx, config.ConnectedAgents, config.ServerReplicas)
	if reconnectErr != nil {
		report.Errors = append(report.Errors, "reconnect storm: "+reconnectErr.Error())
	} else {
		report.Metrics["agent_reconnect_storm"] = distribution("milliseconds", []time.Duration{time.Since(reconnectStarted)})
		report.Invariants["reconnects_valid_and_evenly_distributed"] = reconnectResult.Valid == config.ConnectedAgents && reconnectResult.MaxReplicaSkew <= 1
	}

	runtime.KeepAlive(deployments)
	runtime.KeepAlive(deploymentDigest)
	for name, passed := range report.Invariants {
		if !passed {
			report.Errors = append(report.Errors, "invariant failed: "+name)
		}
	}
	return finishScaleReport(report, started, config.CapacityPath)
}

func finishScaleReport(report ScaleReport, started time.Time, capacityPath string) ScaleReport {
	report.CompletedAt = time.Now().UTC()
	report.DurationMS = report.CompletedAt.Sub(started).Milliseconds()
	report.Resources = mergeResourcePeaks(report.Resources, sampleResources(capacityPath))
	if len(report.Errors) == 0 {
		report.Status = "passed"
	} else {
		report.Status = "failed"
	}
	// A deterministic simulation is never sufficient release evidence on its
	// own, even when every local capacity invariant passes.
	report.ReleaseEligible = false
	return report
}

func makePlacementRequest(count int) ([]placement.Candidate, placement.Request) {
	projectID := fixedUUID(1, 1)
	candidates := make([]placement.Candidate, count)
	for index := range candidates {
		candidates[index] = placement.Candidate{
			ID: fixedUUID(2, uint64(index+1)), ProjectID: projectID,
			Name:      fmt.Sprintf("qualification-cluster-%05d", index+1),
			Labels:    map[string]string{"delivery.astronomer.io/enabled": "true", "environment": "qualification"},
			Connected: true, Compatibility: placement.CompatibilityCompatible,
			Capabilities: map[string]string{"delivery.astronomer.io/helm": "2.9.3"},
		}
	}
	digest := model.Digest("sha256:" + strings.Repeat("a", 64))
	revision := model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: strings.Repeat("b", 40), ArtifactDigest: digest}
	request := placement.Request{
		Placement:         model.Placement{MatchLabels: map[string]string{"delivery.astronomer.io/enabled": "true"}},
		AllowedProjectIDs: []uuid.UUID{projectID}, Candidates: candidates,
		RequiredCapabilities: []model.CapabilityRequirement{{Name: "delivery.astronomer.io/helm", Constraint: ">=2.0.0"}},
		Identity:             placement.SnapshotIdentity{TargetGeneration: 1, BundleVersionID: fixedUUID(3, 1), BundleSpecDigest: digest, ResolvedRevision: revision},
	}
	return candidates, request
}

type simulatedDeployment struct {
	Cluster    uint32
	Target     uint32
	Phase      uint8
	_          [3]byte
	Generation uint64
}

func buildDeploymentDataset(count, clusters int) ([]simulatedDeployment, string) {
	result := make([]simulatedDeployment, count)
	hash := sha256.New()
	var encoded [17]byte
	for index := range result {
		result[index] = simulatedDeployment{Cluster: uint32(index % clusters), Target: uint32(index / clusters), Phase: 1, Generation: 1}
		binary.BigEndian.PutUint32(encoded[0:4], result[index].Cluster)
		binary.BigEndian.PutUint32(encoded[4:8], result[index].Target)
		encoded[8] = result[index].Phase
		binary.BigEndian.PutUint64(encoded[9:17], result[index].Generation)
		_, _ = hash.Write(encoded[:])
	}
	return result, hex.EncodeToString(hash.Sum(nil))
}

type memoryPlanningStore struct {
	snapshot rollout.PlanningSnapshot
	plan     rollout.FrozenRollout
}

func (store *memoryPlanningStore) InTransaction(ctx context.Context, callback func(rollout.PlanningTransaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return callback(store)
}

func (store *memoryPlanningStore) FindByIdempotency(context.Context, uuid.UUID, string) (rollout.FrozenRollout, bool, error) {
	return store.plan, store.plan.ID != uuid.Nil, nil
}
func (store *memoryPlanningStore) LoadSnapshotForUpdate(context.Context, uuid.UUID) (rollout.PlanningSnapshot, error) {
	return store.snapshot, nil
}
func (store *memoryPlanningStore) InsertRollout(_ context.Context, plan rollout.FrozenRollout) error {
	store.plan = plan
	return nil
}
func (*memoryPlanningStore) AppendRolloutCreated(context.Context, rollout.FrozenRollout) error {
	return nil
}
func (*memoryPlanningStore) EnqueueRollout(context.Context, uuid.UUID) error { return nil }

type rolloutSimulationResult struct {
	Releases         int
	Success          bool
	Rollback         bool
	DuplicateRelease bool
}

func simulateRollouts(ctx context.Context, candidates []placement.Candidate) (rolloutSimulationResult, error) {
	projectID := candidates[0].ProjectID
	targetID := fixedUUID(4, 1)
	digest := model.Digest("sha256:" + strings.Repeat("a", 64))
	previousDigest := model.Digest("sha256:" + strings.Repeat("c", 64))
	desired := rollout.VersionIdentity{BundleVersionID: fixedUUID(3, 1), SpecDigest: digest, Source: model.ResolvedSourceSpec{
		SourceID: fixedUUID(5, 1), Type: model.SourceGit, URL: "https://qualification.invalid/platform/config.git", AuthMode: model.AuthNone,
		Trust: model.TrustPolicy{AllowUnsigned: true}, Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: strings.Repeat("b", 40), ArtifactDigest: digest},
	}}
	previous := rollout.VersionIdentity{BundleVersionID: fixedUUID(3, 2), SpecDigest: previousDigest, Source: model.ResolvedSourceSpec{
		SourceID: fixedUUID(5, 1), Type: model.SourceGit, URL: "https://qualification.invalid/platform/config.git", AuthMode: model.AuthNone,
		Trust: model.TrustPolicy{AllowUnsigned: true}, Revision: model.ImmutableRevision{Kind: model.RevisionGitCommit, Value: strings.Repeat("d", 40), ArtifactDigest: previousDigest},
	}}
	placementRequest := placement.Request{
		Placement: model.Placement{AllClusters: true}, AllowedProjectIDs: []uuid.UUID{projectID}, Candidates: candidates,
		Identity: placement.SnapshotIdentity{TargetGeneration: 1, BundleVersionID: desired.BundleVersionID, BundleSpecDigest: desired.SpecDigest, ResolvedRevision: desired.Source.Revision},
	}
	preview, err := placement.Evaluate(placementRequest)
	if err != nil {
		return rolloutSimulationResult{}, err
	}
	previousByCluster := make(map[uuid.UUID]rollout.PreviousDeployment, len(candidates))
	for _, candidate := range candidates {
		previousByCluster[candidate.ID] = rollout.PreviousDeployment{Version: previous, Generation: 1}
	}
	maxConcurrent := min(1_000, len(candidates))
	canaryCount := min(100, max(2, len(candidates)/100))
	strategy := model.RolloutStrategy{
		Type: model.StrategyCanary, MaxConcurrent: uint32(maxConcurrent),
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: uint32(maxConcurrent)},
		ProgressDeadline: model.Duration(time.Hour), FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
		OnFailure: model.FailureRollback, Canary: &model.CanarySpec{Size: model.Amount{Type: model.AmountCount, Value: uint32(canaryCount)}},
	}
	store := &memoryPlanningStore{snapshot: rollout.PlanningSnapshot{
		TargetID: targetID, ProjectID: projectID, TargetGeneration: 1, Desired: desired,
		PlacementRequest: placementRequest, PreviousByCluster: previousByCluster,
	}}
	clock := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	planner, err := rollout.NewPlanner(store, func() time.Time { return clock }, func() uuid.UUID { return fixedUUID(6, 1) })
	if err != nil {
		return rolloutSimulationResult{}, err
	}
	plan, err := planner.Create(ctx, rollout.CreateRequest{TargetID: targetID, ExpectedTargetGeneration: 1,
		PreviewDigest: preview.PreviewDigest, ConfirmAllClusters: true, Strategy: strategy,
		Actor: "qualification@astronomer.invalid", IdempotencyKey: "qualification-rollout"})
	if err != nil {
		return rolloutSimulationResult{}, err
	}

	successRuntime := makeRuntime(plan)
	successRuntime.State = model.RolloutQueued
	released := make(map[uuid.UUID]struct{}, len(candidates))
	result := rolloutSimulationResult{}
	for step := 0; step < len(candidates)*3+100; step++ {
		decision, evaluateErr := evaluateRollout(plan, &successRuntime, clock)
		if evaluateErr != nil {
			return result, evaluateErr
		}
		applyRolloutTransition(&successRuntime, decision)
		for _, release := range decision.Releases {
			if _, exists := released[release.ClusterID]; exists {
				result.DuplicateRelease = true
			}
			released[release.ClusterID] = struct{}{}
			index := plan.Clusters[indexByCluster(plan, release.ClusterID)].Order
			successRuntime.Clusters[index].State = model.RolloutClusterReady
			successRuntime.Clusters[index].EverReleased = true
			successRuntime.Clusters[index].CurrentGeneration = release.Generation
			ready := clock
			successRuntime.Clusters[index].ReadySince = &ready
			result.Releases++
		}
		if successRuntime.State == model.RolloutSucceeded {
			result.Success = len(released) == len(candidates)
			break
		}
		if len(decision.Releases) == 0 && decision.RolloutTransition == nil {
			return result, fmt.Errorf("success rollout made no progress at step %d: %s", step, decision.Blocked)
		}
	}
	if !result.Success {
		return result, errors.New("success rollout did not reach succeeded")
	}

	failureRuntime := makeRuntime(plan)
	failureRuntime.State = model.RolloutProgressing
	first, err := evaluateRollout(plan, &failureRuntime, clock)
	if err != nil || len(first.Releases) < 2 {
		return result, fmt.Errorf("failure canary release: count=%d error=%v", len(first.Releases), err)
	}
	for index, release := range first.Releases {
		position := plan.Clusters[indexByCluster(plan, release.ClusterID)].Order
		failureRuntime.Clusters[position].EverReleased = true
		failureRuntime.Clusters[position].CurrentGeneration = release.Generation
		failureRuntime.Clusters[position].StateChangedAt = clock
		if index < 2 {
			failureRuntime.Clusters[position].State = model.RolloutClusterFailed
			failureRuntime.Clusters[position].Available = false
		} else {
			failureRuntime.Clusters[position].State = model.RolloutClusterReady
			ready := clock
			failureRuntime.Clusters[position].ReadySince = &ready
		}
	}
	for step := 0; step < 20; step++ {
		decision, evaluateErr := evaluateRollout(plan, &failureRuntime, clock)
		if evaluateErr != nil {
			return result, evaluateErr
		}
		applyRolloutTransition(&failureRuntime, decision)
		for _, release := range decision.Releases {
			position := plan.Clusters[indexByCluster(plan, release.ClusterID)].Order
			failureRuntime.Clusters[position].State = model.RolloutClusterReadyPrevious
			failureRuntime.Clusters[position].Available = true
			failureRuntime.Clusters[position].LastAction = rollout.AssignmentRollback
		}
		if failureRuntime.State == model.RolloutRolledBack {
			result.Rollback = true
			break
		}
	}
	if !result.Rollback {
		return result, errors.New("failure rollout did not reach rolled_back")
	}
	return result, nil
}

func makeRuntime(plan rollout.FrozenRollout) rollout.RuntimeSnapshot {
	clusters := make([]rollout.ClusterRuntime, len(plan.Clusters))
	for index, planned := range plan.Clusters {
		clusters[index] = rollout.ClusterRuntime{ClusterID: planned.ClusterID, State: model.RolloutClusterPending, Connected: true, Available: true}
	}
	return rollout.RuntimeSnapshot{Plan: plan, State: model.RolloutQueued, Clusters: clusters}
}

func evaluateRollout(plan rollout.FrozenRollout, snapshot *rollout.RuntimeSnapshot, now time.Time) (rollout.Decision, error) {
	snapshot.Plan = plan
	return rollout.Evaluate(rollout.EvaluateInput{Now: now, Snapshot: *snapshot,
		Lease:       rollout.Lease{Owner: "qualification-worker", Fence: snapshot.Fence, ExpiresAt: now.Add(time.Minute)},
		Maintenance: rollout.MaintenanceGate{Open: true}, CohortApprovals: map[int]rollout.ApprovalDecision{}})
}

func applyRolloutTransition(snapshot *rollout.RuntimeSnapshot, decision rollout.Decision) {
	if decision.RolloutTransition != nil {
		snapshot.State = decision.RolloutTransition.To
	}
}

func indexByCluster(plan rollout.FrozenRollout, id uuid.UUID) int {
	for index := range plan.Clusters {
		if plan.Clusters[index].ClusterID == id {
			return index
		}
	}
	return -1
}

type statusSimulationResult struct{ Input, Output int }

func simulateStatusStorm(ctx context.Context, clusters, eventsPerCluster int) (statusSimulationResult, error) {
	result := statusSimulationResult{}
	digest := "sha256:" + strings.Repeat("e", 64)
	baseTime := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	for cluster := 0; cluster < clusters; cluster++ {
		if cluster%256 == 0 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		statuses := make([]protocol.DeliveryDeploymentStatusV2, eventsPerCluster)
		for event := range statuses {
			statuses[event] = protocol.DeliveryDeploymentStatusV2{
				DeploymentID: fixedUUID(7, uint64(cluster+1)).String(), Generation: 1, SpecDigest: digest,
				Phase: "applying", ObservedAt: baseTime.Add(time.Duration(event) * time.Millisecond),
			}
		}
		statuses[len(statuses)-1].Phase = "ready"
		coalesced, err := agentdelivery.CoalesceStatuses(statuses)
		if err != nil || len(coalesced) != 1 || coalesced[0].Phase != "ready" {
			return result, fmt.Errorf("cluster %d coalescing: count=%d error=%v", cluster, len(coalesced), err)
		}
		payload := protocol.DeliveryStatusV2{ProtocolVersion: protocol.DeliveryProtocolVersion,
			ClusterID: fixedUUID(2, uint64(cluster+1)).String(), SessionSequence: int64(eventsPerCluster), Deployments: coalesced}
		if err := payload.Validate(); err != nil {
			return result, fmt.Errorf("cluster %d coalesced payload: %w", cluster, err)
		}
		result.Input += eventsPerCluster
		result.Output++
	}
	return result, nil
}

type reconnectSimulationResult struct {
	Valid          int
	MaxReplicaSkew int
}

func simulateReconnectStorm(ctx context.Context, agents, replicas int) (reconnectSimulationResult, error) {
	loads := make([]int, replicas)
	result := reconnectSimulationResult{}
	for index := 0; index < agents; index++ {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
		request := protocol.DeliveryStateRequestV2{
			ClusterID: fixedUUID(2, uint64(index+1)).String(), ProtocolVersion: protocol.DeliveryProtocolVersion,
			AckedSnapshotGeneration: 1, AckedETag: "sha256:" + strings.Repeat("f", 64),
			ControllerInventory: protocol.DeliveryControllerInventory{AgentVersion: "v1.0.0", FluxVersion: "v2.9.3", Ready: true,
				Components: map[string]string{"source-controller": "v1", "kustomize-controller": "v1", "helm-controller": "v2"}},
		}
		if err := request.Validate(); err != nil {
			return result, fmt.Errorf("agent %d request: %w", index, err)
		}
		loads[index%replicas]++
		result.Valid++
	}
	minimum, maximum := loads[0], loads[0]
	for _, load := range loads[1:] {
		minimum, maximum = min(minimum, load), max(maximum, load)
	}
	result.MaxReplicaSkew = maximum - minimum
	return result, nil
}

func fixedUUID(domain byte, number uint64) uuid.UUID {
	var id uuid.UUID
	id[0], id[1] = 0xa5, domain
	binary.BigEndian.PutUint64(id[8:], number)
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
