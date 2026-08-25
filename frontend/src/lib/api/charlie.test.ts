import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
const mockedApi = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("@/lib/api/generated/client", () => ({
  listCharlieSessions: (...args: unknown[]) => mockedApi.get(...args),
  getCharlieSession: (...args: unknown[]) => mockedApi.get(...args),
  getCharlieSessionHistory: (...args: unknown[]) => mockedApi.get(...args),
  listCharlieCommands: (...args: unknown[]) => mockedApi.get(...args),
  getActiveCharlieThread: (...args: unknown[]) => mockedApi.get(...args),
  createCharlieThreadMessage: (...args: unknown[]) => mockedApi.post(...args),
  listCharlieFindings: (...args: unknown[]) => mockedApi.get(...args),
  getCharlieFinding: (...args: unknown[]) => mockedApi.get(...args),
  acknowledgeCharlieFinding: (...args: unknown[]) => mockedApi.post(...args),
  dismissCharlieFinding: (...args: unknown[]) => mockedApi.post(...args),
  startCharlieFindingRemediation: (...args: unknown[]) =>
    mockedApi.post(...args),
  requestCharlieFindingVerification: (...args: unknown[]) =>
    mockedApi.post(...args),
  resolveCharlieFinding: (...args: unknown[]) => mockedApi.post(...args),
  decideCharlieApproval: (...args: unknown[]) => mockedApi.post(...args),
}));
import {
  decideCharlieApproval,
  getCharlieActiveThread,
  getCharlieCommands,
  getCharlieSession,
  getCharlieHistory,
  getCharlieFinding,
  listCharlieFindings,
  listCharlieSessions,
  sendCharlieThreadMessage,
  subscribeCharlieSessionEvents,
  transitionCharlieFinding,
} from "./charlie";

