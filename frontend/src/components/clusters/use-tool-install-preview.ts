import { useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/lib/query-keys";
import { previewToolInstall } from "@/lib/api/tools";
export function useToolInstallPreview(
  slug: string,
  clusterId: string,
  preset: string,
  values: string,
  enabled: boolean,
) {
  return useQuery({
    queryKey: queryKeys.tools.preview(slug, clusterId, preset, values),
    queryFn: () =>
      previewToolInstall(slug, {
        cluster_id: clusterId,
        preset,
        values_override: values || undefined,
      }),
    enabled,
  });
}
