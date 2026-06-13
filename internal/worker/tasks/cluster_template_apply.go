// Cluster template apply worker (migration 049).
//
// The handler enqueues this task when an operator applies (or re-applies)
// a cluster template. The worker walks the spec_snapshot stored on the
// cluster_template_applications row and converges the cluster to match.
//
// Steps (in order, each idempotent so re-runs are safe and so partial
// failures can be retried without ill effects):
//
//  1. Mark the row 'applying'.
//
//  2. environment    — UPDATE clusters.environment when the spec sets it.
//     Skipped silently when already equal.
//
//  3. labels         — merge spec.labels into clusters.labels. We MERGE
//     (not overwrite) so a template's labels don't blow
//     away operator-set labels on a cluster.
//
//  4. tools          — for each spec.tools entry, ensure the tool is
//     installed on the cluster. Skips when already
//     installed; otherwise enqueues a tool install
//     operation through the existing ToolInstaller —
//     we DO NOT reimplement helm wiring here.
//
//  5. default_project — when spec.default_project.name is non-empty, look
//     it up on the cluster; if absent, create it with
//     the PSS profile + resource-quota + netpol fields
//     from the spec.
//
//  6. registration_policy — stamp cluster_registration_policies with the
//     token_rotation_days knob so the existing token
//     cleanup task can act on it.
//
// On any step failure the row goes to 'failed' with last_error set and
// the worker returns nil (we deliberately don't return the error: asynq
// would otherwise retry the whole task with exponential backoff, racing
// the operator's reapply click). The reapply endpoint resets status
// back to 'pending' and re-enqueues.
//
// On success the row goes to 'applied' with applied_at = now().
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
)

// ClusterTemplateApplyType is the asynq task type. Re-exported via
// worker.TypeClusterTemplateApply for the mux wiring.
const ClusterTemplateApplyType = "cluster_template:apply"

// isAgentNotConnectedErr returns true when an error originated from the
// tunnel hub failing to find the agent on this pod. Multi-replica server
// deployments terminate the agent's WS on exactly one pod, but the asynq
// queue distributes tasks across all replicas — so a "not connected"
// error from the wrong pod is recoverable by returning the task to the
// queue (asynq retries with backoff and eventually the right pod grabs
// it). Permanent agent-down still surfaces, just after MaxRetry.
func isAgentNotConnectedErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "cluster agent not connected")
}

// ClusterTemplateApplyQueueName is the dedicated asynq queue that both
// the per-cluster apply task and the periodic drift sweep route through.
// These tasks require the tunnel hub (which only lives in the server
// pod) so they're processed by the server-embedded asynq.Server, not by
// the standalone astronomer-worker pod whose tasks.ConfigureClusterTemplateApply
// is intentionally unwired.
const ClusterTemplateApplyQueueName = "tunnel"

// ClusterTemplateDriftCheckType is the periodic drift sweep. Hourly
// cadence; cooperative leader lease keeps multiple worker pods from
// racing on the same row.
const ClusterTemplateDriftCheckType = "cluster_template:drift_check"

const platformSettingArgoCDManageBaselineKey = "argocd.manage_platform_baseline"

var argoCDManagedBaselineToolSlugs = map[string]struct{}{
	"trivy-operator":           {},
	"kube-state-metrics":       {},
	"prometheus-node-exporter": {},
	"fluent-bit":               {},
	"cert-manager":             {},
}

// clusterTemplateDriftCheckLimit caps how many applications the drift
// sweep evaluates per tick. The sweep is meant to surface drift as a UI
// badge, not be the hot path; keeping the batch small keeps the
// arbiter-style lease holder responsive.
const clusterTemplateDriftCheckLimit = 200

// ClusterTemplateApplyPayload is the asynq task body.
type ClusterTemplateApplyPayload struct {
	ClusterID string `json:"cluster_id"`
}