describe("Charlie browser gateway mapping", () => {
  beforeEach(() => vi.clearAllMocks());
  it("maps the generated raw session wire shape", async () => {
    mockedApi.get.mockResolvedValue({
      sessions: [
        {
          id: "s",
          client_session_id: "c",
          intent: "inspect",
          resource_scope_summary: "installation/a",
          state: "active",
          visibility: "private",
          central_revision: 2,
        },
      ],
      mode: "approval",
    });
    await expect(listCharlieSessions()).resolves.toEqual([
      expect.objectContaining({
        id: "s",
        clientSessionId: "c",
        centralRevision: 2,
      }),
    ]);
  });
  it("uses authoritative remote terminal state when local SSE projection is stale", async () => {
    mockedApi.get.mockResolvedValue({
      session: {
        id: "local-session",
        client_session_id: "client-session",
        intent: "inspect",
        resource_scope_summary: "installation",
        state: "active",
        visibility: "private",
        central_revision: 1,
        source: "user",
      },
      remote: { state: "failed", revision: 2 },
    });

    await expect(getCharlieSession("local/session")).resolves.toEqual(
      expect.objectContaining({ id: "local-session", state: "failed" }),
    );
    expect(mockedApi.get).toHaveBeenCalledWith({
      path: { session_id: "local/session" },
      signal: undefined,
    });
  });
  it("maps bounded redacted history items into renderable chat messages", async () => {
    mockedApi.get.mockResolvedValue([
      {
        item_id: "user-1",
        kind: "user_message",
        redacted_content: "question",
        created_at: "2026-08-05T22:19:10Z",
      },
      {
        item_id: "assistant-1",
        kind: "assistant_message",
        redacted_content: "answer",
        citations: [
          {
            id: "chunk-1",
            title: "Astronomer operations",
            source: "knowledge://collection-1/version-1#chunk=0",
          },
        ],
        created_at: "2026-08-05T22:19:25Z",
      },
      {
        item_id: "evidence-1",
        kind: "finding_evidence",
        redacted_content: "bounded evidence",
      },
    ]);

    await expect(getCharlieHistory("session/a")).resolves.toEqual([
      expect.objectContaining({
        id: "user-1",
        role: "user",
        content: "question",
      }),
      expect.objectContaining({
        id: "assistant-1",
        role: "assistant",
        content: "answer",
        citations: [
          {
            id: "chunk-1",
            title: "Astronomer operations",
            source: "knowledge://collection-1/version-1#chunk=0",
          },
        ],
      }),
      expect.objectContaining({
        id: "evidence-1",
        role: "system",
        content: "bounded evidence",
      }),
    ]);
    expect(mockedApi.get).toHaveBeenCalledWith({
      path: { session_id: "session/a" },
      signal: undefined,
    });
  });
  it("maps the generated raw thread session needed to open live progress", async () => {
    mockedApi.post.mockResolvedValue({
      thread: {
        id: "thread-1",
        title: "health",
        state: "active",
        current_session_id: "local-session-1",
        created_at: "2026-08-11T23:27:10Z",
      },
      current_session: {
        id: "local-session-1",
        client_session_id: "client-session-1",
        intent: "assess health",
        resource_scope_summary: "installation/current",
        state: "active",
        visibility: "private",
        central_revision: 1,
        source: "user",
      },
      session_ids: ["local-session-1"],
      needs_continue: false,
      messageable: true,
      receipt: {
        session_id: "central-session-1",
        turn_id: "turn-1",
        accepted_at: "2026-08-11T23:27:12Z",
      },
    });
    await expect(sendCharlieThreadMessage("assess health")).resolves.toEqual(
      expect.objectContaining({
        thread: expect.objectContaining({
          current_session_id: "local-session-1",
          created_at: "2026-08-11T23:27:10Z",
        }),
        current_session: expect.objectContaining({
          id: "local-session-1",
          clientSessionId: "client-session-1",
        }),
        session_ids: ["local-session-1"],
        messageable: true,
        receipt: {
          sessionId: "central-session-1",
          turnId: "turn-1",
          acceptedAt: "2026-08-11T23:27:12Z",
        },
      }),
    );
  });
  it("maps a raw active-thread read consistently with message responses", async () => {
    mockedApi.get.mockResolvedValue({
      thread: {
        id: "thread-2",
        title: "queue health",
        state: "active",
        current_session_id: "local-session-2",
      },
      current_session: {
        id: "local-session-2",
        client_session_id: "client-session-2",
        intent: "inspect queues",
        resource_scope_summary: "installation/current",
        state: "active",
        visibility: "private",
        central_revision: 4,
        source: "user",
      },
      session_ids: ["local-session-1", "local-session-2"],
      needs_continue: true,
      messageable: false,
    });
    await expect(getCharlieActiveThread()).resolves.toEqual(
      expect.objectContaining({
        thread: expect.objectContaining({
          current_session_id: "local-session-2",
        }),
        current_session: expect.objectContaining({ id: "local-session-2" }),
        session_ids: ["local-session-1", "local-session-2"],
        needs_continue: true,
        messageable: false,
      }),
    );
  });
  it("loads the versioned product command catalog and sends a structured command selection", async () => {
    mockedApi.get.mockResolvedValue({
      schema: "astronomer.charlie-command-catalog/v1",
      version: 1,
      commands: [],
    });
    await expect(getCharlieCommands()).resolves.toEqual(
      expect.objectContaining({ version: 1, commands: [] }),
    );
    expect(mockedApi.get).toHaveBeenCalledWith({ signal: undefined });

    mockedApi.post.mockResolvedValue({ thread: null });
    await sendCharlieThreadMessage("/health", {
      command: { id: "health", version: "1", arguments: {} },
    });
    expect(mockedApi.post).toHaveBeenCalledWith(
      expect.objectContaining({
        body: expect.objectContaining({
          message: "/health",
          command: { id: "health", version: "1", arguments: {} },
        }),
      }),
    );
  });
  it("maps bounded local finding data plus on-demand central detail", async () => {
    mockedApi.get.mockResolvedValue({
      finding: {
        id: "f",
        title: "Finding",
        severity: "high",
        state: "open",
        summary: "bounded",
        reason_no_action: "read_only",
        affected_resource: {
          type: "installation",
          id: "a",
          required_verb: "read",
        },
        risk_impact: "availability",
        verification_summary: "re-read",
        proposed_action: {
          label: "Restart",
          mode: "approval",
          eligible: true,
          approval_id: "approval-a",
        },
        detail: {
          finding: {
            confidence: 0.8,
            operator_checks: ["check health"],
            preconditions: ["healthy backup"],
            expectedResult: "ready",
            workflow: {
              state: "manual_remediation_required",
              manual_remediation: {
                preconditions: ["authorized operator"],
                steps: ["review current state"],
                expected_impact: "restore health",
                verification: {
                  method: "product.current_state",
                  steps: ["re-read current state"],
                },
              },
            },
          },
        },
      },
    });
    await expect(getCharlieFinding("f")).resolves.toEqual(
      expect.objectContaining({
        id: "f",
        affectedResource: expect.objectContaining({ id: "a" }),
        confidence: 0.8,
        operatorChecks: ["check health"],
        manualRemediation: expect.objectContaining({
          steps: ["review current state"],
          verificationMethod: "product.current_state",
        }),
        proposedAction: expect.objectContaining({
          capability: "Restart",
          eligible: true,
          approvalId: "approval-a",
        }),
      }),
    );
  });
  it("requests the bounded full finding window for accurate topbar state filtering", async () => {
    mockedApi.get.mockResolvedValue({ items: [] });
    await listCharlieFindings();
    expect(mockedApi.get).toHaveBeenCalledWith({
      query: { limit: 100 },
      signal: undefined,
    });
  });
  it("sends a fresh idempotency key with an approval decision", async () => {
    mockedApi.post.mockResolvedValue({});
    await decideCharlieApproval("approval/a", "approve", "bounded rationale");
    expect(mockedApi.post).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { approval_id: "approval/a" },
        body: expect.objectContaining({
          request_id: expect.any(String),
          decision: "approve",
          rationale: "bounded rationale",
        }),
      }),
    );
  });
  it("maps workflow decisions to fixed product-owned paths", async () => {
    mockedApi.post.mockResolvedValue({});
    await transitionCharlieFinding("finding/a", "request_verification");
    expect(mockedApi.post).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { finding_id: "finding/a" },
        body: expect.objectContaining({ request_id: expect.any(String) }),
      }),
    );
  });
  it("turns stale approval conflicts into a precise safe error", async () => {
    mockedApi.post.mockRejectedValue({ response: { status: 409 } });
    await expect(decideCharlieApproval("a", "approve")).rejects.toThrow(
      /stale or was already decided/i,
    );
  });
});

