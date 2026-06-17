package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

var jwtBase64Encoding = base64.URLEncoding

// MaxArgoCDOperationPolls caps the number of times the reconciler will poll
// upstream ArgoCD for an in-progress operation before declaring it timed out.
// At pollCadence (30s) below, this is a 30-minute ceiling.
const MaxArgoCDOperationPolls = 60

// argoCDPollCadence is how often the reconciler revisits running operations
// to check for completion. Runs as a tick inside runReconciler.
const argoCDPollCadence = 30 * time.Second

const (
	// ArgoCD ships as the bundled astro-argocd subchart in the astronomer
	// namespace, so its ServiceAccounts/cluster Secrets live there.
	argocdNamespace                   = "astronomer"
	argocdApplicationControllerSA     = "argocd-application-controller"
	localArgoCDTokenDuration          = 24 * time.Hour
	localArgoCDTokenRefreshWindow     = 2 * time.Hour
	argocdClusterSecretTypeLabelKey   = argolabels.ArgoCDClusterSecretTypeLabel
	argocdClusterSecretTypeLabelValue = argolabels.ArgoCDClusterSecretTypeValue
)

// ArgoCDQuerier abstracts the ArgoCD-related database queries needed by ArgoCDHandler.
type ArgoCDQuerier interface {
	GetArgoCDInstanceByID(ctx context.Context, id uuid.UUID) (sqlc.ArgocdInstance, error)
	GetArgoCDInstanceByName(ctx context.Context, name string) (sqlc.ArgocdInstance, error)
	GetArgoCDApplicationByName(ctx context.Context, arg sqlc.GetArgoCDApplicationByNameParams) (sqlc.ArgocdApplication, error)
	ListArgoCDInstances(ctx context.Context, arg sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error)
	CreateArgoCDInstance(ctx context.Context, arg sqlc.CreateArgoCDInstanceParams) (sqlc.ArgocdInstance, error)
	UpdateArgoCDInstance(ctx context.Context, arg sqlc.UpdateArgoCDInstanceParams) (sqlc.ArgocdInstance, error)
	UpdateArgoCDInstanceHealth(ctx context.Context, arg sqlc.UpdateArgoCDInstanceHealthParams) error
	DeleteArgoCDInstance(ctx context.Context, id uuid.UUID) error
	CountArgoCDInstances(ctx context.Context) (int64, error)
	// Applications
	ListArgoCDApplications(ctx context.Context, arg sqlc.ListArgoCDApplicationsParams) ([]sqlc.ArgocdApplication, error)
	ListAppsByInstance(ctx context.Context, arg sqlc.ListAppsByInstanceParams) ([]sqlc.ArgocdApplication, error)
	GetArgoCDApplicationByID(ctx context.Context, id uuid.UUID) (sqlc.ArgocdApplication, error)
	UpdateArgoCDApplication(ctx context.Context, arg sqlc.UpdateArgoCDApplicationParams) (sqlc.ArgocdApplication, error)
	CountArgoCDApplications(ctx context.Context) (int64, error)
	CountAppsByInstance(ctx context.Context, argocdInstanceID uuid.UUID) (int64, error)
	CreateArgoCDOperation(ctx context.Context, arg sqlc.CreateArgoCDOperationParams) (sqlc.ArgocdOperation, error)
	GetArgoCDOperation(ctx context.Context, id uuid.UUID) (sqlc.ArgocdOperation, error)
	ListArgoCDOperations(ctx context.Context, arg sqlc.ListArgoCDOperationsParams) ([]sqlc.ArgocdOperation, error)
	ListPendingArgoCDOperations(ctx context.Context, limit int32) ([]sqlc.ArgocdOperation, error)
	GetLatestArgoCDOperationForTarget(ctx context.Context, arg sqlc.GetLatestArgoCDOperationForTargetParams) (sqlc.ArgocdOperation, error)
	MarkArgoCDOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.ArgocdOperation, error)
	MarkArgoCDOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.ArgocdOperation, error)
	MarkArgoCDOperationFailed(ctx context.Context, arg sqlc.MarkArgoCDOperationFailedParams) (sqlc.ArgocdOperation, error)
	MarkArgoCDOperationSuperseded(ctx context.Context, arg sqlc.MarkArgoCDOperationSupersededParams) (sqlc.ArgocdOperation, error)
	RequeueArgoCDOperation(ctx context.Context, id uuid.UUID) (sqlc.ArgocdOperation, error)
	ListRunningArgoCDOperations(ctx context.Context, limit int32) ([]sqlc.ArgocdOperation, error)
	UpdateArgoCDOperationProgress(ctx context.Context, arg sqlc.UpdateArgoCDOperationProgressParams) (sqlc.ArgocdOperation, error)
	CompleteArgoCDOperationWithResult(ctx context.Context, arg sqlc.CompleteArgoCDOperationWithResultParams) (sqlc.ArgocdOperation, error)
	FailArgoCDOperationWithResult(ctx context.Context, arg sqlc.FailArgoCDOperationWithResultParams) (sqlc.ArgocdOperation, error)
	CreateArgoCDOperationEvent(ctx context.Context, arg sqlc.CreateArgoCDOperationEventParams) (sqlc.ArgocdOperationEvent, error)
	ListArgoCDOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.ArgocdOperationEvent, error)
	// Phase B1: managed-cluster index + cluster reads for registration.
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	CreateArgoCDManagedCluster(ctx context.Context, arg sqlc.CreateArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error)
	GetArgoCDManagedCluster(ctx context.Context, arg sqlc.GetArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error)
	ListArgoCDManagedClusters(ctx context.Context, argocdInstanceID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
	ListArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
	DeleteArgoCDManagedCluster(ctx context.Context, arg sqlc.DeleteArgoCDManagedClusterParams) error
	UpdateArgoCDManagedClusterLabels(ctx context.Context, arg sqlc.UpdateArgoCDManagedClusterLabelsParams) (sqlc.ArgocdManagedCluster, error)
	ListArgoCDBaselineOwnershipDecisions(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdBaselineOwnershipDecision, error)
	UpsertArgoCDBaselineOwnershipDecision(ctx context.Context, arg sqlc.UpsertArgoCDBaselineOwnershipDecisionParams) (sqlc.ArgocdBaselineOwnershipDecision, error)
}

// ArgoCDHandler handles ArgoCD endpoints.
type ArgoCDHandler struct {
	queries             ArgoCDQuerier
	log                 *slog.Logger
	http                *http.Client
	authz               authorizationSupport
	encryptor           *auth.Encryptor
	k8s                 kubernetes.Interface
	clusterProxyBaseURL string
	mu                  sync.Mutex
	trigger             chan struct{}
	// helmConcurrency caps the parallel dispatch fan-out for
	// executeOperation; zero falls back to the package default.
	helmConcurrency int
}

// NewArgoCDHandler creates a new ArgoCD handler.
func NewArgoCDHandler(queries ArgoCDQuerier) *ArgoCDHandler {
	return &ArgoCDHandler{
		queries: queries,
		log:     slog.Default(),
		http:    &http.Client{Timeout: 10 * time.Second},
		trigger: make(chan struct{}, 1),
	}
}

// SetEncryptor wires the Fernet encryptor used to decrypt auth_token_encrypted
// before it is surfaced to API clients or used as a Bearer token. When nil,
// instance responses fall back to omitting the token entirely.
func (h *ArgoCDHandler) SetEncryptor(e *auth.Encryptor) {
	h.encryptor = e
}

func (h *ArgoCDHandler) SetKubernetesClient(client kubernetes.Interface) {
	h.k8s = client
}

