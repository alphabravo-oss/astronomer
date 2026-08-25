// Package tasks — migration 070 apiserver allow-list reconciler.
//
// Three task types:
//
//   - "apiserver_allowlist:reconcile"  — single (cluster) reconcile,
//     enqueued by the handler on PUT or the on-demand /reconcile/
//     endpoint.
//   - "apiserver_allowlist:reconcile_all" — periodic 15m sweep over
//     every active (mode != 'disabled') row.
//   - "apiserver_allowlist:cleanup_snapshots" — daily 90d retention
//     prune on apiserver_allowlist_snapshots.
//
// Reconcile algorithm (per row):
//  1. Detect provider via the registry.
//  2. GetEffective from the cloud LB / firewall.
//  3. Render desired from operator CIDRs + Astronomer egress + emergency.
//  4. If mode='monitor': snapshot (with drift flag), mark sync_status
//     based on diff, NO patch.
//  5. If mode='enforce' AND set differs: Apply(desired), snapshot,
//     mark sync_status accordingly.
//  6. Enforcement on a monitor-only provider is a permanent configuration
//     error. The HTTP API rejects new rows and this worker fails closed for
//     legacy rows rather than silently downgrading their declared mode.
//
// The cleartext credentials for the cloud API client live inside the
// provider driver's per-call materializer resolution; nothing persists
// in this worker's memory beyond the duration of one Apply.
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist"
	"github.com/alphabravocompany/astronomer-go/internal/apisvr/allowlist/providers"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// Task type identifiers. Exported for the worker mux + scheduler.
const (
	ApiserverAllowlistReconcileType        = "apiserver_allowlist:reconcile"
	ApiserverAllowlistReconcileAllType     = "apiserver_allowlist:reconcile_all"
	ApiserverAllowlistCleanupSnapshotsType = "apiserver_allowlist:cleanup_snapshots"
)

// SnapshotRetentionDays is the cleanup horizon. Documented contract from
// the schema comment.
const SnapshotRetentionDays = 90

// ApiserverAllowlistReconcilePayload is the per-cluster invocation body.
type ApiserverAllowlistReconcilePayload struct {
	ClusterID string `json:"cluster_id"`
}

// NewApiserverAllowlistReconcileTask builds the asynq envelope for one
// cluster's reconcile.
func NewApiserverAllowlistReconcileTask(clusterID uuid.UUID) (*asynq.Task, error) {
	body, err := json.Marshal(ApiserverAllowlistReconcilePayload{ClusterID: clusterID.String()})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(ApiserverAllowlistReconcileType, body), nil
}

// ApiserverAllowlistQuerier is the DB surface the reconciler uses.
// Tests pass a hand-rolled fake; production wires *sqlc.Queries.
type ApiserverAllowlistQuerier interface {
	GetApiserverAllowlistByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ApiserverAllowlist, error)
	ListActiveApiserverAllowlists(ctx context.Context) ([]sqlc.ApiserverAllowlist, error)
	UpdateApiserverAllowlistReconcileState(ctx context.Context, arg sqlc.UpdateApiserverAllowlistReconcileStateParams) error
	InsertApiserverAllowlistSnapshot(ctx context.Context, arg sqlc.InsertApiserverAllowlistSnapshotParams) (sqlc.ApiserverAllowlistSnapshot, error)
	DeleteApiserverAllowlistSnapshotsOlderThan(ctx context.Context, cutoff time.Time) error
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

// ApiserverAllowlistClusterShaper resolves a sqlc.Cluster into the
// providers.Cluster shape the drivers consume. Defined as an injectable
// closure so the unit tests can stand up a synthetic shaper without
// pulling in the full cluster handler.
type ApiserverAllowlistClusterShaper func(sqlc.Cluster) providers.Cluster

// ApiserverAllowlistReconcileDeps wires the reconciler.
type ApiserverAllowlistReconcileDeps struct {
	Queries       ApiserverAllowlistQuerier
	Registry      *providers.Registry
	ClusterShaper ApiserverAllowlistClusterShaper
	// AstronomerEgress is the runtime-known tunnel egress CIDR list.
	// Defaults to allowlist.AstronomerEgressFromEnv() when nil.
	AstronomerEgress []string
	// EmergencyAccess is the global emergency-access CIDR list (optional).
	EmergencyAccess []string
	// AuditWriter is the queries-shaped audit-row writer. May be nil;
	// the reconciler emits no audit rows in that case.
	AuditWriter any
}

// Metrics ---------------------------------------------------------------

var (
	// apiserverAllowlistDriftGauge mirrors apiserver_allowlists.sync_status
	// for every active row. 1 when drifting, 0 when synced. Reported per-
	// cluster so the operator dashboard can light up a per-cluster badge.
	apiserverAllowlistDriftGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "astronomer",
			Name:      "apiserver_allowlist_drift",
			Help:      "1 when the cluster's apiserver allow-list is drifting from desired, 0 when synced.",
		},
		observability.MetricLabels("cluster"),
	)

	// apiserverAllowlistReconcilesTotal counts every reconcile by
	// cluster, detected provider, and outcome (synced / drifting /
	// applied / failed / skipped).
	apiserverAllowlistReconcilesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "apiserver_allowlist_reconciles_total",
			Help:      "Apiserver allow-list reconcile outcomes by cluster + provider.",
		},
		observability.MetricLabels("cluster", "provider", "outcome"),
	)

	apiserverAllowlistProviderAuthorizationFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "astronomer",
			Name:      "apiserver_allowlist_provider_authorization_failures_total",
			Help:      "Cloud-provider authorization failures while reconciling API-server allow-lists.",
		},
		observability.MetricLabels("cluster", "provider"),
	)
)

