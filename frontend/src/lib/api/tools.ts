import {
  deleteToolsBySlugUninstall,
  getClustersByClusterIdToolsStatus,
  getTools as listToolsOperation,
  getToolsBySlug,
  getToolsOperationsById,
  postToolsBySlugAdopt,
  postToolsBySlugInstall,
  postToolsBySlugPreview,
  putToolsBySlugUpgrade,
} from "@/lib/api/generated/client";
import { idempotencyHeaderParams } from "@/lib/api/idempotency";
import type {
  ClusterTool,
  ClusterToolStatus,
  ToolCategory,
  ToolFormField,
  ToolOperation,
  ToolPreviewResponse,
  ToolStatus,
} from "@/types";
import type { OpenAPIComponents } from "@/types/openapi.generated";

type Schemas = OpenAPIComponents["schemas"];

const TOOL_CATEGORIES = new Set<ToolCategory>([
  "auth",
  "backup",
  "logging",
  "mesh",
  "monitoring",
  "networking",
  "observability",
  "security",
  "other",
]);

function requireData<T>(response: { data?: T }, operation: string): T {
  if (response.data === undefined) {
    throw new Error(`${operation} returned no data payload`);
  }
  return response.data;
}

function toolCategory(value: string): ToolCategory {
  return TOOL_CATEGORIES.has(value as ToolCategory)
    ? (value as ToolCategory)
    : "other";
}

function mapToolFormField(wire: Schemas["ToolFormField"]): ToolFormField {
  return {
    path: wire.path,
    label: wire.label,
    type: wire.type,
    group: wire.group,
    default: wire.default,
    options: wire.options,
    help: wire.help,
    placeholder: wire.placeholder,
    storageClassPath: wire.storage_class_path,
  };
}

/** Explicit generated-wire mapper for the cluster tool catalog. */
export function mapClusterTool(wire: Schemas["ClusterTool"]): ClusterTool {
  return {
    id: wire.id,
    slug: wire.slug,
    name: wire.name,
    description: wire.description,
    icon: wire.icon,
    category: toolCategory(wire.category),
    charts: wire.charts.map((chart) => ({
      chartName: chart.chart_name,
      repoUrl: chart.repo_url,
      namespace: chart.namespace,
      order: chart.order,
    })),
    versionConstraint: wire.version_constraint,
    defaultNamespace: wire.default_namespace,
    isBuiltin: wire.is_builtin,
    isEnabled: wire.is_enabled,
    helmChartId: wire.helm_chart_id,
    presets: wire.presets,
    serviceName: wire.service_name,
    servicePort: wire.service_port,
    servicePath: wire.service_path,
    subServices: wire.sub_services,
    formSchema: wire.form_schema
      ? { fields: wire.form_schema.fields.map(mapToolFormField) }
      : wire.form_schema,
    createdAt: wire.created_at,
    updatedAt: wire.updated_at,
  };
}

export function mapClusterToolStatus(
  wire: Schemas["ClusterToolStatus"],
): ClusterToolStatus {
  return {
    slug: wire.slug,
    name: wire.name,
    status: wire.status as ToolStatus,
    releaseName: wire.release_name,
    namespace: wire.namespace,
    presetUsed: wire.preset_used,
    error: wire.error,
    operation: wire.operation,
  };
}

function mapToolPreview(wire: Schemas["ToolPreview"]): ToolPreviewResponse {
  return {
    charts: wire.charts.map((chart) => ({
      chartName: chart.chart_name,
      chartVersion: chart.chart_version,
      namespace: chart.namespace,
      valuesYaml: chart.values_yaml,
    })),
    preset: wire.preset,
  };
}

export async function getTools(): Promise<ClusterTool[]> {
  const response = await listToolsOperation({ query: { limit: 200 } });
  return (response.data ?? []).map(mapClusterTool);
}

export async function getTool(slug: string): Promise<ClusterTool> {
  const response = await getToolsBySlug({ path: { slug } });
  return mapClusterTool(requireData(response, "getTool"));
}

export async function getClusterToolsStatus(
  clusterId: string,
): Promise<ClusterToolStatus[]> {
  const response = await getClustersByClusterIdToolsStatus({
    path: { cluster_id: clusterId },
  });
  return (response.data ?? []).map(mapClusterToolStatus);
}

export async function previewToolInstall(
  slug: string,
  data: { cluster_id: string; preset: string },
): Promise<ToolPreviewResponse> {
  const response = await postToolsBySlugPreview({
    path: { slug },
    body: data,
  });
  return mapToolPreview(requireData(response, "previewToolInstall"));
}

export async function installTool(
  slug: string,
  data: { cluster_id: string; preset: string; values_override?: string },
): Promise<ToolOperation> {
  const response = await postToolsBySlugInstall({
    path: { slug },
    headerParams: idempotencyHeaderParams(),
    body: data,
  });
  return requireData(response, "installTool");
}

export async function getToolOperation(
  operationId: string,
): Promise<ToolOperation> {
  const response = await getToolsOperationsById({
    path: { id: operationId },
  });
  return requireData(response, "getToolOperation");
}

export async function upgradeTool(
  slug: string,
  data: { cluster_id: string; preset?: string; values_override?: string },
): Promise<ToolOperation> {
  const response = await putToolsBySlugUpgrade({
    path: { slug },
    headerParams: idempotencyHeaderParams(),
    body: data,
  });
  return requireData(response, "upgradeTool");
}

export async function uninstallTool(
  slug: string,
  data: { cluster_id: string },
): Promise<ToolOperation> {
  const response = await deleteToolsBySlugUninstall({
    path: { slug },
    headerParams: idempotencyHeaderParams(),
    body: data,
  });
  return requireData(response, "uninstallTool");
}

export async function adoptTool(
  slug: string,
  data: { cluster_id: string; release_name: string },
): Promise<ToolOperation> {
  const response = await postToolsBySlugAdopt({
    path: { slug },
    headerParams: idempotencyHeaderParams(),
    body: data,
  });
  return requireData(response, "adoptTool");
}