func (h *ArgoCDHandler) SetClusterProxyBaseURL(baseURL string) {
	h.clusterProxyBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// instanceResponse renders an ArgoCD instance for the API. The encrypted
// auth token is decrypted via the Fernet encryptor when configured and
// returned as plaintext under "auth_token". The "auth_token_encrypted"
// column is never exposed.
func (h *ArgoCDHandler) instanceResponse(instance sqlc.ArgocdInstance) map[string]any {
	resp := map[string]any{
		"id":         instance.ID.String(),
		"name":       instance.Name,
		"cluster_id": instance.ClusterID.String(),
		"api_url":    instance.ApiUrl,
		"verify_ssl": instance.VerifySsl,
		"is_healthy": instance.IsHealthy,
		"created_at": instance.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": instance.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if h.encryptor != nil && instance.AuthTokenEncrypted != "" {
		if plaintext, err := h.encryptor.Decrypt(instance.AuthTokenEncrypted); err == nil {
			resp["auth_token"] = plaintext
		} else if h.log != nil {
			h.log.Warn("failed to decrypt argocd auth token", "instance_id", instance.ID.String(), "error", err)
		}
	}
	return resp
}

// instanceResponses is the slice variant used by list endpoints.
func (h *ArgoCDHandler) instanceResponses(instances []sqlc.ArgocdInstance) []map[string]any {
	out := make([]map[string]any, 0, len(instances))
	for _, i := range instances {
		out = append(out, h.instanceResponse(i))
	}
	return out
}

// decryptInstanceToken returns the plaintext auth token for an instance,
// falling back to the encrypted column when no encryptor is configured.
// This keeps existing health probes working in dev environments where the
// "encrypted" column may already hold raw text.
func (h *ArgoCDHandler) decryptInstanceToken(instance sqlc.ArgocdInstance) string {
	token := strings.TrimSpace(instance.AuthTokenEncrypted)
	if h.encryptor == nil || token == "" {
		return token
	}
	if plaintext, err := h.encryptor.Decrypt(token); err == nil {
		return plaintext
	}
	return token
}

// --- Request types ---

// CreateArgoCDInstanceRequest represents the request body for creating an ArgoCD instance.
// Either AuthToken (plaintext, encrypted server-side via the Fernet encryptor)
// or AuthTokenEncrypted (already-encrypted ciphertext, e.g. from migrations)
// may be supplied. Plaintext takes precedence when both are present.
type CreateArgoCDInstanceRequest struct {
	Name               string    `json:"name"`
	ClusterID          uuid.UUID `json:"cluster_id"`
	ApiUrl             string    `json:"api_url"`
	AuthToken          string    `json:"auth_token,omitempty"`
	AuthTokenEncrypted string    `json:"auth_token_encrypted,omitempty"`
	VerifySsl          bool      `json:"verify_ssl"`
}

// resolveAuthToken returns the column value to write for auth_token_encrypted.
// Plaintext input is encrypted via the configured Fernet encryptor; passing
// already-ciphertext input through h.encryptor.Decrypt would round-trip,
// so we keep it as-is when no plaintext was supplied.
func (h *ArgoCDHandler) resolveAuthToken(req CreateArgoCDInstanceRequest) (string, error) {
	if req.AuthToken == "" {
		return req.AuthTokenEncrypted, nil
	}
	if h.encryptor == nil {
		// No encryptor: store plaintext. The deployment will fail Fernet
		// round-trips, which is preferable to silently dropping the secret.
		return req.AuthToken, nil
	}
	return h.encryptor.Encrypt(req.AuthToken)
}

type argocdOperationEnvelope struct {
	ApplicationID      string                    `json:"applicationId,omitempty"`
	InstanceID         string                    `json:"instanceId,omitempty"`
	SyncOptions        *argocdclient.SyncOptions `json:"syncOptions,omitempty"`
	Reason             string                    `json:"reason,omitempty"`
	SyncWindowOverride bool                      `json:"syncWindowOverride,omitempty"`
}

// SyncRequest is the JSON body accepted by POST /argocd/apps/{id}/sync/.
// All fields are optional — an empty body is a "sync at targetRevision,
// no prune, not a dry run" request.
type SyncRequest struct {
	Revision           string `json:"revision,omitempty"`
	Prune              bool   `json:"prune,omitempty"`
	DryRun             bool   `json:"dry_run,omitempty"`
	Reason             string `json:"reason,omitempty"`
	SyncWindowOverride bool   `json:"sync_window_override,omitempty"`
}

func normalizeSyncRequest(req SyncRequest) (SyncRequest, error) {
	req.Revision = strings.TrimSpace(req.Revision)
	req.Reason = strings.TrimSpace(req.Reason)
	if len(req.Reason) > 500 {
		return req, fmt.Errorf("reason must be 500 characters or fewer")
	}
	if req.SyncWindowOverride && req.Reason == "" {
		return req, fmt.Errorf("sync_window_override requires a reason")
	}
	return req, nil
}

func (h *ArgoCDHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *ArgoCDHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *ArgoCDHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.runReconciler(ctx)
}

func (h *ArgoCDHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

func (h *ArgoCDHandler) runReconciler(ctx context.Context) {
	opTicker := time.NewTicker(20 * time.Second)
	healthTicker := time.NewTicker(45 * time.Second)
	pollTicker := time.NewTicker(argoCDPollCadence)
	defer opTicker.Stop()
	defer healthTicker.Stop()
	defer pollTicker.Stop()
	h.processPendingOperations(ctx)
	h.reconcileInstanceHealth(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-opTicker.C:
			h.processPendingOperations(ctx)
		case <-healthTicker.C:
			h.reconcileInstanceHealth(ctx)
		case <-pollTicker.C:
			h.pollRunningOperations(ctx)
		case <-h.trigger:
			h.processPendingOperations(ctx)
		}
	}
}

// --- Endpoints ---

// ListInstances handles GET /api/v1/argocd/instances/.
func (h *ArgoCDHandler) ListInstances(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	instances, err := h.queries.ListArgoCDInstances(r.Context(), sqlc.ListArgoCDInstancesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list ArgoCD instances")
		return
	}

	total, err := h.queries.CountArgoCDInstances(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count ArgoCD instances")
		return
	}

	RespondPaginated(w, r, h.instanceResponses(instances), total)
}

// CreateInstance handles POST /api/v1/argocd/instances/.
func (h *ArgoCDHandler) CreateInstance(w http.ResponseWriter, r *http.Request) {
	var req CreateArgoCDInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}

	if req.Name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Instance name is required")
		return
	}
	if req.ClusterID == uuid.Nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cluster_id is required")
		return
	}
	if strings.TrimSpace(req.ApiUrl) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "api_url is required")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, req.ClusterID, rbac.ResourceClusters, rbac.VerbUpdate) {
		return
	}

	tokenColumn, err := h.resolveAuthToken(req)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptionError, "Failed to encrypt auth token")
		return
	}

	instance, err := h.queries.CreateArgoCDInstance(r.Context(), sqlc.CreateArgoCDInstanceParams{
		Name:               req.Name,
		ClusterID:          req.ClusterID,
		ApiUrl:             req.ApiUrl,
		AuthTokenEncrypted: tokenColumn,
		VerifySsl:          req.VerifySsl,
	})
	if err != nil {
		// Most realistic failure here is FK violation (cluster_id refers to a
		// cluster that doesn't exist) — surface that as 400 rather than a
		// generic 500 so the caller can fix their request.
		if isForeignKeyViolation(err) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidClusterID, "cluster_id does not match any registered cluster")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create ArgoCD instance: "+err.Error())
		return
	}

	recordAudit(r, h.queries, "argocd.instance.create", "argocd_instance", instance.ID.String(), instance.Name, map[string]any{
		"cluster_id": instance.ClusterID.String(),
		"api_url":    instance.ApiUrl,
		"verify_ssl": instance.VerifySsl,
	})

	RespondJSON(w, http.StatusCreated, h.instanceResponse(instance))
}

// GetInstance handles GET /api/v1/argocd/instances/{id}/.
func (h *ArgoCDHandler) GetInstance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return
	}

	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}

	RespondJSON(w, http.StatusOK, h.instanceResponse(instance))
}

// DeleteInstance handles DELETE /api/v1/argocd/instances/{id}/.
func (h *ArgoCDHandler) DeleteInstance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return
	}

	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceClusters, rbac.VerbUpdate) {
		return
	}
	if err := h.queries.DeleteArgoCDInstance(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}

	recordAudit(r, h.queries, "argocd.instance.delete", "argocd_instance", instance.ID.String(), instance.Name, map[string]any{
		"cluster_id": instance.ClusterID.String(),
	})

	w.WriteHeader(http.StatusNoContent)
}

// UpdateInstance handles PUT /api/v1/argocd/instances/{id}/.
func (h *ArgoCDHandler) UpdateInstance(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return
	}

	var req CreateArgoCDInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	current, err := h.queries.GetArgoCDInstanceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, current.ClusterID, rbac.ResourceClusters, rbac.VerbUpdate) {
		return
	}

	tokenColumn, err := h.resolveAuthToken(req)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptionError, "Failed to encrypt auth token")
		return
	}
	if req.AuthToken == "" && req.AuthTokenEncrypted == "" {
		// Preserve the existing column when the caller did not include one.
		tokenColumn = current.AuthTokenEncrypted
	}

	instance, err := h.queries.UpdateArgoCDInstance(r.Context(), sqlc.UpdateArgoCDInstanceParams{
		ID:                 id,
		Name:               req.Name,
		ApiUrl:             req.ApiUrl,
		AuthTokenEncrypted: tokenColumn,
		VerifySsl:          req.VerifySsl,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update ArgoCD instance")
		return
	}

	recordAudit(r, h.queries, "argocd.instance.update", "argocd_instance", instance.ID.String(), instance.Name, map[string]any{
		"cluster_id": instance.ClusterID.String(),
		"api_url":    instance.ApiUrl,
		"verify_ssl": instance.VerifySsl,
	})

	RespondJSON(w, http.StatusOK, h.instanceResponse(instance))
}

// ListAppsByInstance handles GET /api/v1/argocd/instances/{id}/apps/.
func (h *ArgoCDHandler) ListAppsByInstance(w http.ResponseWriter, r *http.Request) {
	instanceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return
	}

	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	apps, err := h.queries.ListAppsByInstance(r.Context(), sqlc.ListAppsByInstanceParams{
		ArgocdInstanceID: instanceID,
		Limit:            limit,
		Offset:           offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list ArgoCD applications")
		return
	}

	total, err := h.queries.CountAppsByInstance(r.Context(), instanceID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count ArgoCD applications")
		return
	}

	RespondPaginated(w, r, apps, total)
}

// ListAllApps handles GET /api/v1/argocd/apps/.
func (h *ArgoCDHandler) ListAllApps(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	apps, err := h.queries.ListArgoCDApplications(r.Context(), sqlc.ListArgoCDApplicationsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list ArgoCD applications")
		return
	}

	total, err := h.queries.CountArgoCDApplications(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count ArgoCD applications")
		return
	}

	RespondPaginated(w, r, apps, total)
}

// GetApp handles GET /api/v1/argocd/apps/{id}/.
func (h *ArgoCDHandler) GetApp(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid application ID")
		return
	}

	app, err := h.queries.GetArgoCDApplicationByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD application not found")
		return
	}

	RespondJSON(w, http.StatusOK, app)
}

// SyncApp handles POST /api/v1/argocd/apps/{id}/sync/.
// Accepts an optional JSON body {"revision": "main", "prune": true, "dry_run": false}
// to influence the sync. An empty body is a default sync at the application's
// targetRevision.
func (h *ArgoCDHandler) SyncApp(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid application ID")
		return
	}

	app, err := h.queries.GetArgoCDApplicationByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD application not found")
		return
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), app.ArgocdInstanceID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, rbac.VerbUpdate) {
		return
	}

	var req SyncRequest
	// An empty body is fine; we only fail on malformed JSON.
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
				return
			}
		}
	}
	req, err = normalizeSyncRequest(req)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}

	op, err := h.enqueueSyncOperation(withOperationIdempotency(r, "argocd"), app, currentUserUUID(r), argocdclient.SyncOptions{
		Revision: req.Revision,
		Prune:    req.Prune,
		DryRun:   req.DryRun,
	}, req.Reason, req.SyncWindowOverride)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SyncError, "Failed to enqueue ArgoCD sync")
		return
	}
	recordAudit(r, h.queries, "argocd.app.sync", "argocd_application", app.ID.String(), app.Name, map[string]any{
		"instance_id":          app.ArgocdInstanceID.String(),
		"revision":             req.Revision,
		"prune":                req.Prune,
		"dry_run":              req.DryRun,
		"sync_window_override": req.SyncWindowOverride,
		"override_reason":      req.Reason,
		"operation_id":         op.ID.String(),
	})
	RespondJSON(w, http.StatusAccepted, argocdOperationResponse(op))
}

// InstanceHealth handles GET /api/v1/argocd/instances/{id}/health/.
// Probes the ArgoCD API to check live reachability.
func (h *ArgoCDHandler) InstanceHealth(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
		return
	}
	healthy := h.probeInstance(r.Context(), instance)
	_ = h.queries.UpdateArgoCDInstanceHealth(r.Context(), sqlc.UpdateArgoCDInstanceHealthParams{
		ID:        instance.ID,
		IsHealthy: healthy,
	})
	if !healthy {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{"is_healthy": false})
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"is_healthy": true})
}

// LiveApplications handles GET /api/v1/argocd/instances/{id}/applications/.
// Fetches applications live from the ArgoCD API.
func (h *ArgoCDHandler) LiveApplications(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
		return
	}
	apps, err := h.fetchInstanceJSON(r.Context(), instance, "/api/v1/applications")
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ArgoCDError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, apps)
}

