package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	charliecontract "github.com/alphabravocompany/astronomer-go/internal/charlie/contract"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const maxCharlieSessionRequestBytes = charlie.MaxCharlieMessageBytes + 8<<10

type CharlieSessionCreator interface {
	Create(context.Context, charlie.CreateSessionInput) (charlie.CreatedSession, error)
}

type CharlieSessionAccess interface {
	ListAccessible(context.Context, uuid.UUID, int32, int32) ([]sqlc.CharlieSession, error)
	CurrentMode(context.Context, uuid.UUID) (charlie.Mode, error)
	Get(context.Context, uuid.UUID, uuid.UUID) (charlie.SessionView, error)
	History(context.Context, uuid.UUID, uuid.UUID, string, int) (json.RawMessage, error)
	Message(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, *charlie.ProductCommandInvocation) (json.RawMessage, error)
	Abort(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	Stream(context.Context, uuid.UUID, uuid.UUID, string, func(charliecontract.Event) error) error
}

type CharlieSessionHandler struct {
	creator CharlieSessionCreator
	access  CharlieSessionAccess
}

func NewCharlieSessionHandler(creator CharlieSessionCreator, access CharlieSessionAccess) *CharlieSessionHandler {
	return &CharlieSessionHandler{creator: creator, access: access}
}

// openapi:request CharlieSessionCreateRequest
type createCharlieSessionRequest struct {
	ClientSessionID  string                    `json:"client_session_id"`
	Intent           string                    `json:"intent"`
	Trigger          string                    `json:"trigger,omitempty"`
	CurrentUIContext string                    `json:"current_ui_context,omitempty"`
	Resources        []charlie.SessionResource `json:"resources,omitempty"`
}

// openapi:request CharlieMessageRequest
type sendCharlieMessageRequest struct {
	ClientMessageID string `json:"client_message_id"`
	Message         string `json:"message"`
}

// openapi:request CharlieAbortRequest
type abortCharlieSessionRequest struct {
	RequestID string `json:"request_id"`
}

type charlieSessionMetadata struct {
	ID                   uuid.UUID `json:"id"`
	ClientSessionID      uuid.UUID `json:"client_session_id"`
	Intent               string    `json:"intent"`
	ResourceScopeSummary string    `json:"resource_scope_summary"`
	State                string    `json:"state"`
	Visibility           string    `json:"visibility"`
	CentralRevision      int64     `json:"central_revision"`
	Source               string    `json:"source"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func safeSessionMetadata(session sqlc.CharlieSession) charlieSessionMetadata {
	return charlieSessionMetadata{
		ID: session.ID, ClientSessionID: session.ClientSessionID, Intent: session.Intent,
		ResourceScopeSummary: session.ResourceScopeSummary, State: session.State,
		Visibility: session.Visibility, CentralRevision: session.CentralRevision,
		Source: session.Source, CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
}

func (h *CharlieSessionHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	var request createCharlieSessionRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	clientID, err := uuid.Parse(request.ClientSessionID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie session request")
		return
	}
	label := strings.TrimSpace(actor.Username)
	if label == "" {
		label = strings.TrimSpace(actor.Email)
	}
	result, err := h.creator.Create(r.Context(), charlie.CreateSessionInput{
		ClientSessionID: clientID, OwnerID: mustUserID(actor), ActorType: "user", ActorLabel: label,
		Intent: request.Intent, Trigger: request.Trigger, CurrentUIContext: request.CurrentUIContext, Resources: request.Resources,
	})
	if err != nil {
		if errors.Is(err, charlie.ErrInvalidSessionRequest) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie session request")
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie session is unavailable")
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	RespondJSON(w, status, map[string]any{"session": safeSessionMetadata(result.Local), "replayed": result.Replayed})
}

func (h *CharlieSessionHandler) List(w http.ResponseWriter, r *http.Request) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return
	}
	offset, ok := boundedQueryInt(w, r, "offset", 0, 0, 100000)
	if !ok {
		return
	}
	limit, ok := boundedQueryInt(w, r, "limit", 20, 1, charlie.MaxCharlieHistoryItems)
	if !ok {
		return
	}
	rows, err := h.access.ListAccessible(r.Context(), mustUserID(actor), int32(offset), int32(limit))
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie session access is denied")
		return
	}
	mode, err := h.access.CurrentMode(r.Context(), mustUserID(actor))
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie mode access is denied")
		return
	}
	result := make([]charlieSessionMetadata, 0, len(rows))
	for _, row := range rows {
		result = append(result, safeSessionMetadata(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"sessions": result, "mode": mode})
}

func (h *CharlieSessionHandler) Get(w http.ResponseWriter, r *http.Request) {
	actor, sessionID, ok := charlieSessionActorAndID(w, r)
	if !ok {
		return
	}
	view, err := h.access.Get(r.Context(), mustUserID(actor), sessionID)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie session access is denied")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{"session": safeSessionMetadata(view.Session), "remote": view.Remote})
}

func (h *CharlieSessionHandler) History(w http.ResponseWriter, r *http.Request) {
	actor, sessionID, ok := charlieSessionActorAndID(w, r)
	if !ok {
		return
	}
	limit, ok := boundedQueryInt(w, r, "limit", 50, 1, charlie.MaxCharlieHistoryItems)
	if !ok {
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if len(cursor) > 128 {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie history cursor")
		return
	}
	history, err := h.access.History(r.Context(), mustUserID(actor), sessionID, cursor, limit)
	if err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Charlie history access is denied")
		return
	}
	history, err = safeCharlieBrowserJSON(history)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie history response is invalid")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(history)
}

func (h *CharlieSessionHandler) Message(w http.ResponseWriter, r *http.Request) {
	actor, sessionID, ok := charlieSessionActorAndID(w, r)
	if !ok {
		return
	}
	var request sendCharlieMessageRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	messageID, err := uuid.Parse(request.ClientMessageID)
	if err != nil || strings.TrimSpace(request.Message) == "" || len([]byte(request.Message)) > charlie.MaxCharlieMessageBytes {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie message request")
		return
	}
	receipt, err := h.access.Message(r.Context(), mustUserID(actor), sessionID, messageID, request.Message, nil)
	if err != nil {
		// Terminal sessions are a client state problem, not bridge outage.
		if strings.Contains(err.Error(), "does not accept messages") {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Charlie session is no longer open for messages")
			return
		}
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie message is unavailable")
		return
	}
	receipt, err = safeCharlieBrowserJSON(receipt)
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie message response is invalid")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write(receipt)
}

func (h *CharlieSessionHandler) Abort(w http.ResponseWriter, r *http.Request) {
	actor, sessionID, ok := charlieSessionActorAndID(w, r)
	if !ok {
		return
	}
	var request abortCharlieSessionRequest
	if !decodeCharlieJSON(w, r, &request) {
		return
	}
	requestID, err := uuid.Parse(request.RequestID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie abort request")
		return
	}
	if err := h.access.Abort(r.Context(), mustUserID(actor), sessionID, requestID); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Charlie session abort is pending")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Events proxies the private Product Bridge stream without buffering or
// persisting event content. The callback acknowledgement happens only after the
// event has been written and flushed to the authenticated browser.
func (h *CharlieSessionHandler) Events(w http.ResponseWriter, r *http.Request) {
	actor, sessionID, ok := charlieSessionActorAndID(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		RespondRequestError(w, r, http.StatusNotImplemented, apierror.InternalError, "Charlie streaming is unavailable")
		return
	}
	lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if len(lastEventID) > 128 || strings.ContainsAny(lastEventID, "\r\n") {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie event cursor")
		return
	}

	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	type delivery struct {
		event charliecontract.Event
		ack   chan error
	}
	deliveries := make(chan delivery)
	finished := make(chan error, 1)
	go func() {
		finished <- h.access.Stream(streamCtx, mustUserID(actor), sessionID, lastEventID, func(event charliecontract.Event) error {
			item := delivery{event: event, ack: make(chan error, 1)}
			select {
			case deliveries <- item:
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
			select {
			case err := <-item.ack:
				return err
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
		})
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": connected\n\n")
	flusher.Flush()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case item := <-deliveries:
			err := writeCharlieSSE(w, item.event)
			if err == nil {
				flusher.Flush()
			}
			item.ack <- err
			if err != nil {
				return
			}
		case err := <-finished:
			if err != nil && r.Context().Err() == nil {
				payload, _ := json.Marshal(map[string]string{"code": "charlie_stream_unavailable", "message": "Charlie event streaming is temporarily unavailable"})
				_ = writeCharlieSSE(w, charliecontract.Event{Event: "charlie.error", Data: payload})
				flusher.Flush()
			}
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writeCharlieSSE(w io.Writer, event charliecontract.Event) error {
	if len(event.ID) > 128 || strings.ContainsAny(event.ID, "\r\n") {
		return fmt.Errorf("invalid Charlie event ID")
	}
	eventName := strings.TrimSpace(event.Event)
	if eventName == "" {
		eventName = "message"
	}
	if len(eventName) > 128 || strings.ContainsAny(eventName, "\r\n") {
		return fmt.Errorf("invalid Charlie event name")
	}
	if event.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", eventName); err != nil {
		return err
	}
	safeData, err := safeCharlieBrowserJSON(event.Data)
	if err != nil {
		return fmt.Errorf("invalid Charlie event data")
	}
	for _, line := range strings.Split(string(safeData), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "\n")
	return err
}

// safeCharlieBrowserJSON converts exact tool arguments into a server-produced
// display summary containing field names only. Exact values remain in Charlie
// and in the signed product action envelope; they never cross the browser API.
func safeCharlieBrowserJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`null`), nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("Charlie browser response contains trailing JSON")
	}
	value = summarizeCharlieToolArguments(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func summarizeCharlieToolArguments(value any) any {
	switch current := value.(type) {
	case []any:
		for index := range current {
			current[index] = summarizeCharlieToolArguments(current[index])
		}
		return current
	case map[string]any:
		clean := make(map[string]any, len(current)+1)
		fields := make([]string, 0, 20)
		hadArguments := false
		for key, item := range current {
			normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(key)), "_", "")
			if normalized == "arguments" || normalized == "exactarguments" {
				hadArguments = true
				if object, ok := item.(map[string]any); ok {
					for field := range object {
						if len(fields) == 20 {
							break
						}
						if len(field) <= 64 && !strings.ContainsAny(field, "\r\n") {
							fields = append(fields, field)
						}
					}
				}
				continue
			}
			if normalized == "argumentsummary" {
				continue
			}
			clean[key] = summarizeCharlieToolArguments(item)
		}
		if hadArguments {
			slices.Sort(fields)
			clean["argument_summary"] = fields
		}
		return clean
	default:
		return value
	}
}

func browserCharlieActor(w http.ResponseWriter, r *http.Request) (*appmiddleware.AuthenticatedUser, bool) {
	actor, authenticated := appmiddleware.GetAuthenticatedUser(r.Context())
	if !authenticated || actor == nil || actor.AuthMethod == "api_token" || mustUserID(actor) == uuid.Nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Browser authentication is required")
		return nil, false
	}
	return actor, true
}

func charlieSessionActorAndID(w http.ResponseWriter, r *http.Request) (*appmiddleware.AuthenticatedUser, uuid.UUID, bool) {
	actor, ok := browserCharlieActor(w, r)
	if !ok {
		return nil, uuid.Nil, false
	}
	sessionID, err := uuid.Parse(chi.URLParam(r, "session_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie session ID")
		return nil, uuid.Nil, false
	}
	return actor, sessionID, true
}

func mustUserID(actor *appmiddleware.AuthenticatedUser) uuid.UUID {
	if actor == nil {
		return uuid.Nil
	}
	parsed, _ := uuid.Parse(actor.ID)
	return parsed
}

func decodeCharlieJSON(w http.ResponseWriter, r *http.Request, output any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		RespondRequestError(w, r, http.StatusUnsupportedMediaType, apierror.InvalidRequest, "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxCharlieSessionRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidBody, "Charlie request is too large")
		} else {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid Charlie request")
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid Charlie request")
		return false
	}
	return true
}

func boundedQueryInt(w http.ResponseWriter, r *http.Request, name string, fallback, minimum, maximum int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid Charlie pagination")
		return 0, false
	}
	return value, true
}
