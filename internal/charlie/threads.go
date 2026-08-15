package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Interactive thread lifecycle (product-local). Sessions remain the authorized
// Charlie agent runs; threads are durable user-facing conversation continuity.
const (
	ThreadStateActive   = "active"
	ThreadStateArchived = "archived"
)

var (
	ErrThreadNotFound        = errors.New("Charlie interactive thread was not found")
	ErrThreadNotOwned        = errors.New("Charlie interactive thread access is denied")
	ErrSessionNotMessageable = errors.New("Charlie session is not messageable")
	ErrThreadInactiveRuntime = errors.New("Charlie runtime is inactive")
	ErrEventCannotOwnThread  = errors.New("event sessions cannot bind interactive threads")
)

// SessionIsMessageable reports whether a product session can accept another
// user message without a continue/new-session. Turn completion must not clear
// this; only terminal session states do.
func SessionIsMessageable(session sqlc.CharlieSession) bool {
	if session.Source != "user" {
		return false
	}
	switch session.State {
	case "active", "waiting_approval":
		return !session.CompletedAt.Valid
	default:
		return false
	}
}

// TruncateThreadTitle bounds first-message text used as a hub title.
// Empty input stays empty so callers can leave an existing title unchanged
// (SetCharlieInteractiveThreadSession only updates title when non-empty).
func TruncateThreadTitle(intent string) string {
	intent = strings.TrimSpace(strings.ReplaceAll(intent, "\n", " "))
	if intent == "" {
		return ""
	}
	if utf8.RuneCountInString(intent) <= 120 {
		return intent
	}
	runes := []rune(intent)
	return string(runes[:117]) + "..."
}

// isContinueableMessageError reports product/bridge failures that mean the
// current live session cannot accept the message and the interactive thread
// should open a fresh session under the same thread (never a blank 409 UX).
func isContinueableMessageError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrSessionNotMessageable) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, frag := range []string{
		"does not accept",
		"no longer open",
		"message is unavailable",
		"session is unavailable",
		"authorization is unavailable",
		"session access is denied",
	} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return false
}

type threadQueries interface {
	GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error)
	GetActiveCharlieInteractiveThread(context.Context, sqlc.GetActiveCharlieInteractiveThreadParams) (sqlc.CharlieInteractiveThread, error)
	GetCharlieInteractiveThread(context.Context, uuid.UUID) (sqlc.CharlieInteractiveThread, error)
	CreateCharlieInteractiveThread(context.Context, sqlc.CreateCharlieInteractiveThreadParams) (sqlc.CharlieInteractiveThread, error)
	ArchiveCharlieInteractiveThread(context.Context, uuid.UUID) (sqlc.CharlieInteractiveThread, error)
	SetCharlieInteractiveThreadSession(context.Context, sqlc.SetCharlieInteractiveThreadSessionParams) (sqlc.CharlieInteractiveThread, error)
	TouchCharlieInteractiveThread(context.Context, uuid.UUID) (sqlc.CharlieInteractiveThread, error)
	ListCharlieInteractiveThreadsForOwner(context.Context, sqlc.ListCharlieInteractiveThreadsForOwnerParams) ([]sqlc.CharlieInteractiveThread, error)
	NextCharlieThreadSessionSequence(context.Context, uuid.UUID) (int32, error)
	AddCharlieThreadSession(context.Context, sqlc.AddCharlieThreadSessionParams) (sqlc.CharlieThreadSession, error)
	ListCharlieThreadSessions(context.Context, uuid.UUID) ([]sqlc.CharlieSession, error)
	BindCharlieSessionThread(context.Context, sqlc.BindCharlieSessionThreadParams) (sqlc.CharlieSession, error)
	GetCharlieSession(context.Context, uuid.UUID) (sqlc.CharlieSession, error)
}

// ThreadSessionCreator creates interactive user sessions (source=user).
type ThreadSessionCreator interface {
	Create(context.Context, CreateSessionInput) (CreatedSession, error)
}

// ThreadMessenger posts a message on an existing session.
type ThreadMessenger interface {
	Message(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, *ProductCommandInvocation) (json.RawMessage, error)
	History(context.Context, uuid.UUID, uuid.UUID, string, int) (json.RawMessage, error)
}

// ThreadService owns interactive thread continuity for one installation binding.
type ThreadService struct {
	queries  threadQueries
	sessions ThreadSessionCreator
	access   ThreadMessenger
	auditor  AuthorityMutationAuditor
	active   func() bool
	now      func() time.Time
}

