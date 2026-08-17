package delivery

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const maxCredentialValueBytes = 256 << 10

// CredentialEncryptor is deliberately write-only. The HTTP surface can seal
// source material, but it cannot decrypt or inspect existing credentials.
type CredentialEncryptor interface {
	Encrypt(plaintext string) (string, error)
}

// SourceQueries is the complete persistence surface used by SourceHandler.
// Public projections returned by these methods omit ciphertext and CA data.
type SourceQueries interface {
	CountDeliverySources(context.Context, sqlc.CountDeliverySourcesParams) (int64, error)
	ListDeliverySources(context.Context, sqlc.ListDeliverySourcesParams) ([]sqlc.ListDeliverySourcesRow, error)
	CreateDeliverySource(context.Context, sqlc.CreateDeliverySourceParams) (sqlc.CreateDeliverySourceRow, error)
	GetDeliverySource(context.Context, sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error)
	DeleteDeliverySource(context.Context, sqlc.DeleteDeliverySourceParams) (int64, error)
	UpdateDeliverySource(context.Context, sqlc.UpdateDeliverySourceParams) (sqlc.UpdateDeliverySourceRow, error)
	RotateDeliverySourceCredential(context.Context, sqlc.RotateDeliverySourceCredentialParams) (sqlc.RotateDeliverySourceCredentialRow, error)
	CreateDeliverySourceResolutionAndOutbox(context.Context, sqlc.CreateDeliverySourceResolutionAndOutboxParams) (sqlc.CreateDeliverySourceResolutionAndOutboxRow, error)
}

// SourceHandler serves the project-scoped delivery source API. Route wiring is
// intentionally external; methods expect a chi path parameter named "id" or
// "sourceID" where a source identifier is needed.
type SourceHandler struct {
	queries              SourceQueries
	encryptor            CredentialEncryptor
	credentialKeyVersion int32
}

// NewSourceHandler creates a source handler. credentialKeyVersion identifies
// the primary Fernet key generation written beside new ciphertext. It must be
// positive before a request containing secret material can succeed.
func NewSourceHandler(queries SourceQueries, encryptor CredentialEncryptor, credentialKeyVersion int32) *SourceHandler {
	return &SourceHandler{queries: queries, encryptor: encryptor, credentialKeyVersion: credentialKeyVersion}
}

