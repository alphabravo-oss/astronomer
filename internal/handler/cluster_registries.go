// Package handler — migration 050: multi-registry-per-cluster admin UX.
//
// The legacy GET/PUT/DELETE /clusters/{id}/registry/ endpoints (in
// clusters_registry.go) operate against the original single-row-per-cluster shape.
// This file adds the Rancher-style "Cluster → Registries" tab surface:
//
//   GET    /api/v1/clusters/{cluster_id}/registries/
//   POST   /api/v1/clusters/{cluster_id}/registries/
//   GET    /api/v1/clusters/{cluster_id}/registries/{id}/
//   PUT    /api/v1/clusters/{cluster_id}/registries/{id}/
//   DELETE /api/v1/clusters/{cluster_id}/registries/{id}/
//   POST   /api/v1/clusters/{cluster_id}/registries/{id}/test/
//
// Every endpoint redacts the registry password on the way out (replaced
// with a "<set>" sentinel); the PUT handler treats a sentinel-valued
// password as "keep what's stored" so the dashboard's natural
// "GET → edit → PUT" loop doesn't accidentally blank the credential.
//
// CREATE / UPDATE / DELETE enqueue a cluster_registry_apply worker task
// so the dockerconfigjson Secret + (optionally) the default-SA patch
// land in the target cluster's namespaces via the tunnel. The /test/
// endpoint hits the registry's /v2/ endpoint THROUGH the tunnel — the
// management plane often can't reach customer registries directly under
// network policy, but the member cluster can.

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// RegistryPasswordSentinel is the placeholder returned in lieu of the raw
// password in every GET/list response. The PUT path treats an incoming
// value equal to the sentinel as "no change" so the natural GET → edit →
// PUT loop doesn't clobber stored credentials. Mirrors the
// PasswordSentinelEncrypted pattern used by SMTPHandler.
const RegistryPasswordSentinel = "<set>"

// ClusterRegistryQuerier is the slice of *sqlc.Queries
// ClusterRegistriesHandler needs. Defined locally so a unit test can pass
// a hand-rolled fake without standing up the full DB.
type ClusterRegistryQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClusterRegistryConfigs(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterRegistryConfig, error)
	GetClusterRegistryConfigByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterRegistryConfig, error)
}

