package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

const dexOperationTaskType = "dex:apply_operation"

type dexOperationStore interface {
	ReserveDexOperation(context.Context, sqlc.ReserveDexOperationParams) (sqlc.ReserveDexOperationRow, error)
	QueueDexOperation(context.Context, sqlc.QueueDexOperationParams) (sqlc.QueueDexOperationRow, error)
}

type dexRegisterPayload struct {
	ClientID              string `json:"client_id"`
	DisplayName           string `json:"display_name"`
	ClientSecretEncrypted string `json:"client_secret_encrypted"`
}

// openapi:request-operation postAuthDexRegisterAsSso
type dexRegisterSSORequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	DisplayName  string `json:"display_name"`
}

type dexOperationResponse struct {
	OperationID       uuid.UUID `json:"operation_id"`
	Action            string    `json:"action"`
	TargetID          uuid.UUID `json:"target_id"`
	RuntimeGeneration int64     `json:"runtime_generation"`
	Status            string    `json:"status"`
	Phase             string    `json:"phase"`
	AttemptCount      int32     `json:"attempt_count"`
	ErrorCode         string    `json:"error_code,omitempty"`
	StatusURL         string    `json:"status_url"`
	CreatedAt         string    `json:"created_at"`
	UpdatedAt         string    `json:"updated_at"`
	CompletedAt       string    `json:"completed_at,omitempty"`
}

func dexOperationFromReserve(row sqlc.ReserveDexOperationRow) sqlc.DexOperation {
	return sqlc.DexOperation{
		ID: row.ID, Action: row.Action, TargetID: row.TargetID, RuntimeGeneration: row.RuntimeGeneration,
		IdempotencyScope: row.IdempotencyScope, IdempotencyKey: row.IdempotencyKey,
		RequestDigest: row.RequestDigest, PayloadEncrypted: row.PayloadEncrypted,
		Status: row.Status, Phase: row.Phase, AttemptCount: row.AttemptCount, LockedUntil: row.LockedUntil,
		ErrorCode: row.ErrorCode, CreatedBy: row.CreatedBy, CompletedAt: row.CompletedAt,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func dexOperationFromQueue(row sqlc.QueueDexOperationRow) sqlc.DexOperation {
	return sqlc.DexOperation(row)
}

func makeDexOperationResponse(row sqlc.DexOperation) dexOperationResponse {
	statusURL := "/api/v1/auth/dex/operations/" + row.ID.String() + "/"
	response := dexOperationResponse{OperationID: row.ID, Action: row.Action, TargetID: row.TargetID,
		RuntimeGeneration: row.RuntimeGeneration, Status: row.Status, Phase: row.Phase,
		AttemptCount: row.AttemptCount, ErrorCode: row.ErrorCode, StatusURL: statusURL,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339)}
	if row.CompletedAt.Valid {
		response.CompletedAt = row.CompletedAt.Time.UTC().Format(time.RFC3339)
	}
	return response
}

func writeDexOperationAccepted(w http.ResponseWriter, row sqlc.DexOperation) {
	response := makeDexOperationResponse(row)
	w.Header().Set("Location", response.StatusURL)
	w.Header().Set("Retry-After", "2")
	RespondJSON(w, http.StatusAccepted, response)
}

func dexRequestDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%x", digest[:]), nil
}

func dexOperationConflict(err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		return true
	}
	var databaseErr *pgconn.PgError
	return errors.As(err, &databaseErr) && databaseErr.Code == "23505"
}