type credentialInput struct {
	Username   string `json:"username,omitempty"`
	Password   string `json:"password,omitempty"`
	Token      string `json:"token,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	KnownHosts string `json:"known_hosts,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

func (c credentialInput) empty() bool {
	return c.Username == "" && c.Password == "" && c.Token == "" && c.PrivateKey == "" && c.KnownHosts == "" && c.Passphrase == ""
}

func (c credentialInput) fluxData(mode model.AuthMode) (map[string]string, error) {
	if err := validateCredentialBounds(c); err != nil {
		return nil, err
	}
	switch mode {
	case model.AuthNone, model.AuthWorkloadIdentity:
		if !c.empty() {
			return nil, errors.New("selected authentication mode does not accept inline credentials")
		}
		return nil, nil
	case model.AuthBasic:
		if c.Username == "" || c.Password == "" || c.Token != "" || c.PrivateKey != "" || c.KnownHosts != "" || c.Passphrase != "" {
			return nil, errors.New("basic authentication requires only username and password")
		}
		return map[string]string{"password": c.Password, "username": c.Username}, nil
	case model.AuthBearer:
		if c.Token == "" || c.Username != "" || c.Password != "" || c.PrivateKey != "" || c.KnownHosts != "" || c.Passphrase != "" {
			return nil, errors.New("bearer authentication requires only token")
		}
		return map[string]string{"bearerToken": c.Token}, nil
	case model.AuthSSH:
		if c.PrivateKey == "" || c.KnownHosts == "" || c.Username != "" || c.Password != "" || c.Token != "" {
			return nil, errors.New("SSH authentication requires private_key and known_hosts, with an optional passphrase")
		}
		values := map[string]string{"identity": c.PrivateKey, "known_hosts": c.KnownHosts}
		if c.Passphrase != "" {
			values["password"] = c.Passphrase
		}
		return values, nil
	default:
		return nil, errors.New("authentication mode is unsupported")
	}
}

func validateCredentialBounds(value credentialInput) error {
	values := []string{value.Username, value.Password, value.Token, value.PrivateKey, value.KnownHosts, value.Passphrase}
	total := 0
	for _, item := range values {
		if !utf8.ValidString(item) || len(item) > maxCredentialValueBytes {
			return errors.New("credential fields must be valid UTF-8 and at most 256 KiB each")
		}
		total += len(item)
	}
	if total > maxCredentialValueBytes {
		return errors.New("credential material must total at most 256 KiB")
	}
	return nil
}

// openapi:request DeliverySourceWrite
type createSourceRequest struct {
	ProjectID   uuid.UUID         `json:"project_id,omitempty"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        model.SourceType  `json:"type"`
	URL         string            `json:"url"`
	AuthMode    model.AuthMode    `json:"auth_mode"`
	Credential  *credentialInput  `json:"credential,omitempty"`
	CABundle    string            `json:"ca_bundle,omitempty"`
	ProxyRef    string            `json:"proxy_ref,omitempty"`
	Trust       model.TrustPolicy `json:"trust_policy"`
}

// openapi:request DeliverySourceCredentialRotate
type rotateCredentialRequest struct {
	ProjectID  uuid.UUID       `json:"project_id,omitempty"`
	AuthMode   model.AuthMode  `json:"auth_mode"`
	Credential credentialInput `json:"credential"`
}

// openapi:request DeliverySourceVerify
type verifySourceRequest struct {
	ProjectID         uuid.UUID `json:"project_id,omitempty"`
	RequestedRevision string    `json:"requested_revision"`
	Chart             string    `json:"chart,omitempty"`
}

// openapi:request DeliverySourcePatch
type updateSourceRequest struct {
	ProjectID   uuid.UUID          `json:"project_id,omitempty"`
	Description *string            `json:"description,omitempty"`
	URL         *string            `json:"url,omitempty"`
	ProxyRef    *string            `json:"proxy_ref,omitempty"`
	Trust       *model.TrustPolicy `json:"trust_policy,omitempty"`
	CABundle    *string            `json:"ca_bundle,omitempty"`
}

type sourceCredentialMetadata struct {
	Configured bool  `json:"configured"`
	KeyVersion int32 `json:"key_version"`
	Epoch      int64 `json:"epoch"`
}

type sourceResponse struct {
	ID             uuid.UUID                `json:"id"`
	ProjectID      uuid.UUID                `json:"project_id"`
	Name           string                   `json:"name"`
	Description    string                   `json:"description,omitempty"`
	Type           model.SourceType         `json:"type"`
	URL            string                   `json:"url"`
	AuthMode       model.AuthMode           `json:"auth_mode"`
	Credential     sourceCredentialMetadata `json:"credential"`
	ProxyRef       string                   `json:"proxy_ref,omitempty"`
	Trust          model.TrustPolicy        `json:"trust_policy"`
	Status         string                   `json:"status"`
	LastResolvedAt *time.Time               `json:"last_resolved_at"`
	LastErrorCode  string                   `json:"last_error_code,omitempty"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
}

// List returns project-scoped, stable, database-paginated source metadata.
func (h *SourceHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	limit, offset, err := parsePagination(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_pagination", err.Error())
		return
	}
	status, err := sourceStatusFilter(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_filter", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery source persistence is unavailable")
		return
	}
	items, err := h.queries.ListDeliverySources(r.Context(), sqlc.ListDeliverySourcesParams{
		ProjectID: projectID, Status: status, QueryOffset: offset, QueryLimit: limit,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	total, err := h.queries.CountDeliverySources(r.Context(), sqlc.CountDeliverySourcesParams{ProjectID: projectID, Status: status})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	response := make([]sourceResponse, 0, len(items))
	for _, item := range items {
		converted, err := sourceFromListRow(item)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
			return
		}
		response = append(response, converted)
	}
	respondPage(w, r, response, total, limit, offset, int64(offset)+int64(len(response)) < total, true)
}

// Create validates and seals all write-only material before any database write.
func (h *SourceHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request createSourceRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	request.ProjectID = projectID
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery source persistence is unavailable")
		return
	}
	if err := validateDisplayFields(request.Name, request.Description); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if len(request.URL) > 2048 {
		respondError(w, http.StatusBadRequest, "validation_error", "url must be at most 2048 bytes")
		return
	}
	if err := validateProxyRef(request.ProxyRef); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	domainSource := model.Source{
		ID: uuid.New(), ProjectID: projectID, Name: request.Name, Description: request.Description,
		Type: request.Type, URL: request.URL, AuthMode: request.AuthMode, Trust: request.Trust,
	}
	if err := domainSource.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	credential := credentialInput{}
	if request.Credential != nil {
		credential = *request.Credential
	}
	credentialData, err := credential.fluxData(request.AuthMode)
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if request.CABundle != "" {
		if err := validateCABundle(request.CABundle); err != nil {
			respondError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
	}
	credentialEncrypted, caEncrypted, keyVersion, epoch, err := h.sealSourceMaterial(credentialData, request.CABundle)
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "credential_encryption_unavailable", "source credential encryption failed")
		return
	}
	if request.AuthMode == model.AuthNone && len(credentialData) == 0 {
		// The schema intentionally records no credential key generation for a
		// public source. A separately sealed private CA does not change that.
		keyVersion = 0
	}
	trust, err := json.Marshal(request.Trust)
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", "trust policy is invalid")
		return
	}
	actor := middleware.AuthenticatedUserUUID(r.Context())
	created, err := h.queries.CreateDeliverySource(r.Context(), sqlc.CreateDeliverySourceParams{
		ProjectID: projectID, Name: request.Name, Description: request.Description,
		SourceType: string(request.Type), Url: request.URL, AuthMode: string(request.AuthMode),
		CredentialEncrypted: credentialEncrypted, CredentialKeyVersion: keyVersion, CredentialEpoch: epoch,
		CaBundleEncrypted: caEncrypted, ProxyRef: request.ProxyRef, TrustPolicy: trust,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	response, err := sourceFromCreateRow(created)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
		return
	}
	recordAudit(r, h.queries, "delivery.source.created", "delivery_source", response.ID.String(), response.Name, map[string]any{
		"project_id":            projectID.String(),
		"source_type":           string(response.Type),
		"auth_mode":             string(response.AuthMode),
		"credential_configured": response.Credential.Configured,
		"ca_configured":         request.CABundle != "",
		"proxy_configured":      request.ProxyRef != "",
		"key_version":           response.Credential.KeyVersion,
	})
	respondData(w, http.StatusCreated, response)
}

