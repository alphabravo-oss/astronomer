import {
  deleteAdminAlertingInhibitionsById,
  getAdminAlertingInhibitions,
  getAdminAlertingInhibitionsById,
  postAdminAlertingInhibitions,
  putAdminAlertingInhibitionsById,
} from "@/lib/api/generated/client";
import {
  createInhibition,
  deleteInhibition,
  getInhibition,
  listInhibitions,
  toInhibitionWriteRequest,
  updateInhibition,
} from "@/lib/api/alerting-inhibitions";

vi.mock("@/lib/api/generated/client", () => ({
  deleteAdminAlertingInhibitionsById: vi.fn(),
  getAdminAlertingInhibitions: vi.fn(),
  getAdminAlertingInhibitionsById: vi.fn(),
  postAdminAlertingInhibitions: vi.fn(),
  putAdminAlertingInhibitionsById: vi.fn(),
}));

const inhibitionWire = {
  id: "4b53f94f-83b4-4f55-99a8-a26c696cb796",
  name: "suppress-node-warnings",
  source_matchers: [
    { label: "alertname", value: "ClusterDown", is_regex: false },
  ],
  target_matchers: [{ label: "severity", value: "warn.*", is_regex: true }],
  equal_labels: ["cluster"],
  enabled: true,
  created_at: "2026-08-24T07:00:00Z",
  updated_at: "2026-08-24T07:05:00Z",
};

const writeBody = {
  name: inhibitionWire.name,
  source_matchers: inhibitionWire.source_matchers,
  target_matchers: inhibitionWire.target_matchers,
  equal_labels: inhibitionWire.equal_labels,
  enabled: true,
};

describe("alerting inhibition generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps camelCase form matchers to the generated wire request", () => {
    expect(
      toInhibitionWriteRequest({
        name: writeBody.name,
        enabled: true,
        sourceMatchers: [
          { label: "alertname", value: "ClusterDown", isRegex: false },
        ],
        targetMatchers: [{ label: "severity", value: "warn.*", isRegex: true }],
        equalLabels: ["cluster"],
      }),
    ).toEqual(writeBody);
  });

  it("maps list and detail response casing explicitly", async () => {
    vi.mocked(getAdminAlertingInhibitions).mockResolvedValueOnce({
      data: [inhibitionWire],
      pagination: { limit: 50, offset: 0, has_more: false, next_offset: null },
    });
    await expect(listInhibitions()).resolves.toEqual([
      expect.objectContaining({
        id: inhibitionWire.id,
        sourceMatchers: [expect.objectContaining({ isRegex: false })],
        equalLabels: ["cluster"],
      }),
    ]);

    vi.mocked(getAdminAlertingInhibitionsById).mockResolvedValueOnce({
      data: inhibitionWire,
    });
    await expect(getInhibition(inhibitionWire.id)).resolves.toEqual(
      expect.objectContaining({ name: inhibitionWire.name }),
    );
  });

  it("uses generated create and update operations", async () => {
    vi.mocked(postAdminAlertingInhibitions).mockResolvedValueOnce({
      data: inhibitionWire,
    });
    await createInhibition(writeBody);
    expect(postAdminAlertingInhibitions).toHaveBeenCalledWith({
      body: writeBody,
    });

    vi.mocked(putAdminAlertingInhibitionsById).mockResolvedValueOnce({
      data: { ...inhibitionWire, enabled: false },
    });
    await updateInhibition(inhibitionWire.id, {
      ...writeBody,
      enabled: false,
    });
    expect(putAdminAlertingInhibitionsById).toHaveBeenCalledWith({
      path: { id: inhibitionWire.id },
      body: { ...writeBody, enabled: false },
    });
  });

  it("uses the generated delete path", async () => {
    vi.mocked(deleteAdminAlertingInhibitionsById).mockResolvedValueOnce(
      undefined,
    );
    await deleteInhibition(inhibitionWire.id);
    expect(deleteAdminAlertingInhibitionsById).toHaveBeenCalledWith({
      path: { id: inhibitionWire.id },
    });
  });
});
