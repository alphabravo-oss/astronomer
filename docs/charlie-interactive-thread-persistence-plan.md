# Charlie interactive thread persistence and dual-track sessions

**Status:** implemented; live qualification remains release-gated
**Date:** 2026-08-07  
**Owners:** Astronomer product Charlie integration + Charlie product agent / central  
**Related:**  
- `astronomer/docs/archive/pre-v1/plans/charlie-agent-integration-plan.md` (historical A-series and `charlie_sessions` ownership)
- `charlie/docs/product-agent-integration-platform-plan.md` (C-series, agent sessions, retention)  
- Live UX: drawer unmount + deliberate non-restore (see `frontend/src/components/charlie/charlie-shell.tsx`)

This document is the single checklist for implementing ChatGPT/Grok-style
**persistent interactive chat** while keeping **system/autonomous investigations**
on a separate track. An implementer or goal-run agent should complete every
checkbox, in order where dependencies require, and record evidence under the
Validation section.

---

## 0. Problem statement

### 0.1 User-visible bug / gap

Today, closing the Charlie drawer and reopening it presents a **blank chat** for
the same user. Answers, multi-turn context, and the live session are gone from
the UI even though:

- Charlie central may still hold encrypted history for a period (1–30 days).
- Astronomer may still have a `charlie_sessions` row (often still `active` after
  a turn, or terminal after abort/complete).
- The Charlie hub already lists private conversations and incident investigations.

Users expect drawer chat to behave like ChatGPT/Grok:

1. Close UI → browse → reopen → **same conversation continues**.
2. **New chat** is the only routine way to start fresh.
3. **Abort** is a stronger control (revoke authority / stop work), not “hide”.
4. System-driven agent work does **not** hijack or wipe the human chat.

### 0.2 Why the current code does this

| Cause | Detail |
| --- | --- |
| Unmount | `{open && <CharlieDrawer />}` destroys local `sessionId` and optimistic state on close. |
| No restore | Drawer deliberately never auto-restores sessions after 409s when product DB said `active` but Charlie no longer accepted messages. |
| Session = chat | One `charlie_sessions` / `agent_sessions` row is treated as both **UI continuity** and **authorized agent run**. When the run ends, the “chat” feels dead. |
| Mixed sources | `source user\|event` and `visibility private\|incident` exist, but hub and drawer do not treat them as two products with different lifecycle tables/APIs. |

### 0.3 Goals

- [ ] **G1** Interactive drawer chat persists for the same user across close/reopen, refresh, and multi-tab (last-writer-wins on active thread).
- [ ] **G2** **New chat** is the only default user action that starts an empty interactive thread.
- [ ] **G3** Closing the drawer never aborts work and never clears the active interactive thread.
- [ ] **G4** User interactive threads and system/autonomous investigations are **separately tracked**, listed, retained, and authorized.
- [ ] **G5** Resume never 409s: either reattach a still-open Charlie session, or **continue** under the same thread with a new live session + preserved history presentation.
- [ ] **G6** No prompt/tool content is stored in Astronomer beyond existing redacted history-via-bridge rules; ownership matrix in the integration plan remains true.
- [ ] **G7** Full automated + live validation with checkable evidence.

### 0.4 Non-goals (this plan)

- [ ] N1 Downstream cluster shell / kubectl via Charlie (still v1 management-plane MCP only).
- [ ] N2 Full ChatGPT-style infinite multi-thread sidebar inside the drawer (v1 keeps **one active interactive thread per user** and a bounded recent-conversation picker; the hub remains the full archive surface).
- [ ] N3 Merging autonomous investigation transcripts into the user’s open chat automatically.
- [ ] N4 Changing Charlie content encryption, retention purge engines, or finding workflow semantics beyond classification/correlation.
- [ ] N5 Public multi-tenant “share this chat” links.

### 0.5 Decisions locked for v1 (change only via explicit plan amendment)

| ID | Decision |
| --- | --- |
| D1 | **Thread** = durable user-facing conversation continuity. **Session** = one authorized Charlie agent run (bridge session). **Investigation** = system/event-driven work (not the drawer’s active chat). |
| D2 | Exactly **one active interactive thread** per `(installation, user)`. |
| D3 | Drawer always binds to that active thread; never auto-binds to investigations. |
| D4 | On reopen: if current live session accepts messages → reattach. Else show history and **auto-continue** (new session under same thread) on next send, or transparently on open if product policy prefers (see P3-C). Default implement: **reattach if open, else history-ready; first send continues**. |
| D5 | Route/UI context chips **refresh from current page** on open; conversation history is preserved; removed chips stay removed until user re-adds. |
| D6 | Hub tabs: **My chats** (interactive threads) vs **Investigations** (event/system). Findings remain on Findings tab. |
| D7 | Prefer **new product tables** for threads rather than overloading `charlie_sessions` forever; keep `charlie_sessions` as the per-run authorization row. |
| D8 | Charlie central may remain session-centric; product maps **thread → ordered sessions**. Continuity for the model is product-orchestrated (reattach or continue with bounded prior context if Charlie requires a new session). |
| D9 | Minimal LOC preferred, but **correct lifecycle > clever flags**. Separate tables when a flag would create dual semantics on every query. |
| D10 | The drawer may browse an owner-scoped archived thread, but that transcript is read-only and is never silently injected into the active thread or a new Charlie turn. |

