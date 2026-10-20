import type { OpenAPIComponents as components } from "@/types/openapi.generated";
import {
  dumpHelmValuesYAML,
  parseHelmValuesYAML,
  resolveSchemaRefs,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from "./helm-values-schema";

export type CatalogVersionProfile =
  components["schemas"]["CatalogVersionProfile"];
export type CatalogConfiguration =
  components["schemas"]["CatalogVersionConfiguration"];

/** Preserve unknown keys, secret references and explicit nulls. Arrays are atomic. */
export function mergeCatalogValues(
  base: HelmValuesObject,
  override: HelmValuesObject,
): HelmValuesObject {
  const result = { ...base };
  for (const [key, value] of Object.entries(override)) {
    if (["__proto__", "constructor", "prototype"].includes(key)) continue;
    const previous = result[key];
    result[key] =
      isObject(previous) && isObject(value)
        ? mergeCatalogValues(previous, value)
        : value;
  }
  return result;
}
function isObject(value: unknown): value is HelmValuesObject {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
export function catalogConfigurationSchema(
  profile?: CatalogVersionProfile,
  upstream?: unknown,
): HelmValuesSchemaNode | null {
  return resolveSchemaRefs(profile?.configuration?.schema ?? upstream);
}
export function initialCatalogValues(
  defaults: string,
  profile?: CatalogVersionProfile,
): string {
  const config = profile?.configuration;
  const preset = config?.presets.find((row) => row.id === config.defaultPreset);
  return dumpHelmValuesYAML(
    mergeCatalogValues(
      parseHelmValuesYAML(defaults) ?? {},
      preset?.values ?? {},
    ),
  );
}
export function changedValuePaths(
  before: HelmValuesObject,
  after: HelmValuesObject,
  prefix = "",
): string[] {
  return [...new Set([...Object.keys(before), ...Object.keys(after)])].flatMap(
    (key) => {
      const path = prefix ? `${prefix}.${key}` : key;
      if (isObject(before[key]) && isObject(after[key]))
        return changedValuePaths(before[key], after[key], path);
      return JSON.stringify(before[key]) === JSON.stringify(after[key])
        ? []
        : [path];
    },
  );
}
