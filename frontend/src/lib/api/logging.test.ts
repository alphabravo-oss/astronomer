import * as generated from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import {
  attachAstronomerLogs,
  createLoggingOutput,
  createLoggingPipeline,
  createLoggingSavedSearch,
  deleteLoggingOutput,
  deleteLoggingPipeline,
  deleteLoggingSavedSearch,
  getLoggingOperations,
  getLoggingOutputs,
  getLoggingPipelines,
  getLoggingSavedSearches,
  updateLoggingOutput,
  updateLoggingSavedSearch,
} from "./logging";

vi.mock("@/lib/api/generated/client", () => ({
  deleteLoggingOutputsById: vi.fn(),
  deleteLoggingPipelinesById: vi.fn(),
  deleteLoggingSavedSearchesById: vi.fn(),
  getClustersByIdLoggingOutputsAttachAstronomer: vi.fn(),
  getLoggingOperations: vi.fn(),
  getLoggingOperationsById: vi.fn(),
  getLoggingOutputs: vi.fn(),
  getLoggingPipelines: vi.fn(),
  getLoggingSavedSearches: vi.fn(),
  postClustersByIdLoggingOutputsAttachAstronomer: vi.fn(),
  postLoggingOperationsByIdRetry: vi.fn(),
  postLoggingOutputs: vi.fn(),
  postLoggingOutputsByIdQuery: vi.fn(),
  postLoggingOutputsByIdTest: vi.fn(),
  postLoggingPipelines: vi.fn(),
  postLoggingSavedSearches: vi.fn(),
  putLoggingOutputsById: vi.fn(),
  putLoggingPipelinesById: vi.fn(),
  putLoggingSavedSearchesById: vi.fn(),
}));

type Schemas = OpenAPIComponents["schemas"];

const outputWire: Schemas["LoggingOutput"] = {
  id: "output-1",
  name: "Production logs",
  output_type: "opensearch",
  configuration: { url: "https://logs.example.com:9200" },
  cluster_id: "cluster-1",
  enabled: true,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:01:00Z",
  is_system: false,
  capabilities: {
    ship: true,
    test: true,
    query: true,
    tail: false,
    aggregate: false,
    link_out: true,
    link_out_url: "https://logs.example.com",
    retention_visibility: true,
    query_mode: "native",
  },
};

const savedSearchWire: Schemas["LoggingSavedSearch"] = {
  id: "saved-1",
  output_id: "output-1",
  name: "Errors",
  query: "status:error",
  namespaces: ["payments"],
  limit: 250,
  direction: "backward",
  live_tail: true,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:01:00Z",
};

const pipelineWire: Schemas["LoggingPipeline"] = {
  id: "pipeline-1",
  name: "Payments",
  cluster_id: "cluster-1",
  namespaces: ["checkout"],
  labels: {},
  filters: [],
  output_ids: ["output-1"],
  output_names: ["Production logs"],
  enabled: true,
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:01:00Z",
};

const operationWire: Schemas["LoggingOperation"] = {
  id: "operation-1",
  targetType: "pipeline",
  targetKey: "pipeline-1",
  operationType: "apply",
  status: "pending",
  attemptCount: 0,
  errorMessage: "",
  createdAt: "2026-08-23T00:00:00Z",
  updatedAt: "2026-08-23T00:00:00Z",
};