---

## 1. Domain model

### 1.1 Three concepts

```
┌─────────────────────────────────────────────────────────────────┐
│ Interactive THREAD (user-facing continuity)                     │
│  - one active per user                                          │
│  - title, updated_at, archived_at                               │
│  - points at current live SESSION (nullable if needs continue)  │
│  - ordered SESSION membership for history stitching             │
└───────────────────────────┬─────────────────────────────────────┘
                            │ 1..n
┌───────────────────────────▼─────────────────────────────────────┐
│ SESSION (authorized Charlie agent run)                          │
│  - existing charlie_sessions + central agent_sessions           │
│  - source=user for interactive; source=event for system         │
│  - state machine: creating|active|waiting_approval|…            │
│  - holds delegations, receipts, cursor                          │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ INVESTIGATION (system/autonomous track)                         │
│  - trigger/fingerprint driven                                   │
│  - links to session(s) with source=event                        │
│  - findings, alerts, not drawer active thread                   │
│  - may later offer “Ask about this” → forks a user thread       │
└─────────────────────────────────────────────────────────────────┘
```

### 1.2 Why not only `source` on `charlie_sessions`?

`source` and `visibility` already exist, but:

1. **UI continuity** needs a stable ID that survives session terminal states.
2. Multiple sessions may belong to one conversation after continue/resume.
3. Hub list for “my chats” should be **threads**, not every failed create attempt.
4. Autonomous work needs different listing, TTL, and “open in drawer” rules.

Flags alone force every query to re-express “is this the user’s current chat?”.

### 1.3 State vocabulary

#### Interactive thread (product-local)

| State | Meaning |
| --- | --- |
| `active` | Shown when drawer opens; receives New messages / Continue. |
| `archived` | Previous chat after New chat; read-only in hub; can “Resume as new active” later (optional v1.1). |

#### Session (existing, both sides)

Keep product states: `creating`, `active`, `waiting_approval`, `completed`, `aborted`, `failed`.  
Cursor rule (already fixed): **turn.\*** does not complete the session; only **session.\*** / abort does.

**Messageable session** (product definition used by resume):

- product state ∈ {`active`, `waiting_approval`}
- Charlie bridge get-session / message probe does not return terminal/closed
- active unexpired delegation for owner (or re-issue on continue)
- connection mode not `disabled` / emergency

#### Investigation (product-local classification)

| State | Meaning |
| --- | --- |
| `open` | Work in progress / has open findings |
| `resolved` | Findings resolved/dismissed and session terminal |
| `expired` | Past retention |

v1 may implement investigation as a **view** over `source=event` sessions + findings rather than a full new table if that ships faster—but the **API and hub must not mix** them with interactive threads. Prefer a real `charlie_investigations` table if any second metadata field is needed (fingerprint, trigger rule id).

---

## 2. Architecture and ownership

### 2.1 Ownership matrix (unchanged principles)

| Data | Owner | Store |
| --- | --- | --- |
| Thread metadata (title, active pointer, archive) | Astronomer | New product tables |
| Session auth metadata | Astronomer | `charlie_sessions` (+ resources, delegations) |
| Message bodies / tool results / model text | Charlie | Encrypted `agent_sessions` / messages |
| Browser-visible history | Charlie via bridge | Redacted history API; product re-auth on every get |
| Investigation / finding cards | Split as today | `charlie_findings` + Charlie findings |

### 2.2 Control flow: drawer open

```
User opens drawer
  → GET /api/v1/charlie/threads/active  (or embed in overview)
  → if no active thread: empty composer (no Charlie session yet)
  → if active thread:
       load thread.sessions ordered
       for current_session_id:
         if messageable → set sessionId, subscribe SSE, load history
         else → load stitched history (all sessions under thread),
                mark needsContinue=true, composer enabled
  → first send:
       if no thread → create thread + session + message
       if needsContinue → create new session under thread, message
       else → POST message on current session
```

### 2.3 Control flow: New chat

```
POST /api/v1/charlie/threads/new
  → archive previous active thread (if any)
  → create empty active thread (no Charlie session until first message)
  → drawer clears transcript UI
  → does NOT abort previous Charlie session by default
     (optional: soft-complete previous live session; do not revoke unless Abort)
```

**Abort** remains session-scoped (or “abort current live session under thread”) and revokes product delegation.

### 2.4 Control flow: autonomous investigation

```
Trigger / alert path (unchanged entry)
  → create session with source=event, visibility=incident (or private service)
  → link trigger_event.session_id
  → create/update investigation row if table exists
  → findings as today
  → NEVER update user's active interactive thread pointer
```

### 2.5 Charlie-side implications

Charlie central can stay session-centric. Product must:

- [ ] Pass a stable **client_session_id** per live run (existing).
- [ ] Optionally pass **thread_id** (or product conversation id) as a non-authoritative attribute in product context for logging/correlation only—not for RBAC.
- [ ] On continue: new CreateSession with **fresh** authorization_ref; history for the model is either:
  - **(Preferred v1)** Charlie still has prior session history only if reattached; continue does **not** replay full ciphertext into a new session automatically unless Charlie adds an explicit “fork with summary” API; UI still shows stitched redacted history to the human.
  - **(v1.1)** Charlie API to continue conversation with prior session id under same integration with policy checks.

