/**
 * App install / upgrade modal — sprint 082+.
 *
 * Shared component for both fresh installs (from Browse / Recommended)
 * and upgrades on already-installed releases (from the Installed row's
 * "Upgrade" action). Two key differences between the modes:
 *
 *   • mode='install' → POST /catalog/installed/, release_name + ns are
 *     editable, defaults to the chart name and its supported system namespace.
 *   • mode='upgrade' → PUT /catalog/installed/{id}/upgrade/, release_name
 *     + ns are read-only (those are the release identity), version
 *     dropdown is the user's actual control.
 *
 * Values editor:
 *   • Pre-filled from GET /catalog/charts/{chart_id}/values/?version=
 *     which lazy-hydrates the chart's defaults on first call (~1-2s)
 *     then caches.
 *   • On upgrade mode it's pre-filled with the release's current
 *     values_override so the user sees what they currently have, not
 *     a wall of fresh defaults to wade through.
 *   • Curated Settings and full YAML round-trip through the same override.
 *     Review is backed by the public server preview and blocks mutation when
 *     validation or preflight finds an unsafe configuration.
 *
 * Submission returns a durable catalog operation receipt. The caller tracks
 * that operation and its Flux rollout; acceptance is not workload readiness.
 */

import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";

import {
  getChartDefaultValues,
  previewCatalogApplication,
} from "@/lib/api/cluster-apps";
import { curateHelmValuesSchema } from "@/lib/catalog-chart-fields";
import {
  hasRenderableSchema,
  mergeSchemaDefaults,
  parseHelmValuesYAML,
  resolveSchemaRefs,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from "@/lib/helm-values-schema";
import { queryKeys } from "@/lib/query-keys";

export function useAppInstallPreview({
  projectId,
  chartId,
  chartName,
  version,
  clusterId,
  selectedVersionId,
  namespace,
  valuesYaml,
  isUpgrade,
  editorMode,
}: {
  projectId: string;
  chartId: string;
  chartName: string;
  version?: string;
  clusterId: string;
  selectedVersionId: string;
  namespace: string;
  valuesYaml: string;
  isUpgrade: boolean;
  editorMode: "form" | "yaml" | "review";
}) {
  const defaultValues = useQuery({
    queryKey: queryKeys.catalog.installChartValues(projectId, chartId, version),
    queryFn: ({ signal }) =>
      getChartDefaultValues(projectId, chartId, version, signal),
    enabled: !!projectId && !!version,
    throwOnError: false,
  });
  const valuesSchema = useMemo(() => {
    const resolved = resolveSchemaRefs(defaultValues.data?.valuesSchema);
    const renderable = hasRenderableSchema(resolved)
      ? (resolved as HelmValuesSchemaNode)
      : null;
    return curateHelmValuesSchema(chartName, renderable);
  }, [defaultValues.data?.valuesSchema, chartName]);
  const schemaValues = useMemo(() => {
    const parsed = parseHelmValuesYAML(valuesYaml);
    if (parsed == null) return null;
    return (
      valuesSchema ? mergeSchemaDefaults(valuesSchema, parsed) : parsed
    ) as HelmValuesObject;
  }, [valuesSchema, valuesYaml]);
  const valuesAreValid = !valuesYaml.trim() || schemaValues != null;
  const preview = useQuery({
    queryKey: queryKeys.catalog.applicationPreview(
      clusterId,
      selectedVersionId,
      namespace,
      valuesYaml,
      isUpgrade ? "upgrade" : "install",
    ),
    queryFn: () =>
      previewCatalogApplication({
        clusterId,
        chartVersionId: selectedVersionId,
        namespace: namespace.trim(),
        valuesOverride: valuesYaml,
        operation: isUpgrade ? "upgrade" : "install",
      }),
    enabled:
      editorMode === "review" &&
      !!selectedVersionId &&
      !!namespace.trim() &&
      valuesAreValid,
    retry: false,
  });
  const blockingPreview = preview.data?.checks.find(
    (check) => check.status === "blocking",
  );

  return {
    defaultValues,
    valuesSchema,
    schemaValues,
    preview,
    blockingPreview,
  };
}
