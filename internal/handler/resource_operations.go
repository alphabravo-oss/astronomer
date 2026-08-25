package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

const maxResourceIdempotencyKeyLength = 255

// Resource manifests are request-buffered for encrypted durable delivery.
// Bound them before parsing/encryption so a caller cannot amplify API memory
// or database usage. Kubernetes objects above 2 MiB should be decomposed.
const maxResourceManifestBytes int64 = 2 << 20

var errResourceOperationIdempotencyConflict = errors.New("resource operation idempotency conflict")
var errResourceOperationTargetBusy = errors.New("resource operation target is busy")

// ResourceOperationQuerier is intentionally narrow so durable Kubernetes
// mutations do not add methods to every ResourceQuerier test fake.
type ResourceOperationQuerier interface {
	GetResourceOperation(context.Context, uuid.UUID) (sqlc.ResourceOperation, error)
}

// ResourceMutationTx is satisfied by transaction-bound *sqlc.Queries. The
// member-cluster effect is forbidden here: this interface can commit only the
// durable receipt, identifier-only delivery intent, and mandatory audit.
type ResourceMutationTx interface {
	CreateResourceOperationIdempotent(context.Context, sqlc.CreateResourceOperationIdempotentParams) (sqlc.ResourceOperation, error)
	tasks.TaskOutboxWriter
	audit.OutboxQuerier
}

type resourceMutationRunTxFunc func(context.Context, func(ResourceMutationTx) error) error

func (h *ResourceHandler) SetResourceOperationStore(store ResourceOperationQuerier) {
	if h != nil {
		h.resourceOperations = store
	}
}

func (h *ResourceHandler) SetResourceMutationRunTx(runTx resourceMutationRunTxFunc) {
	if h != nil {
		h.resourceMutationRunTx = runTx
	}
}

// SetAuthorization wires the live caller-binding lookup used by durable
// receipt polling. It is mandatory: authenticated requests fail closed when
// this support is absent.
func (h *ResourceHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if h != nil {
		h.authz.SetAuthorization(engine, querier)
	}
}

func (h *ResourceHandler) ResourceMutationWired() bool {
	return h != nil && h.resourceOperations != nil && h.resourceMutationRunTx != nil && h.encryptor != nil
}

type resourceMutationIntent struct {
	ClusterID    string
	ResourceType string
	Namespace    string
	Name         string
	Action       string
	AuditVerb    string
	APIPath      string
	Manifest     []byte
	Force        bool
}