**v1 model continuity:** reattach when possible. On continue-with-new-session, the **human** sees full stitched history; the **model** starts with product context + optional last-N redacted turns if Charlie/product already support message seeding (only if existing APIs allow without expanding content into Astronomer DB). Do **not** store full transcript in Postgres product tables.

If model continuity on continue is required in v1, implement a **Charlie-side** “link prior session for retrieval” or inject last-N turns only through the existing bridge history read into the **agent turn request** (Charlie already holds content)—never copy into Astronomer.

### 2.6 Component touch map

| Area | Repo | Files / surfaces (indicative) |
| --- | --- | --- |
| Migration threads | astronomer | `internal/db/migrations/155_charlie_interactive_threads*.sql` |
| SQLC queries | astronomer | `internal/db/queries/charlie.sql` |
| Session + thread services | astronomer | `internal/charlie/sessions.go`, new `threads.go`, `session_access.go` |
| HTTP handlers/routes | astronomer | `internal/handler/charlie_sessions.go`, new thread handlers, `routes_charlie.go` |
| Drawer UI | astronomer | `frontend/src/components/charlie/charlie-shell.tsx`, `lib/api/charlie.ts` |
| Hub UI | astronomer | `frontend/src/routes/dashboard/charlie/index.tsx` |
| Triggers / event sessions | astronomer | `internal/charlie/trigger*.go`, `event_runtime.go` — ensure no active-thread mutation |
| Bridge create session | astronomer | `bridge_adapter.go` (optional attributes only) |
| Agent session lifecycle | charlie | only if continue/fork API or multi-turn completion policy needs adjustment |
| OpenCode engine | charlie | only if system prompt must distinguish interactive vs investigation |
| Tests | both | unit, handler, frontend vitest, live E2E |

---

## 3. Data model (Astronomer)

### 3.1 New table: `charlie_interactive_threads`

```sql
-- Conceptual; implement as migration 155+ with down migration and tests.
CREATE TABLE charlie_interactive_threads (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  connection_id     UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
  owner_user_id     UUID NOT NULL REFERENCES users(id),  -- or platform user FK as existing pattern
  title             VARCHAR(256) NOT NULL DEFAULT '',
  state             VARCHAR(32) NOT NULL,  -- active | archived
  current_session_id UUID NULL REFERENCES charlie_sessions(id) ON DELETE SET NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at       TIMESTAMPTZ NULL,
  CONSTRAINT charlie_interactive_threads_state_chk
    CHECK (state IN ('active', 'archived'))
);

-- At most one active thread per user per connection (installation binding).
CREATE UNIQUE INDEX charlie_interactive_threads_one_active
  ON charlie_interactive_threads (connection_id, owner_user_id)
  WHERE state = 'active';

CREATE INDEX charlie_interactive_threads_owner_updated
  ON charlie_interactive_threads (owner_user_id, updated_at DESC);
```

### 3.2 New table: `charlie_thread_sessions`

Membership of sessions under a thread (ordered continuity).

```sql
CREATE TABLE charlie_thread_sessions (
  thread_id   UUID NOT NULL REFERENCES charlie_interactive_threads(id) ON DELETE CASCADE,
  session_id  UUID NOT NULL REFERENCES charlie_sessions(id) ON DELETE CASCADE,
  sequence    INT NOT NULL,  -- 1..n increasing
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (thread_id, session_id),
  UNIQUE (thread_id, sequence)
);
```

### 3.3 Optional table: `charlie_investigations` (recommended if metadata grows)

If v1 keeps investigations as a filtered session list only, document that and skip
this table—but still separate API list endpoints.

