package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type StreamTicketHandler struct {
	store *auth.StreamTicketStore
	authz authorizationSupport
}

type StreamTicketRequest struct {
	StreamType string `json:"stream_type"`
	ClusterID  string `json:"cluster_id,omitempty"`
}

type StreamTicketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expires_at"`
}

func NewStreamTicketHandler(store *auth.StreamTicketStore) *StreamTicketHandler {
	return &StreamTicketHandler{store: store}
}

func (h *StreamTicketHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

func (h *StreamTicketHandler) Create(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, "stream_tickets_unavailable", "Stream tickets are not configured")
		return
	}
	user, ok := middleware.GetAuthenticatedUser(r.Context())
	if !ok || user == nil || user.ID == "" {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Authentication required")
		return
	}
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		RespondRequestError(w, r, http.StatusUnauthorized, "authentication_required", "Invalid authenticated user")
		return
	}
	var req StreamTicketRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	kind := auth.NormalizeStreamKind(req.StreamType)
	if kind == "" {
		RespondRequestError(w, r, http.StatusBadRequest, "validation_error", "stream_type must be one of events, registration, logs, exec, shell")
		return
	}
	var clusterID uuid.UUID
	if kind != auth.StreamKindEvents {
		clusterID, err = uuid.Parse(req.ClusterID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, "invalid_cluster_id", "cluster_id is required for this stream type")
			return
		}
		verb := rbac.VerbRead
		if kind == auth.StreamKindExec || kind == auth.StreamKindShell {
			verb = rbac.VerbUpdate
		}
		if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceClusters, verb) {
			return
		}
	}
	token, ticket, err := h.store.Issue(userID, kind, clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, "ticket_error", "Failed to issue stream ticket")
		return
	}
	RespondJSON(w, http.StatusCreated, StreamTicketResponse{
		Ticket:    token,
		ExpiresAt: ticket.ExpiresAt.UTC().Format(time.RFC3339),
	})
}