// NewClusterTemplateApplyTask builds an apply task for one cluster.
// Called by ClusterTemplateHandler.Apply / Reapply.
func NewClusterTemplateApplyTask(clusterID uuid.UUID) (*asynq.Task, error) {
	body, err := json.Marshal(ClusterTemplateApplyPayload{ClusterID: clusterID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal cluster template apply payload: %w", err)
	}
	return asynq.NewTask(ClusterTemplateApplyType, body), nil
}

// NewClusterTemplateDriftCheckTask returns the periodic-sweep task.
func NewClusterTemplateDriftCheckTask() (*asynq.Task, error) {
	return asynq.NewTask(ClusterTemplateDriftCheckType, nil), nil
}

// ClusterTemplateApplyQuerier is the slice of *sqlc.Queries the apply
// worker uses. Local interface so unit tests can stand up a fake without
// pulling in the full Queries surface.
type ClusterTemplateApplyQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	UpdateCluster(ctx context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error)
	GetClusterTemplateApplication(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error)
	MarkClusterTemplateApplicationStatus(ctx context.Context, arg sqlc.MarkClusterTemplateApplicationStatusParams) (sqlc.ClusterTemplateApplication, error)
	ListClusterTemplateApplicationsByStatus(ctx context.Context, arg sqlc.ListClusterTemplateApplicationsByStatusParams) ([]sqlc.ClusterTemplateApplication, error)
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	GetToolBySlug(ctx context.Context, slug string) (sqlc.ClusterTool, error)
	GetProjectByNameAndCluster(ctx context.Context, arg sqlc.GetProjectByNameAndClusterParams) (sqlc.Project, error)
	CreateProject(ctx context.Context, arg sqlc.CreateProjectParams) (sqlc.Project, error)
	UpsertClusterRegistrationPolicy(ctx context.Context, arg sqlc.UpsertClusterRegistrationPolicyParams) (sqlc.ClusterRegistrationPolicy, error)
	ListInstalledChartsByCluster(ctx context.Context, arg sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error)
	// Used by the drift sweep's stuck-applying detection (T8.4). We
	// upsert TemplateApplyStuck=True when an 'applying' row has sat
	// past stuckApplyingThreshold, and clear it on the next tick if
	// the row has moved off 'applying'.
	UpsertClusterCondition(ctx context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error)
}

// ConditionTemplateApplyStuck names the condition emitted when a
// cluster_template_applications row has been in 'applying' beyond
// stuckApplyingThreshold. The cluster_condition_reconcile worker picks
// this up and routes to a remedy (reset to 'failed' so the
// failed-row recovery sweep re-enqueues).
const ConditionTemplateApplyStuck = "TemplateApplyStuck"

// stuckApplyingThreshold is the grace window before an 'applying' row
// is treated as wedged. Long enough to cover normal tool-install times
// + a sane post-install hook timeout; short enough that an operator
// staring at the UI for "what's it doing?" gets an answer within a
// reasonable on-call window.
const stuckApplyingThreshold = 10 * time.Minute

// failedApplyMinBackoff is the minimum age of a 'failed'
// cluster_template_applications row before the hourly recovery sweep
// re-enqueues it (T5.5). 15 minutes is long enough that an agent
// reconnect window has resolved (the apply task itself returns to
// 'pending' on agent-not-connected and asynq retries quickly), but
// short enough that a slightly older failure still gets a retry on
// the very next sweep.
const failedApplyMinBackoff = 15 * time.Minute

// ToolInstaller is the bridge to the existing tool-install flow. The
// production *handler.ToolHandler implements this via its EnsureInstalled
// method. We narrow the surface here so the worker package doesn't take
// a transitive dependency on the entire handler package (which would
// import-cycle).
type ToolInstaller interface {
	EnsureInstalled(ctx context.Context, clusterID uuid.UUID, slug, releaseName, preset, valuesYAML string) (sqlc.InstalledChart, error)
}

// ClusterTemplateApplyDeps wires the apply worker. Set once at startup
// via ConfigureClusterTemplateApply; tests can swap fakes.
type ClusterTemplateApplyDeps struct {
	Queries   ClusterTemplateApplyQuerier
	Installer ToolInstaller
	// Registration is the wizard phase machine. Optional; when wired
	// the worker calls it on start (→ provisioning) and on end
	// (→ ready or → failed) so the SSE stream reflects the apply
	// progress without polling. nil-safe.
	Registration ClusterTemplateRegistrationAdvancer
}