// Get returns source metadata without selecting secret-bearing columns.
func (h *SourceHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, sourceID, ok := sourceScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery source persistence is unavailable")
		return
	}
	row, err := h.queries.GetDeliverySource(r.Context(), sqlc.GetDeliverySourceParams{ID: sourceID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	response, err := sourceFromGetRow(row)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
		return
	}
	respondData(w, http.StatusOK, response)
}

// Update replaces credential-free source metadata. Credentials stay on the
// rotate-credential path so this handler never reads or echoes secret bytes.
func (h *SourceHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request updateSourceRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	sourceID, err := pathUUID(r, "id", "sourceID", "source_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery source persistence is unavailable")
		return
	}
	existing, err := h.queries.GetDeliverySource(r.Context(), sqlc.GetDeliverySourceParams{ID: sourceID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	description := existing.Description
	if request.Description != nil {
		description = *request.Description
	}
	sourceURL := existing.Url
	if request.URL != nil {
		sourceURL = *request.URL
	}
	proxyRef := existing.ProxyRef
	if request.ProxyRef != nil {
		proxyRef = *request.ProxyRef
	}
	var trust model.TrustPolicy
	if err := decodeStrictJSON(existing.TrustPolicy, &trust); err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
		return
	}
	if request.Trust != nil {
		trust = *request.Trust
	}
	if err := validateDisplayFields(existing.Name, description); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if len(sourceURL) > 2048 {
		respondError(w, http.StatusBadRequest, "validation_error", "url must be at most 2048 bytes")
		return
	}
	if err := validateProxyRef(proxyRef); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	domainSource := model.Source{
		ID: existing.ID, ProjectID: existing.ProjectID, Name: existing.Name, Description: description,
		Type: model.SourceType(existing.SourceType), URL: sourceURL, AuthMode: model.AuthMode(existing.AuthMode), Trust: trust,
	}
	if err := domainSource.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	trustJSON, err := json.Marshal(trust)
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", "trust policy is invalid")
		return
	}
	var caEncrypted string
	replaceCA := request.CABundle != nil
	if replaceCA {
		if *request.CABundle != "" {
			if err := validateCABundle(*request.CABundle); err != nil {
				respondError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
		}
		_, sealed, _, _, err := h.sealSourceMaterial(nil, *request.CABundle)
		if err != nil {
			respondError(w, http.StatusServiceUnavailable, "credential_encryption_unavailable", "source credential encryption failed")
			return
		}
		caEncrypted = sealed
	}
	updated, err := h.queries.UpdateDeliverySource(r.Context(), sqlc.UpdateDeliverySourceParams{
		Description: description, Url: sourceURL, ProxyRef: proxyRef, TrustPolicy: trustJSON,
		ReplaceCaBundle: replaceCA, CaBundleEncrypted: caEncrypted,
		UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()), ID: sourceID, ProjectID: projectID,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	response, err := sourceFromUpdateRow(updated)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
		return
	}
	recordAudit(r, h.queries, "delivery.source.updated", "delivery_source", response.ID.String(), response.Name, map[string]any{
		"project_id":       projectID.String(),
		"source_type":      string(response.Type),
		"ca_replaced":      replaceCA,
		"proxy_configured": response.ProxyRef != "",
	})
	respondData(w, http.StatusOK, response)
}

