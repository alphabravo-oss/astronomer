// Package builtin provisions the reviewed platform bundle catalog through the
// same immutable delivery targets and rollouts used by every user workload.
// It is intentionally triggered only after an enrolled agent reports a Ready,
// compatible Flux inventory; registration never reaches into the downstream
// cluster or installs Helm releases directly.
package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
)

const (
	systemProjectName       = "astronomer-system"
	systemSourceName        = "astronomer-builtins-prometheus-community"
	systemSourceURL         = "https://prometheus-community.github.io/helm-charts"
	systemSourceDescription = "Release-pinned built-in Helm charts"
	systemActor             = "system:cluster-registration"
)

var identityNamespace = uuid.MustParse("2b079af7-4a47-4dbe-984b-a67d78ab6909")

type Previewer interface {
	Preview(context.Context, uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error)
}

type Planner interface {
	Create(context.Context, rollout.CreateRequest) (rollout.FrozenRollout, error)
}

type Registration interface {
	OnDeliveryApplyStart(context.Context, uuid.UUID) error
	OnDeliveryApplySuccess(context.Context, uuid.UUID) error
	OnDeliveryApplyFailure(context.Context, uuid.UUID, string) error
}

// Provisioner is a retry-safe bridge from registration readiness to normal
// delivery resources. Deterministic IDs and planner idempotency keys allow any
// server replica to reconcile the same cluster without duplicate resources.
type Provisioner struct {
	pool         *pgxpool.Pool
	previewer    Previewer
	planner      Planner
	registration Registration
	catalog      CatalogLoader
}

func NewProvisioner(pool *pgxpool.Pool, previewer Previewer, planner Planner, registration Registration, catalog CatalogLoader) (*Provisioner, error) {
	if pool == nil || previewer == nil || planner == nil || registration == nil || catalog == nil {
		return nil, errors.New("built-in delivery provisioner dependencies are required")
	}
	return &Provisioner{pool: pool, previewer: previewer, planner: planner, registration: registration, catalog: catalog}, nil
}

type registrationState struct {
	phase            registration.Phase
	isLocal          bool
	install          bool
	scanningDisabled bool
	inventoryReady   bool
	latestRetryTime  *time.Time
}

type targetIdentity struct {
	id         uuid.UUID
	generation uint64
	slug       string
	release    string
}

type sourceIdentity struct {
	id   uuid.UUID
	name string
	url  string
	kind model.SourceType
}

// Reconcile is called after an authenticated delivery status commit. Errors do
// not invalidate that status; the next agent observation retries this work.
func (p *Provisioner) Reconcile(ctx context.Context, clusterID uuid.UUID) error {
	if p == nil || p.pool == nil || clusterID == uuid.Nil {
		return errors.New("built-in delivery provisioner is not configured")
	}
	state, err := p.loadRegistrationState(ctx, clusterID)
	if err != nil {
		return err
	}
	if !state.shouldReconcile() {
		return nil
	}
	catalog, err := p.loadCatalog(ctx)
	if err != nil {
		return err
	}
	// Ready remote clusters also receive the scanner, including clusters
	// enrolled before automatic scanning was introduced. Metrics remain opt-in.
	catalog = catalog.ForRegistration(state.install && state.phase != registration.PhaseReady, state.scanningDisabled)
	targets, err := p.ensureAssets(ctx, clusterID, catalog)
	if err != nil {
		return err
	}
	complete, failedCode, failed, err := p.observeTargets(ctx, clusterID, targets, state.latestRetryTime)
	if err != nil {
		return err
	}
	if complete {
		if state.phase == registration.PhaseReady {
			return nil
		}
		return p.registration.OnDeliveryApplySuccess(ctx, clusterID)
	}
	if failed {
		if state.phase == registration.PhaseReady {
			return nil // The failed scanner rollout remains visible in delivery.
		}
		return p.registration.OnDeliveryApplyFailure(ctx, clusterID, failedCode)
	}
	if state.phase != registration.PhaseReady {
		if err := p.registration.OnDeliveryApplyStart(ctx, clusterID); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if err := p.ensureRollout(ctx, target, state.latestRetryTime); err != nil {
			return fmt.Errorf("ensure built-in rollout %s: %w", target.slug, err)
		}
	}
	return nil
}

