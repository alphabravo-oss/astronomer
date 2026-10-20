package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/crypto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/dexconfig"
	"github.com/alphabravocompany/astronomer-go/internal/principal"
)

const principalSearchTimeout = 4 * time.Second

type PrincipalQueries interface {
	CreateAuditLogV1(context.Context, sqlc.CreateAuditLogV1Params) error
	SearchPrincipalUsers(context.Context, sqlc.SearchPrincipalUsersParams) ([]sqlc.SearchPrincipalUsersRow, error)
	ListEnabledDexConnectors(context.Context) ([]sqlc.DexConnector, error)
	GetDexConnectorByID(context.Context, uuid.UUID) (sqlc.DexConnector, error)
}

type PrincipalMutationTx interface {
	audit.OutboxQuerier
	MaterializeExternalPrincipal(context.Context, sqlc.MaterializeExternalPrincipalParams) (sqlc.MaterializeExternalPrincipalRow, error)
}

type principalRunTxFunc func(context.Context, func(PrincipalMutationTx) error) error

type PrincipalHandler struct {
	queries   PrincipalQueries
	directory *principal.Directory
	encryptor *crypto.Encryptor
	runTx     principalRunTxFunc
}

func NewPrincipalHandler(queries PrincipalQueries, directory *principal.Directory) *PrincipalHandler {
	return &PrincipalHandler{queries: queries, directory: directory}
}

func (h *PrincipalHandler) SetEncryptor(encryptor *crypto.Encryptor) { h.encryptor = encryptor }
func (h *PrincipalHandler) SetRunTx(runTx principalRunTxFunc)        { h.runTx = runTx }
func (h *PrincipalHandler) TransactionalAuditWired() bool            { return h != nil && h.runTx != nil }

type principalSearchItem struct {
	Kind          string `json:"kind"`
	UserID        string `json:"user_id,omitempty"`
	PrincipalID   string `json:"principal_id,omitempty"`
	ConnectorID   string `json:"connector_id,omitempty"`
	ConnectorName string `json:"connector_name,omitempty"`
	ConnectorType string `json:"connector_type,omitempty"`
	Subject       string `json:"subject,omitempty"`
	Email         string `json:"email"`
	Username      string `json:"username"`
	DisplayName   string `json:"display_name"`
}

type principalSearchResponse struct {
	Principals []principalSearchItem       `json:"principals"`
	Connectors []principal.ConnectorStatus `json:"connectors"`
}

func (h *PrincipalHandler) Search(w http.ResponseWriter, r *http.Request) {
	queryValues := r.URL.Query()["q"]
	if len(queryValues) != 1 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Exactly one search query is required")
		return
	}
	query := strings.TrimSpace(queryValues[0])
	if len(query) < principal.MinQueryLength || len(query) > principal.MaxQueryLength {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Search query must contain 3 to 128 characters")
		return
	}
	if h == nil || h.queries == nil || h.directory == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "Principal discovery is not configured")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), principalSearchTimeout)
	defer cancel()
	locals, err := h.queries.SearchPrincipalUsers(ctx, sqlc.SearchPrincipalUsersParams{Search: query, ResultLimit: principal.MaxResults})
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.LoadError, "Failed to search the local identity directory")
		return
	}
	connectorRows, err := h.queries.ListEnabledDexConnectors(ctx)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.LoadError, "Failed to load identity connectors")
		return
	}

	items := make([]principalSearchItem, 0, principal.MaxResults)
	localEmails := make(map[string]struct{}, len(locals))
	for _, row := range locals {
		kind := "local"
		if row.Pending {
			kind = "pending"
		}
		displayName := strings.TrimSpace(row.FirstName + " " + row.LastName)
		if displayName == "" {
			displayName = row.Username
		}
		items = append(items, principalSearchItem{Kind: kind, UserID: row.UserID.String(), PrincipalID: row.ExternalPrincipalID, ConnectorID: row.ConnectorID, ConnectorName: row.ConnectorName, ConnectorType: row.ConnectorType, Subject: row.Subject, Email: row.Email, Username: row.Username, DisplayName: displayName})
		localEmails[strings.ToLower(row.Email)] = struct{}{}
	}

	connectors := make([]principal.Connector, 0, len(connectorRows))
	statuses := make([]principal.ConnectorStatus, 0)
	for _, row := range connectorRows {
		connector, decodeErr := h.directoryConnector(row)
		if decodeErr != nil {
			statuses = append(statuses, principal.ConnectorStatus{ConnectorID: row.ID, ConnectorName: row.Name, ConnectorType: row.Type, Supported: row.Type == "ldap", Error: "connector configuration is unavailable"})
			continue
		}
		connectors = append(connectors, connector)
	}
	external, connectorStatuses := h.directory.Search(ctx, connectors, query, principal.MaxResults)
	statuses = append(statuses, connectorStatuses...)
	for _, found := range external {
		if len(items) == principal.MaxResults {
			break
		}
		if _, exists := localEmails[strings.ToLower(found.Email)]; exists {
			continue
		}
		items = append(items, principalSearchItem{Kind: "external", ConnectorID: found.ConnectorID.String(), ConnectorName: found.ConnectorName, ConnectorType: found.ConnectorType, Subject: found.Subject, Email: found.Email, Username: found.Username, DisplayName: found.DisplayName})
	}
	if err := recordMandatoryAudit(r, h.queries, "principal.search", "identity_directory", "", "", map[string]any{"query_length": len(query), "result_count": len(items), "connector_count": len(statuses)}); err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.AuditUnavailable, "Mandatory audit storage is unavailable; principal results were not returned")
		return
	}
	RespondJSON(w, http.StatusOK, principalSearchResponse{Principals: items, Connectors: statuses})
}

