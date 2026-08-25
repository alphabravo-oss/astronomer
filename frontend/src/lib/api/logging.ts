import {
  deleteLoggingOutputsById as deleteLoggingOutputOperation,
  deleteLoggingPipelinesById as deleteLoggingPipelineOperation,
  deleteLoggingSavedSearchesById as deleteLoggingSavedSearchOperation,
  getClustersByIdLoggingOutputsAttachAstronomer as getLoggingAttachStatusOperation,
  getLoggingOperations as listLoggingOperationsOperation,
  getLoggingOperationsById as getLoggingOperationOperation,
  getLoggingOutputs as listLoggingOutputsOperation,
  getLoggingPipelines as listLoggingPipelinesOperation,
  getLoggingSavedSearches as listLoggingSavedSearchesOperation,
  postClustersByIdLoggingOutputsAttachAstronomer as attachAstronomerLogsOperation,
  postLoggingOperationsByIdRetry as retryLoggingOperationOperation,
  postLoggingOutputs as createLoggingOutputOperation,
  postLoggingOutputsByIdQuery as queryLoggingOutputOperation,
  postLoggingOutputsByIdTest as testLoggingOutputOperation,
  postLoggingPipelines as createLoggingPipelineOperation,
  postLoggingSavedSearches as createLoggingSavedSearchOperation,
  putLoggingOutputsById as updateLoggingOutputOperation,
  putLoggingPipelinesById as updateLoggingPipelineOperation,
  putLoggingSavedSearchesById as updateLoggingSavedSearchOperation,
} from "@/lib/api/generated/client";
import type {
  LoggingOperation,
  LoggingOperationEvent,
  LoggingOutput,
  LoggingOutputCapabilities,
  LoggingOutputType,
  LoggingPipeline,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

type LoggingContracts = OpenAPIComponents["schemas"];
type LoggingOutputWire = LoggingContracts["LoggingOutput"];
type LoggingPipelineWire = LoggingContracts["LoggingPipeline"];
type LoggingOperationWire = LoggingContracts["LoggingOperation"];
type LoggingSavedSearchWire = LoggingContracts["LoggingSavedSearch"];

function requireData<T>(value: { data?: T } | undefined, operation: string): T {
  if (!value?.data) throw new Error(`${operation} returned no data payload`);
  return value.data;
}

function mutationHeaders(): { "Idempotency-Key": string } {
  return { "Idempotency-Key": crypto.randomUUID() };
}

function outputTypeOf(output: Partial<LoggingOutput>): LoggingOutputType {
  const outputType = output.outputType ?? output.type;
  if (!outputType) throw new Error("Logging output type is required");
  return outputType;
}

function requiredName(value: string | undefined, resource: string): string {
  const name = value?.trim();
  if (!name) throw new Error(`${resource} name is required`);
  return name;
}

function mapLoggingCapabilities(
  wire: LoggingContracts["LoggingOutputCapabilities"],
): LoggingOutputCapabilities {
  return {
    ship: wire.ship,
    test: wire.test,
    query: wire.query,
    tail: wire.tail,
    aggregate: wire.aggregate,
    linkOut: wire.link_out,
    linkOutUrl: wire.link_out_url,
    retentionVisibility: wire.retention_visibility,
    queryMode: wire.query_mode,
  };
}

/** Deliberate generated-wire to UI mapping; transport responses stay exact. */
export function mapLoggingOutput(wire: LoggingOutputWire): LoggingOutput {
  const outputType = wire.output_type as LoggingOutputType;
  return {
    id: wire.id,
    name: wire.name,
    type: outputType,
    outputType,
    clusterId: wire.cluster_id ?? undefined,
    enabled: wire.enabled,
    isSystem: wire.is_system,
    config: wire.configuration,
    configuration: wire.configuration,
    capabilities: mapLoggingCapabilities(wire.capabilities),
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function loggingOutputBody(
  data: Partial<LoggingOutput>,
): LoggingContracts["LoggingOutputWriteRequest"] {
  return {
    name: requiredName(data.name, "Logging output"),
    output_type: outputTypeOf(data),
    configuration: data.configuration ?? data.config,
    cluster_id: data.clusterId,
    enabled: data.enabled,
  };
}

export async function getLoggingOutputs(): Promise<LoggingOutput[]> {
  const page = await listLoggingOutputsOperation({ query: { limit: 200 } });
  return (page.data ?? []).map(mapLoggingOutput);
}

export async function createLoggingOutput(
  data: Partial<LoggingOutput>,
): Promise<LoggingOutput> {
  const receipt = requireData(
    await createLoggingOutputOperation({
      query: { cluster_id: data.clusterId },
      headerParams: mutationHeaders(),
      body: loggingOutputBody(data),
    }),
    "createLoggingOutput",
  );
  return mapLoggingOutput(receipt.output);
}

/**
 * The management API exposes full-replacement PUT semantics. Callers must
 * provide the current output plus their edits so required fields are never
 * reset by a UI toggle.
 */
export async function updateLoggingOutput(
  id: string,
  data: Partial<LoggingOutput>,
): Promise<LoggingOutput> {
  const receipt = requireData(
    await updateLoggingOutputOperation({
      path: { id },
      headerParams: mutationHeaders(),
      body: loggingOutputBody(data),
    }),
    "updateLoggingOutput",
  );
  return mapLoggingOutput(receipt.output);
}

export async function deleteLoggingOutput(id: string): Promise<void> {
  await deleteLoggingOutputOperation({
    path: { id },
    headerParams: mutationHeaders(),
  });
}

export async function testLoggingOutput(id: string) {
  return requireData(
    await testLoggingOutputOperation({
      path: { id },
      headerParams: mutationHeaders(),
    }),
    "testLoggingOutput",
  );
}

export type LoggingQueryRequest = Omit<
  OpenAPIComponents["schemas"]["LoggingQueryRequest"],
  "query"
> & { query: string };

export type LoggingQueryResult =
  OpenAPIComponents["schemas"]["LoggingQueryResult"];

export async function queryLoggingOutput(
  id: string,
  request: LoggingQueryRequest,
): Promise<LoggingQueryResult> {
  return requireData(
    await queryLoggingOutputOperation({ path: { id }, body: request }),
    "queryLoggingOutput",
  );
}

export type LoggingSavedSearch = CamelizeKeys<
  OpenAPIComponents["schemas"]["LoggingSavedSearch"]
>;

export interface LoggingSavedSearchInput {
  outputId?: string;
  name: string;
  query: string;
  namespaces: string[];
  limit: number;
  direction: "forward" | "backward";
  liveTail: boolean;
}

export function mapLoggingSavedSearch(
  wire: LoggingSavedSearchWire,
): LoggingSavedSearch {
  return {
    id: wire.id,
    outputId: wire.output_id,
    name: wire.name,
    query: wire.query,
    namespaces: wire.namespaces,
    limit: wire.limit,
    direction: wire.direction,
    liveTail: wire.live_tail,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

export async function getLoggingSavedSearches(
  outputId: string,
): Promise<LoggingSavedSearch[]> {
  const response = await listLoggingSavedSearchesOperation({
    query: { output_id: outputId },
  });
  return response.data.map(mapLoggingSavedSearch);
}

export async function createLoggingSavedSearch(
  input: LoggingSavedSearchInput & { outputId: string },
): Promise<LoggingSavedSearch> {
  return mapLoggingSavedSearch(
    requireData(
      await createLoggingSavedSearchOperation({
        body: {
          output_id: input.outputId,
          name: input.name,
          query: input.query,
          namespaces: input.namespaces,
          limit: input.limit,
          direction: input.direction,
          live_tail: input.liveTail,
        },
      }),
      "createLoggingSavedSearch",
    ),
  );
}

export async function updateLoggingSavedSearch(
  id: string,
  input: LoggingSavedSearchInput,
): Promise<LoggingSavedSearch> {
  return mapLoggingSavedSearch(
    requireData(
      await updateLoggingSavedSearchOperation({
        path: { id },
        body: {
          name: input.name,
          query: input.query,
          namespaces: input.namespaces,
          limit: input.limit,
          direction: input.direction,
          live_tail: input.liveTail,
        },
      }),
      "updateLoggingSavedSearch",
    ),
  );
}

export async function deleteLoggingSavedSearch(id: string): Promise<void> {
  await deleteLoggingSavedSearchOperation({ path: { id } });
}

export type LoggingAttachStatus =
  OpenAPIComponents["schemas"]["LoggingAttachStatus"];

export type LoggingAttachResult = Pick<
  CamelizeKeys<OpenAPIComponents["schemas"]["LoggingAttachResult"]>,
  "id" | "name" | "outputType" | "isSystem" | "enabled" | "token"
>;

export async function getLoggingAttachStatus(
  clusterId: string,
): Promise<LoggingAttachStatus> {
  return requireData(
    await getLoggingAttachStatusOperation({ path: { id: clusterId } }),
    "getLoggingAttachStatus",
  );
}

export async function attachAstronomerLogs(
  clusterId: string,
  rotate = false,
): Promise<LoggingAttachResult> {
  const response = await attachAstronomerLogsOperation({
    path: { id: clusterId },
    query: { rotate: rotate || undefined },
    headerParams: mutationHeaders(),
  });
  if (!response?.data) {
    throw new Error("attachAstronomerLogs returned no data payload");
  }
  const payload = response.data;
  const wire = "output" in payload ? payload.output : payload;
  return {
    id: wire.id,
    name: wire.name,
    outputType: wire.output_type,
    isSystem: wire.is_system,
    enabled: wire.enabled,
    token: wire.token,
  };
}

export function mapLoggingPipeline(wire: LoggingPipelineWire): LoggingPipeline {
  return {
    id: wire.id,
    name: wire.name,
    clusterId: wire.cluster_id,
    namespaces: wire.namespaces,
    outputIds: wire.output_ids,
    outputNames: wire.output_names,
    filters: Array.isArray(wire.filters)
      ? (wire.filters as LoggingPipeline["filters"])
      : [],
    enabled: wire.enabled,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

function loggingPipelineBody(
  data: Partial<LoggingPipeline>,
): LoggingContracts["LoggingPipelineWriteRequest"] {
  return {
    name: requiredName(data.name, "Logging pipeline"),
    cluster_id: data.clusterId,
    namespaces: data.namespaces,
    filters: data.filters,
    output_ids: data.outputIds ?? [],
    enabled: data.enabled,
  };
}

export async function getLoggingPipelines(params?: {
  clusterId?: string;
  limit?: number;
}): Promise<LoggingPipeline[]> {
  const page = await listLoggingPipelinesOperation({
    query: {
      cluster_id: params?.clusterId,
      limit: params?.limit,
    },
  });
  return (page.data ?? []).map(mapLoggingPipeline);
}

export async function createLoggingPipeline(
  data: Partial<LoggingPipeline>,
): Promise<LoggingPipeline> {
  const receipt = requireData(
    await createLoggingPipelineOperation({
      query: { cluster_id: data.clusterId },
      headerParams: mutationHeaders(),
      body: loggingPipelineBody(data),
    }),
    "createLoggingPipeline",
  );
  return mapLoggingPipeline(receipt.pipeline);
}

export async function updateLoggingPipeline(
  id: string,
  data: Partial<LoggingPipeline>,
): Promise<LoggingPipeline> {
  const receipt = requireData(
    await updateLoggingPipelineOperation({
      path: { id },
      headerParams: mutationHeaders(),
      body: loggingPipelineBody(data),
    }),
    "updateLoggingPipeline",
  );
  return mapLoggingPipeline(receipt.pipeline);
}

export async function deleteLoggingPipeline(id: string): Promise<void> {
  await deleteLoggingPipelineOperation({
    path: { id },
    headerParams: mutationHeaders(),
  });
}

export function mapLoggingOperation(
  wire: LoggingOperationWire,
): LoggingOperation {
  const events: LoggingOperationEvent[] | undefined = wire.events?.map(
    (event) => ({
      id: event.id,
      level: event.level,
      stage: event.stage,
      message: event.message,
      detail: event.detail,
      createdAt: event.createdAt,
    }),
  );
  return {
    id: wire.id,
    targetType: wire.targetType,
    targetKey: wire.targetKey,
    operation: wire.operationType,
    status: wire.status,
    attemptCount: wire.attemptCount,
    errorMessage: wire.errorMessage || undefined,
    startedAt: wire.startedAt,
    completedAt: wire.completedAt,
    createdAt: wire.createdAt,
    updatedAt: wire.updatedAt,
    events,
  };
}

export async function getLoggingOperations(params?: {
  status?: string;
  target_type?: string;
  target_key?: string;
  limit?: number;
  offset?: number;
}): Promise<LoggingOperation[]> {
  const response = await listLoggingOperationsOperation({
    query: {
      status: params?.status as LoggingOperationWire["status"] | undefined,
      targetType: params?.target_type as
        LoggingOperationWire["targetType"] | undefined,
      targetKey: params?.target_key,
      limit: params?.limit,
      offset: params?.offset,
    },
  });
  return response.data.map(mapLoggingOperation);
}

export async function getLoggingOperation(
  id: string,
): Promise<LoggingOperation> {
  return mapLoggingOperation(
    requireData(
      await getLoggingOperationOperation({ path: { id } }),
      "getLoggingOperation",
    ),
  );
}

export async function retryLoggingOperation(
  id: string,
): Promise<LoggingOperation> {
  return mapLoggingOperation(
    requireData(
      await retryLoggingOperationOperation({
        path: { id },
        headerParams: mutationHeaders(),
      }),
      "retryLoggingOperation",
    ),
  );
}