func (h *DexHandler) reserveAndQueueDexOperation(r *http.Request, action, digest, payload string, generation int64) (sqlc.DexOperation, error) {
	if h.runTx == nil {
		return sqlc.DexOperation{}, errors.New("durable Dex operation persistence is unavailable")
	}
	actor := currentUserUUID(r)
	if !actor.Valid || uuid.UUID(actor.Bytes) == uuid.Nil {
		return sqlc.DexOperation{}, errors.New("authenticated Dex operation actor is unavailable")
	}
	actorID := uuid.UUID(actor.Bytes)
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	scope := "dex:actor:" + actorID.String() + ":action:" + action + ":target:" + dexSettingsSingletonID.String()
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+":"+key))
	var operation sqlc.DexOperation
	err := h.runTx(r.Context(), func(q DexMutationTx) error {
		store, ok := q.(dexOperationStore)
		if !ok {
			return errors.New("durable Dex operation persistence is unavailable")
		}
		var reserveErr error
		reserved, reserveErr := store.ReserveDexOperation(r.Context(), sqlc.ReserveDexOperationParams{
			ID: id, Action: action, TargetID: dexSettingsSingletonID, IdempotencyScope: scope,
			IdempotencyKey: key, RequestDigest: digest, PayloadEncrypted: payload, CreatedBy: actorID,
		})
		operation = dexOperationFromReserve(reserved)
		if reserveErr != nil || !reserved.Created {
			return reserveErr
		}
		queued, reserveErr := store.QueueDexOperation(r.Context(), sqlc.QueueDexOperationParams{ID: id, RuntimeGeneration: generation})
		if reserveErr != nil {
			return reserveErr
		}
		operation = dexOperationFromQueue(queued)
		return recordAuditOutbox(r, q, "dex."+action+".queued", "dex_operation", id.String(), "dex", http.StatusAccepted,
			map[string]any{"action": action, "runtime_generation": generation, "task_type": dexOperationTaskType})
	})
	return operation, err
}

// Apply commits a durable operation and returns before any Kubernetes call.
func (h *DexHandler) Apply(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	settings, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil || settings.IssuerUrl == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoSettings, "Dex settings have not been configured yet; PUT /settings first")
		return
	}
	if !settings.ClusterID.Valid {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingCluster, "Dex settings have no cluster_id; PUT /settings first")
		return
	}
	settings, err = h.normalizeRuntimeIdentity(settings)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.SettingsError, "Dex runtime identity does not match the installed chart")
		return
	}
	connectors, err := h.queries.ListEnabledDexConnectors(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list Dex connectors")
		return
	}
	clients, settings, err := h.loadPublicClients(r.Context(), settings)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Dex static-client secrets are unavailable")
		return
	}
	config, err := h.renderDexConfig(settings, clients, connectors)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.RenderError, "Dex runtime candidate is invalid")
		return
	}
	digest, _ := dexRequestDigest(struct {
		Action     string
		Generation int64
		Config     string
	}{"apply", settings.RuntimeGeneration, string(config)})
	operation, err := h.reserveAndQueueDexOperation(r, "apply", digest, "", settings.RuntimeGeneration)
	if err != nil {
		if dexOperationConflict(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key identifies different Dex input")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusServiceUnavailable, apierror.WriteError, "Failed to queue Dex apply")
		return
	}
	writeDexOperationAccepted(w, operation)
}