// AppHistory handles GET /api/v1/argocd/applications/{id}/history/.
func (h *ArgoCDHandler) AppHistory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid application ID")
		return
	}
	app, err := h.queries.GetArgoCDApplicationByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD application not found")
		return
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), app.ArgocdInstanceID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
		return
	}
	history, err := h.fetchInstanceJSON(r.Context(), instance, "/api/v1/applications/"+app.Name+"/revisions")
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ArgoCDError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, history)
}

// AppManifests handles GET /api/v1/argocd/applications/{id}/manifests/.
func (h *ArgoCDHandler) AppManifests(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid application ID")
		return
	}
	app, err := h.queries.GetArgoCDApplicationByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD application not found")
		return
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), app.ArgocdInstanceID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
		return
	}
	manifests, err := h.fetchInstanceJSON(r.Context(), instance, "/api/v1/applications/"+app.Name+"/manifests")
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ArgoCDError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, manifests)
}

// RefreshApp handles POST /api/v1/argocd/applications/{id}/refresh/.
func (h *ArgoCDHandler) RefreshApp(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid application ID")
		return
	}
	app, err := h.queries.GetArgoCDApplicationByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD application not found")
		return
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), app.ArgocdInstanceID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, rbac.VerbUpdate) {
		return
	}
	hard := strings.EqualFold(r.URL.Query().Get("hard"), "true")
	client := h.argoCDClient(instance)
	upstream, err := client.Refresh(r.Context(), app.Name, hard)
	if err != nil {
		status := http.StatusBadGateway
		if argocdclient.IsKind(err, argocdclient.ErrUnauthorized) {
			status = http.StatusUnauthorized
		} else if argocdclient.IsKind(err, argocdclient.ErrNotFound) {
			status = http.StatusNotFound
		}
		RespondRequestError(w, r, status, apierror.ArgoCDError, err.Error())
		return
	}
	recordAudit(r, h.queries, "argocd.app.refresh", "argocd_application", app.ID.String(), app.Name, map[string]any{
		"instance_id": app.ArgocdInstanceID.String(),
		"hard":        hard,
	})
	RespondJSON(w, http.StatusAccepted, upstream)
}

// fetchInstanceJSON performs a GET against the ArgoCD instance's API and decodes JSON.
func (h *ArgoCDHandler) fetchInstanceJSON(ctx context.Context, instance sqlc.ArgocdInstance, path string) (any, error) {
	return h.callInstance(ctx, instance, http.MethodGet, path, nil)
}

func (h *ArgoCDHandler) callInstance(ctx context.Context, instance sqlc.ArgocdInstance, method, path string, body []byte) (any, error) {
	url := strings.TrimRight(instance.ApiUrl, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	if token := strings.TrimSpace(h.decryptInstanceToken(instance)); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("argocd API returned status %d", resp.StatusCode)
	}
	var payload any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// SyncAppByName handles POST /api/v1/argocd/instances/{id}/applications/{name}/sync/.
func (h *ArgoCDHandler) SyncAppByName(w http.ResponseWriter, r *http.Request) {
	instanceID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return
	}
	name := chi.URLParam(r, "name")
	app, err := h.queries.GetArgoCDApplicationByName(r.Context(), sqlc.GetArgoCDApplicationByNameParams{
		ArgocdInstanceID: instanceID,
		Name:             name,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD application not found")
		return
	}

	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("id", app.ID.String())
	h.SyncApp(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx)))
}

func (h *ArgoCDHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryInt(r, "limit", 50))
	offset := int32(queryInt(r, "offset", 0))
	arg := sqlc.ListArgoCDOperationsParams{Limit: limit, Offset: offset}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	ops, err := h.queries.ListArgoCDOperations(r.Context(), arg)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list ArgoCD operations")
		return
	}
	bindings, restricted, err := h.authz.bindingsForContext(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		if restricted {
			clusterID, err := h.operationClusterID(r.Context(), op)
			if err != nil || !h.authz.allowsCluster(bindings, clusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
				continue
			}
		}
		items = append(items, argocdOperationResponse(op))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"data": items, "limit": limit, "offset": offset})
}

func (h *ArgoCDHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetArgoCDOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD operation not found")
		return
	}
	clusterID, err := h.operationClusterID(r.Context(), op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve ArgoCD operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
		return
	}
	resp := argocdOperationResponse(op)
	if events, err := h.queries.ListArgoCDOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = argocdOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *ArgoCDHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetArgoCDOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := h.operationClusterID(r.Context(), op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve ArgoCD operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceWorkloads, rbac.VerbUpdate) {
		return
	}
	requeued, err := h.queries.RequeueArgoCDOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RetryError, "Failed to retry ArgoCD operation")
		return
	}
	h.TriggerReconcile()
	recordAudit(r, h.queries, "argocd.operation.retry", "argocd_operation", id.String(), op.TargetKey, map[string]any{
		"target_type":     op.TargetType,
		"previous_status": op.Status,
	})
	RespondJSON(w, http.StatusAccepted, argocdOperationResponse(requeued))
}

func (h *ArgoCDHandler) operationClusterID(ctx context.Context, op sqlc.ArgocdOperation) (uuid.UUID, error) {
	var env argocdOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err == nil && env.InstanceID != "" {
		instanceID, parseErr := uuid.Parse(env.InstanceID)
		if parseErr == nil {
			instance, err := h.queries.GetArgoCDInstanceByID(ctx, instanceID)
			if err == nil {
				return instance.ClusterID, nil
			}
		}
	}
	appID, err := uuid.Parse(op.TargetKey)
	if err != nil {
		return uuid.UUID{}, err
	}
	app, err := h.queries.GetArgoCDApplicationByID(ctx, appID)
	if err != nil {
		return uuid.UUID{}, err
	}
	instance, err := h.queries.GetArgoCDInstanceByID(ctx, app.ArgocdInstanceID)
	if err != nil {
		return uuid.UUID{}, err
	}
	return instance.ClusterID, nil
}

func (h *ArgoCDHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load ArgoCD controller status")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *ArgoCDHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	instances, err := h.queries.ListArgoCDInstances(ctx, sqlc.ListArgoCDInstancesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	ops, err := h.queries.ListArgoCDOperations(ctx, sqlc.ListArgoCDOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	healthy := 0
	totalInstances := 0
	for _, instance := range instances {
		if restricted && !h.authz.allowsCluster(bindings, instance.ClusterID, rbac.ResourceWorkloads, rbac.VerbRead) {
			continue
		}
		totalInstances++
		if instance.IsHealthy {
			healthy++
		}
	}
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.ArgocdOperation]{
		Status:    func(op sqlc.ArgocdOperation) string { return op.Status },
		CreatedAt: func(op sqlc.ArgocdOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.ArgocdOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Include: func(ctx context.Context, op sqlc.ArgocdOperation) bool {
			if !restricted {
				return true
			}
			clusterID, err := h.operationClusterID(ctx, op)
			return err == nil && h.authz.allowsCluster(bindings, clusterID, rbac.ResourceWorkloads, rbac.VerbRead)
		},
		Preview:               func(ctx context.Context, op sqlc.ArgocdOperation) map[string]any { return h.operationPreview(ctx, op) },
		StaleThresholdSeconds: 60,
	})
	return map[string]any{
		"reconciler": opSummary.reconcilerMap(),
		"instances": map[string]any{
			"total":     totalInstances,
			"healthy":   healthy,
			"unhealthy": totalInstances - healthy,
		},
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
	}, nil
}

func (h *ArgoCDHandler) enqueueSyncOperation(ctx context.Context, app sqlc.ArgocdApplication, userID pgtype.UUID, opts argocdclient.SyncOptions, reason string, syncWindowOverride bool) (sqlc.ArgocdOperation, error) {
	envelope := argocdOperationEnvelope{
		ApplicationID:      app.ID.String(),
		InstanceID:         app.ArgocdInstanceID.String(),
		Reason:             reason,
		SyncWindowOverride: syncWindowOverride,
	}
	if opts.Revision != "" || opts.Prune || opts.DryRun {
		envelope.SyncOptions = &opts
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return sqlc.ArgocdOperation{}, err
	}
	params := sqlc.CreateArgoCDOperationParams{
		TargetType:    "application",
		TargetKey:     app.ID.String(),
		OperationType: "sync",
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.ArgocdOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := h.queries.(interface {
			CreateArgoCDOperationIdempotent(context.Context, sqlc.CreateArgoCDOperationIdempotentParams) (sqlc.ArgocdOperation, error)
		}); ok {
			op, err = creator.CreateArgoCDOperationIdempotent(ctx, sqlc.CreateArgoCDOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = h.queries.CreateArgoCDOperation(ctx, params)
	}
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func argocdOperationResponse(op sqlc.ArgocdOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func argocdOperationEventsResponse(events []sqlc.ArgocdOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *ArgoCDHandler) operationPreview(ctx context.Context, op sqlc.ArgocdOperation) map[string]any {
	resp := argocdOperationResponse(op)
	if events, err := h.queries.ListArgoCDOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = argocdOperationEventsResponse(lastArgoCDEvents(events, 3))
	}
	return resp
}

func lastArgoCDEvents(events []sqlc.ArgocdOperationEvent, n int) []sqlc.ArgocdOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *ArgoCDHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, dispatch outside — one stuck Argo CD
	// instance must not block other clusters' operations. Same
	// pattern as catalog/tools/monitoring.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingArgoCDOperations(ctx))
}

// claimPendingArgoCDOperations holds h.mu just long enough to mark
// supersession + claim the batch ("running" state). Returns claimedOps
// whose Run closures do the executeOperation call AND inline the
// success-path bookkeeping (because argocd has a two-way split: async
// stays "running" with UpdateArgoCDOperationProgress, sync transitions
// to "completed" with CompleteArgoCDOperationWithResult). OnComplete is
// nil for that reason — the success state is written from inside Run.
func (h *ArgoCDHandler) claimPendingArgoCDOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingArgoCDOperations(ctx, 20)
	if err != nil {
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.ArgocdOperation]{
		ID:        func(op sqlc.ArgocdOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.ArgocdOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.ArgocdOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.ArgocdOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.ArgocdOperation) {
			h.recordArgoCDOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkArgoCDOperationSuperseded(ctx, sqlc.MarkArgoCDOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			})
		},
		MarkRunning: func(ctx context.Context, op sqlc.ArgocdOperation) (sqlc.ArgocdOperation, error) {
			running, err := h.queries.MarkArgoCDOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.ArgocdOperation{}, err
			}
			h.recordArgoCDOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
			})
			return running, nil
		},
		Claimed: func(running sqlc.ArgocdOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					result, err := h.executeOperation(ctx, running)
					if err != nil {
						return err
					}
					// If the operation is still in flight upstream, leave
					// it as 'running' and let pollRunningOperations drive
					// completion. Otherwise mark it complete.
					if result.async {
						h.recordArgoCDOperationEvent(ctx, running.ID, "info", "sync", "operation accepted upstream; polling for completion", map[string]any{
							"phase":       result.phase,
							"operationId": result.operationID,
							"revision":    result.revision,
						})
						_, _ = h.queries.UpdateArgoCDOperationProgress(ctx, sqlc.UpdateArgoCDOperationProgressParams{
							ID:          running.ID,
							Phase:       result.phase,
							OperationID: result.operationID,
							Revision:    result.revision,
							Message:     result.message,
						})
						return nil
					}
					h.recordArgoCDOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{
						"phase":    result.phase,
						"revision": result.revision,
					})
					_, _ = h.queries.CompleteArgoCDOperationWithResult(ctx, sqlc.CompleteArgoCDOperationWithResultParams{
						ID:          running.ID,
						Phase:       firstNonEmptyString(result.phase, "Succeeded"),
						OperationID: result.operationID,
						Revision:    result.revision,
						Message:     result.message,
					})
					return nil
				},
				// OnComplete intentionally nil: Run inlines the success
				// bookkeeping because argocd's terminal state depends on
				// the operationResult.async flag.
				OnFailure: func(ctx context.Context, err error) {
					h.recordArgoCDOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
					_, _ = h.queries.FailArgoCDOperationWithResult(ctx, sqlc.FailArgoCDOperationWithResultParams{
						ID:           running.ID,
						Phase:        "Failed",
						ErrorMessage: err.Error(),
						Message:      err.Error(),
					})
					if h.log != nil {
						h.log.Warn("argocd operation failed", "id", running.ID.String(), "error", err)
					}
				},
			}
		},
	})
}

