package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
)

const ResourceOperationType = "resource:operation"

type ResourceOperationPayload struct {
	OperationID string `json:"operation_id"`
	Generation  int64  `json:"generation"`
}

func NewResourceOperationTask(operationID uuid.UUID, generation int64) (*asynq.Task, error) {
	if operationID == uuid.Nil || generation <= 0 {
		return nil, errors.New("resource operation requires operation_id and generation")
	}
	payload, err := json.Marshal(ResourceOperationPayload{OperationID: operationID.String(), Generation: generation})
	if err != nil {
		return nil, errors.New("marshal resource operation task")
	}
	return asynq.NewTask(ResourceOperationType, payload, asynq.MaxRetry(8), asynq.Timeout(2*time.Minute)), nil
}

type ResourceOperationTaskQuerier interface {
	ClaimResourceOperationGeneration(context.Context, sqlc.ClaimResourceOperationGenerationParams) (sqlc.ResourceOperation, error)
	GetResourceOperation(context.Context, uuid.UUID) (sqlc.ResourceOperation, error)
	MarkResourceOperationSucceeded(context.Context, sqlc.MarkResourceOperationSucceededParams) (sqlc.ResourceOperation, error)
	MarkResourceOperationFailed(context.Context, sqlc.MarkResourceOperationFailedParams) (sqlc.ResourceOperation, error)
	MarkResourceOperationRetrying(context.Context, sqlc.MarkResourceOperationRetryingParams) (sqlc.ResourceOperation, error)
}

func HandleResourceOperation(ctx context.Context, task *asynq.Task) error {
	if task == nil {
		return asynq.SkipRetry
	}
	var payload ResourceOperationPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: invalid resource operation payload", asynq.SkipRetry)
	}
	id, err := uuid.Parse(payload.OperationID)
	if err != nil || payload.Generation <= 0 {
		return fmt.Errorf("%w: invalid resource operation identity", asynq.SkipRetry)
	}
	deps := runtimeDependencies(ctx)
	queries, ok := deps.Queries.(ResourceOperationTaskQuerier)
	if !ok || queries == nil || deps.K8s == nil {
		return errors.New("resource operation runtime is not configured")
	}
	now := time.Now().UTC()
	operation, err := queries.ClaimResourceOperationGeneration(ctx, sqlc.ClaimResourceOperationGenerationParams{
		ID: id, Generation: payload.Generation,
		Now:         pgtype.Timestamptz{Time: now, Valid: true},
		LockedUntil: pgtype.Timestamptz{Time: now.Add(3 * time.Minute), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetResourceOperation(ctx, id)
		if errors.Is(loadErr, pgx.ErrNoRows) {
			return nil
		}
		if loadErr != nil {
			return errors.New("load resource operation failed")
		}
		if existing.ObservedGeneration >= payload.Generation || existing.Status == "succeeded" {
			return nil
		}
		if existing.Generation != payload.Generation {
			return fmt.Errorf("%w: stale resource operation generation", asynq.SkipRetry)
		}
		return errors.New("resource operation lease is held")
	}
	if err != nil {
		return errors.New("claim resource operation failed")
	}
	if operation.Generation != payload.Generation || operation.ObservedGeneration >= payload.Generation {
		return fmt.Errorf("%w: invalid resource operation fence", asynq.SkipRetry)
	}
	if operation.Action != "apply" && operation.Action != "delete" {
		return failResourceOperation(ctx, queries, operation, "invalid_action", 0, true)
	}
	if !strings.HasPrefix(operation.ApiPath, "/") || strings.ContainsAny(operation.ApiPath, "?#") {
		return failResourceOperation(ctx, queries, operation, "invalid_path", 0, true)
	}

	method := http.MethodDelete
	path := operation.ApiPath
	var body []byte
	headers := map[string]string{"Accept": "application/json"}
	if operation.Action == "apply" {
		if deps.ResourceDecryptor == nil {
			return failResourceOperation(ctx, queries, operation, "decryptor_unavailable", 0, false)
		}
		body, err = deps.ResourceDecryptor.DecryptBytes(operation.ManifestEncrypted)
		if err != nil {
			return failResourceOperation(ctx, queries, operation, "decrypt_failed", 0, true)
		}
		defer zeroBytes(body)
		method = http.MethodPatch
		path = kubeutil.ServerSideApplyPath(path, kubeutil.ApplyOptions{FieldManager: "astronomer", Force: operation.ForceApply})
		headers = kubeutil.ApplyPatchHeaders()
	}
	response, effectErr := deps.K8s.Do(ctx, operation.ClusterID.String(), method, path, body, headers)
	if effectErr != nil {
		return failResourceOperation(ctx, queries, operation, "tunnel_unreachable", 0, false)
	}
	if response == nil {
		return failResourceOperation(ctx, queries, operation, "empty_response", 0, false)
	}
	status := response.StatusCode
	if operation.Action == "delete" && status == http.StatusNotFound {
		status = http.StatusNoContent
	} else if status < http.StatusOK || status >= http.StatusMultipleChoices {
		terminal := status == http.StatusBadRequest || status == http.StatusUnauthorized ||
			status == http.StatusForbidden || status == http.StatusNotFound || status == http.StatusUnprocessableEntity
		return failResourceOperation(ctx, queries, operation, "kubernetes_http_error", status, terminal)
	}
	resourceVersion := sanitizedResourceVersion(response.Body)
	_, err = queries.MarkResourceOperationSucceeded(ctx, sqlc.MarkResourceOperationSucceededParams{
		ID: operation.ID, Generation: operation.Generation,
		ObservedStatusCode:      pgtype.Int4{Int32: int32(status), Valid: status > 0},
		ObservedResourceVersion: resourceVersion,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("persist resource operation success failed")
	}
	return nil
}

func failResourceOperation(ctx context.Context, queries ResourceOperationTaskQuerier, operation sqlc.ResourceOperation, code string, status int, terminal bool) error {
	if !terminal {
		retried, retryOK := asynq.GetRetryCount(ctx)
		maximum, maximumOK := asynq.GetMaxRetry(ctx)
		terminal = retryOK && maximumOK && retried >= maximum
	}
	if !terminal {
		_, err := queries.MarkResourceOperationRetrying(ctx, sqlc.MarkResourceOperationRetryingParams{
			ID: operation.ID, Generation: operation.Generation, ErrorCode: code,
			ObservedStatusCode: pgtype.Int4{Int32: int32(status), Valid: status > 0},
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("persist resource operation retry failed")
		}
		return errors.New("resource operation retrying: " + code)
	}
	_, err := queries.MarkResourceOperationFailed(ctx, sqlc.MarkResourceOperationFailedParams{
		ID: operation.ID, Generation: operation.Generation, ErrorCode: code,
		ObservedStatusCode: pgtype.Int4{Int32: int32(status), Valid: status > 0},
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("persist resource operation failure failed")
	}
	return fmt.Errorf("%w: resource operation failed: %s", asynq.SkipRetry, code)
}

func sanitizedResourceVersion(encoded string) string {
	if encoded == "" || len(encoded) > 2<<20 {
		return ""
	}
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return ""
	}
	defer zeroBytes(body)
	var response struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	if len(response.Metadata.ResourceVersion) > 255 {
		return ""
	}
	return response.Metadata.ResourceVersion
}

func zeroBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
