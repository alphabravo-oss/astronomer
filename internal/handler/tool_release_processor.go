package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var errToolLeaseLost = errors.New("tool operation execution lease lost")

func (h *ToolHandler) executeOperation(ctx context.Context, op sqlc.ToolOperation) error {
	var env toolOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	if err := validateToolReleaseEnvelope(env); err != nil {
		return err
	}
	if _, err := uuid.Parse(env.ClusterID); err != nil {
		return err
	}
	if h.helm == nil {
		return errors.New("helm requester not configured")
	}
	if op.OperationType == "uninstall" {
		switch env.ToolSlug {
		case "longhorn":
			if err := h.prepareLonghornUninstall(ctx, op, env); err != nil {
				return err
			}
		case "cis-operator":
			if err := h.prepareCISUninstall(ctx, op, env); err != nil {
				return err
			}
		}
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				_, err := h.queries.RenewToolOperationLease(renewCtx, sqlc.RenewToolOperationLeaseParams{ID: op.ID, AttemptCount: op.AttemptCount})
				stop()
				if err != nil {
					cancel(fmt.Errorf("%w: %v", errToolLeaseLost, err))
					return
				}
			}
		}
	}()
	defer func() { cancel(nil); <-done }()
	for offset := range env.Releases {
		index := offset
		if op.OperationType == "uninstall" || op.OperationType == "rollback" {
			index = len(env.Releases) - 1 - offset
		}
		if env.Releases[index].State == "completed" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		if err := h.executeToolRelease(ctx, op, &env, index); err != nil {
			env.Releases[index].State = "failed"
			env.Releases[index].Error = err.Error()
			if checkpointErr := h.checkpointToolRelease(ctx, op, env, index, "failed"); checkpointErr != nil {
				return errors.Join(err, checkpointErr)
			}
			return err
		}
		env.Releases[index].State = "completed"
		env.Releases[index].Error = ""
		if err := h.checkpointToolRelease(ctx, op, env, index, "completed"); err != nil {
			return err
		}
	}
	return nil
}