```sql
CREATE TABLE charlie_investigations (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  connection_id   UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
  session_id      UUID REFERENCES charlie_sessions(id) ON DELETE SET NULL,
  trigger_event_id UUID NULL, -- FK if exists
  fingerprint     VARCHAR(128) NOT NULL DEFAULT '',
  title           VARCHAR(256) NOT NULL DEFAULT '',
  state           VARCHAR(32) NOT NULL, -- open | resolved | expired
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### 3.4 Changes to existing `charlie_sessions`

- [ ] Keep `source` / `visibility` as today.
- [ ] Add nullable `thread_id UUID REFERENCES charlie_interactive_threads(id) ON DELETE SET NULL` for reverse lookup convenience (optional if membership table is enough).
- [ ] Enforce: `source='user'` sessions used by drawer **must** appear in `charlie_thread_sessions` when created via interactive path.
- [ ] Enforce: `source='event'` sessions **must not** set a user’s active interactive thread.

### 3.5 Retention

| Record | Retention |
| --- | --- |
| Interactive thread metadata | Align with session metadata (e.g. archive 30d after last activity; purge terminal) |
| Thread membership | Cascades with thread/session |
| Charlie encrypted history | Existing 1–30 day session expiry |
| Investigations / findings | Existing finding 1–90 day rules |

- [ ] Document purge job updates: archive threads whose all sessions are terminal and past product retention.
- [ ] Audit: thread create/archive/new-chat only (no content).

### 3.6 Migration checklist

- [ ] **M1** Write `155_charlie_interactive_threads.up.sql` + `.down.sql` (number = next available after repo HEAD).
- [ ] **M2** sqlc generate; fix compile.
- [ ] **M3** Migration unit test (pattern of `migration_*_test.go`): up/down, unique active constraint, FK behavior.
- [ ] **M4** Backfill: for each distinct `(connection_id, owner_user_id)` with recent `source=user` private sessions, create one **archived** thread per historical session OR one archived thread with membership ordered by `created_at` (choose **one session = one archived thread** for simplicity in v1 backfill). Do **not** auto-set active threads for all users; active is created on next drawer use.
- [ ] **M5** No secrets/content columns.

---

## 4. Product API surface (Astronomer)

All routes require existing Charlie feature gate, auth JWT/session, and Charlie
connection active checks. No new public exposure.

### 4.1 Thread APIs

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/charlie/threads/active` | Current user’s active interactive thread + current session summary + `messageable` + `needs_continue` |
| POST | `/api/v1/charlie/threads/new` | Archive active + create empty active thread (idempotent request id optional) |
| GET | `/api/v1/charlie/threads` | List interactive threads (active + archived), paginated, **source user only** |
| GET | `/api/v1/charlie/threads/{thread_id}` | Thread metadata + ordered session ids |
| GET | `/api/v1/charlie/threads/{thread_id}/history` | Stitched redacted history across membership sessions (bridge fan-in) |
| POST | `/api/v1/charlie/threads/{thread_id}/messages` | Preferred send path: attach/continue/create session under thread then message |

Keep existing session APIs for hub deep links and abort:

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/api/v1/charlie/sessions/` | Still used for low-level create; interactive path should prefer thread message API |
| POST | `/api/v1/charlie/sessions/{id}/messages/` | Used when messageable session known |
| POST | `/api/v1/charlie/sessions/{id}/abort/` | Abort **session**; update thread.current_session_id / needs_continue |
| GET | `/api/v1/charlie/sessions/{id}/history` | Single-session history |
| GET | `/api/v1/charlie/sessions/{id}/events` | SSE |

### 4.2 Investigation APIs (list separation)

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/charlie/investigations` | List system investigations only (`source=event` and/or investigations table) |
| GET | `/api/v1/charlie/investigations/{id}` | Detail + linked session + findings summary |

- [ ] **A1** Do not return event sessions from interactive thread list.
- [ ] **A2** Do not return private user threads from investigations list.
- [ ] **A3** Overview endpoint includes `active_thread` summary for drawer bootstrap.

### 4.3 Wire types (illustrative)

```json
{
  "data": {
    "thread": {
      "id": "uuid",
      "title": "what version of k8s…",
      "state": "active",
      "current_session_id": "uuid|null",
      "messageable": true,
      "needs_continue": false,
      "updated_at": "RFC3339"
    },
    "session": {
      "id": "uuid",
      "state": "active",
      "central_revision": 3
    }
  }
}
```

### 4.4 Error contract

| Code | When |
| --- | --- |
| 409 `thread_conflict` | Two actives (should be impossible; repair) |
| 409 `session_not_messageable` | Client POSTed to dead session; body includes `needs_continue: true` |
| 404 | Thread not owned / not found |
| 503 | Bridge unavailable |

- [ ] **A4** Frontend treats `session_not_messageable` by calling continue/send-on-thread, not blanking the chat.

### 4.5 Authorization rules

- [ ] **A5** Thread access: owner_user_id == actor only (private interactive).
- [ ] **A6** History stitch: re-auth each session’s resources via existing session access service.
- [ ] **A7** Investigations: existing incident visibility + resource RBAC.
- [ ] **A8** No cross-user thread listing even for admins in v1 (admins use audit, not chat content).

---

## 5. Charlie / agent changes

### 5.1 Required if reattach works

If multi-turn already leaves central sessions `active` until abort/session.completed:

- [ ] **C1** Confirm product cursor + Charlie runtime **do not** mark session completed on `turn.completed` (Astronomer already fixed; verify Charlie side does not force complete after every turn for interactive mode).
- [ ] **C2** Confirm SubmitMessage on active session works for N turns (existing tests).

### 5.2 Required for durable interactive UX without 409

- [ ] **C3** Document central terminal conditions (idle timeout? history_expires? explicit complete?).
- [ ] **C4** If idle timeout exists, either extend for interactive sessions or ensure product `messageable` probe detects terminal and continues cleanly.
- [ ] **C5** Optional attribute `product_thread_id` on CreateSession context attributes (string, max 64–128, non-authoritative). Reject if used for auth.

### 5.3 Optional Charlie enhancements (v1.1, only if v1 continue loses model context badly)

- [ ] **C6** Bridge/API: continue conversation from prior session id with same integration, copying retrieval scope not content to product.
- [ ] **C7** Agent instructions differ for `intent=interactive_support` vs `investigation`.
- [ ] **C8** Metrics: interactive multi-turn count vs investigation sessions (content-free).

### 5.4 OpenCode / product agent

- [ ] **C9** No change required for drawer persistence itself.
- [ ] **C10** Ensure tool results remain model-visible (MCP content fix already shipped); regression test stays green.
- [ ] **C11** Do not start autonomous tools into a user’s interactive session without a user message.

