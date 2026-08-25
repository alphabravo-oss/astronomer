// Worker bridge: handler ↔ tasks adapter for the snapshot lifecycle workers
// (migration 052).
//
// The worker package (internal/worker/tasks) deliberately doesn't import
// the handler package — that would cycle through routes.go. Instead the
// worker declares the narrow VeleroSnapshotDriver interface, and we
// implement it here, in the handler package, so the worker can drive
// Velero CRDs over the same K8sRequester the handler uses.
//
// The bridge also wires the worker's outcome-metric callback into the
// handler's clusterSnapshotsTotal counter so terminal-phase transitions
// show up in the metrics endpoint without forcing the worker package
// to register its own prometheus state.

package handler

import (
	"context"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// VeleroDriverAdapter implements tasks.VeleroSnapshotDriver against the
// handler's K8sRequester. Production wiring passes a single instance into
// tasks.ClusterSnapshotRuntime.
type VeleroDriverAdapter struct {
	Requester K8sRequester
}

// NewVeleroDriverAdapter returns a driver adapter bound to the given
// requester. nil requester returns an adapter whose methods always
// fail — that lets cmd/server wire unconditionally and degrade clean
// when the tunnel is unavailable.
func NewVeleroDriverAdapter(r K8sRequester) *VeleroDriverAdapter {
	return &VeleroDriverAdapter{Requester: r}
}

// GetBackup fetches a Velero Backup CR + projects its status into the
// worker-side value type.
func (a *VeleroDriverAdapter) GetBackup(ctx context.Context, clusterID, namespace, name string) (tasks.VeleroBackupStatusSnapshot, error) {
	cr, err := getVeleroBackupCRD(ctx, a.Requester, clusterID, namespace, name)
	if err != nil {
		if err == ErrVeleroCRDMissing {
			return tasks.VeleroBackupStatusSnapshot{NotFound: true}, nil
		}
		return tasks.VeleroBackupStatusSnapshot{}, err
	}
	st := decodeBackupStatus(cr)
	out := tasks.VeleroBackupStatusSnapshot{
		Phase:    st.Phase,
		Warnings: st.Warnings,
		Errors:   st.Errors,
	}
	if t, err := parseRFC3339IfNonEmpty(st.StartTimestamp); err == nil {
		out.StartTime = t
	}
	if t, err := parseRFC3339IfNonEmpty(st.CompletionTimestamp); err == nil {
		out.CompletionTime = t
	}
	if len(st.ValidationErrors) > 0 {
		out.ValidationError = st.ValidationErrors[0]
	}
	return out, nil
}

// GetRestore fetches a Velero Restore CR + projects its status.
func (a *VeleroDriverAdapter) GetRestore(ctx context.Context, clusterID, namespace, name string) (tasks.VeleroRestoreStatusSnapshot, error) {
	cr, err := getVeleroRestoreCRD(ctx, a.Requester, clusterID, namespace, name)
	if err != nil {
		if err == ErrVeleroCRDMissing {
			return tasks.VeleroRestoreStatusSnapshot{NotFound: true}, nil
		}
		return tasks.VeleroRestoreStatusSnapshot{}, err
	}
	st := decodeRestoreStatus(cr)
	out := tasks.VeleroRestoreStatusSnapshot{
		Phase:    st.Phase,
		Warnings: st.Warnings,
		Errors:   st.Errors,
	}
	if t, err := parseRFC3339IfNonEmpty(st.StartTimestamp); err == nil {
		out.StartTime = t
	}
	if t, err := parseRFC3339IfNonEmpty(st.CompletionTimestamp); err == nil {
		out.CompletionTime = t
	}
	if len(st.ValidationErrors) > 0 {
		out.ValidationError = st.ValidationErrors[0]
	}
	return out, nil
}

// CreateSnapshot renders the committed desired-state row and idempotently
// creates its Velero Backup. The row ID is carried as an ownership label.
func (a *VeleroDriverAdapter) CreateSnapshot(ctx context.Context, row sqlc.ClusterSnapshot) error {
	spec := decodeSpec(row.Spec)
	body := renderPerClusterBackup(PerClusterSnapshotRender{
		Name: row.VeleroName, Namespace: row.VeleroNamespace,
		IncludedNamespaces: spec.IncludedNamespaces, ExcludedNamespaces: spec.ExcludedNamespaces,
		IncludedResources: spec.IncludedResources, ExcludedResources: spec.ExcludedResources,
		LabelSelector: spec.LabelSelector, SnapshotVolumes: spec.SnapshotVolumes, TTL: spec.TTL,
		StorageLocation: spec.StorageLocation, VolumeSnapshotLocations: spec.VolumeSnapshotLocations,
		SnapshotID: row.ID.String(),
	})
	return createVeleroBackupCRD(ctx, a.Requester, row.ClusterID.String(), body)
}

// CreateRestore renders a committed restore intent. A repeated POST after a
// worker crash is safe because the Velero helper treats AlreadyExists as the
// successful observation of the same immutable operation.
func (a *VeleroDriverAdapter) CreateRestore(ctx context.Context, row sqlc.ClusterRestore, snapshot sqlc.ClusterSnapshot) error {
	spec := decodeRestoreSpec(row.Spec)
	body := renderPerClusterRestore(PerClusterRestoreRender{
		Name: row.VeleroName, Namespace: row.VeleroNamespace, BackupName: snapshot.VeleroName,
		IncludedNamespaces: spec.IncludedNamespaces, ExcludedNamespaces: spec.ExcludedNamespaces,
		NamespaceMapping: spec.NamespaceMapping, LabelSelector: spec.LabelSelector,
		RestorePVs: spec.RestorePVs, RestoreID: row.ID.String(), SnapshotID: row.SnapshotID.String(),
	})
	return createVeleroRestoreCRD(ctx, a.Requester, row.TargetClusterID.String(), body)
}

// DeleteSnapshot uses a deterministic request name derived from the immutable
// snapshot ID. This is the idempotency fence for replay after an uncertain
// remote result.
func (a *VeleroDriverAdapter) DeleteSnapshot(ctx context.Context, clusterID, namespace, backupName, operationID string) error {
	requestName := deterministicDeleteBackupRequestName(backupName, operationID)
	body := renderDeleteBackupRequest(requestName, namespace, backupName)
	return createVeleroDeleteBackupRequest(ctx, a.Requester, clusterID, body)
}

func deterministicDeleteBackupRequestName(backupName, operationID string) string {
	suffix := "-delete-" + sanitizeForName(operationID)
	maxPrefix := 253 - len(suffix)
	prefix := sanitizeForName(backupName)
	if len(prefix) > maxPrefix {
		prefix = strings.TrimRight(prefix[:maxPrefix], "-")
	}
	if prefix == "" {
		prefix = "snapshot"
	}
	return prefix + suffix
}

// parseRFC3339IfNonEmpty parses a Velero status timestamp string. An
// empty input returns (zero, nil) so the caller can branch on a zero
// time. Malformed values bubble the error so the caller falls back to
// the row's existing timestamp.
func parseRFC3339IfNonEmpty(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}

// WireSnapshotWorkerMetrics hooks the worker package's outcome-metric
// callback into the handler-side clusterSnapshotsTotal counter. Calling
// this once at server startup means terminal-phase transitions
// recorded inside the poller show up on the /metrics endpoint.
func WireSnapshotWorkerMetrics() {
	tasks.SetSnapshotOutcomeRecorder(func(clusterID, outcome string) {
		if outcome == "" {
			return
		}
		clusterSnapshotsTotal.WithLabelValues(observability.MetricValues(clusterID, outcome)...).Inc()
	})
	// Publish the per-cluster in-flight gauge the poller aggregates each tick
	// (otherwise cluster_snapshots_in_flight stays a documented-but-dead metric).
	tasks.SetInFlightSnapshotGaugeSetter(SetInFlightSnapshotGauge)
}

// SetInFlightSnapshotGauge updates the per-cluster in-flight gauge to
// `count`. The poller worker calls this on every tick so the gauge
// stays accurate even when snapshots churn quickly. The argument is
// the number of non-terminal rows the poller observed for the cluster.
func SetInFlightSnapshotGauge(clusterID string, count float64) {
	clusterSnapshotsInFlight.WithLabelValues(observability.MetricValues(clusterID)...).Set(count)
}