// openapi:request PrincipalMaterializeRequest
type materializePrincipalRequest struct {
	ConnectorID string `json:"connector_id"`
	Subject     string `json:"subject"`
}

func (h *PrincipalHandler) Materialize(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.queries == nil || h.directory == nil || h.runTx == nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "Principal materialization is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var request materializePrincipalRequest
	if err := decodeStrictJSONBody(r, &request); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Invalid principal materialization request")
		return
	}
	connectorID, err := uuid.Parse(request.ConnectorID)
	if err != nil || strings.TrimSpace(request.Subject) == "" || len(request.Subject) > 512 {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Connector ID and principal subject are required")
		return
	}
	row, err := h.queries.GetDexConnectorByID(r.Context(), connectorID)
	if errors.Is(err, pgx.ErrNoRows) || !row.Enabled {
		apiutil.WriteError(w, r, http.StatusNotFound, apiutil.NotFound, "Identity connector not found")
		return
	}
	if err != nil {
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.LoadError, "Failed to load identity connector")
		return
	}
	connector, err := h.directoryConnector(row)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "Identity connector configuration is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), principalSearchTimeout)
	defer cancel()
	resolved, err := h.directory.Resolve(ctx, connector, request.Subject)
	if errors.Is(err, principal.ErrUnsupported) {
		apiutil.WriteError(w, r, http.StatusUnprocessableEntity, apiutil.ValidationError, "This identity connector does not support principal discovery")
		return
	}
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadGateway, apiutil.LoadError, "External principal could not be verified")
		return
	}
	firstName, lastName := splitPrincipalName(resolved.DisplayName)
	hash := sha256.Sum256([]byte(connectorID.String() + "\x00" + resolved.Subject))
	params := sqlc.MaterializeExternalPrincipalParams{TargetConnectorID: connectorID, TargetSubject: resolved.Subject, PrincipalEmail: strings.ToLower(resolved.Email), LocalUsername: "ext_" + hex.EncodeToString(hash[:12]), PrincipalFirstName: firstName, PrincipalLastName: lastName, ExternalUsername: resolved.Username, PrincipalDisplayName: resolved.DisplayName}
	var materialized sqlc.MaterializeExternalPrincipalRow
	err = h.runTx(ctx, func(q PrincipalMutationTx) error {
		var mutationErr error
		materialized, mutationErr = q.MaterializeExternalPrincipal(ctx, params)
		if mutationErr != nil {
			return mutationErr
		}
		return recordAuditOutbox(r, q, "principal.materialized", "external_principal", materialized.ID.String(), materialized.DisplayName, http.StatusCreated, map[string]any{"connector_id": connectorID.String(), "connector_type": connector.Type, "user_id": materialized.UserID.String()})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apiutil.CreateError, "Failed to materialize external principal")
		return
	}
	RespondJSON(w, http.StatusCreated, map[string]any{"id": materialized.ID, "user_id": materialized.UserID, "kind": "pending", "connector_id": materialized.ConnectorID, "email": materialized.Email, "username": materialized.Username, "display_name": materialized.DisplayName})
}

func (h *PrincipalHandler) directoryConnector(row sqlc.DexConnector) (principal.Connector, error) {
	var config map[string]any
	if err := json.Unmarshal(row.Config, &config); err != nil {
		return principal.Connector{}, err
	}
	if _, ok := dexconfig.Registry()[row.Type]; !ok {
		return principal.Connector{ID: row.ID, Name: row.Name, Type: row.Type, DisplayName: row.DisplayName, Config: config}, nil
	}
	secrets, err := openConnectorSecrets(h.encryptor, row)
	if err != nil {
		return principal.Connector{}, err
	}
	for key, value := range secrets {
		config[key] = value
	}
	return principal.Connector{ID: row.ID, Name: row.Name, Type: row.Type, DisplayName: row.DisplayName, Config: config}, nil
}

func splitPrincipalName(displayName string) (string, string) {
	parts := strings.Fields(displayName)
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}