### 5.5 Charlie checklist summary

- [ ] C1 multi-turn session remains active after turn.completed  
- [ ] C2 multi-turn message acceptance tests  
- [ ] C3 terminal conditions documented  
- [ ] C4 idle/expiry interaction with resume  
- [ ] C5 optional product_thread_id attribute  
- [ ] C9–C11 agent non-regression  

---

## 6. Frontend UX

### 6.1 Drawer lifecycle

- [ ] **F1** Lift or fetch thread state so **close drawer does not clear** the active conversation.
  - Prefer: keep `CharlieDrawer` mounted but hidden **or** re-fetch `/threads/active` on every open (server is source of truth—required for multi-tab).
  - Server fetch on open is mandatory even if local state is cached.
- [ ] **F2** On open: load active thread + history (stitched) + messageable flag.
- [ ] **F3** Composer always available when Charlie connection allows chat (not only when sessionId set).
- [ ] **F4** **New chat** calls `POST /threads/new`, clears local transcript, keeps drawer open.
- [ ] **F5** **Abort** aborts current live session only; thread remains with history; set needs_continue.
- [ ] **F6** Send path uses thread message API (or session message when messageable).
- [ ] **F7** Preserve scroll/copy layout from drawer scroll fix (messages above composer).
- [ ] **F8** Pending approvals visible after reopen (from history/parts).
- [ ] **F9** Streaming: on reopen mid-turn, resume SSE from cursor / refresh history.
- [ ] **F10** Multi-tab: last successful new-chat or send wins via server; no dual actives.

### 6.2 Hub

- [ ] **F11** Conversations tab lists **threads**, not raw sessions (show title, updated_at, state).
- [ ] **F12** Opening a thread from hub sets it active (archive previous active) **or** opens read-only with “Make active” — pick one; default: **Make active** then drawer uses it.
- [ ] **F13** Investigations tab only system/event work.
- [ ] **F14** Deep link `?session=` still works: resolve session → thread if any, else single-session view.

### 6.3 Context chips

- [ ] **F15** On open, recompute route resources; merge with thread-sticky manual pins if product stores them (v1: manual pins are session-local only—acceptable; document).
- [ ] **F16** Do not re-attach resources user removed in this browser session without re-add.

### 6.4 Copy / accessibility

- [ ] **F17** Keep Copy on assistant messages; select-text on transcript.
- [ ] **F18** aria: conversation log still `role="log"`; New chat and Abort distinct labels.

---

## 7. Backend service design (Astronomer)

### 7.1 `ThreadService` responsibilities

- [ ] **S1** `GetActive(ctx, userID) (Thread, error)`
- [ ] **S2** `NewChat(ctx, userID) (Thread, error)` — transaction: archive old, insert active
- [ ] **S3** `EnsureActive(ctx, userID) (Thread, error)` — get or create empty active
- [ ] **S4** `AttachSession(ctx, threadID, sessionID)` — membership + current_session_id
- [ ] **S5** `Messageable(ctx, sessionID) (bool, reason)`
- [ ] **S6** `SendOnThread(ctx, userID, threadID, clientMessageID, text, resources, …)`  
  Implements create/continue/send atomic enough to avoid double sessions on retry (use client_message_id / idempotency).
- [ ] **S7** `StitchedHistory(ctx, userID, threadID, limit)` — ordered sessions, concatenate redacted items with session boundaries (optional small separator, no content from product DB).
- [ ] **S8** Audit: `charlie.thread.created`, `charlie.thread.archived`, `charlie.thread.continued` (metadata only).

### 7.2 Integration with `SessionService`

- [ ] **S9** Interactive create always: `source=user`, `visibility=private`, owner set, attach to thread.
- [ ] **S10** Event create path: never call ThreadService active pointer APIs.
- [ ] **S11** Abort session: clear `current_session_id` if matches; thread stays `active`.
- [ ] **S12** Delegation re-issue on continue (new session), revoke old if still open when aborting.

### 7.3 Messageable probe

- [ ] **S13** Cheap path: product state + completed_at + connection mode.
- [ ] **S14** Optional bridge GetSession when product says active but last message 409’d; cache negative briefly to avoid storms.
- [ ] **S15** On 409 from send: mark needs_continue, retry once via continue path if client used legacy session API.

### 7.4 Idempotency

- [ ] **S16** `client_message_id` remains the idempotency key for messages.
- [ ] **S17** `POST /threads/new` with optional `request_id` prevents double archive races.

---

## 8. Phased implementation plan

Execute phases in order. Do not skip validation gates.

### Phase 0 — Discovery and baseline (no behavior change)

- [ ] **P0-1** Capture current drawer close/reopen behavior (screenshots or API notes).
- [ ] **P0-2** Capture multi-turn 409 regression status (should already be fixed for turn.completed).
- [ ] **P0-3** List all create-session call sites (user chat, triggers, findings, qualification).
- [ ] **P0-4** Confirm hub filters for private vs incident.
- [ ] **P0-5** Write baseline test inventory (existing charlie session tests).
- [ ] **P0-6** Decision log: confirm D1–D10 with any product stakeholder notes in this file’s amendment section if changed.

**Exit:** checklist above complete; no code required.

