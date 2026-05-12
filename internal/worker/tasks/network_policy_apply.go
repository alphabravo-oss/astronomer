// Network policy apply + drift-check workers (migration 068).
//
// Two task types share one querier + K8sRequester wiring:
//
//   - network_policy:apply  — every 5m + on demand from the handler.
//     Walks rows with status IN (pending, failed, drifting), renders
//     the template body, server-side-applies via the tunnel
//     K8sRequester. Marks 'applied' on success or 'failed' + last_error
//     on failure. SSA is idempotent so converged rows fast-fail
//     through the apply path without a wire write.
//
//   - network_policy:drift_check — every 30m. For each 'applied' row,
//     GETs the in-cluster NetworkPolicy and compares its labels to
//     the expected marker (app.kubernetes.io/managed-by=astronomer +
//     astronomer.io/template=<slug>). On divergence (missing labels,
//     missing object) marks 'drifting' so the next apply sweep
//     re-stamps the object.
//
// The reconciler ONLY touches NetworkPolicy objects whose name starts
// with "astronomer-np-" (the prefix in netpol.PolicyName). Operator-
// authored policies with the same name suffix are out of scope and
// never edited.
package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/netpol"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Task type constants — re-exported via worker.TypeNetworkPolicyApply.
const (
	NetworkPolicyApplyType      = "network_policy:apply"
	NetworkPolicyDriftCheckType = "network_policy:drift_check"
)

// networkPolicyApplyBatch caps how many pending/failed rows the apply
// sweep handles per tick. Keeps the leader lease short so a crashed
// worker doesn't strand the queue.
const networkPolicyApplyBatch = 200

// networkPolicyDriftCheckBatch caps the drift sweep size per tick. Half
// the apply batch because each row requires an in-cluster GET round-
// trip (apply is fire-and-forget at the queue layer).
const networkPolicyDriftCheckBatch = 100

// networkPolicyFieldManager is the SSA field manager identifier we
// send on every Apply patch. Stable so the conflict-resolution path is
// predictable across worker restarts.
const networkPolicyFieldManager = "astronomer-netpol"

// NetworkPolicyApplyPayload is the optional task body for a single-row
// fire (operator clicks Reapply). When empty, the worker sweeps all
// pending rows; when set, only the row with the given ID is processed.
type NetworkPolicyApplyPayload struct {
	ApplicationID string `json:"application_id,omitempty"`
}

// NewNetworkPolicyApplyTask builds a generic sweep task. The handler
// calls this on /reapply/ to nudge the queue.
func NewNetworkPolicyApplyTask(applicationID uuid.UUID) (*asynq.Task, error) {
	payload := NetworkPolicyApplyPayload{}
	if applicationID != uuid.Nil {
		payload.ApplicationID = applicationID.String()
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal network policy apply payload: %w", err)
	}
	return asynq.NewTask(NetworkPolicyApplyType, body), nil
}

// NewNetworkPolicyDriftCheckTask returns the periodic drift sweep task.
func NewNetworkPolicyDriftCheckTask() (*asynq.Task, error) {
	return asynq.NewTask(NetworkPolicyDriftCheckType, nil), nil
}

// NetworkPolicyQuerier is the narrow DB surface the worker depends on.
// The production *sqlc.Queries satisfies it; tests stand up a fake.
type NetworkPolicyQuerier interface {
	GetNetworkPolicyApplicationByID(ctx context.Context, id uuid.UUID) (sqlc.NetworkPolicyApplication, error)
	ListPendingNetworkPolicyApplications(ctx context.Context, limit int32) ([]sqlc.NetworkPolicyApplication, error)
	ListAppliedNetworkPolicyApplications(ctx context.Context, limit int32) ([]sqlc.NetworkPolicyApplication, error)
	MarkNetworkPolicyApplicationStatus(ctx context.Context, arg sqlc.MarkNetworkPolicyApplicationStatusParams) (sqlc.NetworkPolicyApplication, error)
	GetNetworkPolicyTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.NetworkPolicyTemplate, error)
}

// NetworkPolicyApplyDeps wires the reconciler. Set once at startup via
// ConfigureNetworkPolicyApply; tests can swap fakes.
type NetworkPolicyApplyDeps struct {
	Queries   NetworkPolicyQuerier
	Requester K8sRequester
}

var networkPolicyDeps NetworkPolicyApplyDeps