func (p *Provisioner) loadRegistrationState(ctx context.Context, clusterID uuid.UUID) (registrationState, error) {
	row, err := sqlc.New(p.pool).GetBuiltinRegistrationState(ctx, clusterID)
	if err != nil {
		return registrationState{}, fmt.Errorf("load built-in registration state: %w", err)
	}
	state := registrationState{phase: registration.Phase(row.RegistrationPhase), isLocal: row.IsLocal,
		install: row.InstallBaseline, scanningDisabled: row.ScanningDisabled, inventoryReady: row.InventoryReady}
	if row.LatestRetryFound {
		state.latestRetryTime = &row.LatestRetryTime
	}
	return state, nil
}

func (p *Provisioner) ensureAssets(ctx context.Context, clusterID uuid.UUID, catalog builtinbundles.Catalog) ([]targetIdentity, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		targets, err := p.ensureAssetsTransaction(ctx, clusterID, catalog)
		if err == nil || !retryableTransactionError(err) {
			return targets, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("ensure built-in delivery assets after transaction retries: %w", lastErr)
}

func (p *Provisioner) ensureAssetsTransaction(ctx context.Context, clusterID uuid.UUID, catalog builtinbundles.Catalog) ([]targetIdentity, error) {
	sources, sourceByURL, err := planCatalogSources(catalog, stableID("project", clusterID.String()))
	if err != nil {
		return nil, err
	}
	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire built-in delivery connection: %w", err)
	}
	locked := false
	session := sqlc.New(conn)
	defer func() {
		if !locked {
			conn.Release()
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		unlocked, unlockErr := session.UnlockBuiltinAssetsSession(cleanupCtx, clusterID.String())
		if unlockErr == nil && unlocked {
			conn.Release()
			return
		}
		rawConn := conn.Hijack()
		_ = rawConn.Close(cleanupCtx)
	}()
	// Acquire the per-cluster lock before opening the serializable transaction.
	// PostgreSQL fixes a serializable snapshot at the first statement; waiting
	// for this lock inside the transaction would leave queued reconcilers with
	// stale snapshots and force avoidable serialization retries.
	if err := session.LockBuiltinAssetsSession(ctx, clusterID.String()); err != nil {
		return nil, fmt.Errorf("lock built-in delivery: %w", err)
	}
	locked = true
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin built-in delivery transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New(tx)

	projectID := stableID("project", clusterID.String())
	if err := q.EnsureBuiltinSystemProject(ctx, sqlc.EnsureBuiltinSystemProjectParams{
		ID: projectID, Name: systemProjectName, ClusterID: clusterID,
	}); err != nil {
		return nil, fmt.Errorf("ensure built-in project: %w", err)
	}
	project, err := q.GetBuiltinSystemProjectIdentity(ctx, projectID)
	if err != nil || project.ClusterID != clusterID || project.Name != systemProjectName || project.ManagedBy != "system" {
		return nil, errors.New("built-in project identity conflict")
	}

	trust := catalogTrustPolicy(catalog)
	trustJSON, _ := json.Marshal(trust)
	for _, source := range sources {
		if err := ensureCatalogSource(ctx, q, projectID, source, trust, trustJSON); err != nil {
			return nil, err
		}
	}

	targets := make([]targetIdentity, 0, len(catalog.Components))
	for _, component := range catalog.Components {
		if !component.DefaultEnabled {
			continue
		}
		source, ok := sourceByURL[component.Source.URL]
		if !ok {
			return nil, fmt.Errorf("built-in component %s has no planned source identity", component.Slug)
		}
		target, err := ensureComponent(ctx, q, projectID, clusterID, source, catalog.Release, component, trust, catalog.DistributionIdentity, catalog.VerificationIdentity)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, errors.New("built-in catalog has no enabled components")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit built-in delivery assets: %w", err)
	}
	return targets, nil
}

func planCatalogSources(catalog builtinbundles.Catalog, projectID uuid.UUID) ([]sourceIdentity, map[string]sourceIdentity, error) {
	if projectID == uuid.Nil {
		return nil, nil, errors.New("built-in source planning requires a project identity")
	}
	if err := catalog.Validate(); err != nil {
		return nil, nil, err
	}
	byURL := make(map[string]sourceIdentity)
	seenIDs := make(map[uuid.UUID]string)
	seenNames := make(map[string]string)
	for _, component := range catalog.Components {
		if !component.DefaultEnabled {
			continue
		}
		source, err := catalogSourceIdentityForComponent(projectID, component, catalog.DistributionIdentity)
		if err != nil {
			return nil, nil, fmt.Errorf("plan built-in source for %s: %w", component.Slug, err)
		}
		if existing, ok := byURL[source.url]; ok {
			if existing != source {
				return nil, nil, fmt.Errorf("ambiguous built-in source identity for %q", source.url)
			}
			continue
		}
		if other, collision := seenIDs[source.id]; collision && other != source.url {
			return nil, nil, fmt.Errorf("built-in source ID collision between %q and %q", other, source.url)
		}
		if other, collision := seenNames[source.name]; collision && other != source.url {
			return nil, nil, fmt.Errorf("built-in source name collision between %q and %q", other, source.url)
		}
		byURL[source.url] = source
		seenIDs[source.id] = source.url
		seenNames[source.name] = source.url
	}
	if len(byURL) == 0 {
		return nil, nil, errors.New("built-in catalog has no enabled components")
	}
	sources := make([]sourceIdentity, 0, len(byURL))
	for _, source := range byURL {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].url < sources[j].url })
	return sources, byURL, nil
}

