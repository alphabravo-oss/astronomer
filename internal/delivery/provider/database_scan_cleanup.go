package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

// Private typed finite retention. SQL applies the same terminal minimum and
// at most 32 unresolved claim allocations per cluster; unknown/crash ownership
// consumes allocation rather than permitting unlimited fresh PVCs/credentials.
type databaseScanResourceRetention struct{ terminalAge time.Duration }

func databaseScanRetentionPolicy() databaseScanResourceRetention {
	return databaseScanResourceRetention{terminalAge: time.Hour}
}

func terminalDatabaseScanClaim(claim sqlc.VulnerabilityDatabaseScanClaim, clock time.Time) bool {
	return (claim.Phase == "completed" || claim.Phase == "failed" || claim.Phase == "superseded") && clock.Sub(claim.UpdatedAt) >= databaseScanRetentionPolicy().terminalAge
}
func (owner *DatabaseScanOwner) cleanupTerminalScanClaim(ctx context.Context, identifier uuid.UUID) error {
	bounded, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	connection, err := owner.pool.Acquire(bounded)
	if err != nil {
		return safeOwnedSnapshotError(bounded, "terminal cleanup connection", err)
	}
	session := sqlc.New(connection)
	attempted, committed := false, false
	var transaction pgx.Tx
	var cluster uuid.UUID
	// Reuse the exact bounded rollback/unlock/hijack-close lifecycle. Cleanup
	// is reduce-only and never runs the canonical compiler or grants a feed.
	defer func() { finishOwnedSnapshotSession(connection, session, &transaction, cluster, &attempted, &committed) }()
	initial, err := session.GetVulnerabilityDatabaseScanClaim(bounded, identifier)
	if err != nil {
		return safeOwnedSnapshotError(bounded, "terminal cleanup claim", err)
	}
	cluster = initial.ClusterID
	attempted = true
	if err := session.LockBuiltinAssetsSession(bounded, cluster.String()); err != nil {
		return safeOwnedSnapshotError(bounded, "terminal cleanup lock", err)
	}
	transaction, err = connection.BeginTx(bounded, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return safeOwnedSnapshotError(bounded, "terminal cleanup transaction", err)
	}
	queries := sqlc.New(transaction)
	current, err := queries.GetVulnerabilityDatabaseScanClaimForUpdate(bounded, identifier)
	if err != nil || !reflect.DeepEqual(current, initial) || !terminalDatabaseScanClaim(current, time.Now().UTC()) {
		return errors.New("owned terminal cleanup claim changed or not retained")
	}
	resources, err := queries.ListOwnedDatabaseResourcesForUpdate(bounded, current.ID)
	if err != nil || len(resources) > 6 {
		return errors.New("owned terminal cleanup resource inventory unavailable")
	}
	// Foreground Job deletion/drain precedes PVC, Secret and deny-policy removal.
	sort.Slice(resources, func(i, j int) bool {
		return terminalResourceOrder(resources[i].Role) < terminalResourceOrder(resources[j].Role)
	})
	for _, resource := range resources {
		if resource.RemovedAt.Valid {
			continue
		}
		if err := removeTerminalDatabaseResource(bounded, owner.requester, current, resource); err != nil {
			return err
		}
		if err := requireDatabaseScanWrite(queries.MarkOwnedDatabaseResourceRemoved(bounded, sqlc.MarkOwnedDatabaseResourceRemovedParams{ClaimID: current.ID, Role: resource.Role})); err != nil {
			return err
		}
	}
	if err := transaction.Commit(bounded); err != nil {
		return safeOwnedSnapshotError(bounded, "terminal cleanup commit", err)
	}
	committed = true
	return nil
}
func terminalResourceOrder(role string) int {
	switch role {
	case "preparation":
		return 0
	case "scan":
		return 1
	case "input":
		return 2
	case "authority":
		return 3
	case "image-pull":
		return 4
	case "deny":
		return 5
	default:
		return 99
	}
}

