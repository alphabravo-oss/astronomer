package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

var errNodeOperationIdempotencyConflict = errors.New("node operation idempotency conflict")

type NodeOperationQuerier interface {
	GetNodeOperation(context.Context, uuid.UUID) (sqlc.NodeOperation, error)
}

type NodeMutationTx interface {
	CreateNodeOperationIdempotent(context.Context, sqlc.CreateNodeOperationIdempotentParams) (sqlc.NodeOperation, error)
	tasks.TaskOutboxWriter
	audit.OutboxQuerier
}

type nodeMutationRunTxFunc func(context.Context, func(NodeMutationTx) error) error

type nodeOperationParameters struct {
	Key                string `json:"key,omitempty"`
	Value              string `json:"value,omitempty"`
	Effect             string `json:"effect,omitempty"`
	IgnoreDaemonSets   bool   `json:"ignore_daemonsets,omitempty"`
	DeleteEmptyDirData bool   `json:"delete_empty_dir_data,omitempty"`
	GracePeriodSeconds *int64 `json:"grace_period_seconds,omitempty"`
	Force              bool   `json:"force,omitempty"`
}

type NodeOperationResponse struct {
	ID                 string          `json:"id"`
	ClusterID          string          `json:"cluster_id"`
	NodeName           string          `json:"node_name"`
	Action             string          `json:"action"`
	Status             string          `json:"status"`
	Generation         int64           `json:"generation"`
	ObservedGeneration int64           `json:"observed_generation"`
	AttemptCount       int32           `json:"attempt_count"`
	ErrorCode          string          `json:"error_code,omitempty"`
	Progress           json.RawMessage `json:"progress"`
	CompletedAt        *time.Time      `json:"completed_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

func nodeOperationResponse(row sqlc.NodeOperation) NodeOperationResponse {
	progress := row.Progress
	if len(progress) == 0 || !json.Valid(progress) {
		progress = json.RawMessage(`{}`)
	}
	out := NodeOperationResponse{
		ID: row.ID.String(), ClusterID: row.ClusterID.String(), NodeName: row.NodeName,
		Action: row.Action, Status: row.Status, Generation: row.Generation,
		ObservedGeneration: row.ObservedGeneration, AttemptCount: row.AttemptCount,
		ErrorCode: row.ErrorCode, Progress: progress,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.CompletedAt.Valid {
		completed := row.CompletedAt.Time.UTC()
		out.CompletedAt = &completed
	}
	return out
}

func (h *ResourceHandler) SetNodeOperationStore(store NodeOperationQuerier) {
	if h != nil {
		h.nodeOperations = store
	}
}

func (h *ResourceHandler) SetNodeMutationRunTx(runTx nodeMutationRunTxFunc) {
	if h != nil {
		h.nodeMutationRunTx = runTx
	}
}

// SetNodeOperationReadAuthorizer installs the action-aware, current RBAC
// check used after a receipt is loaded and bound to its route identifiers.
func (h *ResourceHandler) SetNodeOperationReadAuthorizer(authorize func(context.Context, string, string) (bool, error)) {
	if h != nil {
		h.nodeOperationReadAuthorizer = authorize
	}
}

func (h *ResourceHandler) NodeMutationWired() bool {
	return h != nil && h.nodeOperations != nil && h.nodeMutationRunTx != nil && h.encryptor != nil
}

func (h *ResourceHandler) enqueueNodeOperation(w http.ResponseWriter, r *http.Request, action string, parameters nodeOperationParameters) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	nodeName := strings.TrimSpace(chi.URLParam(r, "node_name"))
	if nodeName == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "node_name is required")
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	caller := currentUserUUID(r)
	if !caller.Valid || caller.Bytes == uuid.Nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Invalid caller")
		return
	}
	if h == nil || h.nodeMutationRunTx == nil || h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Durable node mutation storage is unavailable")
		return
	}
	plain, err := json.Marshal(parameters)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid node operation parameters")
		return
	}
	defer zeroSensitiveBytes(plain)
	encrypted, err := h.encryptor.Encrypt(string(plain))
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Node operation encryption is unavailable")
		return
	}
	digestBytes := sha256.Sum256([]byte(strings.Join([]string{action, clusterID.String(), nodeName, string(plain)}, "\x00")))
	digest := hex.EncodeToString(digestBytes[:])
	scope := "nodes:" + uuid.UUID(caller.Bytes).String() + ":" + r.Method + ":" + r.URL.Path

	var operation sqlc.NodeOperation
	err = h.nodeMutationRunTx(r.Context(), func(q NodeMutationTx) error {
		operation, err = q.CreateNodeOperationIdempotent(r.Context(), sqlc.CreateNodeOperationIdempotentParams{
			IdempotencyScope: scope, IdempotencyKey: key, RequestDigest: digest,
			ClusterID: clusterID, NodeName: nodeName, Action: action,
			ParametersEncrypted: encrypted, CreatedByID: caller,
		})
		if err != nil {
			return err
		}
		if operation.RequestDigest != digest || operation.ClusterID != clusterID || operation.NodeName != nodeName || operation.Action != action {
			return errNodeOperationIdempotencyConflict
		}
		task, taskErr := tasks.NewNodeOperationTask(operation.ID, operation.Generation)
		if taskErr != nil {
			return taskErr
		}
		if _, taskErr = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
			DedupeKey: "node:operation:" + operation.ID.String() + ":" + int64String(operation.Generation),
			QueueName: tasks.ClusterTemplateApplyQueueName, MaxRetry: 8,
			Timeout: 15 * time.Minute, MaxDeliveryAttempts: 20,
		}); taskErr != nil {
			return taskErr
		}
		return recordAuditOutbox(r, q, "cluster.node."+action+".requested", "node_operation",
			operation.ID.String(), nodeName, http.StatusAccepted, map[string]any{
				"operation_id": operation.ID.String(), "cluster_id": clusterID.String(),
				"node_name": nodeName, "action": action,
			})
	})
	if errors.Is(err, errNodeOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different node operation")
		return
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "node_operations_active_target_idx" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Another node operation is already active for this target")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to persist node operation")
		return
	}
	location := "/api/v1/nodes/" + operation.ClusterID.String() + "/" + url.PathEscape(operation.NodeName) + "/operations/" + operation.ID.String() + "/"
	RespondAcceptedOperation(w, location, nodeOperationResponse(operation))
}

func (h *ResourceHandler) GetNodeOperation(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.nodeOperations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Node operation storage is unavailable")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid node operation ID")
		return
	}
	operation, err := h.nodeOperations.GetNodeOperation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Node operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load node operation")
		return
	}
	clusterID, clusterErr := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if clusterErr != nil || operation.ClusterID != clusterID || operation.NodeName != chi.URLParam(r, "node_name") {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Node operation not found")
		return
	}
	if h.nodeOperationReadAuthorizer == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Node operation authorization is unavailable")
		return
	}
	allowed, authErr := h.nodeOperationReadAuthorizer(r.Context(), operation.ClusterID.String(), operation.Action)
	if authErr != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to authorize node operation")
		return
	}
	if !allowed {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Node operation not found")
		return
	}
	RespondJSON(w, http.StatusOK, nodeOperationResponse(operation))
}

func (h *ResourceHandler) previewNodeDrain(w http.ResponseWriter, r *http.Request, clusterID, nodeName string, req drainNodeRequest) {
	if h == nil || h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, "Cluster tunnel requester is unavailable")
		return
	}
	resp, err := h.requester.Do(r.Context(), clusterID, http.MethodGet,
		"/api/v1/pods?fieldSelector=spec.nodeName="+url.QueryEscape(nodeName), nil, requestHeaders(""))
	if err != nil || ensureSuccess(resp) != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.K8sError, "Unable to inspect pods on node")
		return
	}
	var pods drainPodList
	if parseJSONResponse(resp, &pods) != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.K8sError, "Unable to inspect pods on node")
		return
	}
	ignoreDaemonSets := true
	if req.IgnoreDaemonSets != nil {
		ignoreDaemonSets = *req.IgnoreDaemonSets
	}
	out := drainNodeResponse{Node: nodeName, Status: "dry_run"}
	for _, pod := range pods.Items {
		ref := drainNodePodRef{Namespace: pod.Metadata.Namespace, Name: pod.Metadata.Name}
		switch {
		case pod.Metadata.Name == "":
		case pod.Metadata.DeletionTimestamp != "":
			ref.Reason = "pod is already terminating"
			out.Skipped = append(out.Skipped, ref)
		case pod.Status.Phase == "Succeeded" || pod.Status.Phase == "Failed":
			ref.Reason = "pod is already terminal"
			out.Skipped = append(out.Skipped, ref)
		case pod.Metadata.Annotations["kubernetes.io/config.mirror"] != "":
			ref.Reason = "mirror pod cannot be evicted"
			out.Skipped = append(out.Skipped, ref)
		case drainPodOwnedByDaemonSet(pod) && ignoreDaemonSets:
			ref.Reason = "managed by DaemonSet"
			out.Skipped = append(out.Skipped, ref)
		case drainPodOwnedByDaemonSet(pod):
			out.Blockers = append(out.Blockers, podRefString(ref)+": managed by DaemonSet")
		case len(pod.Metadata.OwnerReferences) == 0 && !req.Force:
			out.Blockers = append(out.Blockers, podRefString(ref)+": not managed by a controller")
		case drainPodHasEmptyDir(pod) && !req.DeleteEmptyDirData:
			out.Blockers = append(out.Blockers, podRefString(ref)+": uses emptyDir volume")
		default:
			ref.Reason = "would evict"
			out.Evicted = append(out.Evicted, ref)
		}
	}
	out.Message = fmt.Sprintf("Drain would evict %d pods, skip %d pods, and has %d blockers.", len(out.Evicted), len(out.Skipped), len(out.Blockers))
	RespondJSON(w, http.StatusOK, out)
}

// DrainNode keeps only dry-run inspection synchronous. A real drain is a
// durable, resumable workflow and never performs a member-cluster effect on
// the request goroutine.
func (h *ResourceHandler) DrainNode(w http.ResponseWriter, r *http.Request) {
	clusterID, nodeName, ok := nodeActionParams(w, r)
	if !ok {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	var req drainNodeRequest
	if r.Body != nil {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	if req.GracePeriodSeconds != nil && *req.GracePeriodSeconds < -1 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "grace_period_seconds must be -1 or greater")
		return
	}
	if req.DryRun {
		h.previewNodeDrain(w, r, clusterID, nodeName, req)
		return
	}
	ignoreDaemonSets := true
	if req.IgnoreDaemonSets != nil {
		ignoreDaemonSets = *req.IgnoreDaemonSets
	}
	h.enqueueNodeOperation(w, r, "drain", nodeOperationParameters{
		IgnoreDaemonSets: ignoreDaemonSets, DeleteEmptyDirData: req.DeleteEmptyDirData,
		GracePeriodSeconds: req.GracePeriodSeconds, Force: req.Force,
	})
}

func zeroSensitiveBytes(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
