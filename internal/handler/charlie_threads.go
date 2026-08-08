package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CharlieThreadService interface {
	GetActive(ctx context.Context, ownerID uuid.UUID) (charlie.ActiveThreadView, error)
	NewChat(ctx context.Context, ownerID uuid.UUID) (charlie.ActiveThreadView, error)
	List(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]sqlc.CharlieInteractiveThread, error)
	SendOnThread(ctx context.Context, ownerID uuid.UUID, clientMessageID uuid.UUID, message string, create charlie.CreateSessionInput) (charlie.ActiveThreadView, json.RawMessage, error)
	StitchedHistory(ctx context.Context, ownerID, threadID uuid.UUID, limit int) (json.RawMessage, error)
}

type CharlieThreadHandler struct {
	threads CharlieThreadService
}

func NewCharlieThreadHandler(threads CharlieThreadService) *CharlieThreadHandler {
	return &CharlieThreadHandler{threads: threads}
}

type charlieThreadMetadata struct {
	ID               uuid.UUID  `json:"id"`
	Title            string     `json:"title"`
	State            string     `json:"state"`
	CurrentSessionID *uuid.UUID `json:"current_session_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
}

func safeThreadMetadata(thread sqlc.CharlieInteractiveThread) charlieThreadMetadata {
	meta := charlieThreadMetadata{
		ID: thread.ID, Title: thread.Title, State: thread.State,
		CreatedAt: thread.CreatedAt, UpdatedAt: thread.UpdatedAt,
	}
	if thread.CurrentSessionID.Valid {
		id := uuid.UUID(thread.CurrentSessionID.Bytes)
		meta.CurrentSessionID = &id
	}
	if thread.ArchivedAt.Valid {
		t := thread.ArchivedAt.Time
		meta.ArchivedAt = &t
	}
	return meta
}

func activeThreadResponse(view charlie.ActiveThreadView) map[string]any {
	out := map[string]any{
		"thread":         safeThreadMetadata(view.Thread),
		"messageable":    view.Messageable,
		"needs_continue": view.NeedsContinue,
		"session_ids":    view.SessionIDs,
	}
	if view.CurrentSession != nil {
		out["current_session"] = safeSessionMetadata(*view.CurrentSession)
	}
	return out
}

func (h *CharlieThreadHandler) Active(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	view, err := h.threads.GetActive(r.Context(), mustUserID(actor))
	if errors.Is(err, charlie.ErrThreadNotFound) {
		RespondJSON(w, http.StatusOK, map[string]any{"thread": nil})
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie active thread is unavailable")
		return
	}
	RespondJSON(w, http.StatusOK, activeThreadResponse(view))
}

func (h *CharlieThreadHandler) NewChat(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	view, err := h.threads.NewChat(r.Context(), mustUserID(actor))
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie new chat is unavailable")
		return
	}
	RespondJSON(w, http.StatusCreated, activeThreadResponse(view))
}

func (h *CharlieThreadHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	rows, err := h.threads.List(r.Context(), mustUserID(actor), 50, 0)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie threads are unavailable")
		return
	}
	items := make([]charlieThreadMetadata, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeThreadMetadata(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"threads": items})
}

type sendCharlieThreadMessageRequest struct {
	ClientMessageID  string                    `json:"client_message_id"`
	Message          string                    `json:"message"`
	Trigger          string                    `json:"trigger,omitempty"`
	CurrentUIContext string                    `json:"current_ui_context,omitempty"`
	Resources        []charlie.SessionResource `json:"resources,omitempty"`
}

func (h *CharlieThreadHandler) Message(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	var request sendCharlieThreadMessageRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	messageID, err := uuid.Parse(request.ClientMessageID)
	if err != nil || strings.TrimSpace(request.Message) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie thread message request")
		return
	}
	label := strings.TrimSpace(actor.Username)
	if label == "" {
		label = strings.TrimSpace(actor.Email)
	}
	owner := mustUserID(actor)
	view, receipt, err := h.threads.SendOnThread(r.Context(), owner, messageID, request.Message, charlie.CreateSessionInput{
		ClientSessionID: uuid.New(), OwnerID: owner, ActorType: "user", ActorLabel: label,
		Intent: request.Message, Trigger: request.Trigger, CurrentUIContext: request.CurrentUIContext,
		Resources: request.Resources,
	})
	if err != nil {
		if errors.Is(err, charlie.ErrInvalidSessionRequest) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie thread message request")
			return
		}
		if strings.Contains(err.Error(), "no longer open") || strings.Contains(err.Error(), "does not accept") {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Charlie session is no longer open for messages")
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie thread message is unavailable")
		return
	}
	payload := activeThreadResponse(view)
	if len(receipt) > 0 {
		var decoded any
		if json.Unmarshal(receipt, &decoded) == nil {
			payload["receipt"] = decoded
		}
	}
	RespondJSON(w, http.StatusAccepted, payload)
}

func (h *CharlieThreadHandler) History(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	threadID, err := uuid.Parse(chi.URLParam(r, "thread_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie thread ID")
		return
	}
	raw, err := h.threads.StitchedHistory(r.Context(), mustUserID(actor), threadID, 100)
	if errors.Is(err, charlie.ErrThreadNotFound) || errors.Is(err, charlie.ErrThreadNotOwned) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Charlie thread was not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie thread history is unavailable")
		return
	}
	safe, err := safeCharlieBrowserJSON(raw)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie thread history is invalid")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": json.RawMessage(safe)})
}
