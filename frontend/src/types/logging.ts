import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

// --- Logging Types ---

export type LoggingOutputType =
  | "elasticsearch"
  | "opensearch"
  | "loki"
  | "splunk"
  | "cloudwatch"
  | "datadog"
  | "s3"
  | "syslog";

export type LoggingOutput = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["LoggingOutput"]>,
  "outputType" | "configuration" | "clusterId" | "isSystem" | "capabilities"
> & {
  id: string;
  name: string;
  type?: LoggingOutputType;
  outputType?: LoggingOutputType;
  clusterId?: string;
  clusterName?: string;
  enabled: boolean;
  isSystem?: boolean;
  config?: Record<string, unknown>;
  configuration?: Record<string, unknown>;
  status?: "connected" | "disconnected" | "error";
  capabilities?: LoggingOutputCapabilities;
  createdAt: string;
  updatedAt: string;
};

export type LoggingOutputCapabilities = CamelizeKeys<
  OpenAPIComponents["schemas"]["LoggingOutputCapabilities"]
>;

export type LoggingPipeline = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["LoggingPipeline"]>,
  "clusterId" | "labels" | "filters"
> & {
  id: string;
  name: string;
  description?: string;
  clusterId?: string;
  clusterName?: string;
  namespaces: string[];
  outputIds: string[];
  outputNames: string[];
  filters: LoggingFilter[];
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
};

export interface LoggingFilter {
  type: "include" | "exclude";
  field: string;
  pattern: string;
}

/**
 * A row from `GET /api/v1/logging/operations/`. The Go reconciler emits
 * snake_case keys; the logging API adapter maps them explicitly, so this
 * interface mirrors the feature's camelCase view shape
 * (same pattern as other durable operation records).
 */
export type LoggingOperation = Omit<
  OpenAPIComponents["schemas"]["LoggingOperation"],
  "operationType" | "errorMessage" | "events"
> & {
  id: string;
  targetType: "output" | "pipeline" | string;
  targetKey: string;
  operation: "apply" | "delete" | string;
  status:
    "pending" | "running" | "completed" | "failed" | "superseded" | string;
  payload?: Record<string, unknown>;
  errorMessage?: string;
  startedAt?: string | null;
  completedAt?: string | null;
  createdAt: string;
  updatedAt: string;
  // Returned only on the detail endpoint.
  events?: LoggingOperationEvent[];
};

export type LoggingOperationEvent = Omit<
  OpenAPIComponents["schemas"]["LoggingOperationEvent"],
  "detail"
> & {
  id: string;
  level: string;
  stage: string;
  message: string;
  detail?: Record<string, unknown>;
  createdAt: string;
};
