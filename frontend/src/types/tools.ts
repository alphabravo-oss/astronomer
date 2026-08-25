import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";

// --- Cluster Tools Types ---

export type ToolCategory =
  | "auth"
  | "backup"
  | "logging"
  | "mesh"
  | "monitoring"
  | "networking"
  | "observability"
  | "security"
  | "other";

export type ClusterTool = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterTool"]>,
  "category"
> & {
  category: ToolCategory;
};

export type ToolFormField = CamelizeKeys<
  OpenAPIComponents["schemas"]["ToolFormField"]
>;

export type ToolFormSchema = CamelizeKeys<
  OpenAPIComponents["schemas"]["ToolFormSchema"]
>;

export type ToolOperationEvent =
  OpenAPIComponents["schemas"]["ToolOperationEvent"];

export type ToolOperation = OpenAPIComponents["schemas"]["ToolOperation"];

export type ToolStatus =
  | "not_installed"
  | "installed"
  | "installed_unmanaged"
  | "installing"
  | "upgrading"
  | "failed"
  | "uninstalling"
  | "unknown";

export type ClusterToolStatus = Omit<
  CamelizeKeys<OpenAPIComponents["schemas"]["ClusterToolStatus"]>,
  "status"
> & { status: ToolStatus };

export type ToolPreviewResponse = CamelizeKeys<
  OpenAPIComponents["schemas"]["ToolPreview"]
>;