// ClusterTemplateRegistrationAdvancer is the narrow surface the apply
// worker uses to advance the wizard phase + write per-tool step rows.
// registration.Service implements this natively.
type ClusterTemplateRegistrationAdvancer interface {
	OnTemplateApplyStart(ctx context.Context, clusterID uuid.UUID) error
	OnTemplateApplySuccess(ctx context.Context, clusterID uuid.UUID) error
	OnTemplateApplyFailure(ctx context.Context, clusterID uuid.UUID, errMsg string) error
	// Sprint 23: per-tool step rows. Each tool install gets a row when
	// it starts and the same row is updated to success/failed on
	// completion so the wizard page-3 timeline shows individual progress.
	WriteStep(ctx context.Context, clusterID uuid.UUID, in registration.StepInput) (sqlc.ClusterRegistrationStep, error)
	UpdateStep(ctx context.Context, in registration.UpdateStepInput) (sqlc.ClusterRegistrationStep, error)
}

var clusterTemplateApplyDeps ClusterTemplateApplyDeps

// ConfigureClusterTemplateApply wires runtime dependencies. Called once
// from cmd/server (or the worker process bootstrap).
func ConfigureClusterTemplateApply(deps ClusterTemplateApplyDeps) {
	clusterTemplateApplyDeps = deps
}

// ResetClusterTemplateApply clears the runtime deps. Used by tests so
// per-test ConfigureClusterTemplateApply calls don't bleed between
// goroutine-parallel test cases.
func ResetClusterTemplateApply() {
	clusterTemplateApplyDeps = ClusterTemplateApplyDeps{}
}