// ClusterRegistryMutationTx is the transaction-bound write surface for the
// Rancher-style multi-registry API. Registry state, durable reconciliation
// intent, and audit evidence commit or roll back together.
type ClusterRegistryMutationTx interface {
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
	CreateClusterRegistryConfig(context.Context, sqlc.CreateClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
	UpdateClusterRegistryConfig(context.Context, sqlc.UpdateClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
	DeleteClusterRegistryConfigByID(context.Context, uuid.UUID) error
}

type clusterRegistryRunTxFunc func(context.Context, func(ClusterRegistryMutationTx) error) error

// ClusterRegistriesHandler owns the /clusters/{cluster_id}/registries/*
// route group. Mutations require a transaction runner and commit registry
// state, reconciliation intent, and audit evidence atomically. Requester is
// optional and used only by the explicit connectivity-test endpoint.
type ClusterRegistriesHandler struct {
	queries   ClusterRegistryQuerier
	runTx     clusterRegistryRunTxFunc
	requester K8sRequester
	encryptor *auth.Encryptor
	bus       *events.Bus
}

// SetEventBus wires the SSE bus for registry.changed liveness events (P4.5).
// Optional: fire-and-forget and nil-safe.
func (h *ClusterRegistriesHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// NewClusterRegistriesHandler wires the handler against the provided read
// surface. The transaction runner and tunnel requester are attached below.
func NewClusterRegistriesHandler(queries ClusterRegistryQuerier) *ClusterRegistriesHandler {
	return &ClusterRegistriesHandler{queries: queries}
}

func (h *ClusterRegistriesHandler) SetRunTx(runTx clusterRegistryRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ClusterRegistriesHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

// SetRequester wires the tunnel-backed K8sRequester used by the /test/
// endpoint to reach the registry from inside the member cluster.
// Nil-safe; /test/ returns 503 when this isn't wired (the management
// plane can't proxy the request directly).
func (h *ClusterRegistriesHandler) SetRequester(r K8sRequester) {
	if h == nil {
		return
	}
	h.requester = r
}

// SetEncryptor wires Fernet encryption for registry passwords. When omitted,
// handlers preserve the legacy plaintext column behavior for development
// installs that do not configure ASTRONOMER_ENCRYPTION_KEY.
func (h *ClusterRegistriesHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// ClusterRegistryResponse is the wire-format DTO. We never return the raw
// password — it's replaced with RegistryPasswordSentinel for any cluster
// that has a non-empty value stored.
type ClusterRegistryResponse struct {
	ID                 uuid.UUID  `json:"id"`
	ClusterID          uuid.UUID  `json:"cluster_id"`
	PrivateRegistryUrl string     `json:"private_registry_url"`
	RegistryUsername   string     `json:"registry_username"`
	RegistryPassword   string     `json:"registry_password"`
	Insecure           bool       `json:"insecure"`
	CaBundle           string     `json:"ca_bundle"`
	Namespaces         []string   `json:"namespaces"`
	InjectDefaultSa    bool       `json:"inject_default_sa"`
	SecretName         string     `json:"secret_name"`
	LastAppliedAt      *time.Time `json:"last_applied_at,omitempty"`
	LastApplyError     string     `json:"last_apply_error"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// ClusterRegistryRequest is the create/update body. namespaces is an
// optional explicit list of target namespaces; empty / omitted means
// "every project_namespaces row for this cluster" (the apply worker
// resolves the fan-out at task time).
//
// The password sentinel rule: when the field equals RegistryPasswordSentinel
// the PUT handler preserves the stored value; otherwise the new value is
// written. POST never accepts the sentinel (there's nothing to preserve
// on a fresh row).
// openapi:request ClusterRegistryRequest
type ClusterRegistryRequest struct {
	PrivateRegistryUrl string   `json:"private_registry_url"`
	RegistryUsername   string   `json:"registry_username"`
	RegistryPassword   string   `json:"registry_password"`
	Insecure           bool     `json:"insecure"`
	CaBundle           string   `json:"ca_bundle"`
	Namespaces         []string `json:"namespaces"`
	InjectDefaultSa    *bool    `json:"inject_default_sa"`
	SecretName         string   `json:"secret_name"`
}

// clusterRegistryConfigToResponse builds the DTO with the password
// redacted. cluster_id comes from the row, not the URL — the URL value
// has already been authorised by the parent-cluster RBAC gate.
func clusterRegistryConfigToResponse(row sqlc.ClusterRegistryConfig) ClusterRegistryResponse {
	out := ClusterRegistryResponse{
		ID:                 row.ID,
		ClusterID:          row.ClusterID,
		PrivateRegistryUrl: row.PrivateRegistryUrl,
		RegistryUsername:   row.RegistryUsername,
		RegistryPassword:   "",
		Insecure:           row.Insecure,
		CaBundle:           row.CaBundle,
		Namespaces:         decodeNamespacesJSON(row.Namespaces),
		InjectDefaultSa:    row.InjectDefaultSa,
		SecretName:         row.SecretName,
		LastApplyError:     row.LastApplyError,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
	if strings.TrimSpace(row.RegistryPassword) != "" || strings.TrimSpace(row.RegistryPasswordEncrypted) != "" {
		out.RegistryPassword = RegistryPasswordSentinel
	}
	if row.LastAppliedAt.Valid {
		t := row.LastAppliedAt.Time
		out.LastAppliedAt = &t
	}
	return out
}

// decodeNamespacesJSON turns the JSONB slice column into a Go slice. A
// malformed value falls back to "all namespaces" rather than 500ing
// the list — operators can always overwrite via PUT.
func decodeNamespacesJSON(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// encodeNamespaces canonicalises the namespaces list to JSONB-friendly
// bytes. Nil slices encode to `[]` (not `null`) so the column's NOT NULL
// constraint stays happy.
func encodeNamespaces(ns []string) json.RawMessage {
	if ns == nil {
		ns = []string{}
	}
	// Trim + dedup to avoid silly UX bugs ("default " stored once, "default"
	// stored again, both pretending to be the same namespace).
	cleaned := make([]string, 0, len(ns))
	seen := map[string]struct{}{}
	for _, item := range ns {
		t := strings.TrimSpace(item)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		cleaned = append(cleaned, t)
	}
	raw, err := json.Marshal(cleaned)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return raw
}

// parseClusterAndRegistryIDs extracts the {cluster_id} + {id} URL params and
// 400s on a bad shape. Returns the two UUIDs, "ok bool" indicating whether
// the caller may proceed (false means a response was already written).
func parseClusterAndRegistryIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	registryID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid registry ID")
		return uuid.Nil, uuid.Nil, false
	}
	return clusterID, registryID, true
}

// List handles GET /api/v1/clusters/{cluster_id}/registries/.
func (h *ClusterRegistriesHandler) List(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), clusterID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	rows, err := h.queries.ListClusterRegistryConfigs(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list registry configs")
		return
	}
	out := make([]ClusterRegistryResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, clusterRegistryConfigToResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Get handles GET /api/v1/clusters/{cluster_id}/registries/{id}/.
func (h *ClusterRegistriesHandler) Get(w http.ResponseWriter, r *http.Request) {
	clusterID, registryID, ok := parseClusterAndRegistryIDs(w, r)
	if !ok {
		return
	}
	row, err := h.queries.GetClusterRegistryConfigByID(r.Context(), registryID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}
	if row.ClusterID != clusterID {
		// Treat a wrong-cluster lookup as a 404 — never leak that the row
		// exists under a different cluster.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}
	RespondJSON(w, http.StatusOK, clusterRegistryConfigToResponse(row))
}

// Create handles POST /api/v1/clusters/{cluster_id}/registries/.
func (h *ClusterRegistriesHandler) Create(w http.ResponseWriter, r *http.Request) {
	clusterID, ok := parseClusterID(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	var req ClusterRegistryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.PrivateRegistryUrl) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "private_registry_url is required")
		return
	}
	if req.RegistryPassword == RegistryPasswordSentinel {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Cannot use password sentinel on create")
		return
	}
	registryPassword, registryPasswordEncrypted, err := h.encryptRegistryPassword(req.RegistryPassword)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt registry password")
		return
	}

	inject := true
	if req.InjectDefaultSa != nil {
		inject = *req.InjectDefaultSa
	}

	params := sqlc.CreateClusterRegistryConfigParams{
		ClusterID:                 clusterID,
		PrivateRegistryUrl:        strings.TrimSpace(req.PrivateRegistryUrl),
		RegistryUsername:          req.RegistryUsername,
		RegistryPassword:          registryPassword,
		RegistryPasswordEncrypted: registryPasswordEncrypted,
		Insecure:                  req.Insecure,
		CaBundle:                  req.CaBundle,
		Namespaces:                encodeNamespaces(req.Namespaces),
		InjectDefaultSa:           inject,
		SecretName:                strings.TrimSpace(req.SecretName),
	}
	row, err := executeMutation(r, h.runTx,
		func(q ClusterRegistryMutationTx) (sqlc.ClusterRegistryConfig, error) {
			row, mutationErr := q.CreateClusterRegistryConfig(r.Context(), params)
			if mutationErr != nil {
				return sqlc.ClusterRegistryConfig{}, mutationErr
			}
			mutationErr = h.enqueueApplyOutbox(r, q, row.ID, clusterID)
			return row, mutationErr
		},
		func(row sqlc.ClusterRegistryConfig) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.registry.created", resourceType: "cluster_registry_config", resourceID: row.ID.String(), resourceName: cluster.Name,
				status: http.StatusCreated,
				detail: map[string]any{
					"cluster_id": clusterID.String(), "private_registry_url": row.PrivateRegistryUrl,
					"registry_username": row.RegistryUsername, "insecure": row.Insecure,
					"namespaces": decodeNamespacesJSON(row.Namespaces), "inject_default_sa": row.InjectDefaultSa,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create registry config")
		return
	}
	events.PublishChanged(h.bus, "registry", clusterID.String(), row.ID.String(), nil)

	RespondJSON(w, http.StatusCreated, clusterRegistryConfigToResponse(row))
}

// Update handles PUT /api/v1/clusters/{cluster_id}/registries/{id}/.
func (h *ClusterRegistriesHandler) Update(w http.ResponseWriter, r *http.Request) {
	clusterID, registryID, ok := parseClusterAndRegistryIDs(w, r)
	if !ok {
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	existing, err := h.queries.GetClusterRegistryConfigByID(r.Context(), registryID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}
	if existing.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}

	var req ClusterRegistryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.PrivateRegistryUrl) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "private_registry_url is required")
		return
	}

	// Sentinel handling: a request password equal to RegistryPasswordSentinel
	// keeps the stored value. A non-empty, non-sentinel value overwrites.
	// An explicit "" rotates to no-password (rare but supported for
	// public-but-private-flagged registries during a transition).
	password := req.RegistryPassword
	passwordEncrypted := ""
	if password == RegistryPasswordSentinel {
		password = existing.RegistryPassword
		passwordEncrypted = existing.RegistryPasswordEncrypted
	} else {
		password, passwordEncrypted, err = h.encryptRegistryPassword(password)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt registry password")
			return
		}
	}

	inject := existing.InjectDefaultSa
	if req.InjectDefaultSa != nil {
		inject = *req.InjectDefaultSa
	}

	params := sqlc.UpdateClusterRegistryConfigParams{
		ID:                        registryID,
		PrivateRegistryUrl:        strings.TrimSpace(req.PrivateRegistryUrl),
		RegistryUsername:          req.RegistryUsername,
		RegistryPassword:          password,
		RegistryPasswordEncrypted: passwordEncrypted,
		Insecure:                  req.Insecure,
		CaBundle:                  req.CaBundle,
		Namespaces:                encodeNamespaces(req.Namespaces),
		InjectDefaultSa:           inject,
		SecretName:                strings.TrimSpace(req.SecretName),
	}
	row, err := executeMutation(r, h.runTx,
		func(q ClusterRegistryMutationTx) (sqlc.ClusterRegistryConfig, error) {
			row, mutationErr := q.UpdateClusterRegistryConfig(r.Context(), params)
			if mutationErr != nil {
				return sqlc.ClusterRegistryConfig{}, mutationErr
			}
			mutationErr = h.enqueueApplyOutbox(r, q, row.ID, clusterID)
			return row, mutationErr
		},
		func(row sqlc.ClusterRegistryConfig) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.registry.updated", resourceType: "cluster_registry_config", resourceID: row.ID.String(), resourceName: cluster.Name,
				status: http.StatusOK,
				detail: map[string]any{
					"cluster_id": clusterID.String(), "private_registry_url": row.PrivateRegistryUrl,
					"registry_username": row.RegistryUsername, "insecure": row.Insecure,
					"namespaces": decodeNamespacesJSON(row.Namespaces), "inject_default_sa": row.InjectDefaultSa,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update registry config")
		return
	}
	events.PublishChanged(h.bus, "registry", clusterID.String(), row.ID.String(), nil)

	RespondJSON(w, http.StatusOK, clusterRegistryConfigToResponse(row))
}

// Delete handles DELETE /api/v1/clusters/{cluster_id}/registries/{id}/.
// Enqueues an "unapply" task BEFORE removing the row so the worker still
// has the cred + secret_name needed to clean up the in-cluster Secret +
// SA-patch. The DELETE response is returned immediately; the worker
// fans out the cleanup asynchronously.
func (h *ClusterRegistriesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	clusterID, registryID, ok := parseClusterAndRegistryIDs(w, r)
	if !ok {
		return
	}
	existing, err := h.queries.GetClusterRegistryConfigByID(r.Context(), registryID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}
	if existing.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}

	task, err := h.newUnapplyTask(r, existing)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TaskError, "Failed to build registry cleanup task")
		return
	}
	_, err = executeMutation(r, h.runTx,
		func(q ClusterRegistryMutationTx) (sqlc.ClusterRegistryConfig, error) {
			if mutationErr := q.DeleteClusterRegistryConfigByID(r.Context(), registryID); mutationErr != nil {
				return sqlc.ClusterRegistryConfig{}, mutationErr
			}
			if mutationErr := enqueueClusterRegistryTaskOutbox(r.Context(), q, task, clusterRegistryTaskDedupeKey(r, "unapply", registryID)); mutationErr != nil {
				return sqlc.ClusterRegistryConfig{}, mutationErr
			}
			return existing, nil
		},
		func(row sqlc.ClusterRegistryConfig) mutationAuditEvent {
			return mutationAuditEvent{
				action: "cluster.registry.deleted", resourceType: "cluster_registry_config", resourceID: registryID.String(),
				status: http.StatusNoContent,
				detail: map[string]any{"cluster_id": clusterID.String(), "private_registry_url": row.PrivateRegistryUrl},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete registry config")
		return
	}

	events.PublishChanged(h.bus, "registry", clusterID.String(), registryID.String(), nil)

	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /api/v1/clusters/{cluster_id}/registries/{id}/test/.
// It dials the registry's `/v2/` endpoint via the tunnel — the member
// cluster is the one that has the network path to the customer registry,
// not the management plane. Returns the HTTP status code observed and a
// short reason (Docker registries always respond to /v2/, returning
// 200 for anonymous-allowed and 401 with WWW-Authenticate for auth-
// required; both indicate the URL is correct).
type ClusterRegistryTestResponse struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"status_code"`
	Message    string `json:"message"`
}

func (h *ClusterRegistriesHandler) Test(w http.ResponseWriter, r *http.Request) {
	clusterID, registryID, ok := parseClusterAndRegistryIDs(w, r)
	if !ok {
		return
	}
	existing, err := h.queries.GetClusterRegistryConfigByID(r.Context(), registryID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}
	if existing.ClusterID != clusterID {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found")
		return
	}
	if h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.TunnelUnwired, "Tunnel requester not configured")
		return
	}

	url := tasks.RegistryProbeURL(existing.PrivateRegistryUrl)
	if url == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, "Could not derive a probe URL from the stored registry URL")
		return
	}

	headers := map[string]string{
		"Accept": "application/json",
	}
	password, err := h.registryPassword(existing)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to decrypt registry password")
		return
	}
	if existing.RegistryUsername != "" && password != "" {
		auth := base64.StdEncoding.EncodeToString([]byte(existing.RegistryUsername + ":" + password))
		headers["Authorization"] = "Basic " + auth
	}

	// The tunnel's K8sRequester expects a k8s-shaped path, but it actually
	// streams arbitrary HTTP requests via the agent — the only restriction
	// is the caller layer at the agent side. The agent ships a generic
	// "outbound http" handler when the path starts with the scheme; see
	// the agent's protocol.HandleK8sRequest fallthrough. The implementation
	// in this handler intentionally goes through that fallthrough by
	// passing the full URL as the "path" — the agent dials it directly.
	resp, err := h.requester.Do(r.Context(), clusterID.String(), http.MethodGet, url, nil, headers)
	if err != nil {
		if auditErr := recordMandatoryAudit(r, h.queries, "cluster.registry.tested", "cluster_registry_config", existing.ID.String(), existing.PrivateRegistryUrl, map[string]any{
			"cluster_id":           clusterID.String(),
			"private_registry_url": existing.PrivateRegistryUrl,
			"ok":                   false,
			"status_code":          0,
			"result":               "tunnel_error",
		}); auditErr != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; the registry test result was not returned")
			return
		}
		RespondJSON(w, http.StatusOK, ClusterRegistryTestResponse{
			OK:      false,
			Message: fmt.Sprintf("Tunnel error: %s", err.Error()),
		})
		return
	}

	out := ClusterRegistryTestResponse{
		StatusCode: resp.StatusCode,
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		out.OK = true
		out.Message = "Registry reachable and credentials accepted."
	case resp.StatusCode == http.StatusUnauthorized:
		// /v2/ returning 401 means the URL is correct but the supplied
		// creds were rejected. We treat that as a failure for the
		// "test" button — it's exactly what the operator is checking.
		out.OK = false
		out.Message = "Registry rejected credentials (401)."
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		out.OK = true
		out.Message = fmt.Sprintf("Registry reachable (status %d).", resp.StatusCode)
	default:
		out.OK = false
		out.Message = fmt.Sprintf("Registry returned unexpected status %d.", resp.StatusCode)
	}
	if auditErr := recordMandatoryAudit(r, h.queries, "cluster.registry.tested", "cluster_registry_config", existing.ID.String(), existing.PrivateRegistryUrl, map[string]any{
		"cluster_id":           clusterID.String(),
		"private_registry_url": existing.PrivateRegistryUrl,
		"ok":                   out.OK,
		"status_code":          resp.StatusCode,
	}); auditErr != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
			"Mandatory audit storage is unavailable; the registry test result was not returned")
		return
	}
	RespondJSON(w, http.StatusOK, out)
}