func init() {
	prometheus.MustRegister(apiserverAllowlistDriftGauge, apiserverAllowlistReconcilesTotal, apiserverAllowlistProviderAuthorizationFailures)
}

// Handlers --------------------------------------------------------------

// HandleApiserverAllowlistReconcile is the per-cluster reconcile task.
func (runtime ApiserverAllowlistRuntime) HandleApiserverAllowlistReconcile(ctx context.Context, t *asynq.Task) error {
	if runtime.Deps.Queries == nil || runtime.Deps.Registry == nil {
		return fmt.Errorf("apiserver allowlist reconcile runtime is not configured")
	}
	var p ApiserverAllowlistReconcilePayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal apiserver allowlist reconcile payload: %w", err)
	}
	clusterID, err := uuid.Parse(p.ClusterID)
	if err != nil {
		return fmt.Errorf("invalid cluster_id: %w", err)
	}
	return runtime.ReconcileApiserverAllowlistOnce(ctx, clusterID)
}

// HandleApiserverAllowlistReconcileAll is the periodic 15m sweep handler.
func (runtime ApiserverAllowlistRuntime) HandleApiserverAllowlistReconcileAll(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ApiserverAllowlistReconcileAllType, func() error {
		if runtime.Deps.Queries == nil || runtime.Deps.Registry == nil {
			return fmt.Errorf("apiserver allowlist reconcile runtime is not configured")
		}
		rows, err := runtime.Deps.Queries.ListActiveApiserverAllowlists(ctx)
		if err != nil {
			return fmt.Errorf("list active allow-lists: %w", err)
		}
		for _, row := range rows {
			if err := runtime.ReconcileApiserverAllowlistOnce(ctx, row.ClusterID); err != nil {
				runtimeLogger(ctx).WarnContext(ctx, "apiserver allowlist reconcile error",
					"cluster_id", row.ClusterID.String(),
					"error", err)
			}
		}
		return nil
	})
}

// HandleApiserverAllowlistCleanupSnapshots is the daily 90d retention sweep.
func (runtime ApiserverAllowlistRuntime) HandleApiserverAllowlistCleanupSnapshots(ctx context.Context, _ *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, ApiserverAllowlistCleanupSnapshotsType, func() error {
		if runtime.Deps.Queries == nil {
			return fmt.Errorf("apiserver allowlist cleanup runtime is not configured")
		}
		cutoff := time.Now().Add(-time.Duration(SnapshotRetentionDays) * 24 * time.Hour)
		if err := runtime.Deps.Queries.DeleteApiserverAllowlistSnapshotsOlderThan(ctx, cutoff); err != nil {
			return fmt.Errorf("delete old allow-list snapshots: %w", err)
		}
		return nil
	})
}