// RegisterAsSSO atomically stages the desired Dex generation, operation,
// task intent, and audit evidence. Runtime verification happens in the worker.
func (h *DexHandler) RegisterAsSSO(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	var req dexRegisterSSORequest
	if r.Body != http.NoBody {
		if err := decodeDexRequest(r.Body, &req, true); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
			return
		}
	}
	settings, err := h.queries.GetDexSettings(r.Context(), dexSettingsSingletonID)
	if err != nil || settings.IssuerUrl == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.NoSettings, "Dex settings have not been configured yet")
		return
	}
	if !settings.ClusterID.Valid {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.MissingCluster, "Dex settings require a target cluster before SSO registration")
		return
	}
	settings, err = h.normalizeRuntimeIdentity(settings)
	if err != nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.SettingsError, "Dex runtime identity does not match the installed chart")
		return
	}
	connectors, err := h.queries.ListEnabledDexConnectors(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list enabled Dex connectors")
		return
	}
	if len(connectors) == 0 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "At least one enabled Dex connector is required before SSO registration")
		return
	}
	if req.ClientID == "" {
		req.ClientID = "astronomer"
	}
	if req.DisplayName == "" {
		req.DisplayName = "Sign in with Dex"
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Encryptor is not configured")
		return
	}
	existing, existingErr := h.queries.GetSSOConfigurationByProvider(r.Context(), "dex")
	plainSecret, encryptedSecret := req.ClientSecret, ""
	if req.ClientSecret != "" {
		encryptedSecret, err = h.encryptor.Encrypt(req.ClientSecret)
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt client secret")
		return
	}
	if existingErr == nil && plainSecret == "" {
		encryptedSecret = existing.ClientSecretEncrypted
		if encryptedSecret != "" {
			plainSecret, err = h.encryptor.Decrypt(encryptedSecret)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Existing Dex client secret is unavailable")
		return
	}
	if plainSecret == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "client_secret is required when registering Dex SSO")
		return
	}
	clients, settings, err := h.astronomerPublicClients(r.Context(), settings, req.ClientID, plainSecret)
	if err != nil || validatePublicClients(clients) != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Dex SSO static client is invalid")
		return
	}
	encryptedClients, err := h.encryptPublicClients(clients)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.EncryptUnavailable, "Failed to encrypt Dex public clients")
		return
	}
	candidate := settings
	candidate.PublicClients = mustDexJSON(clients, []byte("[]"))
	candidate.PublicClientsEncrypted = encryptedClients
	config, err := h.renderDexConfig(candidate, clients, connectors)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.RenderError, "Dex runtime candidate is invalid")
		return
	}
	payloadBytes, _ := json.Marshal(dexRegisterPayload{ClientID: req.ClientID, DisplayName: req.DisplayName, ClientSecretEncrypted: encryptedSecret})
	payload, err := h.encryptor.Encrypt(string(payloadBytes))
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt Dex operation")
		return
	}
	digest, _ := dexRequestDigest(struct{ Action, ClientID, DisplayName, Config string }{"register_sso", req.ClientID, req.DisplayName, string(config)})
	actor := currentUserUUID(r)
	if !actor.Valid || h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Durable Dex operations are unavailable")
		return
	}
	actorID := uuid.UUID(actor.Bytes)
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	action := "register_sso"
	scope := "dex:actor:" + actorID.String() + ":action:" + action + ":target:" + dexSettingsSingletonID.String()
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+":"+key))
	stage := sqlc.StageDexSettingsAndDisableSSOParams{ID: settings.ID, IssuerUrl: settings.IssuerUrl, ClusterID: settings.ClusterID,
		Namespace: settings.Namespace, ReleaseName: settings.ReleaseName, ConfigmapName: settings.RuntimeSecretName, RuntimeSecretName: settings.RuntimeSecretName,
		PublicClients: candidate.PublicClients, PublicClientsEncrypted: encryptedClients, Expiry: settings.Expiry, Extra: settings.Extra,
		ChartReleaseName: settings.ChartReleaseName, DeploymentName: settings.DeploymentName, ServiceName: settings.ServiceName, RuntimePhase: settings.RuntimePhase}
	var operation sqlc.DexOperation
	err = h.runTx(r.Context(), func(q DexMutationTx) error {
		store, ok := q.(dexOperationStore)
		if !ok {
			return errors.New("durable Dex operation persistence is unavailable")
		}
		reserved, reserveErr := store.ReserveDexOperation(r.Context(), sqlc.ReserveDexOperationParams{ID: id, Action: action, TargetID: dexSettingsSingletonID, IdempotencyScope: scope, IdempotencyKey: key, RequestDigest: digest, PayloadEncrypted: payload, CreatedBy: actorID})
		err = reserveErr
		operation = dexOperationFromReserve(reserved)
		if err != nil || !reserved.Created {
			return err
		}
		generation, stageErr := q.StageDexSettingsAndDisableSSO(r.Context(), stage)
		if stageErr != nil {
			return stageErr
		}
		queued, queueErr := store.QueueDexOperation(r.Context(), sqlc.QueueDexOperationParams{ID: id, RuntimeGeneration: generation})
		err = queueErr
		if err != nil {
			return err
		}
		operation = dexOperationFromQueue(queued)
		return recordAuditOutbox(r, q, "dex.register_sso.queued", "dex_operation", id.String(), "dex", http.StatusAccepted, map[string]any{"runtime_generation": generation, "client_id": req.ClientID, "task_type": dexOperationTaskType})
	})
	if err != nil {
		if dexOperationConflict(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key identifies different Dex input or another Dex operation is active")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusServiceUnavailable, apierror.WriteError, "Failed to queue Dex SSO registration")
		return
	}
	writeDexOperationAccepted(w, operation)
}