// ConfigureNetworkPolicyApply wires the reconciler runtime. Called once
// from cmd/server (or the worker bootstrap).
func ConfigureNetworkPolicyApply(deps NetworkPolicyApplyDeps) {
	networkPolicyDeps = deps
}

// ResetNetworkPolicyApply clears the runtime deps. Used by tests so
// per-test Configure calls don't bleed across goroutine-parallel cases.
func ResetNetworkPolicyApply() {
	networkPolicyDeps = NetworkPolicyApplyDeps{}
}

// Metrics
//
// astronomer_network_policy_apply_total{template, outcome} counter and
// astronomer_network_policy_applications{cluster, status} gauge.
// Registered idempotently at package init.
var (
	networkPolicyApplyTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "network_policy_apply_total",
			Help:      "Total NetworkPolicy template applies attempted, labeled by template slug and outcome.",
		},
		observability.MetricLabels("template", "outcome"),
	)
	networkPolicyApplicationsGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "network_policy_applications",
			Help:      "Number of NetworkPolicy template applications per (cluster, status) bucket.",
		},
		observability.MetricLabels("cluster", "status"),
	)
)

func init() {
	prometheus.MustRegister(networkPolicyApplyTotal)
	prometheus.MustRegister(networkPolicyApplicationsGauge)
}

// recordNetworkPolicyOutcome bumps the apply counter with an
// outcome=success|failure label. Called from the apply path.
func recordNetworkPolicyOutcome(templateSlug, outcome string) {
	networkPolicyApplyTotal.WithLabelValues(observability.MetricValues(templateSlug, outcome)...).Inc()
}

// SetNetworkPolicyApplicationsGauge writes a gauge sample for the
// (cluster, status) bucket. Exposed so the handler can refresh on
// writes; the apply sweep also calls this for every row it processes.
func SetNetworkPolicyApplicationsGauge(clusterID, status string, n float64) {
	networkPolicyApplicationsGauge.WithLabelValues(observability.MetricValues(clusterID, status)...).Set(n)
}

// HandleNetworkPolicyApply is the asynq handler. When the payload
// carries an application ID we process just that row; otherwise we
// sweep the pending+failed+drifting queue.
func HandleNetworkPolicyApply(ctx context.Context, t *asynq.Task) error {
	if networkPolicyDeps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "network policy apply runtime not configured, skipping")
		return nil
	}
	var payload NetworkPolicyApplyPayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			runtimeLogger().ErrorContext(ctx, "unmarshal network policy apply payload", "error", err)
			return nil
		}
	}
	if payload.ApplicationID != "" {
		id, err := uuid.Parse(payload.ApplicationID)
		if err != nil {
			runtimeLogger().ErrorContext(ctx, "parse application id", "error", err)
			return nil
		}
		return runNetworkPolicyApplyOne(ctx, networkPolicyDeps, id)
	}
	return runPeriodicTaskWithLeader(ctx, NetworkPolicyApplyType, func() error {
		return runNetworkPolicyApplySweep(ctx, networkPolicyDeps)
	})
}

// runNetworkPolicyApplySweep is the periodic-tick body. Reads up to
// networkPolicyApplyBatch rows from the pending queue and applies each.
func runNetworkPolicyApplySweep(ctx context.Context, deps NetworkPolicyApplyDeps) error {
	rows, err := deps.Queries.ListPendingNetworkPolicyApplications(ctx, networkPolicyApplyBatch)
	if err != nil {
		return fmt.Errorf("list pending network policy applications: %w", err)
	}
	for _, row := range rows {
		applyOneNetworkPolicy(ctx, deps, row)
	}
	return nil
}

// runNetworkPolicyApplyOne handles a single-row fire (operator-clicked
// reapply). Loads the row, dispatches the same applyOne logic.
func runNetworkPolicyApplyOne(ctx context.Context, deps NetworkPolicyApplyDeps, id uuid.UUID) error {
	row, err := deps.Queries.GetNetworkPolicyApplicationByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load application %s: %w", id, err)
	}
	applyOneNetworkPolicy(ctx, deps, row)
	return nil
}

