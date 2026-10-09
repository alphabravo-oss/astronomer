export function pipelineDefaults(clusterId?: string) {
  return {
    name: "",
    description: "",
    clusterId: clusterId || "",
    namespaces: [] as string[],
    outputIds: [] as string[],
    labelKey: "",
    labelValue: "",
    labels: {} as Record<string, string>,
    enabled: true,
  };
}