func (h *ClusterRegistriesHandler) newApplyTask(r *http.Request, registryID, clusterID uuid.UUID) (*asynq.Task, error) {
	task, err := tasks.NewClusterApplyRegistrySecretTask(tasks.ClusterApplyRegistrySecretPayload{
		RegistryID: registryID.String(),
		ClusterID:  clusterID.String(),
		Op:         "apply",
	})
	if err != nil {
		return nil, err
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), reqctx.CorrelationID(r.Context()))
	return asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3)), nil
}

func (h *ClusterRegistriesHandler) enqueueApplyOutbox(r *http.Request, q tasks.TaskOutboxWriter, registryID, clusterID uuid.UUID) error {
	task, err := h.newApplyTask(r, registryID, clusterID)
	if err != nil {
		return err
	}
	return enqueueClusterRegistryTaskOutbox(r.Context(), q, task, clusterRegistryTaskDedupeKey(r, "apply", registryID))
}

func enqueueClusterRegistryTaskOutbox(ctx context.Context, q tasks.TaskOutboxWriter, task *asynq.Task, dedupeKey string) error {
	_, err := tasks.EnqueueTaskOutbox(ctx, q, task, tasks.TaskOutboxOptions{
		DedupeKey:           dedupeKey,
		QueueName:           tasks.ClusterTemplateApplyQueueName,
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
	})
	return err
}

