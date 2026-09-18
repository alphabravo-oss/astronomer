import type { MockedFunction } from "vitest";
import {
  createSupportBundle as createSupportBundleRequest,
  executeOpenAPIOperationWithResponse,
  getSupportBundleOperation as getSupportBundleOperationRequest,
} from "@/lib/api/generated/client";
import {
  createSupportBundle,
  downloadSupportBundleArtifact,
  getSupportBundleOperation,
} from "./support-bundles";

vi.mock("@/lib/api/generated/client", () => ({
  createSupportBundle: vi.fn(),
  executeOpenAPIOperationWithResponse: vi.fn(),
  getSupportBundleOperation: vi.fn(),
}));

const createRequest = createSupportBundleRequest as MockedFunction<
  typeof createSupportBundleRequest
>;
const statusRequest = getSupportBundleOperationRequest as MockedFunction<
  typeof getSupportBundleOperationRequest
>;
const downloadRequest = executeOpenAPIOperationWithResponse as MockedFunction<
  typeof executeOpenAPIOperationWithResponse
>;

const operation = {
  id: "00000000-0000-4000-8000-000000000001",
  status: "pending" as const,
  attempt_count: 0,
  size: 0,
  expires_at: "2026-09-11T00:00:00Z",
  created_at: "2026-09-10T00:00:00Z",
  updated_at: "2026-09-10T00:00:00Z",
  status_url:
    "/api/v1/support-bundles/00000000-0000-4000-8000-000000000001/",
};

describe("support bundle API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("creates with a bounded unique idempotency key", async () => {
    createRequest.mockResolvedValueOnce({ data: operation } as never);
    await expect(createSupportBundle()).resolves.toEqual(operation);
    expect(createRequest).toHaveBeenCalledWith(
      expect.objectContaining({
        headerParams: {
          "Idempotency-Key": expect.stringMatching(/^[0-9a-f-]{36}$/),
        },
      }),
    );
  });

  it("forwards polling cancellation", async () => {
    statusRequest.mockResolvedValueOnce({ data: operation } as never);
    const signal = new AbortController().signal;
    await expect(getSupportBundleOperation(operation.id, signal)).resolves.toEqual(
      operation,
    );
    expect(statusRequest).toHaveBeenCalledWith({
      path: { id: operation.id },
      signal,
    });
  });

  it("returns the completed artifact and server filename", async () => {
    const blob = new Blob(["zip"], { type: "application/zip" });
    downloadRequest.mockResolvedValueOnce({
      data: blob,
      headers: { "content-disposition": 'attachment; filename="bundle.zip"' },
      status: 200,
    } as never);
    await expect(downloadSupportBundleArtifact(operation.id)).resolves.toEqual({
      blob,
      filename: "bundle.zip",
    });
  });
});