// operationResult communicates the outcome of executeOperation to its caller
// in processPendingOperations. async==true means the upstream call has been
// accepted and is now in flight; the polling loop will finish the work.
type operationResult struct {
	async       bool
	phase       string
	operationID string
	revision    string
	message     string
}

func (h *ArgoCDHandler) executeOperation(ctx context.Context, op sqlc.ArgocdOperation) (operationResult, error) {
	var env argocdOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return operationResult{}, err
	}
	switch op.OperationType {
	case "sync":
		return h.executeSync(ctx, op, env)
	default:
		return operationResult{}, fmt.Errorf("unsupported argocd operation type: %s", op.OperationType)
	}
}

// executeSync performs the real `POST /api/v1/applications/{name}/sync`
// against the upstream ArgoCD using the decrypted bearer token, then maps
// the response into our internal operationResult. ArgoCD's sync is async:
// the response usually carries phase=Running and the polling loop in
// pollRunningOperations is responsible for converging it to a terminal state.
func (h *ArgoCDHandler) executeSync(ctx context.Context, op sqlc.ArgocdOperation, env argocdOperationEnvelope) (operationResult, error) {
	appID, err := uuid.Parse(env.ApplicationID)
	if err != nil {
		return operationResult{}, err
	}
	app, err := h.queries.GetArgoCDApplicationByID(ctx, appID)
	if err != nil {
		return operationResult{}, err
	}
	instance, err := h.queries.GetArgoCDInstanceByID(ctx, app.ArgocdInstanceID)
	if err != nil {
		return operationResult{}, err
	}
	if !instance.IsHealthy {
		return operationResult{}, fmt.Errorf("argocd instance %s is not healthy", instance.Name)
	}
	h.recordArgoCDOperationEvent(ctx, op.ID, "info", "sync", "calling upstream ArgoCD sync", map[string]any{
		"applicationId":            app.ID.String(),
		"application":              app.Name,
		"instanceId":               instance.ID.String(),
		"instanceName":             instance.Name,
		"apiUrl":                   instance.ApiUrl,
		"syncWindowOverride":       env.SyncWindowOverride,
		"syncWindowOverrideReason": env.Reason,
	})

	client := h.argoCDClient(instance)
	opts := argocdclient.SyncOptions{}
	if env.SyncOptions != nil {
		opts = *env.SyncOptions
	}
	upstream, err := client.Sync(ctx, app.Name, opts)
	if err != nil {
		return operationResult{}, err
	}
	res := operationResultFromApp(upstream)
	// Reflect upstream truth onto our cached application row. Sync is in
	// flight; sync_status mirrors the upstream "phase" rather than fabricating
	// "Synced" before any reconciliation has happened.
	syncStatus := app.SyncStatus
	if upstream.Status.Sync.Status != "" {
		syncStatus = upstream.Status.Sync.Status
	}
	healthStatus := app.HealthStatus
	if upstream.Status.Health.Status != "" {
		healthStatus = upstream.Status.Health.Status
	}
	resourceCreatedCount := app.ResourceCreatedCount
	resourceChangedCount := app.ResourceChangedCount
	resourcePrunedCount := app.ResourcePrunedCount
	if created, changed, pruned, ok := argoCDResourceDriftCountsFromApplication(upstream); ok {
		resourceCreatedCount = created
		resourceChangedCount = changed
		resourcePrunedCount = pruned
	}
	lastSynced := app.LastSynced
	if !res.async && res.phase == "Succeeded" {
		lastSynced = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	if _, updErr := h.queries.UpdateArgoCDApplication(ctx, sqlc.UpdateArgoCDApplicationParams{
		ID:                   app.ID,
		Project:              app.Project,
		RepoUrl:              app.RepoUrl,
		Path:                 app.Path,
		TargetRevision:       app.TargetRevision,
		DestinationCluster:   app.DestinationCluster,
		DestinationNamespace: app.DestinationNamespace,
		SyncStatus:           syncStatus,
		HealthStatus:         healthStatus,
		ResourceCreatedCount: resourceCreatedCount,
		ResourceChangedCount: resourceChangedCount,
		ResourcePrunedCount:  resourcePrunedCount,
		LastSynced:           lastSynced,
	}); updErr != nil && h.log != nil {
		h.log.Warn("failed to update argocd application after sync", "id", app.ID.String(), "error", updErr)
	}
	return res, nil
}

// operationResultFromApp maps an ArgoCD application response into our
// internal operationResult. It encodes the phase mapping spelled out in
// argoCDPhaseToStatus.
func operationResultFromApp(app *argocdclient.Application) operationResult {
	res := operationResult{}
	if app == nil {
		return res
	}
	state := app.Status.OperationState
	if state == nil {
		// No operationState means the sync was rejected without producing
		// one or has not been observed yet; treat as still running so the
		// poll loop checks again.
		res.async = true
		res.phase = "Running"
		res.revision = app.Status.Sync.Revision
		return res
	}
	res.phase = state.Phase
	res.message = state.Message
	if state.SyncResult != nil && state.SyncResult.Revision != "" {
		res.revision = state.SyncResult.Revision
	} else if state.Operation != nil && state.Operation.Sync != nil {
		res.revision = state.Operation.Sync.Revision
	}
	if res.revision == "" {
		res.revision = app.Status.Sync.Revision
	}
	if isTerminalArgoCDPhase(state.Phase) {
		res.async = false
		return res
	}
	res.async = true
	return res
}

func argoCDResourceDriftCountsFromApplication(app *argocdclient.Application) (created, changed, pruned int32, observed bool) {
	if app == nil || len(app.Status.Resources) == 0 {
		return 0, 0, 0, false
	}
	for _, resource := range app.Status.Resources {
		status := normalizeArgoStatus(resource.Status)
		switch {
		case resource.RequiresPruning || status == "extraneous":
			pruned++
		case status == "missing":
			created++
		case status == "outofsync" || status == "modified":
			changed++
		}
	}
	return created, changed, pruned, true
}

// isTerminalArgoCDPhase reports whether a phase reported by ArgoCD's
// operationState is final; non-terminal phases (Running, Terminating, "")
// keep the operation in our 'running' state and continue to be polled.
func isTerminalArgoCDPhase(phase string) bool {
	switch phase {
	case "Succeeded", "Failed", "Error":
		return true
	}
	return false
}

// pollRunningOperations is invoked on a 30s tick. For each running operation
// it calls GetApp upstream and folds the response into our row. Operations
// that exceed MaxArgoCDOperationPolls are timed out as failed.
func (h *ArgoCDHandler) pollRunningOperations(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListRunningArgoCDOperations(ctx, 50)
	if err != nil {
		return
	}
	for _, op := range ops {
		// Only sync operations are async-pollable today.
		if op.OperationType != "sync" {
			continue
		}
		var env argocdOperationEnvelope
		if err := json.Unmarshal(op.Payload, &env); err != nil {
			h.failOperationWithMessage(ctx, op, "Failed", err.Error())
			continue
		}
		appID, err := uuid.Parse(env.ApplicationID)
		if err != nil {
			h.failOperationWithMessage(ctx, op, "Failed", err.Error())
			continue
		}
		app, err := h.queries.GetArgoCDApplicationByID(ctx, appID)
		if err != nil {
			continue
		}
		instance, err := h.queries.GetArgoCDInstanceByID(ctx, app.ArgocdInstanceID)
		if err != nil {
			continue
		}
		if op.PollAttempts >= MaxArgoCDOperationPolls {
			h.failOperationWithMessage(ctx, op, "Failed", "timed out waiting for ArgoCD sync to converge")
			continue
		}
		client := h.argoCDClient(instance)
		upstream, err := client.GetApp(ctx, app.Name)
		if err != nil {
			// Transient errors: bump poll counter, keep retrying. Auth/NotFound
			// errors are terminal.
			if argocdclient.IsKind(err, argocdclient.ErrUnauthorized) || argocdclient.IsKind(err, argocdclient.ErrNotFound) {
				h.failOperationWithMessage(ctx, op, "Failed", err.Error())
				continue
			}
			_, _ = h.queries.UpdateArgoCDOperationProgress(ctx, sqlc.UpdateArgoCDOperationProgressParams{
				ID:          op.ID,
				Phase:       op.Phase,
				OperationID: op.OperationID,
				Revision:    op.Revision,
				Message:     err.Error(),
			})
			continue
		}
		res := operationResultFromApp(upstream)
		resourceCreatedCount := app.ResourceCreatedCount
		resourceChangedCount := app.ResourceChangedCount
		resourcePrunedCount := app.ResourcePrunedCount
		if created, changed, pruned, ok := argoCDResourceDriftCountsFromApplication(upstream); ok {
			resourceCreatedCount = created
			resourceChangedCount = changed
			resourcePrunedCount = pruned
		}
		// Reflect cached app status, regardless of phase.
		_, _ = h.queries.UpdateArgoCDApplication(ctx, sqlc.UpdateArgoCDApplicationParams{
			ID:                   app.ID,
			Project:              app.Project,
			RepoUrl:              app.RepoUrl,
			Path:                 app.Path,
			TargetRevision:       app.TargetRevision,
			DestinationCluster:   app.DestinationCluster,
			DestinationNamespace: app.DestinationNamespace,
			SyncStatus:           firstNonEmptyString(upstream.Status.Sync.Status, app.SyncStatus),
			HealthStatus:         firstNonEmptyString(upstream.Status.Health.Status, app.HealthStatus),
			ResourceCreatedCount: resourceCreatedCount,
			ResourceChangedCount: resourceChangedCount,
			ResourcePrunedCount:  resourcePrunedCount,
			LastSynced:           lastSyncedFor(app, res),
		})
		if res.async {
			_, _ = h.queries.UpdateArgoCDOperationProgress(ctx, sqlc.UpdateArgoCDOperationProgressParams{
				ID:          op.ID,
				Phase:       firstNonEmptyString(res.phase, "Running"),
				OperationID: firstNonEmptyString(res.operationID, op.OperationID),
				Revision:    firstNonEmptyString(res.revision, op.Revision),
				Message:     res.message,
			})
			continue
		}
		// Terminal upstream phase.
		if res.phase == "Succeeded" {
			h.recordArgoCDOperationEvent(ctx, op.ID, "info", "complete", "ArgoCD sync converged", map[string]any{
				"phase":    res.phase,
				"revision": res.revision,
			})
			_, _ = h.queries.CompleteArgoCDOperationWithResult(ctx, sqlc.CompleteArgoCDOperationWithResultParams{
				ID:          op.ID,
				Phase:       res.phase,
				OperationID: firstNonEmptyString(res.operationID, op.OperationID),
				Revision:    res.revision,
				Message:     res.message,
			})
			continue
		}
		// Failed / Error.
		h.recordArgoCDOperationEvent(ctx, op.ID, "error", "complete", "ArgoCD sync failed", map[string]any{
			"phase":   res.phase,
			"message": res.message,
		})
		_, _ = h.queries.FailArgoCDOperationWithResult(ctx, sqlc.FailArgoCDOperationWithResultParams{
			ID:           op.ID,
			Phase:        res.phase,
			OperationID:  firstNonEmptyString(res.operationID, op.OperationID),
			Revision:     res.revision,
			Message:      res.message,
			ErrorMessage: firstNonEmptyString(res.message, res.phase),
		})
	}
}

// argoCDClient builds a typed client for the given instance. The token is
// decrypted on demand to avoid keeping plaintext in memory longer than
// necessary.
//
// We share h.http (the handler's pre-configured *http.Client) for connection
// pooling. Per-instance TLS verification (instance.VerifySsl) is honored by
// existing callers (probeInstance, callInstance) at the request layer; if
// future work needs per-instance TLS configs we'll switch to one client per
// instance keyed on (api_url, verify_ssl).
func (h *ArgoCDHandler) argoCDClient(instance sqlc.ArgocdInstance) *argocdclient.Client {
	return argocdclient.NewClient(instance.ApiUrl, h.decryptInstanceToken(instance), argocdclient.Options{
		VerifySSL:  instance.VerifySsl,
		Timeout:    argocdclient.DefaultTimeout,
		HTTPClient: h.http,
	})
}

func (h *ArgoCDHandler) failOperationWithMessage(ctx context.Context, op sqlc.ArgocdOperation, phase, msg string) {
	h.recordArgoCDOperationEvent(ctx, op.ID, "error", "complete", "ArgoCD sync failed", map[string]any{
		"phase":   phase,
		"message": msg,
	})
	_, _ = h.queries.FailArgoCDOperationWithResult(ctx, sqlc.FailArgoCDOperationWithResultParams{
		ID:           op.ID,
		Phase:        phase,
		OperationID:  op.OperationID,
		Revision:     op.Revision,
		Message:      msg,
		ErrorMessage: msg,
	})
}

func lastSyncedFor(app sqlc.ArgocdApplication, res operationResult) pgtype.Timestamptz {
	if !res.async && res.phase == "Succeeded" {
		return pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	return app.LastSynced
}

func (h *ArgoCDHandler) recordArgoCDOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateArgoCDOperationEvent(ctx, sqlc.CreateArgoCDOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

func (h *ArgoCDHandler) reconcileInstanceHealth(ctx context.Context) {
	instances, err := h.queries.ListArgoCDInstances(ctx, sqlc.ListArgoCDInstancesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return
	}
	for _, instance := range instances {
		h.refreshLocalManagedClusterRegistrations(ctx, instance)
		healthy := h.probeInstance(ctx, instance)
		_ = h.queries.UpdateArgoCDInstanceHealth(ctx, sqlc.UpdateArgoCDInstanceHealthParams{
			ID:        instance.ID,
			IsHealthy: healthy,
		})
	}
}

func (h *ArgoCDHandler) probeInstance(ctx context.Context, instance sqlc.ArgocdInstance) bool {
	url := strings.TrimRight(instance.ApiUrl, "/")
	if url == "" {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/version", nil)
	if err != nil {
		return false
	}
	if token := strings.TrimSpace(h.decryptInstanceToken(instance)); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return false
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	return resp.StatusCode < http.StatusBadRequest
}

func (h *ArgoCDHandler) refreshLocalManagedClusterRegistrations(ctx context.Context, instance sqlc.ArgocdInstance) {
	if h == nil || h.k8s == nil {
		return
	}
	rows, err := h.queries.ListArgoCDManagedClusters(ctx, instance.ID)
	if err != nil {
		return
	}
	client := h.argoCDClient(instance)
	for _, row := range rows {
		cluster, err := h.queries.GetClusterByID(ctx, row.ClusterID)
		if err != nil || !cluster.IsLocal {
			continue
		}
		if err := h.refreshLocalManagedClusterRegistration(ctx, client, instance.ID, cluster, row); err != nil && h.log != nil {
			h.log.Warn("failed to refresh local argocd managed cluster registration", "instance_id", instance.ID.String(), "cluster_id", cluster.ID.String(), "error", err)
		}
	}
}

func (h *ArgoCDHandler) defaultManagedClusterServer(cluster sqlc.Cluster) string {
	if server := strings.TrimSpace(cluster.ApiServerUrl); server != "" {
		return server
	}
	if cluster.IsLocal || h == nil || h.clusterProxyBaseURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/api/v1/clusters/%s/k8s", h.clusterProxyBaseURL, cluster.ID.String())
}

func (h *ArgoCDHandler) refreshLocalManagedClusterRegistration(ctx context.Context, client *argocdclient.Client, instanceID uuid.UUID, cluster sqlc.Cluster, row sqlc.ArgocdManagedCluster) error {
	desiredServer := strings.TrimSpace(cluster.ApiServerUrl)
	if desiredServer == "" {
		desiredServer = strings.TrimSpace(row.ServerUrl)
	}
	if desiredServer == "" {
		return fmt.Errorf("local cluster %s has no api_server_url", cluster.ID)
	}
	refresh, err := h.localManagedClusterNeedsRefresh(ctx, row, desiredServer)
	if err != nil {
		return err
	}
	if !refresh {
		return nil
	}
	return h.upsertLocalManagedClusterRegistration(ctx, client, instanceID, cluster, row, desiredServer)
}

func (h *ArgoCDHandler) localManagedClusterNeedsRefresh(ctx context.Context, row sqlc.ArgocdManagedCluster, desiredServer string) (bool, error) {
	if row.ServerUrl != desiredServer {
		return true, nil
	}
	secret, err := h.lookupArgoCDClusterSecret(ctx, row.ClusterSecretName, row.ServerUrl)
	if err != nil {
		return false, err
	}
	if secret == nil {
		return true, nil
	}
	expiry, ok, err := argoCDClusterTokenExpiry(secret)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	return time.Until(expiry) <= localArgoCDTokenRefreshWindow, nil
}

func (h *ArgoCDHandler) upsertLocalManagedClusterRegistration(ctx context.Context, client *argocdclient.Client, instanceID uuid.UUID, cluster sqlc.Cluster, row sqlc.ArgocdManagedCluster, desiredServer string) error {
	token, err := h.createLocalArgoCDServiceAccountToken(ctx)
	if err != nil {
		return err
	}
	labels, err := h.managedClusterLabels(ctx, cluster)
	if err != nil {
		return err
	}
	if len(row.Labels) > 0 {
		var existing map[string]string
		if err := json.Unmarshal(row.Labels, &existing); err == nil {
			mergeManagedClusterLabelOverrides(labels, existing)
		}
	}
	reg := argocdclient.ClusterRegistration{
		Server: desiredServer,
		Name:   cluster.Name,
		Upsert: true,
		Config: argocdclient.ClusterConfig{
			BearerToken: token,
			TLSClientConfig: &argocdclient.TLSClientConfig{
				Insecure: cluster.CaCertificate == "",
				CAData:   []byte(cluster.CaCertificate),
			},
		},
		Labels: labels,
	}
	upstream, err := client.RegisterCluster(ctx, reg)
	if err != nil {
		return err
	}
	if row.ServerUrl != "" && row.ServerUrl != desiredServer {
		if err := client.UnregisterCluster(ctx, row.ServerUrl); err != nil && !argocdclient.IsKind(err, argocdclient.ErrNotFound) {
			return err
		}
	}
	secretName := clusterSecretNameFromServer(ctx, h.k8s, desiredServer)
	labelsJSON, _ := json.Marshal(labels)
	_, err = h.queries.CreateArgoCDManagedCluster(ctx, sqlc.CreateArgoCDManagedClusterParams{
		ArgocdInstanceID:  instanceID,
		ClusterID:         cluster.ID,
		ClusterSecretName: firstNonEmptyString(secretName, upstream.Name, cluster.Name),
		ServerUrl:         desiredServer,
		Labels:            labelsJSON,
	})
	return err
}

func (h *ArgoCDHandler) createLocalArgoCDServiceAccountToken(ctx context.Context) (string, error) {
	if h == nil || h.k8s == nil {
		return "", fmt.Errorf("kubernetes client not configured")
	}
	duration := int64(localArgoCDTokenDuration.Seconds())
	tokenReq, err := h.k8s.CoreV1().ServiceAccounts(argocdNamespace).CreateToken(ctx, argocdApplicationControllerSA, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			ExpirationSeconds: &duration,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create argocd application-controller token: %w", err)
	}
	return strings.TrimSpace(tokenReq.Status.Token), nil
}

func (h *ArgoCDHandler) lookupArgoCDClusterSecret(ctx context.Context, secretName, server string) (*corev1.Secret, error) {
	if h == nil || h.k8s == nil {
		return nil, nil
	}
	if secretName != "" {
		secret, err := h.k8s.CoreV1().Secrets(argocdNamespace).Get(ctx, secretName, metav1.GetOptions{})
		if err == nil {
			return secret, nil
		}
	}
	return findArgoCDClusterSecretByServer(ctx, h.k8s, server)
}

// astronomerManagedByLabelKey marks the Argo cluster Secret as having been
// stamped by Astronomer. ApplicationSet `clusters` generators can use this
// alone to target every managed cluster, regardless of per-cluster labels.
const astronomerManagedByLabelKey = argolabels.ManagedByLabelKey

// astronomerManagedByLabelValue is the constant value paired with
// astronomerManagedByLabelKey. Kept short so it survives the 63-char Kubernetes
// label-value cap with room to spare.
const astronomerManagedByLabelValue = argolabels.ManagedByLabelValue

const (
	astronomerClusterIDLabelKey              = argolabels.ClusterIDLabelKey
	astronomerClusterNameLabelKey            = argolabels.ClusterNameLabelKey
	astronomerEnvironmentLabelKey            = argolabels.EnvironmentLabelKey
	astronomerIsLocalLabelKey                = argolabels.IsLocalLabelKey
	astronomerRegionLabelKey                 = argolabels.RegionLabelKey
	astronomerProviderLabelKey               = argolabels.ProviderLabelKey
	astronomerDistributionLabelKey           = argolabels.DistributionLabelKey
	astronomerAgentProfileLabelKey           = argolabels.AgentProfileLabelKey
	astronomerAgentVersionLabelKey           = argolabels.AgentVersionLabelKey
	astronomerKubernetesVersionLabelKey      = argolabels.KubernetesVersionLabelKey
	astronomerProjectLabelKey                = argolabels.ProjectLabelKey
	astronomerProjectIDLabelKey              = argolabels.ProjectIDLabelKey
	astronomerProjectMembershipLabelPrefix   = argolabels.ProjectMembershipPrefix
	astronomerProjectIDMembershipLabelPrefix = argolabels.ProjectIDMembershipPrefix
)

// sanitizeLabelKey converts an arbitrary user-supplied label key into a form
// the Kubernetes label-key rules accept: lowercase alphanumerics, '.', '-',
// max 63 chars, must start/end with an alphanumeric. Everything else is
// replaced with '-' and runs collapsed. This is a one-way mapping — the
// reverse is not computed (operators reading the Argo Secret label see the
// sanitized form, not the original).
func sanitizeLabelKey(in string) string {
	return argolabels.SanitizeLabelKey(in)
}

func validateManagedClusterLabelOverrides(labels map[string]string) error {
	for k := range labels {
		if isReservedManagedClusterLabel(k) {
			return fmt.Errorf("label %q uses a reserved Astronomer or ArgoCD prefix", k)
		}
	}
	return nil
}

func mergeManagedClusterLabelOverrides(dst map[string]string, labels map[string]string) {
	for k, v := range labels {
		if isReservedManagedClusterLabel(k) {
			continue
		}
		dst[k] = v
	}
}

func isReservedManagedClusterLabel(key string) bool {
	key = strings.TrimSpace(key)
	return key == "astronomer.io" ||
		key == "argocd.argoproj.io" ||
		strings.HasPrefix(key, "astronomer.io/") ||
		strings.HasPrefix(key, "argocd.argoproj.io/")
}

// managedClusterLabels builds the Astronomer-owned label set stamped onto the
// upstream Argo cluster Secret. ApplicationSet `clusters` generators use a
// `selector.matchLabels` block over these to target by cluster-id / name /
// environment / arbitrary cluster-row labels.
//
// Label conventions:
//   - astronomer.io/managed-by: astronomer         (always)
//   - astronomer.io/cluster-id: <uuid>             (always)
//   - astronomer.io/cluster-name: <name>           (always)
//   - astronomer.io/environment: <env>             (when set)
//   - astronomer.io/region: <region>               (when set)
//   - astronomer.io/provider: <provider>           (when set)
//   - astronomer.io/distribution: <distribution>   (when set)
//   - astronomer.io/agent-privilege-profile: <profile> (always)
//   - astronomer.io/label-<sanitized-k>: <v>       (one per cluster.Labels entry)
//
// Sanitization rules are documented on sanitizeLabelKey. This is a one-way
// projection; the reverse is not computed.
func managedClusterLabels(cluster sqlc.Cluster) map[string]string {
	return argolabels.ManagedClusterLabels(cluster, nil)
}

func managedClusterLabelsForProjects(cluster sqlc.Cluster, projects []sqlc.Project) map[string]string {
	return argolabels.ManagedClusterLabels(cluster, projects)
}

func (h *ArgoCDHandler) managedClusterLabels(ctx context.Context, cluster sqlc.Cluster) (map[string]string, error) {
	projects, err := argolabels.ProjectsForCluster(ctx, h.queries, cluster.ID)
	if err != nil {
		return nil, err
	}
	return managedClusterLabelsForProjects(cluster, projects), nil
}

func argoCDClusterTokenExpiry(secret *corev1.Secret) (time.Time, bool, error) {
	if secret == nil || len(secret.Data["config"]) == 0 {
		return time.Time{}, false, nil
	}
	var cfg struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(secret.Data["config"], &cfg); err != nil {
		return time.Time{}, false, err
	}
	return jwtExpiry(cfg.BearerToken)
}

func jwtExpiry(token string) (time.Time, bool, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) < 2 {
		return time.Time{}, false, nil
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	payload, err := decodeBase64URL(parts[1])
	if err != nil {
		return time.Time{}, false, err
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, false, err
	}
	if claims.Exp == 0 {
		return time.Time{}, false, nil
	}
	return time.Unix(claims.Exp, 0).UTC(), true, nil
}

func decodeBase64URL(raw string) ([]byte, error) {
	if mod := len(raw) % 4; mod != 0 {
		raw += strings.Repeat("=", 4-mod)
	}
	return jwtBase64Encoding.DecodeString(raw)
}

func clusterSecretNameFromServer(ctx context.Context, client kubernetes.Interface, server string) string {
	secret, err := findArgoCDClusterSecretByServer(ctx, client, server)
	if err != nil || secret == nil {
		return ""
	}
	return secret.Name
}

func mergeAstronomerManagedLabels(existing, desired map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(desired))
	for k, v := range existing {
		if strings.HasPrefix(k, "astronomer.io/") {
			continue
		}
		out[k] = v
	}
	for k, v := range desired {
		out[k] = v
	}
	return out
}

func astronomerManagedLabelsPatch(existing, desired map[string]string) map[string]any {
	out := make(map[string]any, len(existing)+len(desired))
	for k := range existing {
		if !strings.HasPrefix(k, "astronomer.io/") {
			continue
		}
		if _, ok := desired[k]; !ok {
			out[k] = nil
		}
	}
	for k, v := range desired {
		out[k] = v
	}
	return out
}

func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func findArgoCDClusterSecretByServer(ctx context.Context, client kubernetes.Interface, server string) (*corev1.Secret, error) {
	if client == nil || server == "" {
		return nil, nil
	}
	secrets, err := client.CoreV1().Secrets(argocdNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: argocdClusterSecretTypeLabelKey + "=" + argocdClusterSecretTypeLabelValue,
	})
	if err != nil {
		return nil, err
	}
	for i := range secrets.Items {
		if strings.TrimSpace(string(secrets.Items[i].Data["server"])) == server {
			return &secrets.Items[i], nil
		}
	}
	return nil, nil
}

// =====================================================================
// Phase B1 — ArgoCD lifecycle endpoints.
//
// The following endpoints write through to upstream ArgoCD (Application,
// AppProject, ApplicationSet, Cluster, Repository) using the typed client
// in internal/handler/argocd. They share the existing instance lookup,
// authorization, and encryption plumbing.
// =====================================================================

// loadInstance is a small helper that resolves the {id} URL param to an
// ArgoCD instance, runs the workload-update authorization gate, and returns
// the instance. On any failure it has already written the response.
func (h *ArgoCDHandler) loadInstance(w http.ResponseWriter, r *http.Request, verb rbac.Verb) (sqlc.ArgocdInstance, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid instance ID")
		return sqlc.ArgocdInstance{}, false
	}
	instance, err := h.queries.GetArgoCDInstanceByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD instance not found")
		return sqlc.ArgocdInstance{}, false
	}
	if !h.authz.authorizeClusterAction(w, r, instance.ClusterID, rbac.ResourceWorkloads, verb) {
		return sqlc.ArgocdInstance{}, false
	}
	return instance, true
}