func catalogSourceIdentity(projectID uuid.UUID, rawURL string) (sourceIdentity, error) {
	normalized, err := builtinbundles.NormalizeSourceURL(rawURL)
	if err != nil {
		return sourceIdentity{}, err
	}
	if normalized != rawURL {
		return sourceIdentity{}, fmt.Errorf("source URL %q is not canonical (want %q)", rawURL, normalized)
	}
	return sourceIdentityFromCanonical(projectID, normalized), nil
}

func sourceIdentityFromCanonical(projectID uuid.UUID, normalized string) sourceIdentity {
	if normalized == systemSourceURL {
		return sourceIdentity{
			id: stableID("source", projectID.String(), systemSourceName), name: systemSourceName, url: normalized,
		}
	}
	digest := sha256.Sum256([]byte(normalized))
	name := fmt.Sprintf("astronomer-builtins-source-%x", digest[:8])
	return sourceIdentity{id: stableID("source", projectID.String(), normalized), name: name, url: normalized}
}

func ensureCatalogSource(ctx context.Context, q builtinSourceQueries, projectID uuid.UUID, source sourceIdentity, trust model.TrustPolicy, trustJSON []byte) error {
	definition := model.Source{
		ID: source.id, ProjectID: projectID, Name: source.name, Description: sourceDescription(source),
		Type: sourceType(source), URL: source.url, AuthMode: model.AuthNone, Trust: trust,
	}
	if err := definition.Validate(); err != nil {
		return fmt.Errorf("validate built-in source %s: %w", source.name, err)
	}
	if err := q.EnsureBuiltinCatalogSource(ctx, sqlc.EnsureBuiltinCatalogSourceParams{
		ID: source.id, ProjectID: projectID, Name: source.name, Description: sourceDescription(source),
		SourceType: string(sourceType(source)), Url: source.url, TrustPolicy: trustJSON,
	}); err != nil {
		return fmt.Errorf("ensure built-in source %s: %w", source.name, err)
	}
	matches, err := q.BuiltinCatalogSourceMatches(ctx, sqlc.BuiltinCatalogSourceMatchesParams{
		ID: source.id, ProjectID: projectID, Name: source.name, Description: sourceDescription(source),
		SourceType: string(sourceType(source)), Url: source.url, TrustPolicy: trustJSON,
	})
	if err != nil {
		return fmt.Errorf("verify built-in source %s: %w", source.name, err)
	}
	if !matches {
		return fmt.Errorf("built-in source %s identity conflict", source.name)
	}
	return nil
}

