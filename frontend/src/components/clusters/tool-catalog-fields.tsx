import { HelmValuesForm } from "@/components/catalog/helm-values-form";
import { CatalogResourceContext } from "@/components/catalog/catalog-resource-field";
import {
  catalogConfigurationSchema,
  mergeCatalogValues,
  changedValuePaths,
} from "@/lib/catalog-configuration";
import { catalogValueErrors } from "@/lib/catalog-values-validation";
import {
  parseHelmValuesYAML,
  getValueAtPath,
  setValueAtPath,
  type HelmValuesObject,
} from "@/lib/helm-values-schema";
import type { ToolPreviewResponse } from "@/types";

export function ToolCatalogFields({
  charts,
  clusterId,
  overrides,
  onChange,
}: {
  charts: ToolPreviewResponse["charts"];
  clusterId: string;
  overrides: HelmValuesObject;
  onChange: (next: HelmValuesObject) => void;
}) {
  // A multi-release tool owns path translation and composition. Its existing
  // tool form remains canonical; all releases share server-side constraints.
  if (charts.length !== 1) return null;
  const chart = charts[0];
  if (!chart.catalogVersion?.configuration) return null;
  const schema = catalogConfigurationSchema(chart.catalogVersion);
  if (!schema) return null;
  let base;
  try {
    base = parseHelmValuesYAML(chart.valuesYaml) ?? {};
  } catch {
    return null;
  }
  const values = mergeCatalogValues(base, overrides);
  const errors = catalogValueErrors(schema, values);
  return (
    <details className="rounded-lg border border-border p-3">
      <summary className="cursor-pointer text-sm font-medium">
        Chart configuration — {chart.chartVersion}
      </summary>
      <div className="mt-3">
        <CatalogResourceContext.Provider
          value={{ clusterId, namespace: chart.namespace }}
        >
          <HelmValuesForm
            schema={schema}
            value={values}
            onChange={(next) =>
              onChange(
                changedValuePaths(values, next).reduce(
                  (result, path) =>
                    setValueAtPath(
                      result,
                      path.split("."),
                      getValueAtPath(next, path.split(".")),
                    ),
                  overrides,
                ),
              )
            }
          />
        </CatalogResourceContext.Provider>
      </div>
      {errors.length > 0 && (
        <ul role="alert" className="text-xs text-status-error">
          {errors.map((error, index) => (
            <li key={index}>
              {error.path}: {error.message}
            </li>
          ))}
        </ul>
      )}
    </details>
  );
}
