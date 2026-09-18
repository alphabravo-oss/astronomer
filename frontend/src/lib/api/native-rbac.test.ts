import { getNativeRbacRules } from "@/lib/api/generated/client";
import { listNativeRules } from "./native-rbac";

vi.mock("@/lib/api/generated/client", () => ({
  getNativeRbacRules: vi.fn(),
  postNativeRbacRules: vi.fn(),
  deleteNativeRbacRulesById: vi.fn(),
}));

describe("native RBAC pagination", () => {
  beforeEach(() => vi.clearAllMocks());

  it("follows canonical next offsets until the complete grant inventory is loaded", async () => {
    vi.mocked(getNativeRbacRules)
      .mockResolvedValueOnce({
        data: [
          {
            id: "rule-1",
            userId: "user-1",
            namespace: "",
            apiGroup: "apps",
            resource: "deployments",
            verbs: ["read"],
            createdAt: "2026-09-10T00:00:00Z",
          },
        ],
        pagination: {
          limit: 500,
          offset: 0,
          has_more: true,
          next_offset: 500,
        },
      })
      .mockResolvedValueOnce({
        data: [
          {
            id: "rule-2",
            userId: "user-1",
            namespace: "default",
            apiGroup: "",
            resource: "pods",
            verbs: ["list"],
            createdAt: "2026-09-10T00:00:00Z",
          },
        ],
        pagination: {
          limit: 500,
          offset: 500,
          has_more: false,
          next_offset: null,
        },
      });

    const signal = new AbortController().signal;
    await expect(listNativeRules("user-1", signal)).resolves.toHaveLength(2);
    expect(getNativeRbacRules).toHaveBeenNthCalledWith(1, {
      query: { userId: "user-1", limit: 500, offset: 0 },
      signal,
    });
    expect(getNativeRbacRules).toHaveBeenNthCalledWith(2, {
      query: { userId: "user-1", limit: 500, offset: 500 },
      signal,
    });
  });

  it("rejects a malformed page that claims more data without advancing", async () => {
    vi.mocked(getNativeRbacRules).mockResolvedValueOnce({
      data: [],
      pagination: {
        limit: 500,
        offset: 0,
        has_more: true,
        next_offset: 0,
      },
    });

    await expect(listNativeRules()).rejects.toThrow(
      "Native RBAC pagination did not advance",
    );
  });
});
