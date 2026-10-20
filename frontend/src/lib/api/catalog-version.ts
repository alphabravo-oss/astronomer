import type { OpenAPIReadComponents as OpenAPIComponents } from "@/types/categories.generated";
import type { HelmChartVersion } from "@/types";
type Schemas = OpenAPIComponents["schemas"];

export function requiredString(
  value: string | undefined,
  field: string,
): string {
  if (!value) throw new Error(`Catalog API response omitted ${field}`);
  return value;
}

export function mapHelmChartVersion(
  wire: Schemas["HelmChartVersion"],
): HelmChartVersion {
  return {
    id: requiredString(wire.id, "chartVersion.id"),
    chartId: requiredString(wire.chart_id, "chartVersion.chart_id"),
    version: requiredString(wire.version, "chartVersion.version"),
    appVersion: wire.app_version ?? "",
    digest: wire.digest,
    urls: wire.urls,
    valuesSchema: wire.values_schema,
    catalogVersion: wire.catalog_version,
    defaultValues: wire.default_values,
    readme: wire.readme,
    createdAtUpstream: wire.created_at_upstream,
    createdAt: requiredString(wire.created_at, "chartVersion.created_at"),
    updatedAt: wire.updated_at,
  };
}
