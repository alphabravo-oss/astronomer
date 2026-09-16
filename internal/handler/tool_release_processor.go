package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if marker == "" {
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
		if item.Revision == revision && item.Description == marker {
			return true, nil
		}
	}
	return false, nil
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
