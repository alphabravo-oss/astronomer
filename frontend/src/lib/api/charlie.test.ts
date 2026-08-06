import { beforeEach, describe, expect, it, vi, type Mocked } from "vitest";
import api from "@/lib/api";
vi.mock("@/lib/api", () => ({ default: { get: vi.fn(), post: vi.fn() } }));
import {
  decideCharlieApproval,
  getCharlieHistory,
  getCharlieFinding,
  listCharlieFindings,
  listCharlieSessions,
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
