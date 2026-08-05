import { beforeEach, describe, expect, it, vi, type Mocked } from "vitest";
import api from "@/lib/api";
vi.mock("@/lib/api", () => ({ default: { get: vi.fn(), post: vi.fn() } }));
import {
  decideCharlieApproval,
  getCharlieFinding,
  listCharlieFindings,
  listCharlieSessions,
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
    await decideCharlieApproval("approval/a", "approve");
    expect(mockedApi.post).toHaveBeenCalledWith(
      "/charlie/approvals/approval%2Fa/decision/",
      expect.objectContaining({
        request_id: expect.any(String),
        decision: "approve",
      }),
    );
  });
});