// ResourceOperationResponse is the public, secret-free durable receipt.
type ResourceOperationResponse struct {
	ID                      string     `json:"id"`
	ClusterID               string     `json:"cluster_id"`
	ResourceType            string     `json:"resource_type"`
	Namespace               string     `json:"namespace,omitempty"`
	ResourceName            string     `json:"resource_name"`
	Action                  string     `json:"action"`
	Status                  string     `json:"status"`
	Generation              int64      `json:"generation"`
	ObservedGeneration      int64      `json:"observed_generation"`
	AttemptCount            int32      `json:"attempt_count"`
	ErrorCode               string     `json:"error_code,omitempty"`
	ObservedStatusCode      *int32     `json:"observed_status_code,omitempty"`
	ObservedResourceVersion string     `json:"observed_resource_version,omitempty"`
	CompletedAt             *time.Time `json:"completed_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

func resourceOperationResponse(row sqlc.ResourceOperation) ResourceOperationResponse {
	out := ResourceOperationResponse{
		ID: row.ID.String(), ClusterID: row.ClusterID.String(), ResourceType: row.ResourceType,
		Namespace: row.Namespace, ResourceName: row.ResourceName, Action: row.Action,
		Status: row.Status, Generation: row.Generation, ObservedGeneration: row.ObservedGeneration,
		AttemptCount: row.AttemptCount, ErrorCode: row.ErrorCode,
		ObservedResourceVersion: row.ObservedResourceVersion,
		CreatedAt:               row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	if row.ObservedStatusCode.Valid {
		status := row.ObservedStatusCode.Int32
		out.ObservedStatusCode = &status
	}
	if row.CompletedAt.Valid {
		completed := row.CompletedAt.Time.UTC()
		out.CompletedAt = &completed
	}
	return out
}

func (h *ResourceHandler) enqueueResourceOperation(w http.ResponseWriter, r *http.Request, intent resourceMutationIntent) {
	clusterID, err := uuid.Parse(intent.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "Idempotency-Key header is required")
		return
	}
	if len(key) > maxResourceIdempotencyKeyLength {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "Idempotency-Key header is too long")
		return
	}
	caller := currentUserUUID(r)
	if !caller.Valid || caller.Bytes == uuid.Nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Invalid caller")
		return
	}
	if h.resourceMutationRunTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Durable resource mutation storage is unavailable")
		return
	}
	manifestEncrypted := ""
	if intent.Action == "apply" {
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Resource manifest encryption is unavailable")
			return
		}
		manifestEncrypted, err = h.encryptor.Encrypt(string(intent.Manifest))
		if err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Resource manifest encryption failed")
			return
		}
	}
	requiredVerb := strings.ToLower(strings.TrimSpace(intent.AuditVerb))
	if requiredVerb != string(rbac.VerbCreate) && requiredVerb != string(rbac.VerbUpdate) && requiredVerb != string(rbac.VerbDelete) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Resource mutation authorization intent is invalid")
		return
	}
	digestInput := strings.Join([]string{intent.Action, requiredVerb, clusterID.String(), intent.ResourceType, intent.Namespace, intent.Name, intent.APIPath, strings.ToLower(boolString(intent.Force))}, "\x00")
	hash := sha256.New()
	_, _ = hash.Write([]byte(digestInput))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(intent.Manifest)
	digest := hex.EncodeToString(hash.Sum(nil))
	scope := "resources:" + uuid.UUID(caller.Bytes).String() + ":" + r.Method + ":" + r.URL.Path

	var operation sqlc.ResourceOperation
	err = h.resourceMutationRunTx(r.Context(), func(q ResourceMutationTx) error {
		var createErr error
		operation, createErr = q.CreateResourceOperationIdempotent(r.Context(), sqlc.CreateResourceOperationIdempotentParams{
			IdempotencyScope: scope, IdempotencyKey: key, RequestDigest: digest,
			ClusterID: clusterID, ResourceType: intent.ResourceType, Namespace: intent.Namespace,
			ResourceName: intent.Name, Action: intent.Action, ApiPath: intent.APIPath, RequiredVerb: requiredVerb,
			ManifestEncrypted: manifestEncrypted, ForceApply: intent.Force, CreatedByID: caller,
		})
		if createErr != nil {
			var pgErr *pgconn.PgError
			if errors.As(createErr, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "resource_operations_active_target_unique" {
				return errResourceOperationTargetBusy
			}
			return createErr
		}
		if operation.RequestDigest != digest || operation.ClusterID != clusterID || operation.Action != intent.Action || operation.ApiPath != intent.APIPath || operation.RequiredVerb != requiredVerb {
			return errResourceOperationIdempotencyConflict
		}
		task, taskErr := tasks.NewResourceOperationTask(operation.ID, operation.Generation)
		if taskErr != nil {
			return taskErr
		}
		if _, taskErr = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
			DedupeKey: "resource:operation:" + operation.ID.String() + ":" + int64String(operation.Generation),
			QueueName: tasks.ClusterTemplateApplyQueueName, MaxRetry: 8,
			Timeout: 2 * time.Minute, MaxDeliveryAttempts: 20,
		}); taskErr != nil {
			return taskErr
		}
		return recordAuditOutbox(r, q, "cluster.resource."+requiredVerb+".requested", "resource_operation",
			operation.ID.String(), intent.Name, http.StatusAccepted, map[string]any{
				"operation_id": operation.ID.String(), "cluster_id": clusterID.String(),
				"resource_type": intent.ResourceType, "namespace": intent.Namespace,
				"resource_name": intent.Name, "action": intent.Action,
			})
	})
	if errors.Is(err, errResourceOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different resource mutation")
		return
	}
	if errors.Is(err, errResourceOperationTargetBusy) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Another resource mutation is already active for this target")
		return
	}
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to persist resource mutation")
		return
	}
	location := "/api/v1/clusters/" + operation.ClusterID.String() + "/resources/operations/" + operation.ID.String() + "/"
	w.Header().Set("Location", location)
	w.Header().Set("Retry-After", "2")
	RespondJSON(w, http.StatusAccepted, resourceOperationResponse(operation))
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func int64String(value int64) string {
	// Avoid exposing request material through fmt formatting in this boundary.
	return strconv.FormatInt(value, 10)
}

// GetResourceOperation returns only sanitized receipt/evidence fields. The
// creator may poll; superusers may inspect operations for support workflows.
func (h *ResourceHandler) GetResourceOperation(w http.ResponseWriter, r *http.Request) {
	if h.resourceOperations == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Resource operation storage is unavailable")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid resource operation ID")
		return
	}
	operation, err := h.resourceOperations.GetResourceOperation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Resource operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to load resource operation")
		return
	}
	clusterID, parseErr := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if parseErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if operation.ClusterID != clusterID {
		// Do not reveal that an operation exists in another cluster.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Resource operation not found")
		return
	}
	if _, ok := middleware.GetAuthenticatedUser(r.Context()); !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	bindings, restricted, authErr := h.authz.bindingsForContext(r.Context())
	if authErr != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	resource, resourceOK := knownNativeK8sResource(operation.ResourceType)
	verb := rbac.Verb(operation.RequiredVerb)
	verbOK := verb == rbac.VerbCreate || verb == rbac.VerbUpdate || verb == rbac.VerbDelete
	allowed := !restricted
	if restricted && h.authz.engine != nil && resourceOK && verbOK {
		allowed = h.authz.engine.CheckPermission(bindings, resource, verb, clusterID, uuid.Nil, operation.Namespace) ||
			h.authz.allowsCluster(bindings, clusterID, rbac.ResourceClusters, rbac.VerbRead)
	}
	if !allowed {
		// Receipt IDs are opaque but still sensitive operational metadata. Hide
		// existence from callers whose originating permission was revoked.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Resource operation not found")
		return
	}
	RespondJSON(w, http.StatusOK, resourceOperationResponse(operation))
}