// translateClientError maps a typed argocd client error onto an HTTP status
// code and writes the response. Returns false if no error.
func translateClientError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusBadGateway
	switch {
	case argocdclient.IsKind(err, argocdclient.ErrUnauthorized):
		status = http.StatusUnauthorized
	case argocdclient.IsKind(err, argocdclient.ErrNotFound):
		status = http.StatusNotFound
	case argocdclient.IsKind(err, argocdclient.ErrConflict):
		status = http.StatusConflict
	case argocdclient.IsKind(err, argocdclient.ErrUnreachable):
		status = http.StatusBadGateway
	}
	RespondRequestError(w, r, status, apierror.ArgoCDError, err.Error())
	return true
}

// --- Application CRUD (B1) -------------------------------------------------

// CreateApplicationRequest is the JSON body shape accepted by
// POST /api/v1/argocd/instances/{id}/applications/.
type CreateApplicationRequest struct {
	Name string                       `json:"name"`
	Spec argocdclient.ApplicationSpec `json:"spec"`
}

// CreateApplication handles POST /api/v1/argocd/instances/{id}/applications/.
// Writes through to upstream ArgoCD. The local argocd_applications table is
// not pre-populated here — the existing list reconciler picks up new
// applications on its next round-trip.
func (h *ArgoCDHandler) CreateApplication(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbCreate)
	if !ok {
		return
	}
	var req CreateApplicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	client := h.argoCDClient(instance)
	app, err := client.CreateApplication(r.Context(), req.Name, req.Spec)
	if translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.app.create", "argocd_application", "", req.Name, map[string]any{
		"instance_id": instance.ID.String(),
		"project":     req.Spec.Project,
	})
	RespondJSON(w, http.StatusCreated, app)
}

