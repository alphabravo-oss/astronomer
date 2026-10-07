export const toolQueryKeys = {
  all: ["tools"] as const,
  list: () => ["tools", "list"] as const,
  detail: (slug: string) => ["tools", "detail", slug] as const,
  clusterStatus: (clusterId: string) =>
    ["tools", "clusterStatus", clusterId] as const,
  configuration: (toolSlug: string, clusterId: string) =>
    ["tools", "configuration", toolSlug, clusterId] as const,
  preview: (
    toolSlug: string,
    clusterId: string,
    preset: string,
    valuesOverride = "",
  ) =>
    ["tools", "preview", toolSlug, clusterId, preset, valuesOverride] as const,
  operation: (operationId: string) =>
    ["tools", "operation", operationId] as const,
};