// applyOneNetworkPolicy is the per-row reconciler. Loads the template,
// renders, SSAs through the tunnel, marks the resulting status.
func applyOneNetworkPolicy(ctx context.Context, deps NetworkPolicyApplyDeps, row sqlc.NetworkPolicyApplication) {
	tmpl, err := deps.Queries.GetNetworkPolicyTemplateByID(ctx, row.TemplateID)
	if err != nil {
		markNetworkPolicyFailure(ctx, deps, row, fmt.Sprintf("load template: %v", err))
		return
	}
	if !tmpl.Enabled {
		// Disabled template — treat as paused. Leave the row alone; the
		// operator can re-enable to resume convergence.
		runtimeLogger().InfoContext(ctx, "skip apply for disabled template", "template_id", tmpl.ID, "application_id", row.ID)
		return
	}
	if deps.Requester == nil {
		// No K8s tunnel — record but don't fail-loop. The 5m sweep will
		// retry once a requester is wired (typically during local-only
		// dev where the worker boots without a cluster).
		markNetworkPolicyFailure(ctx, deps, row, "k8s requester not configured")
		return
	}
	rendered, err := netpol.Render(tmpl.SpecTemplate, netpol.Context{
		Namespace:  row.Namespace,
		PolicyName: row.PolicyName,
	})
	if err != nil {
		markNetworkPolicyFailure(ctx, deps, row, fmt.Sprintf("render: %v", err))
		return
	}
	if err := applyNetworkPolicySSA(ctx, deps, row, rendered); err != nil {
		markNetworkPolicyFailure(ctx, deps, row, err.Error())
		return
	}
	if _, err := deps.Queries.MarkNetworkPolicyApplicationStatus(ctx, sqlc.MarkNetworkPolicyApplicationStatusParams{
		ID:           row.ID,
		Status:       "applied",
		LastError:    "",
		TouchApplied: true,
	}); err != nil {
		runtimeLogger().ErrorContext(ctx, "mark applied", "error", err, "id", row.ID)
		return
	}
	recordNetworkPolicyOutcome(tmpl.Slug, "success")
}

// markNetworkPolicyFailure best-effort stamps last_error + status=failed
// and bumps the failure counter.
func markNetworkPolicyFailure(ctx context.Context, deps NetworkPolicyApplyDeps, row sqlc.NetworkPolicyApplication, msg string) {
	if _, err := deps.Queries.MarkNetworkPolicyApplicationStatus(ctx, sqlc.MarkNetworkPolicyApplicationStatusParams{
		ID:           row.ID,
		Status:       "failed",
		LastError:    msg,
		TouchApplied: false,
	}); err != nil {
		runtimeLogger().ErrorContext(ctx, "persist failure", "error", err, "id", row.ID, "msg", msg)
	}
	// Best-effort: derive the template slug for the metric without
	// re-querying. If the templateID lookup failed earlier we'll just
	// record "unknown".
	slug := "unknown"
	if t, err := deps.Queries.GetNetworkPolicyTemplateByID(ctx, row.TemplateID); err == nil {
		slug = t.Slug
	}
	recordNetworkPolicyOutcome(slug, "failure")
}