### Phase 1 — Schema + queries

- [ ] **P1-1** Migrations M1–M5.
- [ ] **P1-2** sqlc queries for threads, membership, active unique, list, archive.
- [ ] **P1-3** Unit tests for constraints and backfill.
- [ ] **P1-4** Compile `go test` packages for db/sqlc.

**Exit:** migrations apply clean on empty and existing DBs in CI/dev.

### Phase 2 — Thread service + APIs

- [ ] **P2-1** Implement ThreadService S1–S8.
- [ ] **P2-2** Wire handlers + routes (feature-gated).
- [ ] **P2-3** OpenAPI / generated types if project requires.
- [ ] **P2-4** Handler tests: ownership, new chat uniqueness, stitched history auth denial.
- [ ] **P2-5** SessionService hooks S9–S12.
- [ ] **P2-6** Messageable S13–S15 + 409 recovery.

**Exit:** API-only E2E with curl/JWT: create thread via first message, reopen active, new chat archives.

### Phase 3 — Frontend drawer + hub

- [ ] **P3-1** API client methods in `lib/api/charlie.ts`.
- [ ] **P3-2** Drawer F1–F10, F15–F18.
- [ ] **P3-3** Hub F11–F14.
- [ ] **P3-4** Vitest: close/reopen restores transcript (mock active thread); New chat clears; Abort keeps history; no double user bubble regressions remain.
- [ ] **P3-5** Deploy frontend image tag; hard-refresh validation.

**Exit:** human drawer UX matches ChatGPT-style persistence for same user.

### Phase 4 — Dual-track enforcement (investigations)

- [ ] **P4-1** Ensure trigger/event session path never touches active interactive thread (code audit + test).
- [ ] **P4-2** Investigations list API or strict hub filter + tests.
- [ ] **P4-3** Optional `charlie_investigations` table if needed.
- [ ] **P4-4** “Ask about this finding” (optional v1): creates **new** interactive thread with intent referencing finding id only (opaque), does not merge histories.

**Exit:** automated investigation cannot replace user’s active chat pointer (test).

### Phase 5 — Charlie alignment

- [ ] **P5-1** Complete C1–C5, C9–C11.
- [ ] **P5-2** If product continue lacks model memory, document accepted limitation or implement C6.
- [ ] **P5-3** Charlie tests still green for multi-turn and abort.

**Exit:** no 409 on interactive multi-turn; resume reattach works when central active.

### Phase 6 — Hardening, retention, observability

- [ ] **P6-1** Retention/purge for archived threads.
- [ ] **P6-2** Content-free metrics: threads_created, threads_continued, session_not_messageable, drawer_resume_success.
- [ ] **P6-3** Audit events verified content-free.
- [ ] **P6-4** Multi-tab race test.
- [ ] **P6-5** Mode/disclosure change while drawer closed: banner if needed.
- [ ] **P6-6** Load test: stitched history with N sessions bounds (timeouts, max items).

**Exit:** ops runbook section updated.

### Phase 7 — Live qualification

- [ ] **P7-1** Deploy server + frontend (+ charlie agent only if C-series code changed).
- [ ] **P7-2** Live script: login → ask k8s version → close drawer → reopen → ask follow-up → history contains both → New chat empties → old thread in hub.
- [ ] **P7-3** Live: trigger or simulate event investigation → user active thread unchanged.
- [ ] **P7-4** Live: abort mid-thread → history retained → send continues.
- [ ] **P7-5** Record evidence paths in §10.

---

## 9. Detailed task breakdown (atomic)

### 9.1 Astronomer — database

- [ ] T-DB-01 Add up migration interactive threads  
- [ ] T-DB-02 Add down migration  
- [ ] T-DB-03 Add thread_sessions membership  
- [ ] T-DB-04 Unique partial index one active thread  
- [ ] T-DB-05 Optional sessions.thread_id column  
- [ ] T-DB-06 Backfill strategy implemented + tested  
- [ ] T-DB-07 sqlc generate  
- [ ] T-DB-08 Migration test file  

### 9.2 Astronomer — domain

- [ ] T-DOM-01 `Thread` types and validation  
- [ ] T-DOM-02 `ThreadService` create/get/new/list  
- [ ] T-DOM-03 Attach session + sequence allocation (txn)  
- [ ] T-DOM-04 Messageable helper  
- [ ] T-DOM-05 SendOnThread orchestration  
- [ ] T-DOM-06 StitchedHistory  
- [ ] T-DOM-07 Abort interaction  
- [ ] T-DOM-08 Audit helpers  
- [ ] T-DOM-09 Event path invariant: no active thread writes  
- [ ] T-DOM-10 Unit tests ≥ above methods  

### 9.3 Astronomer — HTTP

- [ ] T-HTTP-01 Routes registered behind Charlie feature  
- [ ] T-HTTP-02 Active thread handler  
- [ ] T-HTTP-03 New chat handler  
- [ ] T-HTTP-04 Thread history handler  
- [ ] T-HTTP-05 Thread message handler  
- [ ] T-HTTP-06 List threads handler  
- [ ] T-HTTP-07 Investigations list separation  
- [ ] T-HTTP-08 Error mapping 409/404/503  
- [ ] T-HTTP-09 Handler tests with auth actor  
- [ ] T-HTTP-10 Rate limits: use same non-hostile class as Charlie chat (no surprise 429 for authed users)  