func NewThreadService(queries threadQueries, sessions ThreadSessionCreator, access ThreadMessenger, auditor AuthorityMutationAuditor, active func() bool) (*ThreadService, error) {
	if queries == nil || sessions == nil || access == nil || auditor == nil || active == nil {
		return nil, fmt.Errorf("Charlie threads require queries, session create, messaging, audit, and activation")
	}
	return &ThreadService{queries: queries, sessions: sessions, access: access, auditor: auditor, active: active, now: time.Now}, nil
}

// ActiveThreadView is the drawer bootstrap payload (metadata only + flags).
type ActiveThreadView struct {
	Thread         sqlc.CharlieInteractiveThread `json:"thread"`
	CurrentSession *sqlc.CharlieSession          `json:"current_session,omitempty"`
	Messageable    bool                          `json:"messageable"`
	NeedsContinue  bool                          `json:"needs_continue"`
	SessionIDs     []uuid.UUID                   `json:"session_ids"`
}

func (s *ThreadService) GetActive(ctx context.Context, ownerID uuid.UUID) (ActiveThreadView, error) {
	if !s.active() {
		return ActiveThreadView{}, ErrThreadInactiveRuntime
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return ActiveThreadView{}, fmt.Errorf("Charlie connection is inactive")
	}
	thread, err := s.queries.GetActiveCharlieInteractiveThread(ctx, sqlc.GetActiveCharlieInteractiveThreadParams{
		ConnectionID: connection.ID, OwnerUserID: ownerID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ActiveThreadView{}, ErrThreadNotFound
	}
	if err != nil {
		return ActiveThreadView{}, err
	}
	return s.viewForThread(ctx, thread)
}

func (s *ThreadService) EnsureActive(ctx context.Context, ownerID uuid.UUID) (ActiveThreadView, error) {
	view, err := s.GetActive(ctx, ownerID)
	if err == nil {
		return view, nil
	}
	if !errors.Is(err, ErrThreadNotFound) {
		return ActiveThreadView{}, err
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return ActiveThreadView{}, fmt.Errorf("Charlie connection is inactive")
	}
	// Thread metadata create is content-free correlation only; session create
	// still records allowlisted charlie.session.created when the first message runs.
	thread, err := s.queries.CreateCharlieInteractiveThread(ctx, sqlc.CreateCharlieInteractiveThreadParams{
		ConnectionID: connection.ID, OwnerUserID: ownerID, Title: "", State: ThreadStateActive,
		CurrentSessionID: pgtype.UUID{},
	})
	if err != nil {
		// Race: another tab created active — re-read.
		view, getErr := s.GetActive(ctx, ownerID)
		if getErr == nil {
			return view, nil
		}
		return ActiveThreadView{}, err
	}
	return ActiveThreadView{Thread: thread, Messageable: false, NeedsContinue: false, SessionIDs: nil}, nil
}

func (s *ThreadService) NewChat(ctx context.Context, ownerID uuid.UUID) (ActiveThreadView, error) {
	if !s.active() {
		return ActiveThreadView{}, ErrThreadInactiveRuntime
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return ActiveThreadView{}, fmt.Errorf("Charlie connection is inactive")
	}
	if existing, err := s.queries.GetActiveCharlieInteractiveThread(ctx, sqlc.GetActiveCharlieInteractiveThreadParams{
		ConnectionID: connection.ID, OwnerUserID: ownerID,
	}); err == nil {
		if _, err := s.queries.ArchiveCharlieInteractiveThread(ctx, existing.ID); err != nil {
			return ActiveThreadView{}, err
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ActiveThreadView{}, err
	}
	thread, err := s.queries.CreateCharlieInteractiveThread(ctx, sqlc.CreateCharlieInteractiveThreadParams{
		ConnectionID: connection.ID, OwnerUserID: ownerID, Title: "", State: ThreadStateActive,
	})
	if err != nil {
		return ActiveThreadView{}, err
	}
	return ActiveThreadView{Thread: thread, Messageable: false, NeedsContinue: false}, nil
}

func (s *ThreadService) List(ctx context.Context, ownerID uuid.UUID, limit, offset int32) ([]sqlc.CharlieInteractiveThread, error) {
	if !s.active() {
		return nil, ErrThreadInactiveRuntime
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	connection, err := s.queries.GetActiveCharlieConnection(ctx)
	if err != nil {
		return nil, fmt.Errorf("Charlie connection is inactive")
	}
	return s.queries.ListCharlieInteractiveThreadsForOwner(ctx, sqlc.ListCharlieInteractiveThreadsForOwnerParams{
		ConnectionID: connection.ID, OwnerUserID: ownerID, PageLimit: limit, PageOffset: offset,
	})
}

// AttachUserSession binds a user session into a thread. Event sessions are rejected.
func (s *ThreadService) AttachUserSession(ctx context.Context, ownerID, threadID, sessionID uuid.UUID, title string) (sqlc.CharlieInteractiveThread, error) {
	thread, err := s.ownedThread(ctx, ownerID, threadID)
	if err != nil {
		return sqlc.CharlieInteractiveThread{}, err
	}
	session, err := s.queries.GetCharlieSession(ctx, sessionID)
	if err != nil {
		return sqlc.CharlieInteractiveThread{}, err
	}
	if session.Source != "user" {
		return sqlc.CharlieInteractiveThread{}, ErrEventCannotOwnThread
	}
	if !session.OwnerUserID.Valid || session.OwnerUserID.Bytes != ownerID {
		return sqlc.CharlieInteractiveThread{}, ErrThreadNotOwned
	}
	seq, err := s.queries.NextCharlieThreadSessionSequence(ctx, thread.ID)
	if err != nil {
		return sqlc.CharlieInteractiveThread{}, err
	}
	if _, err := s.queries.AddCharlieThreadSession(ctx, sqlc.AddCharlieThreadSessionParams{
		ThreadID: thread.ID, SessionID: sessionID, Sequence: seq,
	}); err != nil {
		return sqlc.CharlieInteractiveThread{}, err
	}
	if _, err := s.queries.BindCharlieSessionThread(ctx, sqlc.BindCharlieSessionThreadParams{
		ThreadID: pgtype.UUID{Bytes: thread.ID, Valid: true}, ID: sessionID,
	}); err != nil {
		return sqlc.CharlieInteractiveThread{}, err
	}
	// Empty title must stay empty: TruncateThreadTitle no longer invents
	// "New chat", and SQL only overwrites when title is non-empty.
	return s.queries.SetCharlieInteractiveThreadSession(ctx, sqlc.SetCharlieInteractiveThreadSessionParams{
		CurrentSessionID: pgtype.UUID{Bytes: sessionID, Valid: true},
		Title:            TruncateThreadTitle(title),
		ID:               thread.ID,
	})
}

// SendOnThread creates/continues a session under the active thread and posts the message.
func (s *ThreadService) SendOnThread(ctx context.Context, ownerID uuid.UUID, clientMessageID uuid.UUID, message string, command *ProductCommandInvocation, create CreateSessionInput) (ActiveThreadView, json.RawMessage, error) {
	if !s.active() {
		return ActiveThreadView{}, nil, ErrThreadInactiveRuntime
	}
	message = strings.TrimSpace(message)
	if message == "" || clientMessageID == uuid.Nil {
		return ActiveThreadView{}, nil, ErrInvalidSessionRequest
	}
	view, err := s.EnsureActive(ctx, ownerID)
	if err != nil {
		return ActiveThreadView{}, nil, err
	}
	create.OwnerID = ownerID
	if create.Intent == "" {
		create.Intent = message
	}
	if create.ClientSessionID == uuid.Nil {
		create.ClientSessionID = uuid.New()
	}

	var sessionID uuid.UUID
	if view.Messageable && view.CurrentSession != nil {
		sessionID = view.CurrentSession.ID
	} else {
		// SessionService.Create emits allowlisted session audit; thread membership
		// is product correlation without a separate contentful audit event.
		created, createErr := s.sessions.Create(ctx, create)
		if createErr != nil {
			return ActiveThreadView{}, nil, createErr
		}
		sessionID = created.Local.ID
		title := create.Intent
		if view.Thread.Title != "" {
			title = ""
		}
		if _, attachErr := s.AttachUserSession(ctx, ownerID, view.Thread.ID, sessionID, title); attachErr != nil {
			return ActiveThreadView{}, nil, attachErr
		}
	}

	receipt, err := s.access.Message(ctx, ownerID, sessionID, clientMessageID, message, command)
	if err != nil {
		// Session became unmessageable mid-flight (product terminal state or
		// bridge rejection wrapped as "message is unavailable"): one automatic
		// continue under the same interactive thread.
		if view.Messageable && isContinueableMessageError(err) {
			create.ClientSessionID = uuid.New()
			created, createErr := s.sessions.Create(ctx, create)
			if createErr != nil {
				return ActiveThreadView{}, nil, createErr
			}
			// Preserve hub title: empty title does not overwrite.
			if _, attachErr := s.AttachUserSession(ctx, ownerID, view.Thread.ID, created.Local.ID, ""); attachErr != nil {
				return ActiveThreadView{}, nil, attachErr
			}
			receipt, err = s.access.Message(ctx, ownerID, created.Local.ID, clientMessageID, message, command)
			if err != nil {
				return ActiveThreadView{}, nil, err
			}
			sessionID = created.Local.ID
		} else {
			return ActiveThreadView{}, nil, err
		}
	}
	_, _ = s.queries.TouchCharlieInteractiveThread(ctx, view.Thread.ID)
	out, viewErr := s.GetActive(ctx, ownerID)
	if viewErr != nil {
		// Message succeeded; return partial view.
		return view, receipt, nil
	}
	_ = sessionID
	return out, receipt, nil
}

// StitchedHistory returns ordered redacted history items across thread sessions.
func (s *ThreadService) StitchedHistory(ctx context.Context, ownerID, threadID uuid.UUID, limit int) (json.RawMessage, error) {
	thread, err := s.ownedThread(ctx, ownerID, threadID)
	if err != nil {
		return nil, err
	}
	sessions, err := s.queries.ListCharlieThreadSessions(ctx, thread.ID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	items := make([]json.RawMessage, 0, limit)
	for _, session := range sessions {
		if session.Source != "user" {
			continue
		}
		// Soft-skip a single membership failure so one unavailable central
		// session cannot blank the whole thread transcript after abort.
		raw, histErr := s.access.History(ctx, ownerID, session.ID, "", limit)
		if histErr != nil || len(raw) == 0 || string(raw) == "null" {
			continue
		}
		batch := extractHistoryItems(raw)
		items = append(items, batch...)
		if len(items) >= limit {
			items = items[:limit]
			break
		}
	}
	return json.Marshal(items)
}

func extractHistoryItems(raw json.RawMessage) []json.RawMessage {
	var batch []json.RawMessage
	if json.Unmarshal(raw, &batch) == nil {
		return batch
	}
	var wrapped struct {
		Messages []json.RawMessage `json:"messages"`
		Items    []json.RawMessage `json:"items"`
		Data     []json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &wrapped) == nil {
		switch {
		case len(wrapped.Messages) > 0:
			return wrapped.Messages
		case len(wrapped.Items) > 0:
			return wrapped.Items
		case len(wrapped.Data) > 0:
			return wrapped.Data
		}
	}
	return []json.RawMessage{raw}
}

// ClearCurrentSessionOnAbort updates the active thread when its live session is aborted.
func (s *ThreadService) ClearCurrentSessionOnAbort(ctx context.Context, ownerID, sessionID uuid.UUID) error {
	view, err := s.GetActive(ctx, ownerID)
	if errors.Is(err, ErrThreadNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if view.CurrentSession == nil || view.CurrentSession.ID != sessionID {
		return nil
	}
	_, err = s.queries.SetCharlieInteractiveThreadSession(ctx, sqlc.SetCharlieInteractiveThreadSessionParams{
		CurrentSessionID: pgtype.UUID{}, Title: "", ID: view.Thread.ID,
	})
	return err
}

func (s *ThreadService) ownedThread(ctx context.Context, ownerID, threadID uuid.UUID) (sqlc.CharlieInteractiveThread, error) {
	if !s.active() {
		return sqlc.CharlieInteractiveThread{}, ErrThreadInactiveRuntime
	}
	thread, err := s.queries.GetCharlieInteractiveThread(ctx, threadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.CharlieInteractiveThread{}, ErrThreadNotFound
	}
	if err != nil {
		return sqlc.CharlieInteractiveThread{}, err
	}
	if thread.OwnerUserID != ownerID {
		return sqlc.CharlieInteractiveThread{}, ErrThreadNotOwned
	}
	return thread, nil
}

func (s *ThreadService) viewForThread(ctx context.Context, thread sqlc.CharlieInteractiveThread) (ActiveThreadView, error) {
	sessions, err := s.queries.ListCharlieThreadSessions(ctx, thread.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ActiveThreadView{}, err
	}
	ids := make([]uuid.UUID, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	view := ActiveThreadView{Thread: thread, SessionIDs: ids}
	if thread.CurrentSessionID.Valid {
		session, err := s.queries.GetCharlieSession(ctx, thread.CurrentSessionID.Bytes)
		if err == nil {
			view.CurrentSession = &session
			view.Messageable = SessionIsMessageable(session)
			view.NeedsContinue = !view.Messageable
			return view, nil
		}
	}
	// Prefer last membership session if pointer missing.
	if len(sessions) > 0 {
		last := sessions[len(sessions)-1]
		view.CurrentSession = &last
		view.Messageable = SessionIsMessageable(last)
		view.NeedsContinue = !view.Messageable
	} else {
		view.NeedsContinue = false
		view.Messageable = false
	}
	return view, nil
}

// EventSessionsMustNotBindThread is a compile-time documentation helper used in tests.
func EventSessionsMustNotBindThread(source string) error {
	if source == "event" {
		return ErrEventCannotOwnThread
	}
	return nil
}