func ensureComponent(ctx context.Context, q builtinComponentQueries, projectID, clusterID uuid.UUID, source sourceIdentity, release string, component builtinbundles.Component, trust model.TrustPolicy, distributionIdentity, verificationIdentity string) (targetIdentity, error) {
	bundleID := stableID("bundle", projectID.String(), component.Slug)
	if err := q.EnsureBuiltinComponentBundle(ctx, sqlc.EnsureBuiltinComponentBundleParams{
		ID: bundleID, ProjectID: projectID, Name: "astronomer-builtins-" + component.Slug, Description: component.Description,
	}); err != nil {
		return targetIdentity{}, fmt.Errorf("ensure built-in bundle %s: %w", component.Slug, err)
	}
	bundleProject, err := q.GetBuiltinComponentBundleProject(ctx, bundleID)
	if err != nil || bundleProject != projectID {
		return targetIdentity{}, fmt.Errorf("built-in bundle %s identity conflict", component.Slug)
	}

	valuesJSON, err := json.Marshal(component.Values)
	if err != nil {
		return targetIdentity{}, fmt.Errorf("encode built-in values %s: %w", component.Slug, err)
	}
	reconciliation := model.ReconciliationPolicy{
		Interval: model.Duration(5 * time.Minute), RetryInterval: model.Duration(30 * time.Second),
		Timeout: model.Duration(10 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair,
	}
	requirements := make([]model.CapabilityRequirement, 0, len(component.Requirements.Capabilities))
	for _, capability := range component.Requirements.Capabilities {
		requirements = append(requirements, model.CapabilityRequirement{Name: capability})
	}
	draft := model.BundleVersionDraft{
		SourceID: source.id, RequestedRevision: component.Source.Version, Scope: model.ScopePlatform,
		Renderer: model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{
			Chart: component.Source.Chart, ChartVersion: component.Source.Version,
			ReleaseName: component.ReleaseName, TargetNamespace: component.TargetNamespace,
			Values: valuesJSON, InstallRetries: 3, UpgradeRetries: 3,
		}},
		Reconciliation: reconciliation, RequiredCapabilities: requirements,
	}
	artifactDigest, err := model.ParseDigest(component.Source.ChartDigest)
	if err != nil {
		return targetIdentity{}, err
	}
	revision := model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: component.Source.Version, ArtifactDigest: artifactDigest}
	resolved, err := draft.Resolve(revision)
	if err != nil {
		return targetIdentity{}, fmt.Errorf("validate built-in component %s: %w", component.Slug, err)
	}
	specDigest, err := model.CanonicalDigest(struct {
		Spec         model.BundleVersionSpec `json:"spec"`
		Dependencies []uuid.UUID             `json:"dependency_bundle_ids"`
	}{resolved, []uuid.UUID{}})
	if err != nil {
		return targetIdentity{}, err
	}
	resolvedSource := model.ResolvedSourceSpec{
		SourceID: source.id, Type: sourceType(source),
		URL: source.url, AuthMode: model.AuthNone,
		Trust: trust, Revision: revision,
	}
	sourceJSON, _ := json.Marshal(resolvedSource)
	rendererJSON, _ := json.Marshal(draft.Renderer)
	reconciliationJSON, _ := json.Marshal(reconciliation)
	requirementsJSON, _ := json.Marshal(requirements)
	versionID, versionLabel := componentVersionIdentity(bundleID, release, component, distributionIdentity)
	verificationStatus := "unsigned"
	if distributionIdentity != "" {
		verificationStatus = "verified"
		verificationIdentity += ";distribution:" + distributionIdentity + ";upstream:" + component.Source.OriginalURL + ";archive:" + component.Source.OriginalArchiveDigest
	} else {
		verificationIdentity = "source-development:" + release
	}
	if err := q.EnsureBuiltinComponentVersion(ctx, sqlc.EnsureBuiltinComponentVersionParams{
		ID: versionID, BundleID: bundleID, SourceID: source.id, Version: versionLabel,
		RequestedRevision: component.Source.Version, ArtifactDigest: component.Source.ChartDigest,
		SourceSpec: sourceJSON, RendererSpec: rendererJSON, ReconciliationPolicy: reconciliationJSON,
		Requirements: requirementsJSON, SpecDigest: specDigest.String(), VerificationStatus: verificationStatus,
		VerificationIdentity: verificationIdentity,
	}); err != nil {
		return targetIdentity{}, fmt.Errorf("ensure built-in version %s: %w", component.Slug, err)
	}
	version, err := q.GetBuiltinComponentVersionIdentity(ctx, versionID)
	if err != nil || version.BundleID != bundleID || version.SpecDigest != specDigest.String() || version.State != "ready" {
		return targetIdentity{}, fmt.Errorf("built-in version %s identity conflict", component.Slug)
	}

	placementJSON, _ := json.Marshal(model.Placement{ProjectIDs: []uuid.UUID{projectID}, ClusterIDs: []uuid.UUID{clusterID}})
	rolloutPolicyJSON := json.RawMessage(`{"approval_required":false}`)
	targetID := stableID("target", clusterID.String(), component.Slug)
	generation, err := q.EnsureBuiltinComponentTarget(ctx, sqlc.EnsureBuiltinComponentTargetParams{
		ID: targetID, ProjectID: projectID, Name: "astronomer-builtins-" + component.Slug,
		Description: component.Description, BundleVersionID: versionID, Placement: placementJSON,
		RolloutPolicy: rolloutPolicyJSON, ReconciliationPolicy: reconciliationJSON,
	})
	if err != nil {
		return targetIdentity{}, fmt.Errorf("ensure built-in target %s: %w", component.Slug, err)
	}
	if generation < 1 {
		return targetIdentity{}, fmt.Errorf("built-in target %s generation is invalid", component.Slug)
	}
	return targetIdentity{id: targetID, generation: uint64(generation), slug: component.Slug, release: release}, nil
}