### 9.4 Astronomer — frontend

- [ ] T-FE-01 API types + functions  
- [ ] T-FE-02 Drawer open loads active thread  
- [ ] T-FE-03 History render stitched  
- [ ] T-FE-04 Send via thread API  
- [ ] T-FE-05 New chat button  
- [ ] T-FE-06 Abort behavior  
- [ ] T-FE-07 Close/reopen does not blank  
- [ ] T-FE-08 Hub conversations = threads  
- [ ] T-FE-09 Hub investigations filter  
- [ ] T-FE-10 Vitest suite updates + new cases  
- [ ] T-FE-11 Keep scroll-above-composer + copy button  

### 9.5 Charlie

- [ ] T-CH-01 Verify multi-turn session state policy  
- [ ] T-CH-02 Fix if turn completion still terminates interactive sessions  
- [ ] T-CH-03 Document session idle/expiry  
- [ ] T-CH-04 Optional product_thread_id attribute plumbing  
- [ ] T-CH-05 Regression tests agent runtime multi-turn  
- [ ] T-CH-06 No autonomous write into interactive sessions without user turn  

### 9.6 Docs / ops

- [ ] T-DOC-01 Update `charlie-operations.md` resume/new-chat/abort semantics  
- [ ] T-DOC-02 Update integration plan ownership table with **thread** correlation row  
- [ ] T-DOC-03 Runbook: stuck needs_continue, dual-active repair SQL  
- [ ] T-DOC-04 This plan checkboxes updated as work completes  

---

## 10. Test and validation matrix

### 10.1 Automated unit / integration

| ID | Case | Expected |
| --- | --- | --- |
| V-U-01 | NewChat twice sequentially | One active; previous archived |
| V-U-02 | Unique active constraint | Second active insert fails / service serializes |
| V-U-03 | SendOnThread first message | Creates session, membership seq=1, bridge message |
| V-U-04 | SendOnThread when messageable | Same session, seq unchanged |
| V-U-05 | SendOnThread when not messageable | New session seq=2, message ok |
| V-U-06 | Event session create | Active thread pointer unchanged |
| V-U-07 | Stitched history auth | Denied resource → session omitted or 403 per policy (define: omit vs fail closed—**fail closed on any denied resource in private thread**) |
| V-U-08 | Abort | Session aborted; thread active; needs_continue |
| V-U-09 | Cursor turn.completed | Product session stays active |
| V-U-10 | Frontend close/reopen mock | Transcript restored from active thread API |
| V-U-11 | Frontend New chat | Empty UI; API called |
| V-U-12 | Dedupe user bubbles | Still single “You” row |
| V-U-13 | MCP tool content payload | Model-visible JSON still present (regression) |

### 10.2 Live validation script (install-level)

Run on dev cluster with bootstrap admin or test user. Record outputs under
`/tmp/charlie-thread-validation-<date>/`.

- [ ] **V-L-01** Login; open Charlie drawer; send “what version of k8s are we running”; receive version string.
- [ ] **V-L-02** Close drawer (X); navigate elsewhere; reopen drawer; **same messages visible** without sending.
- [ ] **V-L-03** Ask follow-up “what namespace is Astronomer in?”; answer uses continuity; history length ≥ 4 turns.
- [ ] **V-L-04** Refresh browser; reopen drawer; active thread restored (server truth).
- [ ] **V-L-05** Click **New chat**; transcript empty; hub shows previous thread archived/listable.
- [ ] **V-L-06** Open previous thread from hub (if supported); history intact.
- [ ] **V-L-07** Abort on live session; history remains; next message continues (new session under thread).
- [ ] **V-L-08** Concurrent tab A and B: New chat in A; B refresh sees new empty active (or documented last-write behavior).
- [ ] **V-L-09** Force session terminal (abort via API); UI does not 409 loop; continues cleanly.
- [ ] **V-L-10** Trigger or simulate event investigation; GET active thread unchanged; investigations list shows event work.
- [ ] **V-L-11** Scroll long transcript; composer remains visible; copy button works.
- [ ] **V-L-12** Mode stays read_only; no unexpected writes.

### 10.3 Security / privacy checks

- [ ] **V-S-01** User B cannot GET user A thread ids (404).
- [ ] **V-S-02** Thread list never includes other users.
- [ ] **V-S-03** Audit log rows for thread ops contain no message text.
- [ ] **V-S-04** Product DB tables have no prompt/response columns (schema review).
- [ ] **V-S-05** Investigation cannot be set as drawer active by forging session id.

### 10.4 Performance bounds

- [ ] **V-P-01** Stitched history default limit (e.g. last 100 items or 64KiB redacted)—document constants.
- [ ] **V-P-02** Active thread GET p95 budget reasonable on dev (&lt; 300ms without cold bridge preferred; record actual).
- [ ] **V-P-03** No N+1 unbounded bridge history calls without concurrency limit (max parallel 3).

---

## 11. Rollout and rollback

### 11.1 Rollout

1. Migrate DB (threads tables).  
2. Deploy server with thread APIs (backward compatible: old session APIs remain).  
3. Deploy frontend that uses thread APIs.  
4. Only if Charlie code changed: roll agent chart.  