// A successful deletion never deletes report/CVE/observation/witness records.
// Recreated UID, unknown ownership and unavailable readback remain UNKNOWN.
func removeTerminalDatabaseResource(ctx context.Context, requester databaseScanRequester, claim sqlc.VulnerabilityDatabaseScanClaim, resource sqlc.VulnerabilityDatabaseScanResource) error {
	role, valid := databaseScanResourceRole(claim.ID, resource.ResourcePath)
	if !valid || role != resource.Role || resource.ClaimID != claim.ID || resource.ClusterID != claim.ClusterID || !terminalDatabaseScanClaim(claim, time.Now().UTC()) {
		return errors.New("owned terminal resource does not belong to retained claim")
	}
	scope, err := newDatabaseScanScopedRequester(requester, claim.ClusterID, []string{resource.ResourcePath}, nil)
	if err != nil {
		return err
	}
	meta, missing, err := readTerminalDatabaseResource(ctx, scope, claim.ClusterID, resource.ResourcePath)
	if err != nil {
		return err
	}
	if !resource.ResourceUid.Valid {
		return errors.New("owned terminal resource created UID was not durably observed")
	}
	uid := types.UID(uuid.UUID(resource.ResourceUid.Bytes).String())
	if !missing {
		if !terminalDatabaseResourceMetadataMatches(meta, claim, resource, uid) {
			return errors.New("owned terminal resource UID or claim ownership changed")
		}
		deleting, body, err := newDatabaseScanTerminalRequester(requester, claim.ClusterID, claim.ID, resource.ResourcePath, uid, meta.ResourceVersion)
		if err != nil {
			return err
		}
		defer clear(body)
		response, err := deleting.Do(ctx, claim.ClusterID.String(), http.MethodDelete, resource.ResourcePath, body, map[string]string{"Content-Type": "application/json"})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || response == nil || response.StatusCode != 200 && response.StatusCode != 202 && response.StatusCode != 404 {
			return errors.New("owned terminal resource preconditioned deletion unavailable")
		}
		for {
			meta, missing, err = readTerminalDatabaseResource(ctx, scope, claim.ClusterID, resource.ResourcePath)
			if err != nil {
				return err
			}
			if missing {
				break
			}
			if !terminalDatabaseResourceMetadataMatches(meta, claim, resource, uid) {
				return errors.New("owned terminal resource was recreated during cleanup")
			}
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if role == "preparation" || role == "scan" {
		return waitTerminalDatabaseJobPodsGone(ctx, requester, claim.ClusterID, uid)
	}
	return ctx.Err()
}
func terminalDatabaseResourceMetadataMatches(meta metav1.ObjectMeta, claim sqlc.VulnerabilityDatabaseScanClaim, resource sqlc.VulnerabilityDatabaseScanResource, uid types.UID) bool {
	return meta.UID == uid && meta.Name == databaseScanResourceName(resource.ResourcePath) && meta.Namespace == databasejob.ScanNamespace && meta.ResourceVersion != "" && len(meta.ResourceVersion) <= 128 && meta.Labels["app.kubernetes.io/managed-by"] == "astronomer" && meta.Labels["astronomer.io/database-claim"] == claim.ID.String()
}
func readTerminalDatabaseResource(ctx context.Context, scope databaseScanRequester, cluster uuid.UUID, path string) (metav1.ObjectMeta, bool, error) {
	var result metav1.ObjectMeta
	if ctx.Err() != nil {
		return result, false, ctx.Err()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	response, err := scope.Do(bounded, cluster.String(), http.MethodGet, path, nil, nil)
	if bounded.Err() != nil {
		return result, false, bounded.Err()
	}
	if err != nil || response == nil || len(response.Body) > base64.StdEncoding.EncodedLen(databaseScanMaximumAPIBytes) {
		return result, false, errors.New("owned terminal readback unavailable")
	}
	if response.StatusCode == 404 {
		return result, true, nil
	}
	if response.StatusCode != 200 {
		return result, false, errors.New("owned terminal readback unavailable")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(response.Body)
	if err != nil || len(payload) > databaseScanMaximumAPIBytes {
		clear(payload)
		return result, false, errors.New("owned terminal readback invalid")
	}
	defer clear(payload)
	// Closed top-level Kubernetes resource envelopes, bounded opaque spec/data.
	// Secret payload is never retained, formatted, stored or returned to caller.
	var envelope struct {
		metav1.TypeMeta
		Metadata   metav1.ObjectMeta `json:"metadata"`
		Spec       json.RawMessage   `json:"spec,omitempty"`
		Status     json.RawMessage   `json:"status,omitempty"`
		Data       json.RawMessage   `json:"data,omitempty"`
		StringData json.RawMessage   `json:"stringData,omitempty"`
		Immutable  json.RawMessage   `json:"immutable,omitempty"`
		Type       string            `json:"type,omitempty"`
	}
	defer func() {
		clear(envelope.Spec)
		clear(envelope.Status)
		clear(envelope.Data)
		clear(envelope.StringData)
		clear(envelope.Immutable)
	}()
	if jsoncontract.Decode(payload, &envelope) != nil {
		return result, false, errors.New("owned terminal resource envelope invalid")
	}
	kind, version := "", "v1"
	switch envelope.Kind {
	case "Job":
		kind = "Job"
		version = "batch/v1"
	case "Secret":
		kind = "Secret"
	case "PersistentVolumeClaim":
		kind = "PersistentVolumeClaim"
	case "NetworkPolicy":
		kind = "NetworkPolicy"
		version = "networking.k8s.io/v1"
	}
	expectedKind := ""
	switch {
	case strings.Contains(path, "/jobs/"):
		expectedKind = "Job"
	case strings.Contains(path, "/secrets/"):
		expectedKind = "Secret"
	case strings.Contains(path, "/persistentvolumeclaims/"):
		expectedKind = "PersistentVolumeClaim"
	case strings.Contains(path, "/networkpolicies/"):
		expectedKind = "NetworkPolicy"
	}
	if kind == "" || kind != expectedKind || envelope.APIVersion != version {
		return result, false, errors.New("owned terminal resource kind invalid")
	}
	return envelope.Metadata, false, nil
}

func waitTerminalDatabaseJobPodsGone(ctx context.Context, requester databaseScanRequester, cluster uuid.UUID, jobUID types.UID) error {
	path := "/api/v1/namespaces/" + databasejob.ScanNamespace + "/pods?labelSelector=" + url.QueryEscape("batch.kubernetes.io/controller-uid="+string(jobUID)) + "&limit=2"
	scope, err := newDatabaseScanScopedRequester(requester, cluster, []string{path}, nil)
	if err != nil {
		return err
	}
	for {
		payload, err := databaseScanGetBytes(ctx, scope, cluster, path, databaseScanMaximumAPIBytes)
		if err != nil {
			return err
		}
		var pods corev1.PodList
		invalid := jsoncontract.Decode(payload, &pods) != nil || pods.Kind != "PodList" || pods.APIVersion != "v1" || pods.Continue != "" || len(pods.Items) > 1
		clear(payload)
		if invalid {
			return errors.New("owned terminal Job descendant inventory unknown")
		}
		if len(pods.Items) == 0 {
			return ctx.Err()
		}
		pod := &pods.Items[0]
		if normalizeDatabasePodListItem(pod) != nil || pod.Namespace != databasejob.ScanNamespace || len(pod.OwnerReferences) != 1 || pod.OwnerReferences[0].UID != jobUID || pod.OwnerReferences[0].Kind != "Job" || pod.OwnerReferences[0].APIVersion != "batch/v1" || pod.OwnerReferences[0].Controller == nil || !*pod.OwnerReferences[0].Controller {
			return errors.New("owned terminal Job descendants changed")
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (owner *DatabaseScanOwner) cleanupTerminalScanClaims(ctx context.Context) error {
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	rows, err := sqlc.New(owner.pool).ListOwnedDatabaseTerminalClaimsForCleanup(bounded, owner.scanCursors.cleanup)
	if err != nil {
		return errors.New("owned terminal cleanup page unavailable")
	}
	if len(rows) > 32 {
		return errors.New("owned terminal cleanup page exceeds bound")
	}
	var problems []error
	for _, identifier := range rows {
		if err := bounded.Err(); err != nil {
			return errors.Join(errors.Join(problems...), err)
		}
		if identifier == uuid.Nil || identifier.String() <= owner.scanCursors.cleanup.String() {
			return errors.New("owned terminal cleanup cursor invalid")
		}
		err := owner.cleanupTerminalScanClaim(bounded, identifier)
		owner.scanCursors.cleanup = identifier
		if err != nil {
			problems = append(problems, errors.New("owned terminal resource cleanup unknown"))
		}
	}
	if len(rows) < 32 {
		owner.scanCursors.cleanup = uuid.Nil
	} else {
		problems = append(problems, errors.New("owned terminal cleanup sweep incomplete"))
	}
	return errors.Join(problems...)
}