func retryableTransactionError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40001" || postgresError.Code == "40P01"
}

func (p *Provisioner) observeTargets(ctx context.Context, clusterID uuid.UUID, targets []targetIdentity, retryRequestedAt *time.Time) (complete bool, code string, failed bool, err error) {
	ready := 0
	for _, target := range targets {
		rolloutRow, rolloutErr := sqlc.New(p.pool).GetBuiltinLatestTargetRollout(ctx, target.id)
		if rolloutErr != nil && !errors.Is(rolloutErr, pgx.ErrNoRows) {
			return false, "", false, rolloutErr
		}
		deployment, deploymentErr := sqlc.New(p.pool).GetBuiltinTargetDeployment(ctx, sqlc.GetBuiltinTargetDeploymentParams{
			TargetID: target.id, ClusterID: clusterID,
		})
		if deploymentErr != nil && !errors.Is(deploymentErr, pgx.ErrNoRows) {
			return false, "", false, deploymentErr
		}
		observation := evaluateTargetObservation(
			rolloutRow.State,
			rolloutErr == nil,
			retryAfter(retryRequestedAt, rolloutRow.CreatedAt),
			deployment.Phase,
			deployment.LastErrorCode,
			deploymentErr == nil,
		)
		if observation.ready {
			ready++
			continue
		}
		if observation.failed {
			return false, observation.code, true, nil
		}
	}
	return ready == len(targets), "", false, nil
}

type targetObservation struct {
	ready  bool
	failed bool
	code   string
}