// PatchApplication handles PATCH /api/v1/argocd/instances/{id}/applications/{name}/.
// The body is a JSON merge patch applied verbatim to the upstream
// Application's spec.
func (h *ArgoCDHandler) PatchApplication(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbUpdate)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "application name is required")
		return
	}
	raw, _ := io.ReadAll(r.Body)
	client := h.argoCDClient(instance)
	app, err := client.PatchApplication(r.Context(), name, raw)
	if translateClientError(w, r, err) {
		return
	}
	// Don't include the patch body verbatim — it can carry sensitive
	// per-application overrides (e.g. helm values). Length is enough.
	recordAudit(r, h.queries, "argocd.app.update", "argocd_application", "", name, map[string]any{
		"instance_id":     instance.ID.String(),
		"patch_byte_size": len(raw),
	})
	RespondJSON(w, http.StatusOK, app)
}

// DeleteApplication handles DELETE /api/v1/argocd/instances/{id}/applications/{name}/.
// Honors `?cascade=true` to also delete the deployed resources.
func (h *ArgoCDHandler) DeleteApplication(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbDelete)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "application name is required")
		return
	}
	cascade := strings.EqualFold(r.URL.Query().Get("cascade"), "true")
	client := h.argoCDClient(instance)
	if err := client.DeleteApplication(r.Context(), name, cascade); translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.app.delete", "argocd_application", "", name, map[string]any{
		"instance_id": instance.ID.String(),
		"cascade":     cascade,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- AppProject CRUD (B1) --------------------------------------------------

