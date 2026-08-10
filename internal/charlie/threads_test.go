package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSessionIsMessageableOnlyForOpenUserSessions(t *testing.T) {
	userActive := sqlc.CharlieSession{Source: "user", State: "active"}
	if !SessionIsMessageable(userActive) {
		t.Fatal("active user session must be messageable")
	}
	waiting := sqlc.CharlieSession{Source: "user", State: "waiting_approval"}
	if !SessionIsMessageable(waiting) {
		t.Fatal("waiting_approval user session must be messageable")
	}
	completed := sqlc.CharlieSession{
		Source: "user", State: "active",
		CompletedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	if SessionIsMessageable(completed) {
		t.Fatal("completed_at must make session not messageable")
	}
	aborted := sqlc.CharlieSession{Source: "user", State: "aborted"}
	if SessionIsMessageable(aborted) {
		t.Fatal("aborted session must not be messageable")
	}
	event := sqlc.CharlieSession{Source: "event", State: "active"}
	if SessionIsMessageable(event) {
		t.Fatal("event sessions must never be interactive-messageable")
	}
}

func TestEventSessionsMustNotBindThread(t *testing.T) {
	if err := EventSessionsMustNotBindThread("event"); !errors.Is(err, ErrEventCannotOwnThread) {
		t.Fatalf("event bind = %v", err)
	}
	if err := EventSessionsMustNotBindThread("user"); err != nil {
		t.Fatalf("user bind = %v", err)
	}
}

func TestTruncateThreadTitle(t *testing.T) {
	if TruncateThreadTitle("  hi\nthere ") != "hi there" {
		t.Fatalf("got %q", TruncateThreadTitle("  hi\nthere "))
	}
	if TruncateThreadTitle("   ") != "" {
		t.Fatal("empty title must stay empty so continue does not wipe hub titles")
	}
	runes := make([]rune, 200)
	for i := range runes {
		runes[i] = 'a'
	}
	got := TruncateThreadTitle(string(runes))
	if len([]rune(got)) != 120 {
		t.Fatalf("title length = %d want 120 (%q)", len([]rune(got)), got)
	}
}

func TestIsContinueableMessageError(t *testing.T) {
	if !isContinueableMessageError(fmt.Errorf("Charlie message is unavailable")) {
		t.Fatal("bridge wrap must continue")
	}
	if !isContinueableMessageError(fmt.Errorf("Charlie session does not accept messages")) {
		t.Fatal("product terminal must continue")
	}
	if isContinueableMessageError(fmt.Errorf("invalid body")) {
		t.Fatal("validation errors must not continue")
	}
}

type threadQueryFake struct {
	connection sqlc.CharlieConnection
	threads    map[uuid.UUID]sqlc.CharlieInteractiveThread
	sessions   map[uuid.UUID]sqlc.CharlieSession
	membership map[uuid.UUID][]sqlc.CharlieSession
	seq        map[uuid.UUID]int32
}

func (f *threadQueryFake) GetActiveCharlieConnection(context.Context) (sqlc.CharlieConnection, error) {
	if f.connection.ID == uuid.Nil {
		return sqlc.CharlieConnection{}, pgx.ErrNoRows
	}
	return f.connection, nil
}
func (f *threadQueryFake) GetActiveCharlieInteractiveThread(_ context.Context, arg sqlc.GetActiveCharlieInteractiveThreadParams) (sqlc.CharlieInteractiveThread, error) {
	key := arg.ConnectionID.String() + ":" + arg.OwnerUserID.String()
	for _, thread := range f.threads {
		if thread.State == ThreadStateActive && thread.ConnectionID == arg.ConnectionID && thread.OwnerUserID == arg.OwnerUserID {
			return thread, nil
		}
	}
	_ = key
	return sqlc.CharlieInteractiveThread{}, pgx.ErrNoRows
}
func (f *threadQueryFake) GetCharlieInteractiveThread(_ context.Context, id uuid.UUID) (sqlc.CharlieInteractiveThread, error) {
	thread, ok := f.threads[id]
	if !ok {
		return sqlc.CharlieInteractiveThread{}, pgx.ErrNoRows
	}
	return thread, nil
}
func (f *threadQueryFake) CreateCharlieInteractiveThread(_ context.Context, arg sqlc.CreateCharlieInteractiveThreadParams) (sqlc.CharlieInteractiveThread, error) {
	// enforce one active
	for _, thread := range f.threads {
		if thread.State == ThreadStateActive && thread.ConnectionID == arg.ConnectionID && thread.OwnerUserID == arg.OwnerUserID {
			return sqlc.CharlieInteractiveThread{}, errors.New("duplicate active thread")
		}
	}
	thread := sqlc.CharlieInteractiveThread{
		ID: uuid.New(), ConnectionID: arg.ConnectionID, OwnerUserID: arg.OwnerUserID,
		Title: arg.Title, State: arg.State, CurrentSessionID: arg.CurrentSessionID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if f.threads == nil {
		f.threads = map[uuid.UUID]sqlc.CharlieInteractiveThread{}
	}
	f.threads[thread.ID] = thread
	return thread, nil
}
func (f *threadQueryFake) ArchiveCharlieInteractiveThread(_ context.Context, id uuid.UUID) (sqlc.CharlieInteractiveThread, error) {
	thread, ok := f.threads[id]
	if !ok || thread.State != ThreadStateActive {
		return sqlc.CharlieInteractiveThread{}, pgx.ErrNoRows
	}
	thread.State = ThreadStateArchived
	thread.ArchivedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	f.threads[id] = thread
	return thread, nil
}
func (f *threadQueryFake) SetCharlieInteractiveThreadSession(_ context.Context, arg sqlc.SetCharlieInteractiveThreadSessionParams) (sqlc.CharlieInteractiveThread, error) {
	thread, ok := f.threads[arg.ID]
	if !ok {
		return sqlc.CharlieInteractiveThread{}, pgx.ErrNoRows
	}
	thread.CurrentSessionID = arg.CurrentSessionID
	if arg.Title != "" {
		thread.Title = arg.Title
	}
	thread.UpdatedAt = time.Now().UTC()
	f.threads[arg.ID] = thread
	return thread, nil
}
func (f *threadQueryFake) TouchCharlieInteractiveThread(_ context.Context, id uuid.UUID) (sqlc.CharlieInteractiveThread, error) {
	thread, ok := f.threads[id]
	if !ok {
		return sqlc.CharlieInteractiveThread{}, pgx.ErrNoRows
	}
	thread.UpdatedAt = time.Now().UTC()
	f.threads[id] = thread
	return thread, nil
}
func (f *threadQueryFake) ListCharlieInteractiveThreadsForOwner(_ context.Context, arg sqlc.ListCharlieInteractiveThreadsForOwnerParams) ([]sqlc.CharlieInteractiveThread, error) {
	var out []sqlc.CharlieInteractiveThread
	for _, thread := range f.threads {
		if thread.ConnectionID == arg.ConnectionID && thread.OwnerUserID == arg.OwnerUserID {
			out = append(out, thread)
		}
	}
	return out, nil
}
func (f *threadQueryFake) NextCharlieThreadSessionSequence(_ context.Context, threadID uuid.UUID) (int32, error) {
	if f.seq == nil {
		f.seq = map[uuid.UUID]int32{}
	}
	f.seq[threadID]++
	return f.seq[threadID], nil
}
func (f *threadQueryFake) AddCharlieThreadSession(_ context.Context, arg sqlc.AddCharlieThreadSessionParams) (sqlc.CharlieThreadSession, error) {
	session := f.sessions[arg.SessionID]
	if f.membership == nil {
		f.membership = map[uuid.UUID][]sqlc.CharlieSession{}
	}
	f.membership[arg.ThreadID] = append(f.membership[arg.ThreadID], session)
	return sqlc.CharlieThreadSession{ThreadID: arg.ThreadID, SessionID: arg.SessionID, Sequence: arg.Sequence}, nil
}
func (f *threadQueryFake) ListCharlieThreadSessions(_ context.Context, threadID uuid.UUID) ([]sqlc.CharlieSession, error) {
	return append([]sqlc.CharlieSession(nil), f.membership[threadID]...), nil
}
func (f *threadQueryFake) BindCharlieSessionThread(_ context.Context, arg sqlc.BindCharlieSessionThreadParams) (sqlc.CharlieSession, error) {
	session, ok := f.sessions[arg.ID]
	if !ok {
		return sqlc.CharlieSession{}, pgx.ErrNoRows
	}
	session.ThreadID = arg.ThreadID
	f.sessions[arg.ID] = session
	return session, nil
}
func (f *threadQueryFake) GetCharlieSession(_ context.Context, id uuid.UUID) (sqlc.CharlieSession, error) {
	session, ok := f.sessions[id]
	if !ok {
		return sqlc.CharlieSession{}, pgx.ErrNoRows
	}
	return session, nil
}

type threadSessionCreatorFake struct {
	created []CreatedSession
	err     error
}

func (f *threadSessionCreatorFake) Create(_ context.Context, input CreateSessionInput) (CreatedSession, error) {
	if f.err != nil {
		return CreatedSession{}, f.err
	}
	session := sqlc.CharlieSession{
		ID: uuid.New(), ClientSessionID: input.ClientSessionID, Source: "user", Visibility: "private",
		State: "active", Intent: input.Intent,
		OwnerUserID: pgtype.UUID{Bytes: input.OwnerID, Valid: true},
	}
	out := CreatedSession{Local: session}
	f.created = append(f.created, out)
	return out, nil
}

type threadMessengerFake struct {
	messages       []string
	historyBySess  map[uuid.UUID]json.RawMessage
	historyErr     map[uuid.UUID]error
	messageErr     error
	messageErrOnce error // consumed once then cleared
	history        json.RawMessage
	err            error
}

func (f *threadMessengerFake) Message(_ context.Context, _, sessionID, _ uuid.UUID, message string) (json.RawMessage, error) {
	if f.messageErrOnce != nil {
		err := f.messageErrOnce
		f.messageErrOnce = nil
		return nil, err
	}
	if f.messageErr != nil {
		return nil, f.messageErr
	}
	if f.err != nil {
		return nil, f.err
	}
	f.messages = append(f.messages, message)
	_ = sessionID
	return json.RawMessage(`{"accepted":true}`), nil
}
func (f *threadMessengerFake) History(_ context.Context, _, sessionID uuid.UUID, _ string, _ int) (json.RawMessage, error) {
	if f.historyErr != nil {
		if err, ok := f.historyErr[sessionID]; ok {
			return nil, err
		}
	}
	if f.historyBySess != nil {
		if raw, ok := f.historyBySess[sessionID]; ok {
			return raw, nil
		}
	}
	if f.history == nil {
		return json.RawMessage(`[]`), nil
	}
	return f.history, nil
}

type threadAuditorFake struct{ actions []string }

func (a *threadAuditorFake) RecordCharlieSessionLifecycle(_ context.Context, audit SessionLifecycleAudit) error {
	a.actions = append(a.actions, audit.Action)
	return nil
}
func (a *threadAuditorFake) RecordCharlieAuthorityMutation(_ context.Context, audit AuthorityMutationAudit) error {
	a.actions = append(a.actions, audit.Action)
	return nil
}

func newThreadServiceFixture(t *testing.T) (*ThreadService, *threadQueryFake, *threadSessionCreatorFake, *threadMessengerFake, uuid.UUID) {
	t.Helper()
	owner := uuid.New()
	conn := sqlc.CharlieConnection{ID: uuid.New(), Active: true}
	queries := &threadQueryFake{connection: conn, threads: map[uuid.UUID]sqlc.CharlieInteractiveThread{}, sessions: map[uuid.UUID]sqlc.CharlieSession{}}
	sessions := &threadSessionCreatorFake{}
	// Hook create into query store so Attach can find sessions.
	sessionsWrapped := &threadSessionCreatorFake{}
	messenger := &threadMessengerFake{}
	auditor := &threadAuditorFake{}
	svc, err := NewThreadService(queries, sessionsWrapped, messenger, auditor, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	// Replace creator with one that registers sessions on the fake store.
	svc.sessions = createAndStore{store: queries, inner: sessions}
	_ = sessions
	return svc, queries, sessions, messenger, owner
}

type createAndStore struct {
	store *threadQueryFake
	inner *threadSessionCreatorFake
}

func (c createAndStore) Create(ctx context.Context, input CreateSessionInput) (CreatedSession, error) {
	out, err := c.inner.Create(ctx, input)
	if err != nil {
		return out, err
	}
	if c.store.sessions == nil {
		c.store.sessions = map[uuid.UUID]sqlc.CharlieSession{}
	}
	c.store.sessions[out.Local.ID] = out.Local
	return out, nil
}

func TestThreadServiceNewChatArchivesPreviousActive(t *testing.T) {
	svc, queries, _, _, owner := newThreadServiceFixture(t)
	first, err := svc.EnsureActive(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.NewChat(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if first.Thread.ID == second.Thread.ID {
		t.Fatal("new chat must create a distinct active thread")
	}
	if second.Thread.State != ThreadStateActive {
		t.Fatalf("state=%s", second.Thread.State)
	}
	archived := 0
	active := 0
	for _, thread := range queries.threads {
		if thread.OwnerUserID != owner {
			continue
		}
		switch thread.State {
		case ThreadStateArchived:
			archived++
		case ThreadStateActive:
			active++
		}
	}
	if archived != 1 || active != 1 {
		t.Fatalf("archived=%d active=%d", archived, active)
	}
}

func TestThreadServiceSendCreatesSessionAndContinueWhenNotMessageable(t *testing.T) {
	svc, queries, sessions, messenger, owner := newThreadServiceFixture(t)
	// First send creates session under thread.
	view, receipt, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "what version of k8s", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "what version of k8s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(receipt) || len(messenger.messages) != 1 {
		t.Fatalf("receipt/messages failed: %s %v", receipt, messenger.messages)
	}
	if view.CurrentSession == nil || !view.Messageable {
		t.Fatalf("expected messageable session: %+v", view)
	}
	if len(sessions.created) != 1 {
		t.Fatalf("created sessions=%d", len(sessions.created))
	}
	// Terminalize current session → needs continue.
	sess := *view.CurrentSession
	sess.State = "aborted"
	queries.sessions[sess.ID] = sess
	view2, _, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "follow up", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "follow up",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions.created) != 2 {
		t.Fatalf("continue should create second session, got %d", len(sessions.created))
	}
	if view2.Thread.ID != view.Thread.ID {
		t.Fatal("continue must stay on the same thread")
	}
	if len(queries.membership[view.Thread.ID]) != 2 {
		t.Fatalf("membership=%d", len(queries.membership[view.Thread.ID]))
	}
}

func TestThreadServiceAttachRejectsEventSessions(t *testing.T) {
	svc, queries, _, _, owner := newThreadServiceFixture(t)
	view, err := svc.EnsureActive(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	eventID := uuid.New()
	queries.sessions[eventID] = sqlc.CharlieSession{
		ID: eventID, Source: "event", Visibility: "incident", State: "active",
	}
	if _, err := svc.AttachUserSession(context.Background(), owner, view.Thread.ID, eventID, "nope"); !errors.Is(err, ErrEventCannotOwnThread) {
		t.Fatalf("attach event = %v", err)
	}
}

func TestThreadServiceGetActiveNotFound(t *testing.T) {
	svc, _, _, _, owner := newThreadServiceFixture(t)
	if _, err := svc.GetActive(context.Background(), owner); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestThreadServiceContinuePreservesTitle(t *testing.T) {
	svc, queries, sessions, _, owner := newThreadServiceFixture(t)
	view, _, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "what version of k8s", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "what version of k8s",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantTitle := TruncateThreadTitle("what version of k8s")
	if view.Thread.Title != wantTitle {
		t.Fatalf("initial title=%q want %q", view.Thread.Title, wantTitle)
	}
	// Terminalize → continue under same thread.
	sess := *view.CurrentSession
	sess.State = "aborted"
	queries.sessions[sess.ID] = sess
	view2, _, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "follow up", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "follow up",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions.created) != 2 {
		t.Fatalf("created=%d", len(sessions.created))
	}
	if view2.Thread.Title != wantTitle {
		t.Fatalf("title wiped on continue: got %q want %q", view2.Thread.Title, wantTitle)
	}
	// Empty attach must not invent "New chat".
	if TruncateThreadTitle("") != "" {
		t.Fatal("empty truncate still invents a title")
	}
}

func TestThreadServiceStitchedHistoryAcrossAbortedAndActive(t *testing.T) {
	svc, queries, _, messenger, owner := newThreadServiceFixture(t)
	view, _, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "first", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	abortedID := view.CurrentSession.ID
	// Abort first session and continue with a second.
	aborted := *view.CurrentSession
	aborted.State = "aborted"
	queries.sessions[abortedID] = aborted
	view2, _, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "second", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	activeID := view2.CurrentSession.ID
	if activeID == abortedID {
		t.Fatal("continue must create a distinct session")
	}
	// History for aborted + active membership (simulates History allowing terminal).
	messenger.historyBySess = map[uuid.UUID]json.RawMessage{
		abortedID: json.RawMessage(`[
			{"item_id":"u1","kind":"user_message","redacted_content":"first"},
			{"item_id":"a1","kind":"assistant_message","redacted_content":"answer-one"}
		]`),
		activeID: json.RawMessage(`[
			{"item_id":"u2","kind":"user_message","redacted_content":"second"}
		]`),
	}
	raw, err := svc.StitchedHistory(context.Background(), owner, view.Thread.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("stitch json: %v %s", err, raw)
	}
	if len(items) != 3 {
		t.Fatalf("stitched items=%d want 3 body=%s", len(items), raw)
	}
	// Soft-skip: if aborted history fails, still return active membership turns.
	messenger.historyErr = map[uuid.UUID]error{abortedID: fmt.Errorf("Charlie session is unavailable")}
	raw2, err := svc.StitchedHistory(context.Background(), owner, view.Thread.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var items2 []map[string]any
	if err := json.Unmarshal(raw2, &items2); err != nil {
		t.Fatal(err)
	}
	if len(items2) != 1 {
		t.Fatalf("soft-skip stitch items=%d want 1 body=%s", len(items2), raw2)
	}
}

func TestThreadServiceMidFlightBridgeFailureContinues(t *testing.T) {
	svc, _, sessions, messenger, owner := newThreadServiceFixture(t)
	view, _, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "open", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "open",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Messageable || view.CurrentSession == nil {
		t.Fatal("expected messageable session after first send")
	}
	// Product still thinks messageable, but bridge rejects (desync / central closed).
	messenger.messageErrOnce = fmt.Errorf("Charlie message is unavailable")
	view2, _, err := svc.SendOnThread(context.Background(), owner, uuid.New(), "retry", CreateSessionInput{
		ActorType: "user", ActorLabel: "admin", Intent: "retry",
	})
	if err != nil {
		t.Fatalf("continue on bridge failure: %v", err)
	}
	if len(sessions.created) != 2 {
		t.Fatalf("expected second session after bridge failure, got %d", len(sessions.created))
	}
	if view2.Thread.ID != view.Thread.ID {
		t.Fatal("must stay on same interactive thread")
	}
	if view2.Thread.Title != TruncateThreadTitle("open") {
		t.Fatalf("title after bridge-continue=%q", view2.Thread.Title)
	}
}