// Delete removes an unreferenced source. Foreign-key restrictions are exposed
// as a typed 409 and never bypassed with cascading handler behavior.
func (h *SourceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	projectID, sourceID, ok := sourceScope(w, r)
	if !ok {
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery source persistence is unavailable")
		return
	}
	deleted, err := h.queries.DeleteDeliverySource(r.Context(), sqlc.DeleteDeliverySourceParams{ID: sourceID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	if deleted == 0 {
		respondDatabaseError(w, pgx.ErrNoRows)
		return
	}
	recordAudit(r, h.queries, "delivery.source.deleted", "delivery_source", sourceID.String(), "", map[string]any{
		"project_id": projectID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// Verify schedules a durable, fenced source resolution. Credentials are loaded
// by the worker from encrypted storage and never enter the task or response.
func (h *SourceHandler) Verify(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request verifySourceRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	sourceID, err := pathUUID(r, "id", "sourceID", "source_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery source persistence is unavailable")
		return
	}
	revision := strings.TrimSpace(request.RequestedRevision)
	if revision == "" || len(revision) > 256 || containsControl(revision) {
		respondError(w, http.StatusBadRequest, "validation_error", "requested_revision must be 1 through 256 safe bytes")
		return
	}
	source, err := h.queries.GetDeliverySource(r.Context(), sqlc.GetDeliverySourceParams{ID: sourceID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	if source.Status == "revoked" {
		respondError(w, http.StatusConflict, "source_revoked", "a revoked source cannot be verified")
		return
	}
	chart := strings.TrimSpace(request.Chart)
	switch model.SourceType(source.SourceType) {
	case model.SourceHelmHTTP, model.SourceHelmOCI:
		if chart == "" || len(chart) > model.MaxNameLength || containsControl(chart) || strings.Contains(chart, "..") {
			respondError(w, http.StatusBadRequest, "validation_error", "chart is required and must be a bounded safe name for Helm sources")
			return
		}
	case model.SourceGit, model.SourceOCIArtifact:
		if chart != "" {
			respondError(w, http.StatusBadRequest, "validation_error", "chart is valid only for Helm sources")
			return
		}
	default:
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source type is invalid")
		return
	}
	resolution, err := h.queries.CreateDeliverySourceResolutionAndOutbox(r.Context(), sqlc.CreateDeliverySourceResolutionAndOutboxParams{
		SourceID: sourceID, BundleVersionID: pgtype.UUID{}, RequestedRevision: revision, ChartName: chart,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	recordAudit(r, h.queries, "delivery.source.verification_requested", "delivery_source", sourceID.String(), source.Name, map[string]any{
		"project_id":    projectID.String(),
		"resolution_id": resolution.ID.String(),
		"source_type":   source.SourceType,
		"status":        resolution.Status,
	})
	respondData(w, http.StatusAccepted, struct {
		ID       uuid.UUID `json:"id"`
		SourceID uuid.UUID `json:"source_id"`
		Status   string    `json:"status"`
	}{resolution.ID, resolution.SourceID, resolution.Status})
}

// RotateCredential replaces write-only source credentials and increments the
// credential epoch in the scoped SQL update. Existing material is never read.
func (h *SourceHandler) RotateCredential(w http.ResponseWriter, r *http.Request) {
	if err := validateIdempotencyKey(r); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_idempotency_key", err.Error())
		return
	}
	var request rotateCredentialRequest
	if err := decodeRequest(w, r, &request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	projectID, err := projectIDFromRequest(r, request.ProjectID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return
	}
	sourceID, err := pathUUID(r, "id", "sourceID", "source_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return
	}
	if h == nil || h.queries == nil {
		respondError(w, http.StatusServiceUnavailable, "service_unavailable", "delivery source persistence is unavailable")
		return
	}
	existing, err := h.queries.GetDeliverySource(r.Context(), sqlc.GetDeliverySourceParams{ID: sourceID, ProjectID: projectID})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	var trust model.TrustPolicy
	if err := decodeStrictJSON(existing.TrustPolicy, &trust); err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
		return
	}
	domainSource := model.Source{
		ID: existing.ID, ProjectID: existing.ProjectID, Name: existing.Name, Description: existing.Description,
		Type: model.SourceType(existing.SourceType), URL: existing.Url, AuthMode: request.AuthMode, Trust: trust,
	}
	if err := domainSource.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if request.AuthMode == model.AuthNone || request.AuthMode == model.AuthWorkloadIdentity {
		respondError(w, http.StatusBadRequest, "validation_error", "credential rotation requires basic, bearer, or SSH authentication")
		return
	}
	values, err := request.Credential.fluxData(request.AuthMode)
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	ciphertext, _, keyVersion, _, err := h.sealSourceMaterial(values, "")
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, "credential_encryption_unavailable", "source credential encryption failed")
		return
	}
	rotated, err := h.queries.RotateDeliverySourceCredential(r.Context(), sqlc.RotateDeliverySourceCredentialParams{
		AuthMode: string(request.AuthMode), CredentialEncrypted: ciphertext, CredentialKeyVersion: keyVersion,
		UpdatedBy: middleware.AuthenticatedUserUUID(r.Context()), ID: sourceID, ProjectID: projectID,
	})
	if err != nil {
		respondDatabaseError(w, err)
		return
	}
	response, err := sourceFromRotateRow(rotated)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "invalid_persisted_state", "stored delivery source metadata is invalid")
		return
	}
	recordAudit(r, h.queries, "delivery.source.credential_rotated", "delivery_source", sourceID.String(), response.Name, map[string]any{
		"project_id":       projectID.String(),
		"auth_mode":        string(response.AuthMode),
		"credential_epoch": response.Credential.Epoch,
		"key_version":      response.Credential.KeyVersion,
	})
	respondData(w, http.StatusOK, response)
}

func (h *SourceHandler) sealSourceMaterial(credential map[string]string, caBundle string) (credentialEncrypted, caEncrypted string, keyVersion int32, epoch int64, err error) {
	if len(credential) == 0 && caBundle == "" {
		return "", "", 0, 0, nil
	}
	if h.encryptor == nil || h.credentialKeyVersion < 1 {
		return "", "", 0, 0, errors.New("credential encryptor is unavailable")
	}
	if len(credential) != 0 {
		keys := make([]string, 0, len(credential))
		for key := range credential {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make(map[string]string, len(credential))
		for _, key := range keys {
			ordered[key] = credential[key]
		}
		plaintext, marshalErr := json.Marshal(ordered)
		if marshalErr != nil {
			return "", "", 0, 0, marshalErr
		}
		credentialEncrypted, err = h.encryptor.Encrypt(string(plaintext))
		clear(plaintext)
		if err != nil || credentialEncrypted == "" {
			return "", "", 0, 0, errors.New("encrypt credential")
		}
	}
	if caBundle != "" {
		caEncrypted, err = h.encryptor.Encrypt(caBundle)
		if err != nil || caEncrypted == "" {
			return "", "", 0, 0, errors.New("encrypt CA bundle")
		}
	}
	return credentialEncrypted, caEncrypted, h.credentialKeyVersion, 1, nil
}

func sourceScope(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	projectID, err := projectIDFromRequest(r, uuid.Nil)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_project_scope", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	sourceID, err := pathUUID(r, "id", "sourceID", "source_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid_resource_id", err.Error())
		return uuid.Nil, uuid.Nil, false
	}
	return projectID, sourceID, true
}

func sourceStatusFilter(r *http.Request) (pgtype.Text, error) {
	values, exists := r.URL.Query()["status"]
	if !exists {
		return pgtype.Text{}, nil
	}
	if len(values) != 1 {
		return pgtype.Text{}, errors.New("status must occur exactly once")
	}
	switch values[0] {
	case "pending", "ready", "degraded", "revoked":
		return pgtype.Text{String: values[0], Valid: true}, nil
	default:
		return pgtype.Text{}, errors.New("status must be pending, ready, degraded, or revoked")
	}
}

func validateProxyRef(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 128 || len(utilvalidation.IsDNS1123Subdomain(value)) != 0 {
		return errors.New("proxy_ref must be a DNS subdomain of at most 128 bytes")
	}
	return nil
}

func validateCABundle(value string) error {
	if len(value) > maxCredentialValueBytes {
		return errors.New("ca_bundle must be at most 256 KiB")
	}
	certificates := x509.NewCertPool()
	if !certificates.AppendCertsFromPEM([]byte(value)) {
		return errors.New("ca_bundle must contain at least one valid PEM certificate")
	}
	return nil
}

type sourceRecord struct {
	ID                   uuid.UUID
	ProjectID            uuid.UUID
	Name                 string
	Description          string
	SourceType           string
	URL                  string
	AuthMode             string
	CredentialKeyVersion int32
	CredentialEpoch      int64
	ProxyRef             string
	TrustPolicy          json.RawMessage
	Status               string
	LastResolvedAt       pgtype.Timestamptz
	LastErrorCode        string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func sourceFromRecord(record sourceRecord) (sourceResponse, error) {
	var trust model.TrustPolicy
	if err := decodeStrictJSON(record.TrustPolicy, &trust); err != nil {
		return sourceResponse{}, err
	}
	if err := trust.Validate(); err != nil {
		return sourceResponse{}, err
	}
	stored := model.Source{
		ID: record.ID, ProjectID: record.ProjectID, Name: record.Name, Description: record.Description,
		Type: model.SourceType(record.SourceType), URL: record.URL, AuthMode: model.AuthMode(record.AuthMode), Trust: trust,
	}
	if err := stored.Validate(); err != nil {
		return sourceResponse{}, err
	}
	var lastResolvedAt *time.Time
	if record.LastResolvedAt.Valid {
		value := record.LastResolvedAt.Time
		lastResolvedAt = &value
	}
	return sourceResponse{
		ID: record.ID, ProjectID: record.ProjectID, Name: record.Name, Description: record.Description,
		Type: model.SourceType(record.SourceType), URL: record.URL, AuthMode: model.AuthMode(record.AuthMode),
		Credential: sourceCredentialMetadata{
			Configured: record.CredentialKeyVersion > 0, KeyVersion: record.CredentialKeyVersion, Epoch: record.CredentialEpoch,
		},
		ProxyRef: record.ProxyRef, Trust: trust, Status: record.Status, LastResolvedAt: lastResolvedAt,
		LastErrorCode: record.LastErrorCode, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, nil
}

func sourceFromListRow(row sqlc.ListDeliverySourcesRow) (sourceResponse, error) {
	return sourceFromRecord(sourceRecord{row.ID, row.ProjectID, row.Name, row.Description, row.SourceType, row.Url, row.AuthMode, row.CredentialKeyVersion, row.CredentialEpoch, row.ProxyRef, row.TrustPolicy, row.Status, row.LastResolvedAt, row.LastErrorCode, row.CreatedAt, row.UpdatedAt})
}

func sourceFromGetRow(row sqlc.GetDeliverySourceRow) (sourceResponse, error) {
	return sourceFromRecord(sourceRecord{row.ID, row.ProjectID, row.Name, row.Description, row.SourceType, row.Url, row.AuthMode, row.CredentialKeyVersion, row.CredentialEpoch, row.ProxyRef, row.TrustPolicy, row.Status, row.LastResolvedAt, row.LastErrorCode, row.CreatedAt, row.UpdatedAt})
}

func sourceFromCreateRow(row sqlc.CreateDeliverySourceRow) (sourceResponse, error) {
	return sourceFromRecord(sourceRecord{row.ID, row.ProjectID, row.Name, row.Description, row.SourceType, row.Url, row.AuthMode, row.CredentialKeyVersion, row.CredentialEpoch, row.ProxyRef, row.TrustPolicy, row.Status, row.LastResolvedAt, row.LastErrorCode, row.CreatedAt, row.UpdatedAt})
}

func sourceFromRotateRow(row sqlc.RotateDeliverySourceCredentialRow) (sourceResponse, error) {
	return sourceFromRecord(sourceRecord{row.ID, row.ProjectID, row.Name, row.Description, row.SourceType, row.Url, row.AuthMode, row.CredentialKeyVersion, row.CredentialEpoch, row.ProxyRef, row.TrustPolicy, row.Status, row.LastResolvedAt, row.LastErrorCode, row.CreatedAt, row.UpdatedAt})
}

func sourceFromUpdateRow(row sqlc.UpdateDeliverySourceRow) (sourceResponse, error) {
	return sourceFromRecord(sourceRecord{row.ID, row.ProjectID, row.Name, row.Description, row.SourceType, row.Url, row.AuthMode, row.CredentialKeyVersion, row.CredentialEpoch, row.ProxyRef, row.TrustPolicy, row.Status, row.LastResolvedAt, row.LastErrorCode, row.CreatedAt, row.UpdatedAt})
}

func decodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing JSON value")
	}
	return nil
}