// CreateArgoProjectRequest is the body for POST /api/v1/argocd/instances/{id}/projects/.
type CreateArgoProjectRequest struct {
	Name string                      `json:"name"`
	Spec argocdclient.AppProjectSpec `json:"spec"`
}

// CreateProject handles POST /api/v1/argocd/instances/{id}/projects/.
func (h *ArgoCDHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbCreate)
	if !ok {
		return
	}
	var req CreateArgoProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	if err := validateArgoProjectSpec(req.Spec); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	client := h.argoCDClient(instance)
	out, err := client.CreateProject(r.Context(), req.Name, req.Spec)
	if translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.project.create", "argocd_project", "", req.Name, map[string]any{
		"instance_id": instance.ID.String(),
	})
	RespondJSON(w, http.StatusCreated, out)
}

// ListProjects handles GET /api/v1/argocd/instances/{id}/projects/.
// Reads live from upstream — projects are not cached locally.
func (h *ArgoCDHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	projects, err := h.fetchInstanceJSON(r.Context(), instance, "/api/v1/projects")
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ArgoCDError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, projects)
}

// PatchProject handles PATCH /api/v1/argocd/instances/{id}/projects/{name}/.
func (h *ArgoCDHandler) PatchProject(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbUpdate)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "project name is required")
		return
	}
	raw, _ := io.ReadAll(r.Body)
	if err := validateArgoProjectPatch(raw); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	client := h.argoCDClient(instance)
	out, err := client.PatchProject(r.Context(), name, raw)
	if translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.project.update", "argocd_project", "", name, map[string]any{
		"instance_id":     instance.ID.String(),
		"patch_byte_size": len(raw),
	})
	RespondJSON(w, http.StatusOK, out)
}

// DeleteProject handles DELETE /api/v1/argocd/instances/{id}/projects/{name}/.
func (h *ArgoCDHandler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbDelete)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "project name is required")
		return
	}
	client := h.argoCDClient(instance)
	if err := client.DeleteProject(r.Context(), name); translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.project.delete", "argocd_project", "", name, map[string]any{
		"instance_id": instance.ID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- ApplicationSet CRUD (B1) ----------------------------------------------

// CreateApplicationSetRequest is the body for the create endpoint.
type CreateApplicationSetRequest struct {
	Name string                          `json:"name"`
	Spec argocdclient.ApplicationSetSpec `json:"spec"`
}

// CreateApplicationSet handles POST /api/v1/argocd/instances/{id}/applicationsets/.
// When the spec uses the `cluster` generator with a label selector, ArgoCD
// will pick up the labels we stamp on the cluster Secrets via
// RegisterManagedCluster below.
func (h *ArgoCDHandler) CreateApplicationSet(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbCreate)
	if !ok {
		return
	}
	var req CreateApplicationSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	if len(req.Spec.Generators) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "at least one generator is required")
		return
	}
	if err := validateApplicationSetClusterGenerators(req.Spec); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	client := h.argoCDClient(instance)
	out, err := client.CreateApplicationSet(r.Context(), req.Name, req.Spec)
	if translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.appset.create", "argocd_applicationset", "", req.Name, map[string]any{
		"instance_id":     instance.ID.String(),
		"generator_count": len(req.Spec.Generators),
	})
	RespondJSON(w, http.StatusCreated, out)
}

func validateApplicationSetClusterGenerators(spec argocdclient.ApplicationSetSpec) error {
	for i, generator := range spec.Generators {
		if err := validateApplicationSetGeneratorClusterSelector(generator, fmt.Sprintf("generators[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

func validateApplicationSetGeneratorClusterSelector(generator argocdclient.ApplicationSetGenerator, path string) error {
	if generator.Cluster != nil && !selectorRequiresAstronomerManagedCluster(generator.Cluster.Selector) {
		return fmt.Errorf("%s cluster generator must include %s=%s", path, astronomerManagedByLabelKey, astronomerManagedByLabelValue)
	}
	if generator.Matrix != nil {
		for i, child := range generator.Matrix.Generators {
			if err := validateApplicationSetGeneratorClusterSelector(child, fmt.Sprintf("%s.matrix.generators[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func selectorRequiresAstronomerManagedCluster(selector *argocdclient.LabelSelector) bool {
	if selector == nil {
		return false
	}
	if selector.MatchLabels != nil {
		if got, ok := selector.MatchLabels[astronomerManagedByLabelKey]; ok {
			return got == astronomerManagedByLabelValue
		}
	}
	for _, expr := range selector.MatchExpressions {
		if expr.Key != astronomerManagedByLabelKey || expr.Operator != "In" {
			continue
		}
		for _, value := range expr.Values {
			if value == astronomerManagedByLabelValue {
				return true
			}
		}
	}
	return false
}

// ListApplicationSets handles GET /api/v1/argocd/instances/{id}/applicationsets/.
func (h *ArgoCDHandler) ListApplicationSets(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	out, err := h.fetchInstanceJSON(r.Context(), instance, "/api/v1/applicationsets")
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ArgoCDError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, out)
}

// DeleteApplicationSet handles DELETE /api/v1/argocd/instances/{id}/applicationsets/{name}/.
func (h *ArgoCDHandler) DeleteApplicationSet(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbDelete)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, "applicationset name is required")
		return
	}
	client := h.argoCDClient(instance)
	if err := client.DeleteApplicationSet(r.Context(), name); translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.appset.delete", "argocd_applicationset", "", name, map[string]any{
		"instance_id": instance.ID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- Cluster registration (B1) ---------------------------------------------

// RegisterClusterRequest is the body for POST .../clusters/{cluster_id}/register/.
// The handler builds the credentials block from our stored cluster row;
// callers only need to supply optional labels / overrides.
type RegisterClusterRequest struct {
	// Override the destination server URL. Defaults to cluster.api_server_url.
	// For agent-connected clusters this can be the Astronomer proxy URL —
	// ArgoCD will dial it when reconciling.
	ServerOverride string `json:"server,omitempty"`
	// Override the displayed cluster name; defaults to cluster.name.
	NameOverride string `json:"name,omitempty"`
	// Bearer token to embed in the registered cluster credentials. For
	// agent-connected clusters this is typically a ServiceAccount token
	// minted inside the destination cluster.
	BearerToken string `json:"bearer_token,omitempty"`
	// CAData is the PEM-encoded CA bundle for verifying the destination
	// API server. Defaults to cluster.ca_certificate.
	CAData string `json:"ca_data,omitempty"`
	// Insecure skips TLS verification (default false).
	Insecure bool `json:"insecure,omitempty"`
	// Labels stamped onto the upstream cluster Secret. The
	// ApplicationSet `cluster` generator's selector matches these. We
	// always add astronomer.io/cluster-id and astronomer.io/cluster-name.
	Labels map[string]string `json:"labels,omitempty"`
	// Project scopes the cluster to a single AppProject.
	Project string `json:"project,omitempty"`
	// Namespaces, when non-empty, restricts which namespaces ArgoCD will
	// manage on this cluster.
	Namespaces []string `json:"namespaces,omitempty"`
}

// RegisterManagedCluster handles
// POST /api/v1/argocd/instances/{id}/clusters/{cluster_id}/register/.
//
// It registers one of OUR managed clusters into the upstream ArgoCD by
// posting a Cluster object via the upstream HTTP API, then records the
// mapping in argocd_managed_clusters so we can list / unregister later.
//
// The bearer token is treated as a credential and re-encrypted via the
// existing Encryptor before being passed to upstream. (We do not persist it.)
func (h *ArgoCDHandler) RegisterManagedCluster(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbUpdate)
	if !ok {
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	var req RegisterClusterRequest
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		if len(strings.TrimSpace(string(raw))) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
				return
			}
		}
	}

	server := strings.TrimSpace(req.ServerOverride)
	if server == "" {
		server = h.defaultManagedClusterServer(cluster)
	}
	if server == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "cluster has no api_server_url; supply 'server' override")
		return
	}
	caData := req.CAData
	if caData == "" {
		caData = cluster.CaCertificate
	}
	bearerToken := strings.TrimSpace(req.BearerToken)
	if bearerToken == "" && cluster.IsLocal {
		bearerToken, err = h.createLocalArgoCDServiceAccountToken(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusBadGateway, apierror.TokenError, "Failed to mint local cluster token: "+err.Error())
			return
		}
	}
	if bearerToken == "" && !req.Insecure {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "bearer_token is required (or set insecure=true with caution)")
		return
	}
	if err := validateManagedClusterLabelOverrides(req.Labels); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}

	// Stamp our own labels so ApplicationSet selectors can target by our
	// cluster ID / name without the user needing to remember the upstream's
	// label scheme. User-supplied labels may add non-reserved labels, but may
	// not overwrite Astronomer-owned or ArgoCD-owned labels.
	labels, err := h.managedClusterLabels(r.Context(), cluster)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ProjectLookupError, "Failed to load project labels")
		return
	}
	mergeManagedClusterLabelOverrides(labels, req.Labels)

	reg := argocdclient.ClusterRegistration{
		Server: server,
		Name:   firstNonEmptyString(req.NameOverride, cluster.Name),
		Upsert: true,
		Config: argocdclient.ClusterConfig{
			BearerToken: bearerToken,
			TLSClientConfig: &argocdclient.TLSClientConfig{
				Insecure: req.Insecure,
				CAData:   []byte(caData),
			},
		},
		Labels:     labels,
		Namespaces: req.Namespaces,
		Project:    req.Project,
	}

	client := h.argoCDClient(instance)
	upstream, err := client.RegisterCluster(r.Context(), reg)
	if translateClientError(w, r, err) {
		return
	}

	secretName := clusterSecretNameFromServer(r.Context(), h.k8s, server)
	labelsJSON, _ := json.Marshal(labels)
	_, _ = h.queries.CreateArgoCDManagedCluster(r.Context(), sqlc.CreateArgoCDManagedClusterParams{
		ArgocdInstanceID:  instance.ID,
		ClusterID:         cluster.ID,
		ClusterSecretName: firstNonEmptyString(secretName, upstream.Name, cluster.Name),
		ServerUrl:         server,
		Labels:            labelsJSON,
	})

	recordAudit(r, h.queries, "argocd.cluster.register", "argocd_managed_cluster", cluster.ID.String(), cluster.Name, map[string]any{
		"instance_id": instance.ID.String(),
		"server":      server,
		"insecure":    req.Insecure,
		"project":     req.Project,
	})

	RespondJSON(w, http.StatusCreated, map[string]any{
		"cluster_id":         cluster.ID.String(),
		"argocd_instance_id": instance.ID.String(),
		"server":             server,
		"name":               upstream.Name,
		"labels":             labels,
		"upstream":           upstream,
	})
}