// applyNetworkPolicySSA sends a Server-Side Apply PATCH for the
// rendered NetworkPolicy. Path target is
// /apis/networking.k8s.io/v1/namespaces/{ns}/networkpolicies/{name}.
// fieldManager + force ensure we converge on re-apply.
func applyNetworkPolicySSA(ctx context.Context, deps NetworkPolicyApplyDeps, row sqlc.NetworkPolicyApplication, body []byte) error {
	path := fmt.Sprintf(
		"/apis/networking.k8s.io/v1/namespaces/%s/networkpolicies/%s?fieldManager=%s&force=true",
		row.Namespace, row.PolicyName, networkPolicyFieldManager,
	)
	resp, err := deps.Requester.Do(ctx, row.ClusterID.String(), http.MethodPatch, path, body, map[string]string{
		"Content-Type": "application/apply-patch+yaml",
		"Accept":       "application/json",
	})
	if err != nil {
		return fmt.Errorf("apply network policy: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("apply network policy status=%d body=%s", resp.StatusCode, resp.Body)
	}
	return nil
}

// DeleteNetworkPolicyInCluster removes the in-cluster NetworkPolicy
// associated with a deleted application row. Exposed so the handler
// can call it inline before deleting the DB row — the brief specifies
// "single tx" semantics from the operator's perspective (delete row
// + delete resource together).
func DeleteNetworkPolicyInCluster(ctx context.Context, requester K8sRequester, clusterID uuid.UUID, namespace, policyName string) error {
	if requester == nil {
		return fmt.Errorf("k8s requester not configured")
	}
	path := fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/networkpolicies/%s", namespace, policyName)
	resp, err := requester.Do(ctx, clusterID.String(), http.MethodDelete, path, nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("delete network policy status=%d body=%s", resp.StatusCode, resp.Body)
	}
	return nil
}

// ────────────────────────────────────────────────────────────────────────
// Drift check (periodic)
// ────────────────────────────────────────────────────────────────────────

// HandleNetworkPolicyDriftCheck is the asynq handler for the periodic
// drift sweep. Walks 'applied' rows, GETs each in-cluster
// NetworkPolicy, and marks 'drifting' on divergence so the next apply
// sweep re-stamps it.
func HandleNetworkPolicyDriftCheck(ctx context.Context, _ *asynq.Task) error {
	if networkPolicyDeps.Queries == nil {
		return nil
	}
	return runPeriodicTaskWithLeader(ctx, NetworkPolicyDriftCheckType, func() error {
		return runNetworkPolicyDriftSweep(ctx, networkPolicyDeps)
	})
}

func runNetworkPolicyDriftSweep(ctx context.Context, deps NetworkPolicyApplyDeps) error {
	rows, err := deps.Queries.ListAppliedNetworkPolicyApplications(ctx, networkPolicyDriftCheckBatch)
	if err != nil {
		return fmt.Errorf("list applied network policy applications: %w", err)
	}
	drift := 0
	for _, row := range rows {
		if deps.Requester == nil {
			// No requester means we can't tell — leave rows alone.
			continue
		}
		drifted, derr := checkNetworkPolicyDrift(ctx, deps, row)
		if derr != nil {
			runtimeLogger().WarnContext(ctx, "drift check error", "error", derr, "id", row.ID)
			continue
		}
		if drifted {
			drift++
			if _, err := deps.Queries.MarkNetworkPolicyApplicationStatus(ctx, sqlc.MarkNetworkPolicyApplicationStatusParams{
				ID:           row.ID,
				Status:       "drifting",
				LastError:    "drift detected; will reapply on next tick",
				TouchApplied: false,
			}); err != nil {
				runtimeLogger().WarnContext(ctx, "mark drifting", "error", err, "id", row.ID)
			}
		}
	}
	runtimeLogger().InfoContext(ctx, "network policy drift sweep", "evaluated", len(rows), "drift", drift)
	return nil
}

// checkNetworkPolicyDrift returns true when the in-cluster NetworkPolicy
// is missing OR its label set no longer matches the marker labels we
// stamp on apply (managed-by + template slug). This is deliberately a
// label-equality check rather than a full spec diff: a deep diff
// against the rendered manifest is expensive and adds little value
// over the apply-on-drift loop (which restamps the full spec).
func checkNetworkPolicyDrift(ctx context.Context, deps NetworkPolicyApplyDeps, row sqlc.NetworkPolicyApplication) (bool, error) {
	tmpl, err := deps.Queries.GetNetworkPolicyTemplateByID(ctx, row.TemplateID)
	if err != nil {
		return false, fmt.Errorf("load template: %w", err)
	}
	path := fmt.Sprintf("/apis/networking.k8s.io/v1/namespaces/%s/networkpolicies/%s", row.Namespace, row.PolicyName)
	resp, err := deps.Requester.Do(ctx, row.ClusterID.String(), http.MethodGet, path, nil, map[string]string{
		"Accept": "application/json",
	})
	if err != nil {
		return false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		// Object disappeared — drift.
		return true, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return false, fmt.Errorf("get network policy status=%d", resp.StatusCode)
	}
	var live struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &live); err != nil {
		return false, fmt.Errorf("decode network policy: %w", err)
	}
	if live.Metadata.Labels["app.kubernetes.io/managed-by"] != "astronomer" {
		return true, nil
	}
	if live.Metadata.Labels["astronomer.io/template"] != tmpl.Slug {
		return true, nil
	}
	return false, nil
}

// applyTimestampForRow is unused but reserved for future per-row
// staleness gating — exposed so the test surface stays consistent if
// the drift check later considers row.UpdatedAt + cooldown.
var _ = applyTimestampForRow

func applyTimestampForRow(row sqlc.NetworkPolicyApplication) time.Time {
	if row.LastAppliedAt.Valid {
		return row.LastAppliedAt.Time
	}
	return row.UpdatedAt
}