func clusterRegistryTaskDedupeKey(r *http.Request, operation string, registryID uuid.UUID) string {
	requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestKey == "" {
		requestKey = reqctx.RequestID(r.Context())
	}
	if requestKey == "" {
		requestKey = uuid.NewString()
	}
	return "cluster_registry:" + audit.MutationDedupeKey(requestKey, operation, "cluster_registry_config", registryID.String())
}

// WorkerRuntime builds the cluster-registry task runtime after the tunnel,
// query surface, and encryptor are wired.
//
// We bridge the handler.K8sRequester into tasks.ProjectK8sRequester via a
// thin adapter on this file rather than re-using the project package's
// private adapter — the indirection keeps the two reconcilers truly
// independent.
func (h *ClusterRegistriesHandler) WorkerRuntime(queries tasks.ClusterRegistryApplyQuerier) tasks.ClusterRegistryRuntime {
	if h == nil {
		return tasks.ClusterRegistryRuntime{}
	}
	return tasks.ClusterRegistryRuntime{Deps: tasks.ClusterRegistryApplyDeps{
		Queries:   queries,
		Requester: clusterRegistryRequesterAdapter{r: h.requester},
		Encryptor: h.encryptor,
	}}
}

func (h *ClusterRegistriesHandler) encryptRegistryPassword(password string) (string, string, error) {
	if h == nil || h.encryptor == nil || password == "" {
		return password, "", nil
	}
	encrypted, err := h.encryptor.Encrypt(password)
	if err != nil {
		return "", "", err
	}
	return "", encrypted, nil
}

