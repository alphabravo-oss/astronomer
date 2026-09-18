import * as yaml from "js-yaml";
import type { ClusterTool, ToolFormField, ToolPreviewResponse } from "@/types";

/** Projects release-local preview values back into the tool form's field paths. */
export function previewToolFieldValues(
  charts: ToolPreviewResponse["charts"],
  tool: Pick<ClusterTool, "charts" | "defaultNamespace">,
  fields: ToolFormField[],
): Record<string, string> {
  const root: Record<string, unknown> = {};
  for (const chart of charts) {
    const definition = tool.charts.find(
      (item) =>
        item.chartName === chart.chartName &&
        (item.namespace || tool.defaultNamespace) === chart.namespace,
    );
    const parsed = yaml.load(chart.valuesYaml);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      if (definition?.valuesKey) root[definition.valuesKey] = parsed;
      else Object.assign(root, parsed);
    }
  }
  const result: Record<string, string> = {};
  for (const field of fields) {
    let value: unknown = root;
    for (const segment of field.path.split(".")) {
      value =
        value && typeof value === "object"
          ? (value as Record<string, unknown>)[segment]
          : undefined;
    }
    if (
      typeof value === "string" ||
      typeof value === "number" ||
      typeof value === "boolean"
    )
      result[field.path] = String(value);
  }
  return result;
}