Feature flag (optional):

- [ ] `charlie.interactive_threads` — when false, drawer keeps legacy create-every-time behavior for emergency.

### 11.2 Rollback

- [ ] Frontend rollback alone restores old UX but leaves empty thread rows (harmless).
- [ ] Server rollback: old frontend uses session APIs; thread tables unused.
- [ ] Down migration only if no production dependency—prefer forward fix.

### 11.3 Compatibility

- [ ] Old clients posting only `/sessions` + `/messages` still work.
- [ ] New frontend should not require Charlie central upgrade if C1 already true.

---

## 12. Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| 409 on resume | Messageable probe + auto-continue; never leave composer stuck |
| Dual active threads | Partial unique index + transactional NewChat |
| Model loses memory on continue | Reattach preferred; document; optional Charlie continue later |
| History stitch leaks incident data into private thread | Membership only interactive sessions; RBAC fail closed |
| Event path sets user thread | Explicit invariant test; code review list of create sites |
| Large stitched history cost | Hard limits; pagination |
| Users confuse Abort vs New chat | Copy: Abort = stop authority; New chat = start fresh conversation |
| Multi-tab races | Server authoritative active thread |
| Retention surprise | Document 1–30d Charlie history; archived threads may outlive content → hub shows empty with expiry notice |

---

## 13. Suggested implementation order for a single goal-run

1. P0 discovery  
2. P1 schema  
3. P2 ThreadService + APIs + tests  
4. P3 drawer/hub frontend + tests  
5. P4 investigation isolation  
6. P5 Charlie verification  
7. P6 hardening  
8. P7 live validation + evidence  

Estimate (rough): **3–6 focused engineering days** if Charlie multi-turn already stays active; longer if central completes every turn or continue needs C6.

---

## 14. Acceptance criteria (definition of done)

All must be true:

- [ ] **AC1** Same user closes and reopens drawer: prior interactive messages still shown without clicking anything else.
- [ ] **AC2** Follow-up message works on the same thread without forced blank slate.
- [ ] **AC3** New chat clears composer transcript and starts a new active thread; previous recoverable from hub.
- [ ] **AC4** Abort does not wipe thread history; does revoke session authority.
- [ ] **AC5** System/event investigations do not replace or clear the user’s active interactive thread.
- [ ] **AC6** Hub separates My chats vs Investigations.
- [ ] **AC7** No new contentful columns in Astronomer Charlie tables.
- [ ] **AC8** Automated tests V-U-* pass; live V-L-01–V-L-07 pass on target env.
- [ ] **AC9** This plan’s phase checkboxes updated to done with PR/commit references.

---

## 15. Amendment log

| Date | Change | Author |
| --- | --- | --- |
| 2026-08-07 | Initial plan from drawer persistence / dual-track discussion | implementation planning |

When a locked decision D1–D10 changes, add a row here and update §0.5.

---

## 16. Appendix A — Current code anchors

| Item | Location |
| --- | --- |
| Drawer unmount | `frontend/src/components/charlie/charlie-shell.tsx` `{open && <CharlieDrawer />}` |
| No auto-restore comment | same file, CharlieDrawer state init |
| Session create | `internal/charlie/sessions.go` |
| Cursor turn vs session | `internal/charlie/session_access.go` `sessionCursorState` |
| SQL sessions | `internal/db/queries/charlie.sql` |
| Hub conversations/investigations | `frontend/src/routes/dashboard/charlie/index.tsx` |
| Historical ownership matrix | `docs/archive/pre-v1/plans/charlie-agent-integration-plan.md` §4.1a |
| Charlie session states | `charlie/internal/agent/types.go`, `transitions.go` |
| MCP model-visible tool content | `internal/charlie/mcp.go` `boundedActionContent` |

## 17. Appendix B — Example product sequences

### B1 Happy resume

1. User sends M1 → thread T1, session S1.  
2. Close drawer.  
3. Open drawer → GET active → T1, S1 messageable → history [M1,A1].  
4. Send M2 on S1.

### B2 Continue after terminal session

1. S1 aborted or central completed.  
2. Open drawer → history […], needs_continue.  
3. Send M3 → create S2 under T1, attach seq=2, message M3.  
4. UI history shows full stitch.

### B3 New chat

1. T1 active with history.  
2. New chat → T1 archived, T2 active empty.  
3. Hub lists T1.

### B4 Investigation isolation

1. T2 active empty or with chat.  
2. Trigger creates S_event.  
3. Active thread still T2; investigations list shows S_event.

---

## 18. Appendix C — Goal-run execution protocol

For an automated goal agent:

1. Work only from this file’s checkboxes.  
2. After each phase, run that phase’s tests; do not start the next phase on red.  
3. Prefer minimal diffs; do not “improve” unrelated Charlie UX.  
4. When blocked on product decision, stop and record under Amendment log rather than inventing multi-thread drawer scope.  
5. Live deploy only on the known dev host pattern used for Charlie (`KUBECONFIG=/etc/rancher/k3s/k3s.yaml`, namespaces `astronomer` / `astronomer-charlie`) unless instructed otherwise.  
6. Never log message content into operational evidence files; store pass/fail and ids only.

**End of plan.**