func evaluateTargetObservation(rolloutState string, rolloutFound, retryRequested bool, deploymentPhase, deploymentError string, deploymentFound bool) targetObservation {
	// The agent's observed deployment state is the current state of the exact
	// desired generation. Flux can recover after a rollout has crossed its
	// deadline, so Ready must override the rollout's stale terminal result.
	if deploymentFound && deploymentPhase == "ready" {
		return targetObservation{ready: true}
	}
	if rolloutFound {
		switch rolloutState {
		case "failed", "rejected", "aborted", "rolled_back", "rollback_failed":
			// A retry request newer than this terminal rollout deliberately
			// suppresses its stale failure while ensureRollout creates the
			// next immutable attempt.
			if retryRequested {
				return targetObservation{}
			}
			return targetObservation{failed: true, code: "built_in_rollout_" + rolloutState}
		case "draft", "resolving", "awaiting_approval", "queued", "progressing", "paused", "rolling_back":
			return targetObservation{}
		}
	}
	if deploymentFound && (deploymentPhase == "failed" || deploymentPhase == "timed_out" || deploymentPhase == "rollback_failed") {
		if deploymentError == "" {
			deploymentError = "built_in_delivery_failed"
		}
		return targetObservation{failed: true, code: deploymentError}
	}
	return targetObservation{}
}

func (p *Provisioner) ensureRollout(ctx context.Context, target targetIdentity, retryRequestedAt *time.Time) error {
	row, err := sqlc.New(p.pool).GetBuiltinRolloutAttempt(ctx, target.id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		switch row.State {
		case "resolving", "awaiting_approval", "queued", "progressing", "paused", "rolling_back", "succeeded":
			return nil
		}
		if !retryAfter(retryRequestedAt, row.CreatedAt) {
			return nil
		}
	}
	snapshot, result, err := p.previewer.Preview(ctx, target.id)
	if err != nil {
		return err
	}
	if result.SelectedCount != 1 {
		return fmt.Errorf("built-in target selected %d clusters", result.SelectedCount)
	}
	key := "builtins:" + target.release + ":" + target.slug
	if row.AttemptCount > 0 {
		key = fmt.Sprintf("%s:retry-%d", key, row.AttemptCount)
	}
	strategy := model.RolloutStrategy{
		Type: model.StrategyAllAtOnce, MaxConcurrent: 1,
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
		ProgressDeadline: model.Duration(30 * time.Minute),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
		OnFailure:        model.FailurePause,
	}
	_, err = p.planner.Create(ctx, rollout.CreateRequest{
		TargetID: target.id, ExpectedTargetGeneration: snapshot.TargetGeneration,
		PreviewDigest: result.PreviewDigest, Strategy: strategy,
		Actor: systemActor, IdempotencyKey: key,
		Audit: builtInRolloutAuditIntent(target, key),
	})
	return err
}

func builtInRolloutAuditIntent(target targetIdentity, idempotencyKey string) audit.Intent {
	const action = "delivery.rollout.created"
	const resourceType = "delivery_rollout"
	return audit.Intent{
		Event: audit.Event{
			Source: "service", ActionClass: "system", Action: action,
			ResourceType: resourceType, ResourceID: target.id.String(),
			ResourceName: "astronomer-builtins-" + target.slug,
			RequestID:    idempotencyKey,
			Detail: map[string]any{
				"actor":             systemActor,
				"target_generation": target.generation,
				"builtin_slug":      target.slug,
				"release":           target.release,
			},
		},
		DedupeKey: audit.MutationDedupeKey(idempotencyKey, action, resourceType, target.id.String()),
	}
}

func retryAfter(requestedAt *time.Time, rolloutCreatedAt time.Time) bool {
	return requestedAt != nil && requestedAt.After(rolloutCreatedAt)
}

func stableID(parts ...string) uuid.UUID {
	return uuid.NewSHA1(identityNamespace, []byte(strings.Join(parts, "\x00")))
}