// prepareCISUninstall removes scan-owned resources while the operator and its
// CRDs are still present. The upstream chart creates a fixed-name Sonobuoy
// service outside the Helm release. If it survives an uninstall, the next
// ClusterScan remains Pending forever with "Waiting for another scan to
// finish". Keeping this cleanup in the API lifecycle makes reinstall reliable
// without requiring users to repair adopted clusters out of band.
func (h *ToolHandler) prepareCISUninstall(ctx context.Context, op sqlc.ToolOperation, env toolOperationEnvelope) error {
	if h.k8s == nil {
		return errors.New("Kubernetes requester is required to clean up CIS scan resources")
	}
	const scansPath = "/apis/cis.cattle.io/v1/clusterscans"
	response, err := h.k8s.Do(ctx, env.ClusterID, http.MethodGet, scansPath, nil, requestHeaders(""))
	if err != nil {
		return fmt.Errorf("list CIS scans before uninstall: %w", err)
	}
	if response == nil {
		return errors.New("list CIS scans before uninstall: empty response")
	}
	if response.StatusCode != http.StatusNotFound {
		if err := ensureSuccess(response); err != nil {
			return fmt.Errorf("list CIS scans before uninstall: %w", err)
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name   string            `json:"name"`
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := parseJSONResponse(response, &list); err != nil {
			return fmt.Errorf("decode CIS scans before uninstall: %w", err)
		}
		removed := 0
		for _, scan := range list.Items {
			if scan.Metadata.Labels["app.kubernetes.io/managed-by"] != "astronomer-go" || strings.TrimSpace(scan.Metadata.Name) == "" {
				continue
			}
			path := scansPath + "/" + url.PathEscape(scan.Metadata.Name)
			deleted, deleteErr := h.k8s.Do(ctx, env.ClusterID, http.MethodDelete, path, nil, requestHeaders(""))
			if deleteErr != nil {
				return fmt.Errorf("delete CIS scan %s before uninstall: %w", scan.Metadata.Name, deleteErr)
			}
			if deleted == nil {
				return fmt.Errorf("delete CIS scan %s before uninstall: empty response", scan.Metadata.Name)
			}
			if deleted.StatusCode != http.StatusNotFound && deleted.StatusCode >= http.StatusBadRequest {
				return fmt.Errorf("delete CIS scan %s before uninstall: %w", scan.Metadata.Name, responseError(deleted))
			}
			removed++
		}
		h.recordToolOperationEvent(ctx, op.ID, "info", "uninstall.prepared", "Astronomer-managed CIS scans were removed before chart uninstall", map[string]any{"scan_count": removed})
	}

	removed, err := h.removeCISRunnerResources(ctx, env.ClusterID)
	if err != nil {
		return err
	}
	h.recordToolOperationEvent(ctx, op.ID, "info", "uninstall.prepared", "CIS runner resources were removed before chart uninstall", map[string]any{"resource_count": removed})
	return nil
}

func (h *ToolHandler) removeCISRunnerResources(ctx context.Context, clusterID string) (int, error) {
	// Sonobuoy resources do not carry owner references. Interrupted runs can
	// therefore leave ConfigMaps as well as the singleton Service behind. Only
	// objects whose instance label names an Astronomer-created scan are removed.
	collections := []string{
		"/apis/batch/v1/namespaces/cis-operator-system/jobs",
		"/apis/apps/v1/namespaces/cis-operator-system/daemonsets",
		"/apis/apps/v1/namespaces/cis-operator-system/deployments",
		"/apis/apps/v1/namespaces/cis-operator-system/statefulsets",
		"/apis/apps/v1/namespaces/cis-operator-system/replicasets",
		"/api/v1/namespaces/cis-operator-system/pods",
		"/api/v1/namespaces/cis-operator-system/services",
		"/api/v1/namespaces/cis-operator-system/configmaps",
		"/api/v1/namespaces/cis-operator-system/secrets",
		"/api/v1/namespaces/cis-operator-system/serviceaccounts",
		"/apis/rbac.authorization.k8s.io/v1/namespaces/cis-operator-system/roles",
		"/apis/rbac.authorization.k8s.io/v1/namespaces/cis-operator-system/rolebindings",
		"/apis/rbac.authorization.k8s.io/v1/clusterroles",
		"/apis/rbac.authorization.k8s.io/v1/clusterrolebindings",
	}
	removed := 0
	for _, collection := range collections {
		response, err := h.k8s.Do(ctx, clusterID, http.MethodGet, collection, nil, requestHeaders(""))
		if err != nil {
			return removed, fmt.Errorf("list CIS runner resources at %s: %w", collection, err)
		}
		if response == nil {
			return removed, fmt.Errorf("list CIS runner resources at %s: empty response", collection)
		}
		if response.StatusCode == http.StatusNotFound {
			continue
		}
		if err := ensureSuccess(response); err != nil {
			return removed, fmt.Errorf("list CIS runner resources at %s: %w", collection, err)
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name   string            `json:"name"`
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := parseJSONResponse(response, &list); err != nil {
			return removed, fmt.Errorf("decode CIS runner resources at %s: %w", collection, err)
		}
		for _, item := range list.Items {
			name := strings.TrimSpace(item.Metadata.Name)
			instance := item.Metadata.Labels["app.kubernetes.io/instance"]
			managed := item.Metadata.Labels["app.kubernetes.io/name"] == "rancher-cis-benchmark" && strings.HasPrefix(instance, "security-scan-runner-astronomer-cis-")
			if collection == "/api/v1/namespaces/cis-operator-system/services" && name == "service-rancher-cis-benchmark" && !managed {
				return removed, errors.New("CIS runner service exists without Astronomer scan ownership labels")
			}
			if name == "" || !managed {
				continue
			}
			path := collection + "/" + url.PathEscape(name)
			deleted, deleteErr := h.k8s.Do(ctx, clusterID, http.MethodDelete, path, nil, requestHeaders(""))
			if deleteErr != nil {
				return removed, fmt.Errorf("delete CIS runner resource %s: %w", path, deleteErr)
			}
			if deleted == nil {
				return removed, fmt.Errorf("delete CIS runner resource %s: empty response", path)
			}
			if deleted.StatusCode != http.StatusNotFound && deleted.StatusCode >= http.StatusBadRequest {
				return removed, fmt.Errorf("delete CIS runner resource %s: %w", path, responseError(deleted))
			}
			removed++
		}
	}
	return removed, nil
}

func (h *ToolHandler) prepareLonghornUninstall(ctx context.Context, op sqlc.ToolOperation, env toolOperationEnvelope) error {
	result, err := prepareLonghornDeletion(ctx, h.k8s, env.ClusterID, env.ConfirmDataDeletion, env.ConfirmFailedReleaseCleanup)
	if err != nil {
		return err
	}
	if result == longhornDeletionCRDAbsent {
		h.recordToolOperationEvent(ctx, op.ID, "warn", "uninstall.prepared", "Longhorn deletion confirmation CRD was absent during failed-release cleanup", map[string]any{
			"namespace": "longhorn-system",
			"setting":   "deleting-confirmation-flag",
		})
		return nil
	}
	if result == longhornDeletionCreated {
		h.recordToolOperationEvent(ctx, op.ID, "warn", "uninstall.prepared", "Longhorn deletion confirmation was recreated during failed-release cleanup", map[string]any{
			"namespace": "longhorn-system",
			"setting":   "deleting-confirmation-flag",
		})
		return nil
	}
	h.recordToolOperationEvent(ctx, op.ID, "warn", "uninstall.prepared", "Longhorn persistent-data deletion confirmed", map[string]any{
		"namespace": "longhorn-system",
		"setting":   "deleting-confirmation-flag",
	})
	return nil
}

func (h *ToolHandler) checkpointToolRelease(ctx context.Context, op sqlc.ToolOperation, env toolOperationEnvelope, index int, state string) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	release := env.Releases[index]
	detail, err := json.Marshal(map[string]any{"releaseName": release.ReleaseName, "namespace": release.Namespace, "stepIndex": index, "stepCount": len(env.Releases), "operationType": op.OperationType, "revision": release.Revision, "error": release.Error})
	if err != nil {
		return err
	}
	level := "info"
	if state == "failed" {
		level = "error"
	}
	_, err = h.queries.CheckpointToolOperation(ctx, sqlc.CheckpointToolOperationParams{ID: op.ID, AttemptCount: op.AttemptCount, Payload: raw, EventLevel: level, EventStage: "release." + state, EventMessage: fmt.Sprintf("%s/%s %s", release.Namespace, release.ReleaseName, state), EventDetail: detail})
	if errors.Is(err, pgx.ErrNoRows) {
		return errToolLeaseLost
	}
	if err != nil {
		return fmt.Errorf("persist release progress: %w", err)
	}
	op.Payload = raw
	h.publishToolOperationChanged(op)
	return nil
}

func (h *ToolHandler) executeToolRelease(ctx context.Context, op sqlc.ToolOperation, env *toolOperationEnvelope, index int) error {
	release := &env.Releases[index]
	execution := env.release(index)
	if op.OperationType == "rollback" {
		execution.Preset = release.PreviousPreset
	}
	execution.Description = fmt.Sprintf("astronomer tool operation %s release %d", op.ID, index)
	clusterID, _ := uuid.Parse(env.ClusterID)
	item, lookupErr := h.queries.GetInstalledChartByRelease(ctx, sqlc.GetInstalledChartByReleaseParams{ClusterID: clusterID, ReleaseName: release.ReleaseName, Namespace: release.Namespace})
	if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
		return lookupErr
	}
	if lookupErr == nil && item.ToolSlug.Valid && item.ToolSlug.String != env.ToolSlug {
		return errors.New("release belongs to another tool")
	}
	status, exists, err := existingHelmReleaseStatus(ctx, h.helm, env.ClusterID, release.ReleaseName, release.Namespace)
	if err != nil {
		return err
	}
	if exists && (status == nil || !status.Success) {
		return errors.New("Helm returned unsuccessful release status")
	}
	if op.OperationType == "rollback" && release.ExpectedRevision == 0 && exists {
		owned, err := h.toolReleaseHasMarker(ctx, *env, *release, release.OperationMarker, status.Revision)
		if err != nil {
			return err
		}
		if !owned {
			return errors.New("release advanced outside the source operation; refusing rollback")
		}
	}
	if op.OperationType == "uninstall" || (op.OperationType == "rollback" && release.RollbackRevision == 0) {
		if exists && (lookupErr != nil || !item.ToolSlug.Valid) {
			owned, err := h.toolReleaseHasMarker(ctx, *env, *release, release.OperationMarker, status.Revision)
			if err != nil {
				return err
			}
			if !owned {
				return errors.New("refusing to uninstall an unowned release")
			}
		}
		release.State = "running"
		if err := h.checkpointToolRelease(ctx, op, *env, index, "started"); err != nil {
			return err
		}
		if exists {
			result, err := h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{ReleaseName: release.ReleaseName, Namespace: release.Namespace})
			if err != nil {
				return err
			}
			if result == nil || !result.Success {
				return errors.New("Helm uninstall was unsuccessful")
			}
		}
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return nil
		}
		if !item.ToolSlug.Valid || item.ToolSlug.String != env.ToolSlug {
			return errors.New("release is not owned by this tool")
		}
		return h.queries.DeleteInstalledChart(ctx, item.ID)
	}
	// Completed Helm work with an uncommitted database checkpoint is recognized
	// by an exact operation marker in Helm's durable release history. A newer
	// external revision never counts as this operation's successful attempt.
	if exists && release.ExpectedRevision > 0 {
		history, err := h.helm.History(ctx, env.ClusterID, release.ReleaseName, release.Namespace)
		if err != nil {
			return err
		}
		if history == nil || !history.Success {
			return errors.New("Helm release history unavailable")
		}
		for _, revision := range history.Revisions {
			if revision.Revision == status.Revision && revision.Description == execution.Description && revision.Status == "deployed" {
				release.Revision = status.Revision
				return adoptExistingToolRelease(ctx, h.queries, clusterID, execution, status)
			}
		}
		if status.Revision >= release.ExpectedRevision && helmReleaseReady(status) {
			return errors.New("release advanced outside this operation; review before retrying")
		}
	}
	if release.ExpectedRevision == 0 {
		if exists {
			release.PreviousRevision = status.Revision
		}
		if op.OperationType != "rollback" && lookupErr == nil {
			release.PreviousValuesYAML = item.ValuesOverride
			release.PreviousPreset = item.PresetUsed.String
		}
		release.ExpectedRevision = release.PreviousRevision + 1
		release.OperationMarker = execution.Description
	}
	release.State = "running"
	if err := h.checkpointToolRelease(ctx, op, *env, index, "started"); err != nil {
		return err
	}
	switch op.OperationType {
	case "adopt":
		if !exists {
			return errors.New("cannot adopt an absent Helm release")
		}
		if !helmReleaseReady(status) {
			return errors.New("cannot adopt a Helm release that is not ready")
		}
	case "install":
		if exists && helmReleaseReady(status) {
			// Installation naturally adopts an existing ready release, including
			// length-one plans. Persist the actual observed revision.
			break
		}
		messageType := protocol.MsgHelmInstall
		if exists {
			messageType = protocol.MsgHelmUpgrade
		}
		status, err = h.sendHelmRaw(ctx, execution, messageType)
	case "upgrade":
		if !exists {
			return errors.New("cannot upgrade an absent Helm release")
		}
		if lookupErr != nil || !item.ToolSlug.Valid {
			return errors.New("cannot upgrade an unowned tool release")
		}
		status, err = h.sendHelmRaw(ctx, execution, protocol.MsgHelmUpgrade)
	case "rollback":
		if release.RollbackRevision == 0 {
			return errors.New("release has no previous revision to roll back to")
		}
		status, err = h.helm.Do(ctx, env.ClusterID, protocol.MsgHelmRollback, protocol.HelmRequestPayload{ReleaseName: release.ReleaseName, Namespace: release.Namespace, Revision: release.RollbackRevision, Description: execution.Description})
	default:
		return fmt.Errorf("unsupported tool operation type: %s", op.OperationType)
	}
	if err != nil {
		return err
	}
	if status == nil || !status.Success {
		return errors.New("Helm operation was unsuccessful")
	}
	if !helmReleaseReady(status) {
		if err := h.checkToolReleaseReady(ctx, op, execution); err != nil {
			return err
		}
		status, err = h.helm.Status(ctx, env.ClusterID, release.ReleaseName, release.Namespace)
		if err != nil {
			return err
		}
	}
	release.Revision = status.Revision
	return adoptExistingToolRelease(ctx, h.queries, clusterID, execution, status)
}