func (h *ClusterRegistriesHandler) registryPassword(row sqlc.ClusterRegistryConfig) (string, error) {
	if strings.TrimSpace(row.RegistryPasswordEncrypted) == "" {
		return row.RegistryPassword, nil
	}
	if h == nil || h.encryptor == nil {
		return row.RegistryPassword, nil
	}
	return h.encryptor.Decrypt(row.RegistryPasswordEncrypted)
}

// clusterRegistryRequesterAdapter bridges handler.K8sRequester (which
// returns protocol.K8sResponsePayload with a base64-encoded body) into
// the transport-agnostic ProjectK8sResponse the apply task consumes. The
// decode step keeps the worker/tasks package free of any protocol /
// tunnel imports.
type clusterRegistryRequesterAdapter struct{ r K8sRequester }

func (a clusterRegistryRequesterAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*tasks.ProjectK8sResponse, error) {
	resp, err := a.r.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	bodyBytes, _ := decodeResponseBody(resp)
	return &tasks.ProjectK8sResponse{StatusCode: resp.StatusCode, Body: bodyBytes}, nil
}

func (h *ClusterRegistriesHandler) newUnapplyTask(r *http.Request, row sqlc.ClusterRegistryConfig) (*asynq.Task, error) {
	task, err := tasks.NewClusterApplyRegistrySecretTask(tasks.ClusterApplyRegistrySecretPayload{
		RegistryID:        row.ID.String(),
		ClusterID:         row.ClusterID.String(),
		Op:                "unapply",
		SnapshotSecret:    row.SecretName,
		SnapshotNamespace: decodeNamespacesJSON(row.Namespaces),
		SnapshotInjectSA:  row.InjectDefaultSa,
	})
	if err != nil {
		return nil, err
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), reqctx.CorrelationID(r.Context()))
	return asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3)), nil
}