describe("logging generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps raw output wire fields and capabilities into the UI model", async () => {
    vi.mocked(generated.getLoggingOutputs).mockResolvedValueOnce({
      data: [outputWire],
      count: 1,
      next: null,
      previous: null,
    });

    await expect(getLoggingOutputs()).resolves.toEqual([
      expect.objectContaining({
        id: "output-1",
        type: "opensearch",
        outputType: "opensearch",
        clusterId: "cluster-1",
        capabilities: expect.objectContaining({
          linkOut: true,
          linkOutUrl: "https://logs.example.com",
          retentionVisibility: true,
        }),
      }),
    ]);
    expect(generated.getLoggingOutputs).toHaveBeenCalledWith({
      query: { limit: 200 },
    });
  });

  it("maps the create view model to the exact snake_case request", async () => {
    vi.mocked(generated.postLoggingOutputs).mockResolvedValueOnce({
      data: {
        output: outputWire,
        operation: {
          ...operationWire,
          targetType: "output",
          targetKey: "output-1",
        },
      },
    });

    await createLoggingOutput({
      name: "Production logs",
      type: "opensearch",
      clusterId: "cluster-1",
      enabled: true,
      config: { url: "https://logs.example.com:9200" },
    });

    expect(generated.postLoggingOutputs).toHaveBeenCalledWith({
      query: { cluster_id: "cluster-1" },
      headerParams: { "Idempotency-Key": expect.any(String) },
      body: {
        name: "Production logs",
        output_type: "opensearch",
        configuration: { url: "https://logs.example.com:9200" },
        cluster_id: "cluster-1",
        enabled: true,
      },
    });

    vi.mocked(generated.deleteLoggingOutputsById).mockResolvedValueOnce({
      data: {
        output: outputWire,
        operation: {
          ...operationWire,
          targetType: "output",
          targetKey: "output-1",
          operationType: "delete",
        },
      },
    });
    await deleteLoggingOutput("output-1");
    expect(generated.deleteLoggingOutputsById).toHaveBeenCalledWith({
      path: { id: "output-1" },
      headerParams: { "Idempotency-Key": expect.any(String) },
    });
  });

  it("unwraps the durable hosted-Loki attach receipt", async () => {
    vi.mocked(
      generated.postClustersByIdLoggingOutputsAttachAstronomer,
    ).mockResolvedValueOnce({
      data: {
        output: { ...outputWire, output_type: "loki", token: "test-only" },
        operation: {
          ...operationWire,
          targetType: "output",
          targetKey: "output-1",
        },
      },
    });

    await expect(attachAstronomerLogs("cluster-1")).resolves.toEqual(
      expect.objectContaining({
        id: "output-1",
        outputType: "loki",
        token: "test-only",
      }),
    );
    expect(
      generated.postClustersByIdLoggingOutputsAttachAstronomer,
    ).toHaveBeenCalledWith({
      path: { id: "cluster-1" },
      query: { rotate: undefined },
      headerParams: { "Idempotency-Key": expect.any(String) },
    });
  });

  it("rejects partial replacement updates before required fields can be erased", async () => {
    await expect(
      updateLoggingOutput("output-1", { enabled: false }),
    ).rejects.toThrow("Logging output name is required");
    expect(generated.putLoggingOutputsById).not.toHaveBeenCalled();
  });

  it("uses the server's camelCase operation filters and maps operationType", async () => {
    vi.mocked(generated.getLoggingOperations).mockResolvedValueOnce({
      data: [
        {
          id: "operation-1",
          targetType: "output",
          targetKey: "output-1",
          operationType: "apply",
          status: "running",
          attemptCount: 1,
          errorMessage: "",
          createdAt: "2026-08-23T00:00:00Z",
          updatedAt: "2026-08-23T00:01:00Z",
        },
      ],
      pagination: {
        limit: 100,
        offset: 0,
        total: 1,
        has_more: false,
        next_offset: null,
      },
    });

    await expect(
      getLoggingOperations({
        status: "running",
        target_type: "output",
        target_key: "output-1",
        limit: 100,
      }),
    ).resolves.toEqual([
      expect.objectContaining({ operation: "apply", status: "running" }),
    ]);
    expect(generated.getLoggingOperations).toHaveBeenCalledWith({
      query: {
        status: "running",
        targetType: "output",
        targetKey: "output-1",
        limit: 100,
        offset: undefined,
      },
    });
  });

  it("round-trips pipeline destinations through the generated contract", async () => {
    vi.mocked(generated.getLoggingPipelines).mockResolvedValueOnce({
      data: [pipelineWire],
      count: 1,
      next: null,
      previous: null,
    });
    await expect(
      getLoggingPipelines({ clusterId: "cluster-1", limit: 200 }),
    ).resolves.toEqual([
      expect.objectContaining({
        id: "pipeline-1",
        outputIds: ["output-1"],
        outputNames: ["Production logs"],
      }),
    ]);

    vi.mocked(generated.postLoggingPipelines).mockResolvedValueOnce({
      data: { pipeline: pipelineWire, operation: operationWire },
    });
    await createLoggingPipeline({
      name: "Payments",
      clusterId: "cluster-1",
      namespaces: ["checkout"],
      outputIds: ["output-1"],
      filters: [],
      enabled: true,
    });
    expect(generated.postLoggingPipelines).toHaveBeenCalledWith({
      query: { cluster_id: "cluster-1" },
      headerParams: { "Idempotency-Key": expect.any(String) },
      body: {
        name: "Payments",
        cluster_id: "cluster-1",
        namespaces: ["checkout"],
        filters: [],
        output_ids: ["output-1"],
        enabled: true,
      },
    });

    vi.mocked(generated.deleteLoggingPipelinesById).mockResolvedValueOnce({
      data: {
        pipeline: pipelineWire,
        operation: { ...operationWire, operationType: "delete" },
      },
    });
    await deleteLoggingPipeline("pipeline-1");
    expect(generated.deleteLoggingPipelinesById).toHaveBeenCalledWith({
      path: { id: "pipeline-1" },
      headerParams: { "Idempotency-Key": expect.any(String) },
    });
  });
});