func (h *ToolHandler) toolReleaseHasMarker(ctx context.Context, env toolOperationEnvelope, release toolRelease, marker string, revision int) (bool, error) {
	if marker == "" && !env.ConfirmFailedReleaseCleanup {
		return false, nil
	}
	history, err := h.helm.History(ctx, env.ClusterID, release.ReleaseName, release.Namespace)
	if err != nil {
		return false, err
	}
	if history == nil || !history.Success {
		return false, errors.New("Helm release history unavailable")
	}
	for _, item := range history.Revisions {
		if marker != "" && item.Revision == revision && item.Description == marker {
			return true, nil
		}
		if env.ConfirmFailedReleaseCleanup &&
			release.ExpectedRevision > 0 &&
			item.Revision == revision &&
			item.Revision == release.ExpectedRevision &&
			item.Chart == release.ChartName+"-"+release.Version &&
			isFailedHelmReleaseStatus(item.Status) {
			return true, nil
		}
	}
	return false, nil
}

func isFailedHelmReleaseStatus(status string) bool {
	switch status {
	case "failed", "uninstalling", "pending-install", "pending-upgrade", "pending-rollback", "pending-uninstall":
		return true
	default:
		return false
	}
}

func (h *ToolHandler) finishToolOperation(ctx context.Context, op sqlc.ToolOperation, operationErr error) {
	status, message := "completed", ""
	if operationErr != nil {
		status, message = "failed", operationErr.Error()
	}
	finished, err := h.queries.FinishToolOperation(ctx, sqlc.FinishToolOperationParams{ID: op.ID, AttemptCount: op.AttemptCount, FinalStatus: status, ErrorMessage: message})
	if err == nil {
		h.publishToolOperationChanged(finished)
	} else if h.log != nil {
		h.log.WarnContext(ctx, "finish tool operation failed", "operation_id", op.ID, "error", err)
	}
}