describe("Charlie session event transport", () => {
  class FakeEventSource {
    static instances: FakeEventSource[] = [];
    onopen: (() => void) | null = null;
    onerror: (() => void) | null = null;
    listeners = new Map<string, Set<(event: Event) => void>>();
    closed = false;

    constructor(public url: string) {
      FakeEventSource.instances.push(this);
    }

    addEventListener(type: string, listener: (event: Event) => void) {
      const listeners = this.listeners.get(type) ?? new Set();
      listeners.add(listener);
      this.listeners.set(type, listeners);
    }

    removeEventListener(type: string, listener: (event: Event) => void) {
      this.listeners.get(type)?.delete(listener);
    }

    close() {
      this.closed = true;
    }

    emit(type: string, data: string) {
      const event = new MessageEvent(type, { data, lastEventId: "1" });
      this.listeners.get(type)?.forEach((listener) => listener(event));
    }
  }

  beforeEach(() => {
    vi.useFakeTimers();
    FakeEventSource.instances = [];
    vi.stubGlobal("EventSource", FakeEventSource);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("treats stream closure after a terminal turn event as successful", () => {
    const onEvent = vi.fn();
    const onError = vi.fn();
    const unsubscribe = subscribeCharlieSessionEvents(
      "session-1",
      onEvent,
      onError,
    );
    const source = FakeEventSource.instances[0];
    source.emit(
      "turn.completed",
      JSON.stringify({
        turn_id: "turn-1",
        type: "turn.completed",
        data: {},
      }),
    );
    source.onerror?.();
    vi.advanceTimersByTime(60_000);
    expect(onEvent).toHaveBeenCalledOnce();
    expect(onError).not.toHaveBeenCalled();
    expect(source.closed).toBe(true);
    expect(FakeEventSource.instances).toHaveLength(1);
    unsubscribe();
  });
});