describe("logging saved-search generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists only the selected output and maps raw wire casing", async () => {
    vi.mocked(generated.getLoggingSavedSearches).mockResolvedValueOnce({
      data: [savedSearchWire],
    });
    await expect(getLoggingSavedSearches("output-1")).resolves.toEqual([
      expect.objectContaining({
        id: "saved-1",
        outputId: "output-1",
        liveTail: true,
      }),
    ]);
    expect(generated.getLoggingSavedSearches).toHaveBeenCalledWith({
      query: { output_id: "output-1" },
    });
  });

  it("maps camelCase view state to the typed snake_case create contract", async () => {
    vi.mocked(generated.postLoggingSavedSearches).mockResolvedValueOnce({
      data: savedSearchWire,
    });
    await createLoggingSavedSearch({
      outputId: "output-1",
      name: "Errors",
      query: "status:error",
      namespaces: ["payments"],
      limit: 250,
      direction: "backward",
      liveTail: true,
    });
    expect(generated.postLoggingSavedSearches).toHaveBeenCalledWith({
      body: {
        output_id: "output-1",
        name: "Errors",
        query: "status:error",
        namespaces: ["payments"],
        limit: 250,
        direction: "backward",
        live_tail: true,
      },
    });
  });

  it("updates immutable-output saved state without resending output_id", async () => {
    vi.mocked(generated.putLoggingSavedSearchesById).mockResolvedValueOnce({
      data: savedSearchWire,
    });
    await updateLoggingSavedSearch("saved-1", {
      name: "Warnings",
      query: "status:warn",
      namespaces: [],
      limit: 100,
      direction: "forward",
      liveTail: false,
    });
    expect(generated.putLoggingSavedSearchesById).toHaveBeenCalledWith({
      path: { id: "saved-1" },
      body: {
        name: "Warnings",
        query: "status:warn",
        namespaces: [],
        limit: 100,
        direction: "forward",
        live_tail: false,
      },
    });
  });

  it("deletes by owner-scoped id", async () => {
    vi.mocked(generated.deleteLoggingSavedSearchesById).mockResolvedValueOnce();
    await deleteLoggingSavedSearch("saved-1");
    expect(generated.deleteLoggingSavedSearchesById).toHaveBeenCalledWith({
      path: { id: "saved-1" },
    });
  });
});