func (h *DexHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "operation_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid Dex operation id")
		return
	}
	store, ok := h.queries.(interface {
		GetDexOperation(context.Context, uuid.UUID) (sqlc.DexOperation, error)
	})
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Dex operation persistence is unavailable")
		return
	}
	operation, err := store.GetDexOperation(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Dex operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read Dex operation")
		return
	}
	RespondJSON(w, http.StatusOK, makeDexOperationResponse(operation))
}

// ExecuteDexOperation is the tunnel-worker effect boundary.
func (h *DexHandler) ExecuteDexOperation(ctx context.Context, operation sqlc.DexOperation) error {
	if h == nil || h.k8s == nil {
		return errors.New("Dex Kubernetes requester is unavailable")
	}
	settings, err := h.queries.GetDexSettingsForGeneration(ctx, sqlc.GetDexSettingsForGenerationParams{ID: operation.TargetID, RuntimeGeneration: operation.RuntimeGeneration})
	if err != nil {
		return fmt.Errorf("load Dex generation: %w", err)
	}
	connectors, err := h.queries.ListEnabledDexConnectors(ctx)
	if err != nil {
		return fmt.Errorf("load Dex connectors: %w", err)
	}
	clients, settings, err := h.loadPublicClients(ctx, settings)
	if err != nil {
		return fmt.Errorf("load Dex clients: %w", err)
	}
	result, err := h.reconcileDexRuntime(ctx, settings, clients, connectors)
	if err != nil {
		return err
	}
	if !result.Applied {
		return nil
	}
	if store, ok := h.queries.(interface {
		SetDexOperationPhase(context.Context, sqlc.SetDexOperationPhaseParams) error
	}); ok {
		if err := store.SetDexOperationPhase(ctx, sqlc.SetDexOperationPhaseParams{ID: operation.ID, Phase: "finalizing"}); err != nil {
			return fmt.Errorf("advance Dex operation phase: %w", err)
		}
	}
	if operation.Action == "apply" {
		_, err = h.queries.RestoreDexSSOForGeneration(ctx, sqlc.RestoreDexSSOForGenerationParams{ID: settings.ID, RuntimeGeneration: settings.RuntimeGeneration})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if operation.Action != "register_sso" {
		return fmt.Errorf("unsupported Dex operation action %q", operation.Action)
	}
	if h.encryptor == nil {
		return errors.New("Dex operation decryptor is unavailable")
	}
	plain, err := h.encryptor.Decrypt(operation.PayloadEncrypted)
	if err != nil {
		return fmt.Errorf("decrypt Dex operation: %w", err)
	}
	var payload dexRegisterPayload
	if err := json.Unmarshal([]byte(plain), &payload); err != nil {
		return fmt.Errorf("decode Dex operation: %w", err)
	}
	config, _ := json.Marshal(map[string]any{"issuer_url": settings.IssuerUrl})
	_, err = h.queries.EnableDexSSOForGeneration(ctx, sqlc.EnableDexSSOForGenerationParams{DisplayName: payload.DisplayName, Config: config, ClientID: payload.ClientID, ClientSecretEncrypted: payload.ClientSecretEncrypted, RuntimeGeneration: settings.RuntimeGeneration})
	return err
}
