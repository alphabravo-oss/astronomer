import { afterEach, beforeEach, describe, expect, it, vi, type Mocked } from "vitest";
import api from "@/lib/api";
vi.mock("@/lib/api", () => ({ default: { get: vi.fn(), post: vi.fn() } }));
import {
  decideCharlieApproval,
  getCharlieActiveThread,
  getCharlieCommands,
  getCharlieHistory,
  getCharlieFinding,
  listCharlieFindings,
  listCharlieSessions,
  sendCharlieThreadMessage,
  subscribeCharlieSessionEvents,
  transitionCharlieFinding,
} from "./charlie";
const mockedApi = api as Mocked<typeof api>;

describe("Charlie browser gateway mapping", () => {
  beforeEach(() => vi.clearAllMocks());
  it("maps the interceptor-camelized session envelope", async () => {
    mockedApi.get.mockResolvedValue({
      data: {
        data: {
          sessions: [
            {
              id: "s",
              clientSessionId: "c",
              intent: "inspect",
              resourceScopeSummary: "installation/a",
              state: "active",
              visibility: "private",
              centralRevision: 2,
            },
          ],
        },
      },
    });
    await expect(listCharlieSessions()).resolves.toEqual([
      expect.objectContaining({
        id: "s",
        clientSessionId: "c",
        centralRevision: 2,
      }),
    ]);
  });
  it("maps bounded redacted history items into renderable chat messages", async () => {
    mockedApi.get.mockResolvedValue({
      data: {
        data: [
          {
            itemId: "user-1",
            kind: "user_message",
            redactedContent: "question",
            createdAt: "2026-08-05T22:19:10Z",
          },
          {
            itemId: "assistant-1",
            kind: "assistant_message",
            redactedContent: "answer",
            citations: [
              {
                id: "chunk-1",
                title: "Astronomer operations",
                source: "knowledge://collection-1/version-1#chunk=0",
              },
            ],
            createdAt: "2026-08-05T22:19:25Z",
          },
          {
            itemId: "evidence-1",
            kind: "finding_evidence",
            redactedContent: "bounded evidence",
          },
        ],
      },
    });

    await expect(getCharlieHistory("session/a")).resolves.toEqual([
      expect.objectContaining({ id: "user-1", role: "user", content: "question" }),
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
      expect.objectContaining({ id: "evidence-1", role: "system", content: "bounded evidence" }),
    ]);
    expect(mockedApi.get).toHaveBeenCalledWith(
      "/charlie/sessions/session%2Fa/history/",
    );
  });
  it("maps the camelized thread session needed to open live progress", async () => {
    mockedApi.post.mockResolvedValue({
      data: {
        thread: {
          id: "thread-1",
          title: "health",
          state: "active",
          currentSessionId: "local-session-1",
          createdAt: "2026-08-11T23:27:10Z",
        },
        currentSession: {
          id: "local-session-1",
          clientSessionId: "client-session-1",
          intent: "assess health",
          resourceScopeSummary: "installation/current",
          state: "active",
          visibility: "private",
          centralRevision: 1,
          source: "user",
        },
        sessionIds: ["local-session-1"],
        needsContinue: false,
        messageable: true,
        receipt: {
          sessionId: "central-session-1",
          turnId: "turn-1",
          acceptedAt: "2026-08-11T23:27:12Z",
        },
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
  it("maps a camelized active-thread read consistently with message responses", async () => {
    mockedApi.get.mockResolvedValue({
      data: {
        thread: {
          id: "thread-2",
          title: "queue health",
          state: "active",
          currentSessionId: "local-session-2",
        },
        currentSession: {
          id: "local-session-2",
          clientSessionId: "client-session-2",
          intent: "inspect queues",
          resourceScopeSummary: "installation/current",
          state: "active",
          visibility: "private",
          centralRevision: 4,
          source: "user",
        },
        sessionIds: ["local-session-1", "local-session-2"],
        needsContinue: true,
        messageable: false,
      },
    });
    await expect(getCharlieActiveThread()).resolves.toEqual(
      expect.objectContaining({
        thread: expect.objectContaining({ current_session_id: "local-session-2" }),
        current_session: expect.objectContaining({ id: "local-session-2" }),
        session_ids: ["local-session-1", "local-session-2"],
        needs_continue: true,
        messageable: false,
      }),
    );
  });
  it("loads the versioned product command catalog and sends a structured command selection", async () => {
    mockedApi.get.mockResolvedValue({ data: { schema: "astronomer.charlie-command-catalog/v1", version: 1, commands: [] } });
    await expect(getCharlieCommands()).resolves.toEqual(expect.objectContaining({ version: 1, commands: [] }));
    expect(mockedApi.get).toHaveBeenCalledWith("/charlie/commands/");

    mockedApi.post.mockResolvedValue({ data: { thread: null } });
    await sendCharlieThreadMessage("/health", {
      command: { id: "health", version: "1", arguments: {} },
    });
    expect(mockedApi.post).toHaveBeenCalledWith(
      "/charlie/threads/messages/",
      expect.objectContaining({
        message: "/health",
        command: { id: "health", version: "1", arguments: {} },
      }),
    );
  });
  it("maps bounded local finding data plus on-demand central detail", async () => {
    mockedApi.get.mockResolvedValue({
      data: {
        data: {
          finding: {
            id: "f",
            title: "Finding",
            severity: "high",
            state: "open",
            summary: "bounded",
            reasonNoAction: "read_only",
            affectedResource: {
              type: "installation",
              id: "a",
              requiredVerb: "read",
            },
            riskImpact: "availability",
            verificationSummary: "re-read",
            proposedAction: {
              label: "Restart",
              mode: "approval",
              eligible: true,
              approvalId: "approval-a",
            },
            detail: {
              finding: {
                confidence: 0.8,
                operatorChecks: ["check health"],
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
    mockedApi.get.mockResolvedValue({ data: { data: { items: [] } } });
    await listCharlieFindings();
    expect(mockedApi.get).toHaveBeenCalledWith("/charlie/findings/", {
      params: { limit: 100 },
    });
  });
  it("sends a fresh idempotency key with an approval decision", async () => {
    mockedApi.post.mockResolvedValue({ data: {} });
    await decideCharlieApproval("approval/a", "approve", "bounded rationale");
    expect(mockedApi.post).toHaveBeenCalledWith(
      "/charlie/approvals/approval%2Fa/decision/",
      expect.objectContaining({
        request_id: expect.any(String),
        decision: "approve",
        rationale: "bounded rationale",
      }),
    );
  });
  it("maps workflow decisions to fixed product-owned paths", async () => {
    mockedApi.post.mockResolvedValue({ data: {} });
    await transitionCharlieFinding("finding/a", "request_verification");
    expect(mockedApi.post).toHaveBeenCalledWith(
      "/charlie/findings/finding%2Fa/request-verification/",
      expect.objectContaining({ request_id: expect.any(String) }),
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
    const unsubscribe = subscribeCharlieSessionEvents("session-1", onEvent, onError);
    const source = FakeEventSource.instances[0];
    source.emit("turn.completed", JSON.stringify({
      turn_id: "turn-1",
      type: "turn.completed",
      data: {},
    }));
    source.onerror?.();
    vi.advanceTimersByTime(60_000);
    expect(onEvent).toHaveBeenCalledOnce();
    expect(onError).not.toHaveBeenCalled();
    expect(source.closed).toBe(true);
    expect(FakeEventSource.instances).toHaveLength(1);
    unsubscribe();
  });
});