// templateSpec is the parsed shape of the spec JSONB. Unknown fields are
// ignored at apply time (the handler validates at write time); the worker
// is forgiving so a forward-compatible template doesn't 500 the queue.
type templateSpec struct {
	Environment        string            `json:"environment,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	Tools              []templateTool    `json:"tools,omitempty"`
	DefaultProject     *templateProject  `json:"default_project,omitempty"`
	RegistrationPolicy *templatePolicy   `json:"registration_policy,omitempty"`
}

type templateTool struct {
	Slug   string          `json:"slug"`
	Preset string          `json:"preset,omitempty"`
	Values json.RawMessage `json:"values,omitempty"`
}

type templateProject struct {
	Name                     string `json:"name"`
	PodSecurityProfile       string `json:"pod_security_profile,omitempty"`
	ResourceQuotaCpuLimit    string `json:"resource_quota_cpu_limit,omitempty"`
	ResourceQuotaMemoryLimit string `json:"resource_quota_memory_limit,omitempty"`
	ResourceQuotaPodCount    int32  `json:"resource_quota_pod_count,omitempty"`
	NetworkPolicyMode        string `json:"network_policy_mode,omitempty"`
}

type templatePolicy struct {
	TokenRotationDays int32 `json:"token_rotation_days,omitempty"`
}

// HandleClusterTemplateApply is the asynq handler. Loads the
// cluster_template_applications row, decodes the spec_snapshot, walks
// each step. Returns nil on terminal outcomes (applied OR failed): the
// row's status reflects the result; the reapply endpoint is the
// operator's recovery hook.
func HandleClusterTemplateApply(ctx context.Context, t *asynq.Task) error {
	if clusterTemplateApplyDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "cluster template apply runtime not configured, skipping")
		return nil
	}
	var payload ClusterTemplateApplyPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		runtimeLogger().ErrorContext(ctx, "unmarshal cluster template apply payload", "error", err)
		// Returning nil so asynq doesn't retry a structurally bad
		// payload that will never succeed.
		return nil
	}
	clusterID, err := uuid.Parse(payload.ClusterID)
	if err != nil {
		runtimeLogger().ErrorContext(ctx, "parse cluster id", "error", err, "raw", payload.ClusterID)
		return nil
	}
	return runClusterTemplateApply(ctx, clusterTemplateApplyDeps, clusterID)
}

// runClusterTemplateApply is the testable core. Split from
// HandleClusterTemplateApply so the unit tests don't need to construct
// an asynq.Task to exercise the apply logic.
func runClusterTemplateApply(ctx context.Context, deps ClusterTemplateApplyDeps, clusterID uuid.UUID) error {
	app, err := deps.Queries.GetClusterTemplateApplication(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Application row was deleted (operator detached) between
			// enqueue and execution. Not an error.
			return nil
		}
		runtimeLogger().ErrorContext(ctx, "load cluster template application", "error", err, "cluster_id", clusterID)
		return nil
	}

	// Mark applying. We don't gate on the existing status — the only
	// caller is the apply enqueue which always sets pending first, but
	// the periodic sweep may pick up a stuck 'applying' row and force a
	// re-run.
	if _, err := deps.Queries.MarkClusterTemplateApplicationStatus(ctx, sqlc.MarkClusterTemplateApplicationStatusParams{
		ClusterID: clusterID,
		Status:    "applying",
		LastError: "",
	}); err != nil {
		runtimeLogger().ErrorContext(ctx, "mark applying", "error", err, "cluster_id", clusterID)
		return nil
	}

	// Wizard phase: connected → provisioning. Idempotent; nil-safe.
	if deps.Registration != nil {
		if err := deps.Registration.OnTemplateApplyStart(ctx, clusterID); err != nil {
			runtimeLogger().WarnContext(ctx, "wizard phase advance on apply-start failed",
				"cluster_id", clusterID, "error", err)
		}
	}

	var spec templateSpec
	if len(app.SpecSnapshot) > 0 {
		if err := json.Unmarshal(app.SpecSnapshot, &spec); err != nil {
			persistApplyFailure(ctx, deps, clusterID, fmt.Sprintf("spec parse: %v", err))
			return nil
		}
	}

	// Load the cluster so we can compute the labels merge and the
	// idempotent environment skip.
	cluster, err := deps.Queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		persistApplyFailure(ctx, deps, clusterID, fmt.Sprintf("load cluster: %v", err))
		return nil
	}

	if err := applyClusterMutations(ctx, deps, cluster, spec); err != nil {
		persistApplyFailure(ctx, deps, clusterID, err.Error())
		return nil
	}
	if err := applyTools(ctx, deps, cluster, spec); err != nil {
		// "cluster agent not connected" means the agent's WS terminates
		// on a different server pod than the one this asynq worker is
		// running in. Reset the row to pending and return the error to
		// asynq so it gets re-enqueued; eventually the pod that holds
		// the agent's WS connection will pick the task up. Same applies
		// when the agent is mid-reconnect (server pod restart).
		if isAgentNotConnectedErr(err) {
			runtimeLogger().WarnContext(ctx, "apply deferred: agent not on this pod, returning task to queue",
				"cluster_id", clusterID, "error", err)
			_, _ = deps.Queries.MarkClusterTemplateApplicationStatus(ctx, sqlc.MarkClusterTemplateApplicationStatusParams{
				ClusterID: clusterID,
				Status:    "pending",
				LastError: "deferred: agent connected on a sibling pod, retrying",
			})
			return err
		}
		persistApplyFailure(ctx, deps, clusterID, err.Error())
		return nil
	}
	if err := applyDefaultProject(ctx, deps, clusterID, spec); err != nil {
		persistApplyFailure(ctx, deps, clusterID, err.Error())
		return nil
	}
	if err := applyRegistrationPolicy(ctx, deps, clusterID, app.TemplateID, spec); err != nil {
		persistApplyFailure(ctx, deps, clusterID, err.Error())
		return nil
	}

	appliedAt := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	if _, err := deps.Queries.MarkClusterTemplateApplicationStatus(ctx, sqlc.MarkClusterTemplateApplicationStatusParams{
		ClusterID: clusterID,
		Status:    "applied",
		LastError: "",
		AppliedAt: appliedAt,
	}); err != nil {
		runtimeLogger().ErrorContext(ctx, "mark applied", "error", err, "cluster_id", clusterID)
		return nil
	}

	// Wizard phase: provisioning → ready. Nil-safe.
	if deps.Registration != nil {
		if err := deps.Registration.OnTemplateApplySuccess(ctx, clusterID); err != nil {
			runtimeLogger().WarnContext(ctx, "wizard phase advance on apply-success failed",
				"cluster_id", clusterID, "error", err)
		}
	}
	return nil
}

// persistApplyFailure best-effort writes a failed status. Used at every
// terminal-failure branch so the operator sees the cause without
// re-running asynq retries (which would hide the error behind
// exponential backoff).
func persistApplyFailure(ctx context.Context, deps ClusterTemplateApplyDeps, clusterID uuid.UUID, msg string) {
	if _, err := deps.Queries.MarkClusterTemplateApplicationStatus(ctx, sqlc.MarkClusterTemplateApplicationStatusParams{
		ClusterID: clusterID,
		Status:    "failed",
		LastError: msg,
	}); err != nil {
		runtimeLogger().ErrorContext(ctx, "persist apply failure", "error", err, "cluster_id", clusterID, "msg", msg)
	}
	// Wizard phase: → failed. Nil-safe.
	if deps.Registration != nil {
		if err := deps.Registration.OnTemplateApplyFailure(ctx, clusterID, msg); err != nil {
			runtimeLogger().WarnContext(ctx, "wizard phase advance on apply-failure failed",
				"cluster_id", clusterID, "error", err)
		}
	}
}

// applyClusterMutations handles steps 2 + 3 (environment + labels).
// Both are no-ops when the spec doesn't set the corresponding field.
// Labels are MERGED (not replaced): a template can add tier=prod without
// destroying operator-set keys like cost-center=infra.
func applyClusterMutations(ctx context.Context, deps ClusterTemplateApplyDeps, cluster sqlc.Cluster, spec templateSpec) error {
	desiredEnv := cluster.Environment
	if spec.Environment != "" {
		desiredEnv = spec.Environment
	}

	labels := mergeLabels(cluster.Labels, spec.Labels)
	if equalEnv(cluster.Environment, desiredEnv) && jsonbEqual(cluster.Labels, labels) {
		return nil
	}

	// UpdateCluster overwrites display_name, description, region too —
	// pass the current cluster's values to preserve them. The fields we
	// actually want to mutate are environment + labels; the others are
	// pass-through.
	annotations := cluster.Annotations
	if len(annotations) == 0 {
		annotations = json.RawMessage(`{}`)
	}
	if _, err := deps.Queries.UpdateCluster(ctx, sqlc.UpdateClusterParams{
		ID:          cluster.ID,
		DisplayName: cluster.DisplayName,
		Description: cluster.Description,
		Environment: desiredEnv,
		Region:      cluster.Region,
		Labels:      labels,
		Annotations: annotations,
	}); err != nil {
		return fmt.Errorf("update cluster: %w", err)
	}
	return nil
}

// applyTools walks spec.tools and ensures each is installed. Reuses the
// ToolInstaller surface so we don't duplicate helm + adopt logic. When
// the Installer dep isn't wired (tests without it), each tool is treated
// as a no-op success.
func applyTools(ctx context.Context, deps ClusterTemplateApplyDeps, cluster sqlc.Cluster, spec templateSpec) error {
	if len(spec.Tools) == 0 {
		return nil
	}
	if deps.Installer == nil {
		// Without an installer wired we can't actually install tools; we
		// still treat the step as a success because failing here would
		// permanently keep the application in 'failed' status even when
		// the operator is running in a unit-test or chart-less env.
		return nil
	}
	argocdOwnsBaseline := !cluster.IsLocal && argoCDManagePlatformBaselineSetting(ctx, deps.Queries)
	// Sprint 23: emit a per-tool step row + SSE event so the wizard
	// progress timeline (page 3) shows individual install progress
	// instead of one opaque "Applying Platform Baseline → applied"
	// transition. Each tool gets a running row when its install starts
	// and the same row is updated to success / failed on completion.
	// Registration service is nil-safe: when unwired (test fakes) we
	// just install without writing step rows.
	for _, t := range spec.Tools {
		if argocdOwnsBaseline && isArgoCDManagedBaselineTool(t.Slug) {
			runtimeLogger().InfoContext(ctx, "skipping cluster-template tool install because ArgoCD owns platform baseline",
				"cluster_id", cluster.ID.String(),
				"tool_slug", t.Slug)
			continue
		}
		valuesYAML := ""
		if len(t.Values) > 0 {
			valuesYAML = string(t.Values)
		}
		var stepID uuid.UUID
		if deps.Registration != nil {
			step, werr := deps.Registration.WriteStep(ctx, cluster.ID, registration.StepInput{
				StepName:    "tool_installing:" + t.Slug,
				Status:      "running",
				ProgressPct: 0,
				Detail: map[string]any{
					"slug":   t.Slug,
					"preset": t.Preset,
				},
				MarkStarted: true,
			})
			if werr == nil {
				stepID = step.ID
			}
		}
		_, err := deps.Installer.EnsureInstalled(ctx, cluster.ID, t.Slug, t.Slug, t.Preset, valuesYAML)
		if err != nil {
			if deps.Registration != nil && stepID != uuid.Nil {
				_, _ = deps.Registration.UpdateStep(ctx, registration.UpdateStepInput{
					StepID:       stepID,
					Status:       "failed",
					ErrorMessage: err.Error(),
				})
			}
			return fmt.Errorf("ensure tool %q installed: %w", t.Slug, err)
		}
		if deps.Registration != nil && stepID != uuid.Nil {
			_, _ = deps.Registration.UpdateStep(ctx, registration.UpdateStepInput{
				StepID: stepID,
				Status: "success",
			})
		}
	}
	return nil
}

func isArgoCDManagedBaselineTool(slug string) bool {
	_, ok := argoCDManagedBaselineToolSlugs[strings.TrimSpace(slug)]
	return ok
}

func argoCDManagePlatformBaselineSetting(ctx context.Context, q ClusterTemplateApplyQuerier) bool {
	row, err := q.GetPlatformSetting(ctx, platformSettingArgoCDManageBaselineKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true
		}
		runtimeLogger().WarnContext(ctx, "failed to read argocd platform baseline setting; preserving legacy template installs", "error", err)
		return false
	}
	var enabled bool
	if err := json.Unmarshal(row.Value, &enabled); err != nil {
		runtimeLogger().WarnContext(ctx, "failed to parse argocd platform baseline setting; preserving legacy template installs", "error", err)
		return false
	}
	return enabled
}

// applyDefaultProject creates the spec'd project when absent. We don't
// edit an existing project — operators may have customised it after the
// initial apply, and stomping their changes on every reapply would be
// the wrong default. Operators who want to re-converge a project's
// policy fields use the projects:update PATCH endpoint directly.
func applyDefaultProject(ctx context.Context, deps ClusterTemplateApplyDeps, clusterID uuid.UUID, spec templateSpec) error {
	if spec.DefaultProject == nil || spec.DefaultProject.Name == "" {
		return nil
	}
	dp := spec.DefaultProject
	if _, err := deps.Queries.GetProjectByNameAndCluster(ctx, sqlc.GetProjectByNameAndClusterParams{
		Name:      dp.Name,
		ClusterID: clusterID,
	}); err == nil {
		// Already exists — leave it alone.
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup default project: %w", err)
	}

	netPolicy := dp.NetworkPolicyMode
	if netPolicy == "" {
		netPolicy = "none"
	}
	pss := dp.PodSecurityProfile
	if pss == "" {
		pss = "baseline"
	}
	if _, err := deps.Queries.CreateProject(ctx, sqlc.CreateProjectParams{
		Name:                     dp.Name,
		DisplayName:              dp.Name,
		Description:              "Created by cluster template",
		ClusterID:                clusterID,
		Namespaces:               json.RawMessage(`[]`),
		ResourceQuota:            json.RawMessage(`{}`),
		LimitRange:               json.RawMessage(`{}`),
		NetworkPolicyMode:        netPolicy,
		CreatedByID:              pgtype.UUID{},
		PodSecurityProfile:       pss,
		ResourceQuotaCpuLimit:    dp.ResourceQuotaCpuLimit,
		ResourceQuotaMemoryLimit: dp.ResourceQuotaMemoryLimit,
		ResourceQuotaPodCount:    dp.ResourceQuotaPodCount,
	}); err != nil {
		return fmt.Errorf("create default project: %w", err)
	}
	return nil
}

// applyRegistrationPolicy stamps the cluster's registration policy with
// the rotation knob from the spec. Idempotent: the Upsert query is
// ON CONFLICT DO UPDATE so re-running with the same days is a no-op.
func applyRegistrationPolicy(ctx context.Context, deps ClusterTemplateApplyDeps, clusterID, templateID uuid.UUID, spec templateSpec) error {
	if spec.RegistrationPolicy == nil {
		return nil
	}
	tmpl := pgtype.UUID{Bytes: templateID, Valid: true}
	if _, err := deps.Queries.UpsertClusterRegistrationPolicy(ctx, sqlc.UpsertClusterRegistrationPolicyParams{
		ClusterID:         clusterID,
		TokenRotationDays: spec.RegistrationPolicy.TokenRotationDays,
		SourceTemplateID:  tmpl,
	}); err != nil {
		return fmt.Errorf("upsert registration policy: %w", err)
	}
	return nil
}

// mergeLabels returns a new JSONB blob with existing keys preserved and
// spec keys overlaid. Both arguments are tolerant of nil/empty inputs.
// Empty result returns `{}` so the column never holds a JSON null
// (which the API contract for clusters.labels treats as "absent").
func mergeLabels(existing json.RawMessage, additions map[string]string) json.RawMessage {
	merged := map[string]string{}
	if len(existing) > 0 {
		// We deliberately ignore non-string values (existing labels
		// should only have string values; if they don't, that's a bug
		// elsewhere — don't compound it here by dropping the key).
		var current map[string]any
		if err := json.Unmarshal(existing, &current); err == nil {
			for k, v := range current {
				if s, ok := v.(string); ok {
					merged[k] = s
				}
			}
		}
	}
	for k, v := range additions {
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

// equalEnv compares two environment strings tolerating empty as "no
// change requested". Used to skip a no-op UpdateCluster call.
func equalEnv(have, want string) bool {
	if want == "" {
		return true
	}
	return have == want
}

// jsonbEqual returns true when two JSONB blobs decode to the same
// string->string map. Used to skip a no-op UpdateCluster when the
// existing labels already match the merged spec. Tolerant of nil/empty
// — both treated as `{}`.
func jsonbEqual(a, b json.RawMessage) bool {
	var ma, mb map[string]any
	if len(a) > 0 {
		_ = json.Unmarshal(a, &ma)
	}
	if len(b) > 0 {
		_ = json.Unmarshal(b, &mb)
	}
	if len(ma) != len(mb) {
		return false
	}
	for k, v := range ma {
		if vb, ok := mb[k]; !ok || fmt.Sprint(v) != fmt.Sprint(vb) {
			return false
		}
	}
	return true
}

// ────────────────────────────────────────────────────────────────────────
// Drift check (periodic, hourly)
// ────────────────────────────────────────────────────────────────────────

// HandleClusterTemplateDriftCheck is the periodic sweep that compares
// every 'applied' application's spec_snapshot against the live cluster
// state and emits a metric + audit row when they differ. Doesn't
// auto-correct — drift surfaces as a UI badge so the operator decides
// whether to reapply.
//
// Intentionally minimal: hashes the live (labels, environment) tuple
// against the snapshot. Tool/project drift could be added later but the
// labels + environment hash already catches the high-value cases
// (someone manually retiered a cluster, someone removed a label).
func HandleClusterTemplateDriftCheck(ctx context.Context, _ *asynq.Task) error {
	if clusterTemplateApplyDeps.Queries == nil {
		return nil
	}
	return runPeriodicTaskWithLeader(ctx, ClusterTemplateDriftCheckType, func() error {
		apps, err := clusterTemplateApplyDeps.Queries.ListClusterTemplateApplicationsByStatus(ctx, sqlc.ListClusterTemplateApplicationsByStatusParams{
			Status: "applied",
			Limit:  int32(clusterTemplateDriftCheckLimit),
		})
		if err != nil {
			return err
		}
		drift := 0
		for _, app := range apps {
			cluster, err := clusterTemplateApplyDeps.Queries.GetClusterByID(ctx, app.ClusterID)
			if err != nil {
				continue
			}
			var spec templateSpec
			_ = json.Unmarshal(app.SpecSnapshot, &spec)
			if !equalEnv(cluster.Environment, spec.Environment) {
				drift++
				continue
			}
			merged := mergeLabels(cluster.Labels, spec.Labels)
			if !jsonbEqual(cluster.Labels, merged) {
				drift++
				continue
			}
		}
		runtimeLogger().InfoContext(ctx, "cluster template drift sweep", "evaluated", len(apps), "drift", drift)
		// Stuck-row recovery. A `failed` cluster_template_applications
		// row should NOT need a manual reapply click — the operator has
		// already opted in via install_baseline=true, and the failure is
		// usually transient (agent reconnect window, helm post-install
		// hook timeout, image pull race). Walk the failed rows and
		// re-enqueue them; the apply task itself is idempotent and
		// already handles agent-not-connected via asynq retry.
		//
		// Bounded by clusterTemplateDriftCheckLimit and rate-limited by
		// the hourly sweep cadence so a permanently-broken cluster
		// doesn't pin a worker burning retries — asynq's per-task
		// MaxRetry caps that on the apply side.
		if enqueuer := failedApplyEnqueuer; enqueuer != nil {
			failed, ferr := clusterTemplateApplyDeps.Queries.ListClusterTemplateApplicationsByStatus(ctx, sqlc.ListClusterTemplateApplicationsByStatusParams{
				Status: "failed",
				Limit:  int32(clusterTemplateDriftCheckLimit),
			})
			if ferr == nil {
				// T5.5 — per-row time-based backoff. The hourly sweep
				// cadence is already a coarse backoff, but it
				// re-enqueues every cluster on every tick regardless
				// of how recently it failed. A cluster whose helm
				// pre-install hook fails consistently would pile a
				// fresh failed-step row each hour. Skip rows whose
				// updated_at is younger than failedApplyMinBackoff
				// (15m) so a freshly-failed apply gets one fast
				// retry from the agent reconnect path, then waits
				// before the next attempt.
				skipped := 0
				enqueued := 0
				for _, app := range failed {
					if time.Since(app.UpdatedAt) < failedApplyMinBackoff {
						skipped++
						continue
					}
					task, terr := NewClusterTemplateApplyTask(app.ClusterID)
					if terr != nil {
						continue
					}
					_, _ = enqueuer.Enqueue(task, asynq.Queue(ClusterTemplateApplyQueueName))
					enqueued++
				}
				if enqueued > 0 || skipped > 0 {
					runtimeLogger().InfoContext(ctx, "cluster template recovery sweep",
						"re_enqueued", enqueued, "skipped_backoff", skipped)
				}
			}
		}

		// T8.4 — stuck-applying detection. Walk 'applying' rows; any
		// that have sat past stuckApplyingThreshold get a
		// TemplateApplyStuck=True condition so the cluster-condition
		// reconciler can route them to remediation, and the
		// cluster-detail UI surfaces a red badge instead of leaving
		// the user staring at a never-finishing spinner.
		applying, aerr := clusterTemplateApplyDeps.Queries.ListClusterTemplateApplicationsByStatus(ctx, sqlc.ListClusterTemplateApplicationsByStatusParams{
			Status: "applying",
			Limit:  int32(clusterTemplateDriftCheckLimit),
		})
		if aerr == nil {
			stuck := 0
			for _, app := range applying {
				if time.Since(app.UpdatedAt) <= stuckApplyingThreshold {
					continue
				}
				_, cerr := clusterTemplateApplyDeps.Queries.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
					ClusterID: app.ClusterID,
					Type:      ConditionTemplateApplyStuck,
					Status:    "True",
					Reason:    "ApplyingOverThreshold",
					Message: fmt.Sprintf(
						"cluster_template_applications has been 'applying' for %s (last update %s) — exceeds %s",
						time.Since(app.UpdatedAt).Round(time.Second),
						app.UpdatedAt.UTC().Format(time.RFC3339),
						stuckApplyingThreshold,
					),
				})
				if cerr != nil {
					runtimeLogger().WarnContext(ctx, "stuck-applying condition write failed",
						"cluster_id", app.ClusterID, "error", cerr)
					continue
				}
				stuck++
			}
			if stuck > 0 {
				runtimeLogger().InfoContext(ctx, "cluster template stuck-applying sweep", "marked", stuck)
			}
		}
		return nil
	})
}

// FailedApplyEnqueuer is the slim asynq Client surface the recovery
// sweep needs. Server-side glue passes the existing apply queue client.
type FailedApplyEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

var failedApplyEnqueuer FailedApplyEnqueuer

// ConfigureFailedApplyEnqueuer wires the asynq client the drift sweep
// uses to re-enqueue stuck `failed` rows. Optional — when unwired the
// sweep continues to do its drift-detection work and just skips the
// recovery step. nil-safe.
func ConfigureFailedApplyEnqueuer(e FailedApplyEnqueuer) {
	failedApplyEnqueuer = e
}