// ReconcileApiserverAllowlistOnce runs the per-cluster reconcile algorithm.
// Exported so the handler's on-demand reconcile path can call directly
// instead of going through asynq for an immediate result.
func (runtime ApiserverAllowlistRuntime) ReconcileApiserverAllowlistOnce(ctx context.Context, clusterID uuid.UUID) error {
	deps := runtime.Deps
	row, err := deps.Queries.GetApiserverAllowlistByClusterID(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("load allow-list row: %w", err)
	}
	clusterRow, err := deps.Queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return fmt.Errorf("load cluster row: %w", err)
	}
	var pc providers.Cluster
	if deps.ClusterShaper != nil {
		pc = deps.ClusterShaper(clusterRow)
	} else {
		pc = providers.Cluster{ID: clusterID, Provider: clusterRow.Provider, Name: clusterRow.Name}
	}
	detectedID, driver := deps.Registry.Detect(ctx, pc)
	if driver == nil {
		// Detected as unknown; record state, no patch.
		operatorCIDRs := decodeCIDRsRow(row.Cidrs)
		desired := allowlist.Render(operatorCIDRs, egressOrEnv(deps.AstronomerEgress), deps.EmergencyAccess)
		return stampReconcileOutcome(ctx, deps, row, providers.ProviderUnknown, "failed", "no provider driver detected", desired, []string{})
	}
	capability := deps.Registry.CapabilityFor(ctx, pc)
	if row.Mode == "enforce" && !capability.CanEnforce {
		operatorCIDRs := decodeCIDRsRow(row.Cidrs)
		desired := allowlist.Render(operatorCIDRs, egressOrEnv(deps.AstronomerEgress), deps.EmergencyAccess)
		unsupported := &providers.UnsupportedEnforcementError{Provider: detectedID, Reason: capability.Reason}
		_ = stampReconcileOutcome(ctx, deps, row, detectedID, "failed", unsupported.Error(), desired, []string{})
		return unsupported
	}

	// Step 2: GetEffective from the cloud LB / firewall.
	effective, getErr := driver.GetEffective(ctx, pc)
	if getErr != nil {
		// Real error: stamp + emit metric + bail.
		apiserverAllowlistReconcilesTotal.WithLabelValues(observability.MetricValues(clusterID.String(), detectedID, "failed")...).Inc()
		recordApiserverAllowlistProviderAuthorizationFailure(clusterID, detectedID, getErr)
		_ = stampReconcileOutcome(ctx, deps, row, detectedID, "failed", fmt.Sprintf("get_effective: %v", getErr), nil, []string{})
		return getErr
	}
	if effective == nil {
		effective = []string{}
	}
	effective = allowlist.CanonicaliseEffective(effective)

	// Step 3: Render desired.
	operatorCIDRs := decodeCIDRsRow(row.Cidrs)
	desired := allowlist.Render(operatorCIDRs, egressOrEnv(deps.AstronomerEgress), deps.EmergencyAccess)
	drift := !allowlist.SameSet(effective, desired)

	// Always snapshot — both monitor + enforce.
	desiredJSON, _ := json.Marshal(desired)
	effectiveJSON, _ := json.Marshal(effective)
	_, _ = deps.Queries.InsertApiserverAllowlistSnapshot(ctx, sqlc.InsertApiserverAllowlistSnapshotParams{
		ClusterID:      clusterID,
		EffectiveCidrs: effectiveJSON,
		DesiredCidrs:   desiredJSON,
		Drift:          drift,
	})

	// Step 4-6: dispatch on mode.
	switch row.Mode {
	case "monitor":
		status := "synced"
		if drift {
			status = "drifting"
		}
		apiserverAllowlistReconcilesTotal.WithLabelValues(observability.MetricValues(clusterID.String(), detectedID, status)...).Inc()
		setDriftGauge(clusterID.String(), drift)
		return stampReconcileOutcome(ctx, deps, row, detectedID, status, "", desired, effective)
	case "enforce":
		if !drift {
			apiserverAllowlistReconcilesTotal.WithLabelValues(observability.MetricValues(clusterID.String(), detectedID, "synced")...).Inc()
			setDriftGauge(clusterID.String(), false)
			return stampReconcileOutcome(ctx, deps, row, detectedID, "synced", "", desired, effective)
		}
		applyErr := driver.Apply(ctx, pc, desired)
		if applyErr != nil {
			apiserverAllowlistReconcilesTotal.WithLabelValues(observability.MetricValues(clusterID.String(), detectedID, "failed")...).Inc()
			recordApiserverAllowlistProviderAuthorizationFailure(clusterID, detectedID, applyErr)
			setDriftGauge(clusterID.String(), true)
			_ = stampReconcileOutcome(ctx, deps, row, detectedID, "failed", applyErr.Error(), desired, effective)
			return applyErr
		}
		apiserverAllowlistReconcilesTotal.WithLabelValues(observability.MetricValues(clusterID.String(), detectedID, "applied")...).Inc()
		setDriftGauge(clusterID.String(), false)
		return stampReconcileOutcome(ctx, deps, row, detectedID, "synced", "", desired, desired)
	default:
		// Unknown mode (shouldn't reach here due to CHECK constraint).
		return nil
	}
}

func recordApiserverAllowlistProviderAuthorizationFailure(clusterID uuid.UUID, provider providers.ProviderID, err error) {
	if providers.IsAuthorizationError(err) {
		apiserverAllowlistProviderAuthorizationFailures.WithLabelValues(observability.MetricValues(clusterID.String(), provider)...).Inc()
	}
}

// stampReconcileOutcome persists the per-tick state on the row.
func stampReconcileOutcome(ctx context.Context, deps ApiserverAllowlistReconcileDeps, row sqlc.ApiserverAllowlist, detected, status, lastErr string, _ []string, effective []string) error {
	effectiveJSON, _ := json.Marshal(effective)
	return deps.Queries.UpdateApiserverAllowlistReconcileState(ctx, sqlc.UpdateApiserverAllowlistReconcileStateParams{
		ClusterID:        row.ClusterID,
		DetectedProvider: detected,
		SyncStatus:       status,
		LastError:        lastErr,
		EffectiveCidrs:   effectiveJSON,
	})
}

func setDriftGauge(clusterID string, drift bool) {
	v := float64(0)
	if drift {
		v = 1
	}
	apiserverAllowlistDriftGauge.WithLabelValues(observability.MetricValues(clusterID)...).Set(v)
}

// decodeCIDRsRow unwraps a JSONB CIDR list column into []string.
// Returns empty slice on parse failure (the reconciler should not crash
// on a malformed row — log via the snapshot diff + last_error instead).
func decodeCIDRsRow(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

// egressOrEnv returns the configured AstronomerEgress slice; falls back
// to the env-derived list when the deps slot is empty.
func egressOrEnv(configured []string) []string {
	if len(configured) > 0 {
		return configured
	}
	return allowlist.AstronomerEgressFromEnv()
}