// ListManagedClusters handles GET /api/v1/argocd/instances/{id}/clusters/.
// Lists the clusters we've registered into this ArgoCD instance.
func (h *ArgoCDHandler) ListManagedClusters(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	rows, err := h.queries.ListArgoCDManagedClusters(r.Context(), instance.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list managed clusters")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var labels map[string]string
		_ = json.Unmarshal(row.Labels, &labels)
		out = append(out, map[string]any{
			"id":                  row.ID.String(),
			"argocd_instance_id":  row.ArgocdInstanceID.String(),
			"cluster_id":          row.ClusterID.String(),
			"server":              row.ServerUrl,
			"cluster_secret_name": row.ClusterSecretName,
			"labels":              labels,
			"created_at":          row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	RespondJSON(w, http.StatusOK, out)
}

// RefreshManagedClusterLabels handles
// POST /api/v1/argocd/instances/{id}/clusters/{cluster_id}/refresh-labels/.
// It re-stamps the Astronomer-owned labels on the upstream ArgoCD cluster
// Secret from the current cluster row, then mirrors the label JSON onto
// argocd_managed_clusters.
func (h *ArgoCDHandler) RefreshManagedClusterLabels(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbUpdate)
	if !ok {
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	row, err := h.queries.GetArgoCDManagedCluster(r.Context(), sqlc.GetArgoCDManagedClusterParams{
		ArgocdInstanceID: instance.ID,
		ClusterID:        clusterID,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster is not registered with this ArgoCD")
		return
	}
	secret, err := h.lookupArgoCDClusterSecret(r.Context(), row.ClusterSecretName, row.ServerUrl)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ArgoCDSecretError, "Failed to load ArgoCD cluster Secret: "+err.Error())
		return
	}
	if secret == nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "ArgoCD cluster Secret not found")
		return
	}

	labels, err := h.managedClusterLabels(r.Context(), cluster)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ProjectLookupError, "Failed to load project labels")
		return
	}
	merged := mergeAstronomerManagedLabels(secret.Labels, labels)
	if !stringMapEqual(secret.Labels, merged) {
		patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"labels": astronomerManagedLabelsPatch(secret.Labels, labels)}})
		if _, err := h.k8s.CoreV1().Secrets(argocdNamespace).Patch(r.Context(), secret.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			RespondRequestError(w, r, http.StatusBadGateway, apierror.ArgoCDSecretError, "Failed to patch ArgoCD cluster Secret: "+err.Error())
			return
		}
	}
	labelsJSON, _ := json.Marshal(labels)
	updated, err := h.queries.UpdateArgoCDManagedClusterLabels(r.Context(), sqlc.UpdateArgoCDManagedClusterLabelsParams{
		ArgocdInstanceID: instance.ID,
		ClusterID:        clusterID,
		Labels:           labelsJSON,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update managed cluster labels")
		return
	}
	recordAudit(r, h.queries, "argocd.cluster.refresh_labels", "argocd_managed_cluster", clusterID.String(), row.ClusterSecretName, map[string]any{
		"instance_id": instance.ID.String(),
		"server":      row.ServerUrl,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"id":                  updated.ID.String(),
		"argocd_instance_id":  updated.ArgocdInstanceID.String(),
		"cluster_id":          updated.ClusterID.String(),
		"server":              updated.ServerUrl,
		"cluster_secret_name": updated.ClusterSecretName,
		"labels":              labels,
		"created_at":          updated.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// UnregisterManagedCluster handles
// DELETE /api/v1/argocd/instances/{id}/clusters/{cluster_id}/register/.
// Calls upstream UnregisterCluster *and* removes the local mapping. If the
// upstream call fails with NotFound we still drop the local row so the index
// reflects truth.
func (h *ArgoCDHandler) UnregisterManagedCluster(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbUpdate)
	if !ok {
		return
	}
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	row, err := h.queries.GetArgoCDManagedCluster(r.Context(), sqlc.GetArgoCDManagedClusterParams{
		ArgocdInstanceID: instance.ID,
		ClusterID:        clusterID,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster is not registered with this ArgoCD")
		return
	}
	client := h.argoCDClient(instance)
	if err := client.UnregisterCluster(r.Context(), row.ServerUrl); err != nil {
		if !argocdclient.IsKind(err, argocdclient.ErrNotFound) {
			translateClientError(w, r, err)
			return
		}
	}
	_ = h.queries.DeleteArgoCDManagedCluster(r.Context(), sqlc.DeleteArgoCDManagedClusterParams{
		ArgocdInstanceID: instance.ID,
		ClusterID:        clusterID,
	})
	recordAudit(r, h.queries, "argocd.cluster.unregister", "argocd_managed_cluster", clusterID.String(), row.ClusterSecretName, map[string]any{
		"instance_id": instance.ID.String(),
		"server":      row.ServerUrl,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- Repository credentials (B1) -------------------------------------------

// RepoCreateRequest is the JSON body shape accepted on the repo create
// endpoint. Plaintext secrets are encrypted via the configured Encryptor
// before being forwarded to upstream — but ArgoCD itself stores credentials
// in its own Secret, so the encryption is purely defense-in-depth for our
// own logs/audit trail.
type RepoCreateRequest struct {
	Repo          string `json:"repo"`
	Type          string `json:"type,omitempty"`
	Name          string `json:"name,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	Insecure      bool   `json:"insecure,omitempty"`
	EnableLFS     bool   `json:"enable_lfs,omitempty"`
	Project       string `json:"project,omitempty"`
}

func (req RepoCreateRequest) toClient() argocdclient.RepositoryCreate {
	return argocdclient.RepositoryCreate{
		Repo:          req.Repo,
		Name:          req.Name,
		Type:          req.Type,
		Username:      req.Username,
		Password:      req.Password,
		SSHPrivateKey: req.SSHPrivateKey,
		Insecure:      req.Insecure,
		EnableLFS:     req.EnableLFS,
		Project:       req.Project,
	}
}

// CreateRepo handles POST /api/v1/argocd/instances/{id}/repos/.
func (h *ArgoCDHandler) CreateRepo(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbCreate)
	if !ok {
		return
	}
	var req RepoCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "repo URL is required")
		return
	}
	// Defense-in-depth: round-trip the secret through the Encryptor so
	// it lands in our request audit (when we add one) ciphered. We still
	// pass plaintext to ArgoCD because that's what the upstream API expects.
	if h.encryptor != nil {
		if req.Password != "" {
			if _, err := h.encryptor.Encrypt(req.Password); err != nil && h.log != nil {
				h.log.Warn("failed to encrypt repo password for audit", "error", err)
			}
		}
		if req.SSHPrivateKey != "" {
			if _, err := h.encryptor.Encrypt(req.SSHPrivateKey); err != nil && h.log != nil {
				h.log.Warn("failed to encrypt repo ssh key for audit", "error", err)
			}
		}
	}
	client := h.argoCDClient(instance)
	out, err := client.CreateRepository(r.Context(), req.toClient())
	if translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.repo.create", "argocd_repository", "", req.Repo, map[string]any{
		"instance_id":     instance.ID.String(),
		"type":            req.Type,
		"username":        req.Username,
		"insecure":        req.Insecure,
		"project":         req.Project,
		"has_password":    req.Password != "",
		"has_private_key": req.SSHPrivateKey != "",
	})
	RespondJSON(w, http.StatusCreated, out)
}

// ListRepos handles GET /api/v1/argocd/instances/{id}/repos/. Pulls live
// from upstream — repos are not cached locally.
func (h *ArgoCDHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	client := h.argoCDClient(instance)
	repos, err := client.ListRepositories(r.Context())
	if translateClientError(w, r, err) {
		return
	}
	RespondJSON(w, http.StatusOK, repos)
}

// DeleteRepo handles DELETE /api/v1/argocd/instances/{id}/repos/.
// The repo URL is supplied via `?repo=<url>` query string because the URL
// itself often contains slashes / special chars that don't compose cleanly
// into a path parameter.
func (h *ArgoCDHandler) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbDelete)
	if !ok {
		return
	}
	repoURL := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repoURL == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "repo query parameter is required")
		return
	}
	client := h.argoCDClient(instance)
	if err := client.DeleteRepository(r.Context(), repoURL); translateClientError(w, r, err) {
		return
	}
	recordAudit(r, h.queries, "argocd.repo.delete", "argocd_repository", "", repoURL, map[string]any{
		"instance_id": instance.ID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// TestRepo handles POST /api/v1/argocd/instances/{id}/repos/test/. The body
// matches the create body — useful for "validate before save" UX.
func (h *ArgoCDHandler) TestRepo(w http.ResponseWriter, r *http.Request) {
	instance, ok := h.loadInstance(w, r, rbac.VerbRead)
	if !ok {
		return
	}
	var req RepoCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "repo URL is required")
		return
	}
	client := h.argoCDClient(instance)
	out, err := client.TestRepository(r.Context(), req.toClient())
	if translateClientError(w, r, err) {
		return
	}
	RespondJSON(w, http.StatusOK, out)
}

// isForeignKeyViolation reports whether err is a PostgreSQL FK violation
// (SQLSTATE 23503). Used by handlers to translate "you referenced a parent
// row that doesn't exist" into a 400 Bad Request rather than a generic 500.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}
