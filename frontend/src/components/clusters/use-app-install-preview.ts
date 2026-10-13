/**
 * App install / upgrade page — sprint 082+.
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

import { catalogValueErrors } from "@/lib/catalog-values-validation";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";

import {
  getChartDefaultValues,
  previewCatalogApplication,
} from "@/lib/api/cluster-apps";
import {
  catalogConfigurationSchema,
  type CatalogVersionProfile,
} from "@/lib/catalog-configuration";
import {
  hasRenderableSchema,
  parseHelmValuesYAML,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from "@/lib/helm-values-schema";
import { queryKeys } from "@/lib/query-keys";

export function useAppInstallPreview({
  projectId,
  chartId,
  version,
  catalogVersion,
  clusterId,
  selectedVersionId,
  fromChartVersionId,
  namespace,
  valuesYaml,
  isUpgrade,
  editorMode,
}: {
  projectId: string;
  chartId: string;
  chartName: string;
  version?: string;
  catalogVersion?: CatalogVersionProfile;
  clusterId: string;
  selectedVersionId: string;
  fromChartVersionId?: string;
  namespace: string;
  valuesYaml: string;
  isUpgrade: boolean;
  editorMode: "form" | "yaml" | "review";
}) {
  const defaultValues = useQuery({
    queryKey: queryKeys.catalog.installChartValues(projectId, chartId, version),
    queryFn: ({ signal }) =>
      getChartDefaultValues(projectId, chartId, version, signal),
    select: useCallback(
      (data: Awaited<ReturnType<typeof getChartDefaultValues>>) => ({
        ...data,
        catalogVersion,
      }),
      [catalogVersion],
    ),
    enabled: !!projectId && !!version,
    throwOnError: false,
  });
  const valuesSchema = useMemo(() => {
    const resolved = catalogConfigurationSchema(
      defaultValues.data?.catalogVersion,
      defaultValues.data?.valuesSchema,
    );
    const renderable = hasRenderableSchema(resolved)
      ? (resolved as HelmValuesSchemaNode)
      : null;
    return renderable;
  }, [defaultValues.data]);
  const schemaValues = useMemo(() => {
    let parsed;
    try {
      parsed = parseHelmValuesYAML(valuesYaml);
    } catch {
      return null;
    }
    if (parsed == null) return null;
    return parsed as HelmValuesObject;
  }, [valuesYaml]);
  const fieldErrors = catalogValueErrors(valuesSchema, schemaValues);
  const valuesAreValid = !valuesYaml.trim() || schemaValues != null;
  const preview = useQuery({
    queryKey: queryKeys.catalog.applicationPreview(
      clusterId,
      selectedVersionId,
      namespace,
      valuesYaml,
      isUpgrade ? "upgrade" : "install",
      fromChartVersionId,
    ),
    queryFn: ({ signal }) =>
      previewCatalogApplication(
        {
          clusterId,
          chartVersionId: selectedVersionId,
          fromChartVersionId,
          namespace: namespace.trim(),
          valuesOverride: valuesYaml,
          operation: isUpgrade ? "upgrade" : "install",
        },
        signal,
      ),
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
    fieldErrors,
  };
}
